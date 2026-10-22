package messaging

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/shibingli/ydx/errs"
)

// friendRequestWriteError preserves a lost compare-and-set as a 409 instead of a store outage. |
// friendRequestWriteError 把输掉的比较并交换保留为 409，而不是存储故障。
func friendRequestWriteError(err error) error {
	if errors.Is(err, ErrRequestClosed) {
		return ErrRequestClosed
	}
	return errs.Wrap(err, ErrUnavailable)
}

// FriendSummary returns the whole friend page: accepted friends, both request directions and
// blocks. | FriendSummary 返回完整好友页数据：已接受好友、两个方向的申请与拉黑。
func (s *Service) FriendSummary(ctx context.Context, actor Actor) (FriendSummary, error) {
	if !actor.valid() {
		return FriendSummary{}, ErrInvalidInput
	}
	friendships, err := s.store.ListFriendships(ctx, actor.TenantID, actor.ID)
	if err != nil {
		return FriendSummary{}, errs.Wrap(err, ErrUnavailable)
	}

	ids := make([]string, 0, len(friendships))
	for _, f := range friendships {
		if f.UserLowID == actor.ID {
			ids = append(ids, f.UserHighID)
		} else {
			ids = append(ids, f.UserLowID)
		}
	}
	profiles, err := s.directory.Profiles(ctx, actor.TenantID, ids)
	if err != nil {
		return FriendSummary{}, errs.Wrap(err, ErrUnavailable)
	}

	summary := FriendSummary{Friends: make([]FriendView, 0, len(friendships))}
	for _, f := range friendships {
		friendID := f.UserHighID
		if f.UserLowID != actor.ID {
			friendID = f.UserLowID
		}
		profile, ok := profiles[friendID]
		if !ok {
			continue
		}
		view := FriendView{Profile: profile, Since: f.CreatedAt}
		if note, err := s.store.GetFriendNote(ctx, actor.TenantID, actor.ID, friendID); err == nil {
			view.Remark = note.Remark
		} else if !isNotFound(err) {
			return FriendSummary{}, errs.Wrap(err, ErrUnavailable)
		}
		summary.Friends = append(summary.Friends, view)
	}
	sort.SliceStable(summary.Friends, func(i, j int) bool {
		return strings.ToLower(summary.Friends[i].Display()) < strings.ToLower(summary.Friends[j].Display())
	})

	incoming, err := s.store.ListFriendRequests(ctx, actor.TenantID, actor.ID, true, FriendPending)
	if err != nil {
		return FriendSummary{}, errs.Wrap(err, ErrUnavailable)
	}
	outgoing, err := s.store.ListFriendRequests(ctx, actor.TenantID, actor.ID, false, FriendPending)
	if err != nil {
		return FriendSummary{}, errs.Wrap(err, ErrUnavailable)
	}
	summary.Incoming, err = s.requestViews(ctx, actor, incoming, true)
	if err != nil {
		return FriendSummary{}, err
	}
	summary.Outgoing, err = s.requestViews(ctx, actor, outgoing, false)
	if err != nil {
		return FriendSummary{}, err
	}

	blocks, err := s.store.ListBlocks(ctx, actor.TenantID, actor.ID)
	if err != nil {
		return FriendSummary{}, errs.Wrap(err, ErrUnavailable)
	}
	blockedIDs := make([]string, 0, len(blocks))
	for _, b := range blocks {
		blockedIDs = append(blockedIDs, b.BlockedID)
	}
	blocked, err := s.directory.Profiles(ctx, actor.TenantID, blockedIDs)
	if err != nil {
		return FriendSummary{}, errs.Wrap(err, ErrUnavailable)
	}
	summary.Blocked = make([]Profile, 0, len(blockedIDs))
	for _, id := range blockedIDs {
		if profile, ok := blocked[id]; ok {
			summary.Blocked = append(summary.Blocked, profile)
		}
	}
	return summary, nil
}

