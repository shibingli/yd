package gormstore

import (
	"context"
	"time"

	"github.com/shibingli/yd/messaging"
	"gorm.io/gorm"
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

// UpdateFriendRequest advances the state machine. | UpdateFriendRequest 推进状态机。
func (s *Store) UpdateFriendRequest(ctx context.Context, req messaging.FriendRequest) error {
	result := s.dbc(ctx).Model(&messaging.FriendRequest{}).
		Where("tenant_id = ? AND id = ?", req.TenantID, req.ID).
		Updates(map[string]any{
			"status":       req.Status,
			"message":      req.Message,
			"created_at":   req.CreatedAt,
			"responded_at": req.RespondedAt,
		})
	return queryError(result.Error)
}

// AcceptFriendRequest accepts a request and creates the friendship atomically. | AcceptFriendRequest 原子地接受申请并创建好友关系。
func (s *Store) AcceptFriendRequest(ctx context.Context, req messaging.FriendRequest, friendship messaging.Friendship) error {
	return s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
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

// DeleteFriendship removes the canonical pair. | DeleteFriendship 删除规范化好友对。
func (s *Store) DeleteFriendship(ctx context.Context, tenantID, userA, userB string) error {
	low, high := orderedPair(userA, userB)
	result := s.dbc(ctx).
		Where("tenant_id = ? AND user_low_id = ? AND user_high_id = ?", tenantID, low, high).
		Delete(&messaging.Friendship{})
	return rowsError(result)
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

// SetFriendNote upserts one remark. | SetFriendNote upsert 一条备注。
func (s *Store) SetFriendNote(ctx context.Context, note messaging.FriendNote) error {
	if err := s.dbc(ctx).Clauses(onConflictDoNothing()).Create(&note).Error; err != nil {
		return createError(err)
	}
	if note.Remark == "" {
		if err := s.dbc(ctx).
			Where("tenant_id = ? AND owner_id = ? AND friend_id = ?", note.TenantID, note.OwnerID, note.FriendID).
			Delete(&messaging.FriendNote{}).Error; err != nil {
			return queryError(err)
		}
		return nil
	}
	if err := s.dbc(ctx).Model(&messaging.FriendNote{}).
		Where("tenant_id = ? AND owner_id = ? AND friend_id = ?", note.TenantID, note.OwnerID, note.FriendID).
		Updates(map[string]any{"remark": note.Remark, "updated_at": note.UpdatedAt}).Error; err != nil {
		return queryError(err)
	}
	return nil
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
	return s.dbc(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(onConflictDoNothing()).Create(&b).Error; err != nil {
			return createError(err)
		}
		low, high := orderedPair(b.BlockerID, b.BlockedID)
		if err := tx.
			Where("tenant_id = ? AND user_low_id = ? AND user_high_id = ?", b.TenantID, low, high).
			Delete(&messaging.Friendship{}).Error; err != nil {
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
