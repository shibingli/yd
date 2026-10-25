package gormstore

import (
	"context"
	"errors"
	"time"

	"github.com/shibingli/yd/messaging"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateFriendRequest inserts one directed request. | CreateFriendRequest 插入一条有向申请。
func (s *Store) CreateFriendRequest(ctx context.Context, req messaging.FriendRequest) error {
	if err := s.dbc(ctx).Create(&req).Error; err != nil {
		return createError(err)
	}
	return nil
}

// GetFriendRequest reads one request by id. | GetFriendRequest 按 ID 读取一条申请。
func (s *Store) GetFriendRequest(ctx context.Context, tenantID, id string) (messaging.FriendRequest, error) {
	var req messaging.FriendRequest
	if err := s.dbc(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&req).Error; err != nil {
		return messaging.FriendRequest{}, queryError(err)
	}
	return req, nil
}

// FriendRequestBetween reads one direction of a request. | FriendRequestBetween 读取某一方向的申请。
func (s *Store) FriendRequestBetween(ctx context.Context, tenantID, requesterID, addresseeID string) (messaging.FriendRequest, error) {
	var req messaging.FriendRequest
	if err := s.dbc(ctx).
		Where("tenant_id = ? AND requester_id = ? AND addressee_id = ?", tenantID, requesterID, addresseeID).
		First(&req).Error; err != nil {
		return messaging.FriendRequest{}, queryError(err)
	}
	return req, nil
}

// UpdateFriendRequest advances the state machine, guarded by the expected current state. |
// UpdateFriendRequest 推进状态机，并以期望的当前状态作为并发前置条件。
func (s *Store) UpdateFriendRequest(ctx context.Context, req messaging.FriendRequest, expected messaging.FriendRequestStatus) error {
	result := s.dbc(ctx).Model(&messaging.FriendRequest{}).
		Where("tenant_id = ? AND id = ? AND status = ?", req.TenantID, req.ID, expected).
		Updates(map[string]any{
			"status":       req.Status,
			"message":      req.Message,
			"created_at":   req.CreatedAt,
			"responded_at": req.RespondedAt,
		})
	if result.Error != nil {
		return queryError(result.Error)
	}
	if result.RowsAffected == 0 {
		return messaging.ErrRequestClosed
	}
	return nil
}

// AcceptFriendRequest accepts a request and creates the friendship atomically. | AcceptFriendRequest 原子地接受申请并创建好友关系。
func (s *Store) AcceptFriendRequest(ctx context.Context, req messaging.FriendRequest, friendship messaging.Friendship) error {
	return s.transaction(ctx, []string{"yd_msg_friend_requests", "yd_msg_friendships"}, func(tx *gorm.DB) error {
		result := tx.Model(&messaging.FriendRequest{}).
			Where("tenant_id = ? AND id = ? AND status = ?", req.TenantID, req.ID, messaging.FriendPending).
			Updates(map[string]any{"status": req.Status, "responded_at": req.RespondedAt})
		if err := rowsError(result); err != nil {
			return err
		}
		if err := tx.Clauses(onConflictDoNothing()).Create(&friendship).Error; err != nil {
			return createError(err)
		}
		return nil
	})
}

// ListFriendRequests lists one direction of requests. | ListFriendRequests 列出某一方向的申请。
func (s *Store) ListFriendRequests(ctx context.Context, tenantID, userID string, incoming bool, status messaging.FriendRequestStatus) ([]messaging.FriendRequest, error) {
	column := "requester_id"
	if incoming {
		column = "addressee_id"
	}
	var out []messaging.FriendRequest
	if err := s.dbc(ctx).
		Where("tenant_id = ? AND "+column+" = ? AND status = ?", tenantID, userID, status).
		Order("created_at DESC").
		Find(&out).Error; err != nil {
		return nil, queryError(err)
	}
	return out, nil
}

// CountPendingFriendRequests counts incoming pending requests. | CountPendingFriendRequests 统计收到的待处理申请。
func (s *Store) CountPendingFriendRequests(ctx context.Context, tenantID, userID string) (int, error) {
	var count int64
	if err := s.dbc(ctx).Model(&messaging.FriendRequest{}).
		Where("tenant_id = ? AND addressee_id = ? AND status = ?", tenantID, userID, messaging.FriendPending).
		Count(&count).Error; err != nil {
		return 0, queryError(err)
	}
	return int(count), nil
}

// CreateFriendship stores one canonical pair. | CreateFriendship 保存一条规范化好友对。
func (s *Store) CreateFriendship(ctx context.Context, f messaging.Friendship) error {
	if err := s.dbc(ctx).Clauses(onConflictDoNothing()).Create(&f).Error; err != nil {
		return createError(err)
	}
	return nil
}

// DeleteFriendship removes the canonical pair and both owners' notes for it. Notes are private
// rows keyed by the pair, so leaving them behind would make an old remark resurface on a later
// re-friend and would leave a relation row with no friendship. |
// DeleteFriendship 删除规范化好友对及双方对它的备注。备注是以好友对为键的私有行，留下它们会让旧备注在
// 以后重新加好友时重新出现，也会留下一条没有好友关系的关联行。
func (s *Store) DeleteFriendship(ctx context.Context, tenantID, userA, userB string) error {
	low, high := orderedPair(userA, userB)
	return queryError(s.transaction(ctx, []string{"yd_msg_friendships", "yd_msg_friend_notes"}, func(tx *gorm.DB) error {
		result := tx.
			Where("tenant_id = ? AND user_low_id = ? AND user_high_id = ?", tenantID, low, high).
			Delete(&messaging.Friendship{})
		if err := rowsError(result); err != nil {
			return err
		}
		return deleteFriendNotes(tx, tenantID, userA, userB)
	}))
}

// deleteFriendNotes removes both directions of the private remarks for one pair. |
// deleteFriendNotes 删除一对用户两个方向的私有备注。
func deleteFriendNotes(tx *gorm.DB, tenantID, userA, userB string) error {
	return tx.Where("tenant_id = ? AND ((owner_id = ? AND friend_id = ?) OR (owner_id = ? AND friend_id = ?))",
		tenantID, userA, userB, userB, userA).Delete(&messaging.FriendNote{}).Error
}

// GetFriendship reads the canonical pair. | GetFriendship 读取规范化好友对。
func (s *Store) GetFriendship(ctx context.Context, tenantID, userA, userB string) (messaging.Friendship, error) {
	low, high := orderedPair(userA, userB)
	var f messaging.Friendship
	if err := s.dbc(ctx).
		Where("tenant_id = ? AND user_low_id = ? AND user_high_id = ?", tenantID, low, high).
		First(&f).Error; err != nil {
		return messaging.Friendship{}, queryError(err)
	}
	return f, nil
}

// ListFriendships lists every accepted friendship of one user. | ListFriendships 列出某个用户的全部已接受好友关系。
func (s *Store) ListFriendships(ctx context.Context, tenantID, userID string) ([]messaging.Friendship, error) {
	var out []messaging.Friendship
	if err := s.dbc(ctx).
		Where("tenant_id = ? AND (user_low_id = ? OR user_high_id = ?)", tenantID, userID, userID).
		Order("created_at DESC").
		Find(&out).Error; err != nil {
		return nil, queryError(err)
	}
	return out, nil
}

// FriendNotes resolves the owner's remarks for many friends in one query per batch. |
// FriendNotes 每个批次用一条查询解析所有者对多位好友的备注。
func (s *Store) FriendNotes(ctx context.Context, tenantID, ownerID string, friendIDs []string) (map[string]messaging.FriendNote, error) {
	out := make(map[string]messaging.FriendNote, len(friendIDs))
	for _, batch := range chunks(friendIDs) {
		var rows []messaging.FriendNote
		if err := s.dbc(ctx).
			Where("tenant_id = ? AND owner_id = ? AND friend_id IN ?", tenantID, ownerID, batch).
			Find(&rows).Error; err != nil {
			return nil, queryError(err)
		}
		for _, row := range rows {
			out[row.FriendID] = row
		}
	}
	return out, nil
}

// GetFriendNote reads the owner's remark for a friend. | GetFriendNote 读取所有者对好友的备注。
func (s *Store) GetFriendNote(ctx context.Context, tenantID, ownerID, friendID string) (messaging.FriendNote, error) {
	var note messaging.FriendNote
	if err := s.dbc(ctx).
		Where("tenant_id = ? AND owner_id = ? AND friend_id = ?", tenantID, ownerID, friendID).
		First(&note).Error; err != nil {
		return messaging.FriendNote{}, queryError(err)
	}
	return note, nil
}

// SetFriendNote upserts one remark. The friendship row is locked for the whole write, so a
// concurrent unfriend/block either removes the note after it is written or makes this write fail,
// never leaving a note for a friendship that no longer exists. |
// SetFriendNote upsert 一条备注。整个写入期间锁定好友关系行，因此并发解除好友/拉黑要么在备注写入后
// 清理它，要么让本次写入失败，绝不会给已不存在的好友关系留下备注。
func (s *Store) SetFriendNote(ctx context.Context, note messaging.FriendNote) error {
	return queryError(s.transaction(ctx, []string{"yd_msg_friend_notes"}, func(tx *gorm.DB) error {
		low, high := orderedPair(note.OwnerID, note.FriendID)
		var friendship messaging.Friendship
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND user_low_id = ? AND user_high_id = ?", note.TenantID, low, high).
			Take(&friendship).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return messaging.ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := tx.Clauses(onConflictDoNothing()).Create(&note).Error; err != nil {
			return createError(err)
		}
		if note.Remark == "" {
			return tx.
				Where("tenant_id = ? AND owner_id = ? AND friend_id = ?", note.TenantID, note.OwnerID, note.FriendID).
				Delete(&messaging.FriendNote{}).Error
		}
		return tx.Model(&messaging.FriendNote{}).
			Where("tenant_id = ? AND owner_id = ? AND friend_id = ?", note.TenantID, note.OwnerID, note.FriendID).
			Updates(map[string]any{"remark": note.Remark, "updated_at": note.UpdatedAt}).Error
	}))
}

// CreateBlock stores one direction of a block. | CreateBlock 保存拉黑关系的一个方向。
func (s *Store) CreateBlock(ctx context.Context, b messaging.Block) error {
	if err := s.dbc(ctx).Clauses(onConflictDoNothing()).Create(&b).Error; err != nil {
		return createError(err)
	}
	return nil
}

// BlockUser stores the block, removes the friendship and closes pending requests atomically. |
// BlockUser 原子地保存拉黑、删除好友关系并关闭待处理申请。
func (s *Store) BlockUser(ctx context.Context, b messaging.Block) error {
	return s.transaction(ctx, []string{"yd_msg_blocks", "yd_msg_friendships", "yd_msg_friend_notes", "yd_msg_friend_requests"}, func(tx *gorm.DB) error {
		if err := tx.Clauses(onConflictDoNothing()).Create(&b).Error; err != nil {
			return createError(err)
		}
		low, high := orderedPair(b.BlockerID, b.BlockedID)
		if err := tx.
			Where("tenant_id = ? AND user_low_id = ? AND user_high_id = ?", b.TenantID, low, high).
			Delete(&messaging.Friendship{}).Error; err != nil {
			return queryError(err)
		}
		if err := deleteFriendNotes(tx, b.TenantID, b.BlockerID, b.BlockedID); err != nil {
			return queryError(err)
		}
		now := b.CreatedAt
		// Requests sent by the blocker are cancelled; requests sent by the blocked user are rejected. |
		// 拉黑者发出的申请取消，被拉黑者发来的申请拒绝。
		if err := tx.Model(&messaging.FriendRequest{}).
			Where("tenant_id = ? AND requester_id = ? AND addressee_id = ? AND status = ?",
				b.TenantID, b.BlockerID, b.BlockedID, messaging.FriendPending).
			Updates(map[string]any{"status": messaging.FriendCancelled, "responded_at": &now}).Error; err != nil {
			return queryError(err)
		}
		if err := tx.Model(&messaging.FriendRequest{}).
			Where("tenant_id = ? AND requester_id = ? AND addressee_id = ? AND status = ?",
				b.TenantID, b.BlockedID, b.BlockerID, messaging.FriendPending).
			Updates(map[string]any{"status": messaging.FriendRejected, "responded_at": &now}).Error; err != nil {
			return queryError(err)
		}
		return nil
	})
}

// DeleteBlock removes one direction of a block. | DeleteBlock 删除拉黑关系的一个方向。
func (s *Store) DeleteBlock(ctx context.Context, tenantID, blockerID, blockedID string) error {
	result := s.dbc(ctx).
		Where("tenant_id = ? AND blocker_id = ? AND blocked_id = ?", tenantID, blockerID, blockedID).
		Delete(&messaging.Block{})
	return rowsError(result)
}

// GetBlock reads one direction of a block. | GetBlock 读取拉黑关系的一个方向。
func (s *Store) GetBlock(ctx context.Context, tenantID, blockerID, blockedID string) (messaging.Block, error) {
	var b messaging.Block
	if err := s.dbc(ctx).
		Where("tenant_id = ? AND blocker_id = ? AND blocked_id = ?", tenantID, blockerID, blockedID).
		First(&b).Error; err != nil {
		return messaging.Block{}, queryError(err)
	}
	return b, nil
}

// EitherBlocked reports whether either direction exists. | EitherBlocked 报告任一方向是否存在拉黑。
func (s *Store) EitherBlocked(ctx context.Context, tenantID, userA, userB string) (bool, error) {
	var count int64
	if err := s.dbc(ctx).Model(&messaging.Block{}).
		Where("tenant_id = ? AND ((blocker_id = ? AND blocked_id = ?) OR (blocker_id = ? AND blocked_id = ?))",
			tenantID, userA, userB, userB, userA).
		Count(&count).Error; err != nil {
		return false, queryError(err)
	}
	return count > 0, nil
}

// ListBlocks lists users blocked by one actor. | ListBlocks 列出某个主体拉黑的用户。
func (s *Store) ListBlocks(ctx context.Context, tenantID, blockerID string) ([]messaging.Block, error) {
	var out []messaging.Block
	if err := s.dbc(ctx).
		Where("tenant_id = ? AND blocker_id = ?", tenantID, blockerID).
		Order("created_at DESC").
		Find(&out).Error; err != nil {
		return nil, queryError(err)
	}
	return out, nil
}

// orderedPair orders two ids lexicographically. | orderedPair 按字典序排列两个 ID。
func orderedPair(a, b string) (low, high string) {
	if a < b {
		return a, b
	}
	return b, a
}

// nowUTC is a tiny helper for tests and cleanup cutoffs. | nowUTC 是测试与清理截止时间使用的小工具。
func nowUTC() time.Time { return time.Now().UTC() }