// requestViews resolves both sides of a batch of friend requests. | requestViews 解析一批好友申请的双方资料。
func (s *Service) requestViews(ctx context.Context, actor Actor, list []FriendRequest, incoming bool) ([]FriendRequestView, error) {
	if len(list) == 0 {
		return []FriendRequestView{}, nil
	}
	ids := make([]string, 0, len(list)*2)
	for _, req := range list {
		ids = append(ids, req.RequesterID, req.AddresseeID)
	}
	profiles, err := s.directory.Profiles(ctx, actor.TenantID, ids)
	if err != nil {
		return nil, errs.Wrap(err, ErrUnavailable)
	}
	out := make([]FriendRequestView, 0, len(list))
	for _, req := range list {
		out = append(out, FriendRequestView{
			FriendRequest: req,
			Requester:     profiles[req.RequesterID],
			Addressee:     profiles[req.AddresseeID],
			Incoming:      incoming,
		})
	}
	return out, nil
}

// SendFriendRequest creates a pending request to another active user of the same tenant. |
// SendFriendRequest 向同一租户内的另一个活跃用户发起待处理申请。
func (s *Service) SendFriendRequest(ctx context.Context, actor Actor, targetID, message string) (FriendRequest, error) {
	if !actor.valid() {
		return FriendRequest{}, ErrInvalidInput
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return FriendRequest{}, ErrInvalidInput
	}
	if targetID == actor.ID {
		return FriendRequest{}, ErrSelfOperation
	}
	if _, err := s.requireActiveUser(ctx, actor.TenantID, targetID); err != nil {
		return FriendRequest{}, err
	}
	blocked, err := s.store.EitherBlocked(ctx, actor.TenantID, actor.ID, targetID)
	if err != nil {
		return FriendRequest{}, errs.Wrap(err, ErrUnavailable)
	}
	if blocked {
		return FriendRequest{}, ErrBlocked
	}
	if _, err := s.store.GetFriendship(ctx, actor.TenantID, actor.ID, targetID); err == nil {
		return FriendRequest{}, errs.Detail(ErrDuplicateRequest, "already friends | 已经是好友")
	} else if !isNotFound(err) {
		return FriendRequest{}, errs.Wrap(err, ErrUnavailable)
	}

	reverse, err := s.store.FriendRequestBetween(ctx, actor.TenantID, targetID, actor.ID)
	if err == nil && reverse.Status == FriendPending {
		return FriendRequest{}, errs.Detail(ErrDuplicateRequest, "the other side already sent a request | 对方已发来申请")
	}
	if err != nil && !isNotFound(err) {
		return FriendRequest{}, errs.Wrap(err, ErrUnavailable)
	}

	message = truncate(strings.TrimSpace(message), 255)
	now := s.options.Now()
	existing, err := s.store.FriendRequestBetween(ctx, actor.TenantID, actor.ID, targetID)
	switch {
	case err == nil:
		if existing.Status == FriendPending {
			return FriendRequest{}, ErrDuplicateRequest
		}
		expectedStatus := existing.Status
		existing.Status = FriendPending
		existing.Message = message
		existing.CreatedAt = now
		existing.RespondedAt = nil
		if err := s.store.UpdateFriendRequest(ctx, existing, expectedStatus); err != nil {
			return FriendRequest{}, friendRequestWriteError(err)
		}
		return s.friendRequestAfterWrite(ctx, actor, existing, targetID)
	case isNotFound(err):
		id, err := s.options.NewID("fr_")
		if err != nil {
			return FriendRequest{}, err
		}
		req := FriendRequest{
			CreatedAt:   now,
			ID:          id,
			TenantID:    actor.TenantID,
			RequesterID: actor.ID,
			AddresseeID: targetID,
			Message:     message,
			Status:      FriendPending,
		}
		if err := s.store.CreateFriendRequest(ctx, req); err != nil {
			return FriendRequest{}, errs.Wrap(err, ErrUnavailable)
		}
		return s.friendRequestAfterWrite(ctx, actor, req, targetID)
	default:
		return FriendRequest{}, errs.Wrap(err, ErrUnavailable)
	}
}

// friendRequestAfterWrite emits the notification and returns the stored request. | friendRequestAfterWrite 发出通知并返回已存申请。
func (s *Service) friendRequestAfterWrite(ctx context.Context, actor Actor, req FriendRequest, targetID string) (FriendRequest, error) {
	s.emit(ctx, Event{
		Type:     EventFriendRequested,
		TenantID: actor.TenantID,
		ActorID:  actor.ID,
		Audience: []string{targetID},
	})
	return req, nil
}

// RespondFriendRequest accepts or rejects an incoming pending request. | RespondFriendRequest 接受或拒绝收到的待处理申请。
func (s *Service) RespondFriendRequest(ctx context.Context, actor Actor, requestID string, accept bool) error {
	if !actor.valid() {
		return ErrInvalidInput
	}
	req, err := s.store.GetFriendRequest(ctx, actor.TenantID, strings.TrimSpace(requestID))
	if err != nil {
		return mapStoreError(err, ErrNotFound)
	}
	if req.AddresseeID != actor.ID {
		return ErrForbidden
	}
	if req.Status != FriendPending {
		return errs.Detail(ErrInvalidInput, "request is not pending | 申请不在待处理状态")
	}
	now := s.options.Now()
	req.RespondedAt = &now
	if !accept {
		expectedStatus := req.Status
		req.Status = FriendRejected
		if err := s.store.UpdateFriendRequest(ctx, req, expectedStatus); err != nil {
			return friendRequestWriteError(err)
		}
		s.emit(ctx, Event{Type: EventFriendRejected, TenantID: actor.TenantID, ActorID: actor.ID, Audience: []string{req.RequesterID}})
		return nil
	}

	req.Status = FriendAccepted
	friendshipID, err := s.options.NewID("fs_")
	if err != nil {
		return err
	}
	low, high := canonicalPair(req.RequesterID, req.AddresseeID)
	friendship := Friendship{
		CreatedAt:  now,
		ID:         friendshipID,
		TenantID:   actor.TenantID,
		UserLowID:  low,
		UserHighID: high,
	}
	if err := s.store.AcceptFriendRequest(ctx, req, friendship); err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventFriendAccepted, TenantID: actor.TenantID, ActorID: actor.ID, Audience: []string{req.RequesterID}})
	return nil
}

// CancelFriendRequest withdraws an outgoing pending request. | CancelFriendRequest 撤回自己发出的待处理申请。
func (s *Service) CancelFriendRequest(ctx context.Context, actor Actor, requestID string) error {
	if !actor.valid() {
		return ErrInvalidInput
	}
	req, err := s.store.GetFriendRequest(ctx, actor.TenantID, strings.TrimSpace(requestID))
	if err != nil {
		return mapStoreError(err, ErrNotFound)
	}
	if req.RequesterID != actor.ID {
		return ErrForbidden
	}
	if req.Status != FriendPending {
		return errs.Detail(ErrInvalidInput, "request is not pending | 申请不在待处理状态")
	}
	now := s.options.Now()
	expectedStatus := req.Status
	req.Status = FriendCancelled
	req.RespondedAt = &now
	if err := s.store.UpdateFriendRequest(ctx, req, expectedStatus); err != nil {
		return friendRequestWriteError(err)
	}
	s.emit(ctx, Event{Type: EventFriendRejected, TenantID: actor.TenantID, ActorID: actor.ID, Audience: []string{req.AddresseeID}})
	return nil
}

// RemoveFriend deletes an accepted friendship. | RemoveFriend 删除已接受的好友关系。
func (s *Service) RemoveFriend(ctx context.Context, actor Actor, friendID string) error {
	if !actor.valid() {
		return ErrInvalidInput
	}
	if friendID == actor.ID {
		return ErrSelfOperation
	}
	if err := s.store.DeleteFriendship(ctx, actor.TenantID, actor.ID, friendID); err != nil {
		return mapStoreError(err, ErrNotFound)
	}
	s.emit(ctx, Event{Type: EventFriendRemoved, TenantID: actor.TenantID, ActorID: actor.ID, Audience: []string{friendID}})
	return nil
}

// BlockUser blocks another user, drops the friendship and closes pending requests. | BlockUser 拉黑另一用户，
// 删除好友关系并关闭待处理申请。
func (s *Service) BlockUser(ctx context.Context, actor Actor, targetID string) error {
	if !actor.valid() {
		return ErrInvalidInput
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == actor.ID {
		return ErrSelfOperation
	}
	if _, err := s.requireActiveUser(ctx, actor.TenantID, targetID); err != nil {
		return err
	}
	block := Block{
		ID:        mustID(s, "bk_"),
		CreatedAt: s.options.Now(),
		TenantID:  actor.TenantID,
		BlockerID: actor.ID,
		BlockedID: targetID,
	}
	if err := s.store.BlockUser(ctx, block); err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	s.emit(ctx, Event{Type: EventFriendBlocked, TenantID: actor.TenantID, ActorID: actor.ID, Audience: []string{targetID}})
	return nil
}

// UnblockUser removes one direction of a block. | UnblockUser 解除一个方向的拉黑。
func (s *Service) UnblockUser(ctx context.Context, actor Actor, targetID string) error {
	if !actor.valid() {
		return ErrInvalidInput
	}
	if err := s.store.DeleteBlock(ctx, actor.TenantID, actor.ID, strings.TrimSpace(targetID)); err != nil {
		return mapStoreError(err, ErrNotFound)
	}
	return nil
}

// SetFriendRemark stores the actor's alias for an accepted friend. | SetFriendRemark 保存主体对已接受好友的备注。
func (s *Service) SetFriendRemark(ctx context.Context, actor Actor, friendID, remark string) error {
	if !actor.valid() {
		return ErrInvalidInput
	}
	if _, err := s.store.GetFriendship(ctx, actor.TenantID, actor.ID, friendID); err != nil {
		return mapStoreError(err, ErrNotFriend)
	}
	note := FriendNote{
		ID:        mustID(s, "fn_"),
		UpdatedAt: s.options.Now(),
		TenantID:  actor.TenantID,
		OwnerID:   actor.ID,
		FriendID:  friendID,
		Remark:    truncate(strings.TrimSpace(remark), 128),
	}
	if err := s.store.SetFriendNote(ctx, note); err != nil {
		return mapStoreError(err, ErrNotFriend)
	}
	return nil
}

// IsFriend reports an accepted friendship. | IsFriend 报告是否存在已接受好友关系。
func (s *Service) IsFriend(ctx context.Context, tenantID, userA, userB string) (bool, error) {
	if _, err := s.store.GetFriendship(ctx, tenantID, userA, userB); err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, errs.Wrap(err, ErrUnavailable)
	}
	return true, nil
}

// requireFriend enforces the friendship rule for direct messages. | requireFriend 为单聊强制好友规则。
func (s *Service) requireFriend(ctx context.Context, tenantID, userA, userB string) error {
	if userA == userB {
		return ErrSelfOperation
	}
	blocked, err := s.store.EitherBlocked(ctx, tenantID, userA, userB)
	if err != nil {
		return errs.Wrap(err, ErrUnavailable)
	}
	if blocked {
		return ErrBlocked
	}
	ok, err := s.IsFriend(ctx, tenantID, userA, userB)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFriend
	}
	return nil
}

// requireActiveUser resolves one active profile or reports not-found. | requireActiveUser 解析一个活跃资料，缺失时报告未找到。
func (s *Service) requireActiveUser(ctx context.Context, tenantID, userID string) (Profile, error) {
	profiles, err := s.directory.Profiles(ctx, tenantID, []string{userID})
	if err != nil {
		return Profile{}, errs.Wrap(err, ErrUnavailable)
	}
	profile, ok := profiles[userID]
	if !ok || !profile.Active {
		return Profile{}, ErrNotFound
	}
	return profile, nil
}

// canonicalPair orders two user ids lexicographically. | canonicalPair 按字典序排列两个用户 ID。
func canonicalPair(a, b string) (low, high string) {
	if a < b {
		return a, b
	}
	return b, a
}

// truncate caps a string by bytes without splitting a UTF-8 rune. | truncate 按字节截断且不切断 UTF-8 符文。
func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && (value[limit]&0xC0) == 0x80 {
		limit--
	}
	return value[:limit]
}
