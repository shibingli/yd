package gormstore

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/shibingli/yd/security/auth"
	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/log"
)

// CreateUser stores a new global user together with its memberships. | CreateUser 存入新的全局用户及其成员关系。
//
// SQL | SQL:
//
//	SELECT count(*) FROM yd_auth_tenants WHERE id = ?
//	SELECT count(*) FROM yd_auth_users WHERE id = ?
//	SELECT count(*) FROM yd_auth_users WHERE username_key = ?
//	INSERT INTO yd_auth_users (...) VALUES (...)
//	INSERT INTO yd_auth_user_tenants (...) VALUES (...)
//
// The username is unique across every tenant, so the third statement has no
// tenant_id in its WHERE clause — that is the one deliberate exception to the
// tenant-first rule, and it is what makes a tenant-less sign-in unambiguous. |
// 登录名在所有租户之间唯一，因此第三条语句的 WHERE 不含 tenant_id —— 这是「租户打头」
// 规则唯一刻意的例外，也正是无租户登录得以无歧义的原因。
func (s *Store) CreateUser(ctx context.Context, u *auth.User) error {
	if u == nil || u.ID == "" {
		return errs.Detail(auth.ErrInvalidInput, "user requires an id | 用户必须带 ID")
	}
	if strings.TrimSpace(u.TenantID) == "" {
		return auth.ErrTenantRequired
	}
	if strings.TrimSpace(u.Username) == "" {
		return errs.Detail(auth.ErrInvalidInput, "user requires a username | 用户必须带用户名")
	}
	row, err := userToRow(u)
	if err != nil {
		return err
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	return s.transaction(ctx, []string{tableUsers, tableUserTenants}, func(tx *gorm.DB) error {
		known, err := existsTenant(tx, row.TenantID)
		if err != nil {
			return err
		}
		if !known {
			// A user outside a known tenant is unreachable, so the write is refused. | 不属于已知租户的用户不可达，因此拒绝写入。
			return auth.ErrNotFound
		}
		defaultIsMember := row.DefaultTenantID == row.TenantID
		for _, tenantID := range u.TenantIDs {
			if tenantID == "" || tenantID == row.TenantID {
				continue
			}
			known, err := existsTenant(tx, tenantID)
			if err != nil {
				return err
			}
			if !known {
				return auth.ErrNotFound
			}
			if tenantID == row.DefaultTenantID {
				defaultIsMember = true
			}
		}
		if !defaultIsMember {
			return auth.ErrNotFound
		}
		if err := lockRoleNames(tx, row.TenantID, u.Roles); err != nil {
			return err
		}
		dup, err := countRows(tx, &userRow{}, "id = ?", row.ID)
		if err != nil {
			return err
		}
		if dup > 0 {
			return errs.Detail(auth.ErrConflict, "user id %[1]q already exists | 用户 ID %[1]q 已存在", row.ID)
		}
		dup, err = countRows(tx, &userRow{}, "username_key = ?", row.UsernameKey)
		if err != nil {
			return err
		}
		if dup > 0 {
			return errs.Detail(auth.ErrConflict, "username %[1]q already exists | 用户名 %[1]q 已存在", row.Username)
		}
		if err := tx.Create(&row).Error; err != nil {
			if isDuplicate(err) {
				s.log.Debug("the unique index rejected a duplicate user | 唯一索引拒绝了重复用户",
					log.Str("user_id", row.ID))
				return errs.WrapDetail(err, auth.ErrConflict, "user id or username already exists | 用户 ID 或用户名已存在")
			}
			return err
		}
		if err := createMembership(tx, row.ID, row.TenantID, u.Roles, row.DefaultTenantID == row.TenantID, u.CreatedAt); err != nil {
			return err
		}
		for _, tenantID := range u.TenantIDs {
			if tenantID == "" || tenantID == row.TenantID {
				continue
			}
			known, err := existsTenant(tx, tenantID)
			if err != nil {
				return err
			}
			if !known {
				return auth.ErrNotFound
			}
			if err := createMembership(tx, row.ID, tenantID, nil, row.DefaultTenantID == tenantID, u.CreatedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

// createMembership inserts one membership row. | createMembership 插入一条成员关系行。
func createMembership(tx *gorm.DB, userID, tenantID string, roles []string, isDefault bool, createdAt time.Time) error {
	row, err := userTenantToRow(userID, tenantID, roles, isDefault, createdAt)
	if err != nil {
		return err
	}
	if err := tx.Create(&row).Error; err != nil {
		if isDuplicate(err) {
			return errs.WrapDetail(err, auth.ErrConflict, "the user already belongs to that tenant | 用户已属于该租户")
		}
		return err
	}
	return nil
}

// GetUser returns the user acting in that tenant. | GetUser 返回在该租户中活动的用户。
//
// SQL | SQL:
//
//	SELECT * FROM yd_auth_users WHERE id = ? ORDER BY yd_auth_users.id LIMIT 1
//	SELECT * FROM yd_auth_user_tenants WHERE user_id = ? AND tenant_id = ?
//	SELECT * FROM yd_auth_user_tenants WHERE user_id = ?
//
// A user who is not a member of the tenant misses the membership lookup and is
// reported as auth.ErrNotFound, exactly like a user who never existed. | 不是该租户成员的用户
// 无法命中成员关系查询，报 auth.ErrNotFound，与从不存在的用户完全一致。
func (s *Store) GetUser(ctx context.Context, tenantID, userID string) (*auth.User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	var row userRow
	if err := s.db.WithContext(ctx).Where("id = ?", userID).First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return s.userView(s.db.WithContext(ctx), &row, tenantID)
}

// GetUserByUsername returns the user of that tenant by username. | GetUserByUsername 按用户名返回该租户下的用户。
//
// SQL | SQL:
//
//	SELECT * FROM yd_auth_users WHERE username_key = ? ORDER BY yd_auth_users.id LIMIT 1
//	SELECT * FROM yd_auth_user_tenants WHERE user_id = ? AND tenant_id = ?
//
// The lookup goes through username_key, the folded copy that
// yd_idx_auth_users_username is unique on, so "Alice" and "alice" are one account
// across the whole deployment. | 查询走 username_key —— yd_idx_auth_users_username 在其上唯一的
// 折叠副本，因此 "Alice" 与 "alice" 在整个部署里是同一个账号。
func (s *Store) GetUserByUsername(ctx context.Context, tenantID, username string) (*auth.User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	var row userRow
	if err := s.db.WithContext(ctx).Where("username_key = ?", keyOf(username)).First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return s.userView(s.db.WithContext(ctx), &row, tenantID)
}

// GetUserByName returns the globally unique user by sign-in name, acting in the
// user's default tenant. | GetUserByName 按全局唯一的登录名返回用户，并以默认租户作为活动租户。
func (s *Store) GetUserByName(ctx context.Context, username string) (*auth.User, error) {
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	var row userRow
	if err := s.db.WithContext(ctx).Where("username_key = ?", keyOf(username)).First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return s.userView(s.db.WithContext(ctx), &row, defaultTenantOf(&row))
}

// GetUserByID returns the user by global id, acting in the user's default tenant. | GetUserByID 按全局 ID 返回用户，并以默认租户作为活动租户。
func (s *Store) GetUserByID(ctx context.Context, userID string) (*auth.User, error) {
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	var row userRow
	if err := s.db.WithContext(ctx).Where("id = ?", userID).First(&row).Error; err != nil {
		return nil, notFound(err)
	}
	return s.userView(s.db.WithContext(ctx), &row, defaultTenantOf(&row))
}

// defaultTenantOf returns the default tenant of a row, falling back to the home tenant. | defaultTenantOf 返回行的默认租户，缺失时回退到身份租户。
func defaultTenantOf(row *userRow) string {
	if row == nil {
		return ""
	}
	if row.DefaultTenantID != "" {
		return row.DefaultTenantID
	}
	return row.TenantID
}

// userView materialises one user in tenantID and loads its memberships. | userView 在 tenantID 中构造用户并载入其全部成员关系。
func (s *Store) userView(tx *gorm.DB, row *userRow, tenantID string) (*auth.User, error) {
	var membership userTenantRow
	err := tx.Where("user_id = ? AND tenant_id = ?", row.ID, tenantID).First(&membership).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && tenantID == defaultTenantOf(row) && tenantID != row.TenantID {
		// A corrupt default membership must not lock the account out; fall back to the home tenant,
		// matching the memory store until an explicit SetDefaultTenant repairs the choice. |
		// 损坏的默认成员关系不得把账号锁在门外；回退到身份租户，与内存实现一致，直到显式
		// SetDefaultTenant 修正。
		tenantID = row.TenantID
		err = tx.Where("user_id = ? AND tenant_id = ?", row.ID, tenantID).First(&membership).Error
	}
	if err != nil {
		return nil, notFound(err)
	}
	roles, err := membershipFromRow(&membership)
	if err != nil {
		return nil, err
	}
	var all []userTenantRow
	if err := tx.Where("user_id = ?", row.ID).Find(&all).Error; err != nil {
		return nil, err
	}
	u, err := userFromRow(row)
	if err != nil {
		return nil, err
	}
	u.TenantID = tenantID
	u.Roles = roles
	u.TenantIDs = make([]string, 0, len(all))
	for i := range all {
		u.TenantIDs = append(u.TenantIDs, all[i].TenantID)
	}
	return u, nil
}

// ListUsers returns every member of the tenant ordered by creation time and then by id. | ListUsers 按创建时间、再按 ID 返回该租户的全部成员。
//
// SQL | SQL:
//
//	SELECT count(*) FROM yd_auth_tenants WHERE id = ?
//	SELECT user_id FROM yd_auth_user_tenants WHERE tenant_id = ? ORDER BY created_at ASC, user_id ASC
//	SELECT * FROM yd_auth_users WHERE id IN (...)
//	SELECT * FROM yd_auth_user_tenants WHERE user_id IN (...)
func (s *Store) ListUsers(ctx context.Context, tenantID string) ([]*auth.User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	db := s.db.WithContext(ctx)
	known, err := existsTenant(db, tenantID)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, auth.ErrNotFound
	}
	var links []userTenantRow
	if err := db.Where("tenant_id = ?", tenantID).Order("created_at ASC, user_id ASC").Find(&links).Error; err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return []*auth.User{}, nil
	}
	ids := make([]string, 0, len(links))
	for i := range links {
		ids = append(ids, links[i].UserID)
	}
	var rows []userRow
	if err := db.Where("id IN ?", ids).Order("created_at ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	memberships, err := loadMemberships(db, ids)
	if err != nil {
		return nil, err
	}
	rolesByTenant := make(map[string][]string, len(links))
	for i := range links {
		roles, err := membershipFromRow(&links[i])
		if err != nil {
			return nil, err
		}
		rolesByTenant[links[i].UserID] = roles
	}
	out := make([]*auth.User, 0, len(rows))
	for i := range rows {
		u, err := userFromRow(&rows[i])
		if err != nil {
			return nil, err
		}
		u.TenantID = tenantID
		u.Roles = rolesByTenant[u.ID]
		u.TenantIDs = membershipTenants(memberships[u.ID])
		out = append(out, u)
	}
	return out, nil
}

// GetUsers resolves a batch of users visible in tenantID in three statements. | GetUsers 用三条语句解析 tenantID 内可见的一批用户。
//
// SQL | SQL:
//
//	SELECT count(*) FROM yd_auth_tenants WHERE id = ?
//	SELECT * FROM yd_auth_user_tenants WHERE tenant_id = ? AND user_id IN (...)
//	SELECT * FROM yd_auth_users WHERE id IN (...)
//	SELECT * FROM yd_auth_user_tenants WHERE user_id IN (...)
//
// 旧路径让调用方对每个 ID 各走一次 GetUser；批量版本把往返次数固定为四条，与 ID 数量无关。 |
// The old path made callers run GetUser once per id; the batch version fixes the round trips at four regardless
// of how many ids are requested.
func (s *Store) GetUsers(ctx context.Context, tenantID string, ids []string) ([]*auth.User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, auth.ErrTenantRequired
	}
	if len(ids) == 0 {
		return []*auth.User{}, nil
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	db := s.db.WithContext(ctx)
	known, err := existsTenant(db, tenantID)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, auth.ErrNotFound
	}
	var links []userTenantRow
	if err := db.Where("tenant_id = ? AND user_id IN ?", tenantID, ids).
		Order("created_at ASC, user_id ASC").Find(&links).Error; err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return []*auth.User{}, nil
	}
	memberIDs := make([]string, 0, len(links))
	for i := range links {
		memberIDs = append(memberIDs, links[i].UserID)
	}
	var rows []userRow
	if err := db.Where("id IN ?", memberIDs).
		Order("created_at ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	memberships, err := loadMemberships(db, memberIDs)
	if err != nil {
		return nil, err
	}
	return usersForTenant(tenantID, rows, links, memberships)
}

// ListUsersPage returns one page of tenant members plus the filtered total. | ListUsersPage 返回租户成员的一页与过滤后的总数。
//
// SQL | SQL:
//
//	SELECT count(*) FROM yd_auth_users JOIN yd_auth_user_tenants ut ... WHERE ...
//	SELECT yd_auth_users.* FROM yd_auth_users JOIN yd_auth_user_tenants ut ... WHERE ... LIMIT ? OFFSET ?
//	SELECT * FROM yd_auth_user_tenants WHERE user_id IN (...)
//
// The keyword is pushed into the WHERE clause and the page into LIMIT/OFFSET: the previous list loaded every
// member and filtered in Go. | 关键字下推到 WHERE，分页下推到 LIMIT/OFFSET：旧列表先载入全部成员再在 Go 里过滤。
func (s *Store) ListUsersPage(ctx context.Context, tenantID, keyword string, limit, offset int) ([]*auth.User, int, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, 0, auth.ErrTenantRequired
	}
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	db := s.db.WithContext(ctx)
	known, err := existsTenant(db, tenantID)
	if err != nil {
		return nil, 0, err
	}
	if !known {
		return nil, 0, auth.ErrNotFound
	}
	base := func() *gorm.DB {
		query := db.Model(&userRow{}).
			Joins("JOIN "+tableUserTenants+" ut ON ut.user_id = "+tableUsers+".id").
			Where("ut.tenant_id = ?", tenantID)
		if folded := strings.ToLower(strings.TrimSpace(keyword)); folded != "" {
			pattern := "%" + sqlLikeEscape(folded) + "%"
			query = query.Where(
				"(LOWER("+tableUsers+".username_key) LIKE ? ESCAPE '!' OR LOWER("+tableUsers+".display_name) LIKE ? ESCAPE '!' OR LOWER("+tableUsers+".email) LIKE ? ESCAPE '!')",
				pattern, pattern, pattern,
			)
		}
		return query
	}
	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []userRow
	if err := base().
		Select(tableUsers + ".*").
		Order(tableUsers + ".created_at DESC, " + tableUsers + ".id DESC").
		Offset(offset).
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	if len(rows) == 0 {
		return []*auth.User{}, int(total), nil
	}
	ids := make([]string, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	memberships, err := loadMemberships(db, ids)
	if err != nil {
		return nil, 0, err
	}
	links := make([]userTenantRow, 0, len(rows))
	for _, id := range ids {
		for _, link := range memberships[id] {
			if link.TenantID == tenantID {
				links = append(links, link)
				break
			}
		}
	}
	users, err := usersForTenant(tenantID, rows, links, memberships)
	if err != nil {
		return nil, 0, err
	}
	return users, int(total), nil
}

// CountUsers returns the number of members of one tenant in one COUNT query. |
// CountUsers 用一条 COUNT 查询返回一个租户的成员数。
func (s *Store) CountUsers(ctx context.Context, tenantID string) (int, error) {
	if strings.TrimSpace(tenantID) == "" {
		return 0, auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	db := s.db.WithContext(ctx)
	known, err := existsTenant(db, tenantID)
	if err != nil {
		return 0, err
	}
	if !known {
		return 0, auth.ErrNotFound
	}
	var count int64
	if err := db.Model(&userTenantRow{}).Where("tenant_id = ?", tenantID).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

// RecentUsers returns the newest members of one tenant without loading the roster. |
// RecentUsers 返回一个租户最新的成员，不加载整份名册。
func (s *Store) RecentUsers(ctx context.Context, tenantID string, limit int) ([]*auth.User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, auth.ErrTenantRequired
	}
	if limit <= 0 {
		limit = 5
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	db := s.db.WithContext(ctx)
	known, err := existsTenant(db, tenantID)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, auth.ErrNotFound
	}
	var links []userTenantRow
	if err := db.Where("tenant_id = ?", tenantID).
		Order("created_at DESC, user_id DESC").
		Limit(limit).
		Find(&links).Error; err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return []*auth.User{}, nil
	}
	ids := make([]string, 0, len(links))
	for i := range links {
		ids = append(ids, links[i].UserID)
	}
	var rows []userRow
	if err := db.Where("id IN ?", ids).Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	memberships, err := loadMemberships(db, ids)
	if err != nil {
		return nil, err
	}
	return usersForTenant(tenantID, rows, links, memberships)
}

// SearchUsers returns active tenant members matching the keyword, ordered by display name. |
// SearchUsers 返回匹配关键字的租户活跃成员，按展示名排序。
//
// SQL | SQL:
//
//	SELECT count(*) FROM yd_auth_tenants WHERE id = ?
//	SELECT yd_auth_users.* FROM yd_auth_users JOIN yd_auth_user_tenants ut ... WHERE ... LIMIT ?
//	SELECT * FROM yd_auth_user_tenants WHERE user_id IN (...)
//
// 关键字在 SQL 内过滤并带 LIMIT：旧实现先载入整张用户表再在内存里过滤排序。 |
// The keyword is filtered in SQL and the limit applied there: the old implementation loaded the whole user table
// before filtering and sorting in memory.
func (s *Store) SearchUsers(ctx context.Context, tenantID, keyword, excludeID string, limit int) ([]*auth.User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, auth.ErrTenantRequired
	}
	if limit <= 0 {
		limit = 20
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	db := s.db.WithContext(ctx)
	known, err := existsTenant(db, tenantID)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, auth.ErrNotFound
	}
	query := db.Model(&userRow{}).
		Select(tableUsers+".*").
		Joins("JOIN "+tableUserTenants+" ut ON ut.user_id = "+tableUsers+".id").
		Where("ut.tenant_id = ? AND "+tableUsers+".status = ?", tenantID, string(auth.StatusActive))
	if excludeID != "" {
		query = query.Where(tableUsers+".id <> ?", excludeID)
	}
	if folded := strings.ToLower(strings.TrimSpace(keyword)); folded != "" {
		pattern := "%" + sqlLikeEscape(folded) + "%"
		query = query.Where(
			"(LOWER("+tableUsers+".username_key) LIKE ? ESCAPE '!' OR LOWER("+tableUsers+".display_name) LIKE ? ESCAPE '!' OR LOWER("+tableUsers+".email) LIKE ? ESCAPE '!')",
			pattern, pattern, pattern,
		)
	}
	var rows []userRow
	if err := query.
		Order("LOWER(COALESCE(NULLIF(" + tableUsers + ".display_name, ''), " + tableUsers + ".username)) ASC, " + tableUsers + ".id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []*auth.User{}, nil
	}
	ids := make([]string, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	memberships, err := loadMemberships(db, ids)
	if err != nil {
		return nil, err
	}
	links := make([]userTenantRow, 0, len(rows))
	for _, id := range ids {
		for _, link := range memberships[id] {
			if link.TenantID == tenantID {
				links = append(links, link)
				break
			}
		}
	}
	return usersForTenant(tenantID, rows, links, memberships)
}

// usersForTenant converts rows into domain users carrying tenant roles and memberships. |
// usersForTenant 把行转换成携带租户角色与成员关系的领域用户。
func usersForTenant(tenantID string, rows []userRow, links []userTenantRow, memberships map[string][]userTenantRow) ([]*auth.User, error) {
	rolesByUser := make(map[string][]string, len(links))
	member := make(map[string]struct{}, len(links))
	for i := range links {
		roles, err := membershipFromRow(&links[i])
		if err != nil {
			return nil, err
		}
		rolesByUser[links[i].UserID] = roles
		member[links[i].UserID] = struct{}{}
	}
	out := make([]*auth.User, 0, len(rows))
	for i := range rows {
		if _, ok := member[rows[i].ID]; !ok {
			continue
		}
		user, err := userFromRow(&rows[i])
		if err != nil {
			return nil, err
		}
		user.TenantID = tenantID
		user.Roles = rolesByUser[user.ID]
		user.TenantIDs = membershipTenants(memberships[user.ID])
		out = append(out, user)
	}
	return out, nil
}

// sqlLikeEscape escapes LIKE metacharacters with '!' as the escape character. | sqlLikeEscape 以 '!' 为转义符转义 LIKE 元字符。
func sqlLikeEscape(value string) string {
	replacer := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	return replacer.Replace(value)
}

// loadMemberships loads every membership of the given users in one query. | loadMemberships 用一次查询载入给定用户的全部成员关系。
func loadMemberships(tx *gorm.DB, userIDs []string) (map[string][]userTenantRow, error) {
	out := make(map[string][]userTenantRow, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	var rows []userTenantRow
	if err := tx.Where("user_id IN ?", userIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		out[rows[i].UserID] = append(out[rows[i].UserID], rows[i])
	}
	return out, nil
}

// membershipTenants returns the sorted tenant ids of a membership list. | membershipTenants 返回成员关系列表中已排序的租户 ID。
func membershipTenants(rows []userTenantRow) []string {
	out := make([]string, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].TenantID)
	}
	return out
}

// UpdateUser replaces the stored profile and the roles inside tenantID. | UpdateUser 覆盖库中资料及在 tenantID 内的角色。
//
// SQL | SQL:
//
//	SELECT * FROM yd_auth_users WHERE id = ? ORDER BY yd_auth_users.id LIMIT 1
//	SELECT * FROM yd_auth_user_tenants WHERE user_id = ? AND tenant_id = ?
//	SELECT count(*) FROM yd_auth_users WHERE username_key = ? AND id <> ?
//	UPDATE yd_auth_users SET ... WHERE id = ?
//	UPDATE yd_auth_user_tenants SET roles = ?, updated_at = ? WHERE user_id = ? AND tenant_id = ?
func (s *Store) UpdateUser(ctx context.Context, tenantID string, u *auth.User) error {
	if strings.TrimSpace(tenantID) == "" {
		return auth.ErrTenantRequired
	}
	if u == nil || u.ID == "" {
		return errs.Detail(auth.ErrInvalidInput, "user requires an id | 用户必须带 ID")
	}
	if u.TenantID != "" && u.TenantID != tenantID {
		return auth.ErrNotFound
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	return s.transaction(ctx, []string{tableUsers, tableUserTenants}, func(tx *gorm.DB) error {
		var stored userRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", u.ID).First(&stored).Error; err != nil {
			return notFound(err)
		}
		// Lock role rows before the membership row. DeleteRole takes the same order (role, then
		// membership), so a role deleted between a service-level validation and this write is seen
		// here instead of being resurrected into the membership JSON. |
		// 先锁角色行再锁成员关系行。DeleteRole 采用相同顺序（角色，再成员关系），因此在服务层校验与本次
		// 写入之间被删除的角色会在这里被发现，而不会被重新写回成员关系的 JSON。
		if err := lockRoleNames(tx, tenantID, u.Roles); err != nil {
			return err
		}
		var membership userTenantRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND tenant_id = ?", u.ID, tenantID).First(&membership).Error; err != nil {
			return notFound(err)
		}
		row, err := userToRow(u)
		if err != nil {
			return err
		}
		// The home and default tenants are identity, never payload: a crafted or
		// stale request cannot move them. | 身份租户与默认租户属于身份而非载荷：构造的或过期的请求
		// 都不能挪动它们。
		row.TenantID = stored.TenantID
		row.DefaultTenantID = stored.DefaultTenantID
		if row.UsernameKey != stored.UsernameKey {
			dup, err := countRows(tx, &userRow{}, "username_key = ? AND id <> ?", row.UsernameKey, row.ID)
			if err != nil {
				return err
			}
			if dup > 0 {
				return errs.Detail(auth.ErrConflict, "username %[1]q already exists | 用户名 %[1]q 已存在", row.Username)
			}
		}
		err = tx.Model(&userRow{}).Where("id = ?", row.ID).Updates(map[string]any{
			"username":     row.Username,
			"username_key": row.UsernameKey,
			"email":        row.Email,
			"display_name": row.DisplayName,
			"updated_at":   row.UpdatedAt,
		}).Error
		if err != nil {
			if isDuplicate(err) {
				s.log.Debug("the unique index rejected a username change | 唯一索引拒绝了用户名变更",
					log.Str("user_id", row.ID))
				return errs.WrapDetail(err, auth.ErrConflict, "username %[1]q already exists | 用户名 %[1]q 已存在", row.Username)
			}
			return err
		}
		encoded, err := encodeRoles(u.Roles)
		if err != nil {
			return err
		}
		return tx.Model(&userTenantRow{}).
			Where("user_id = ? AND tenant_id = ?", u.ID, tenantID).
			Updates(map[string]any{"roles": encoded, "updated_at": row.UpdatedAt}).Error
	})
}

// SetUserStatus changes only the lifecycle status of one tenant member. |
// SetUserStatus 只修改某个租户成员的生命周期状态。
func (s *Store) SetUserStatus(ctx context.Context, tenantID, userID string, status auth.Status, updatedAt time.Time) error {
	return s.setUserColumns(ctx, tenantID, userID, map[string]any{
		"status":     string(status),
		"updated_at": updatedAt,
	})
}

// SetUserCredentials changes the password hash and the sealed sec_key in one statement. |
// SetUserCredentials 用一条语句修改口令散列与封装的 sec_key。
func (s *Store) SetUserCredentials(ctx context.Context, tenantID, userID, passwordHash string, encryptedSecKey []byte, updatedAt time.Time) error {
	return s.setUserColumns(ctx, tenantID, userID, map[string]any{
		"password_hash":     passwordHash,
		"encrypted_sec_key": encryptedSecKey,
		"updated_at":        updatedAt,
	})
}

// SetUserSecKey changes only the sealed sec_key. | SetUserSecKey 只修改封装的 sec_key。
func (s *Store) SetUserSecKey(ctx context.Context, tenantID, userID string, encryptedSecKey []byte, updatedAt time.Time) error {
	return s.setUserColumns(ctx, tenantID, userID, map[string]any{
		"encrypted_sec_key": encryptedSecKey,
		"updated_at":        updatedAt,
	})
}

// setUserColumns locks the user and its membership, then updates only the named columns. |
// setUserColumns 锁定用户与成员关系后只更新指定列。
func (s *Store) setUserColumns(ctx context.Context, tenantID, userID string, columns map[string]any) error {
	if strings.TrimSpace(tenantID) == "" {
		return auth.ErrTenantRequired
	}
	if strings.TrimSpace(userID) == "" {
		return auth.ErrNotFound
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	return s.transaction(ctx, []string{tableUsers}, func(tx *gorm.DB) error {
		var user userRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", userID).First(&user).Error; err != nil {
			return notFound(err)
		}
		var membership userTenantRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND tenant_id = ?", userID, tenantID).First(&membership).Error; err != nil {
			return notFound(err)
		}
		return tx.Model(&userRow{}).Where("id = ?", userID).Updates(columns).Error
	})
}

// ListUserTenants returns every tenant the user belongs to, ordered by creation time. | ListUserTenants 按创建时间返回该用户隶属的全部租户。
//
// SQL | SQL:
//
//	SELECT count(*) FROM yd_auth_users WHERE id = ?
//	SELECT t.* FROM yd_auth_tenants t JOIN yd_auth_user_tenants m ON m.tenant_id = t.id
//	  WHERE m.user_id = ? ORDER BY t.created_at ASC, t.id ASC
func (s *Store) ListUserTenants(ctx context.Context, userID string) ([]*auth.Tenant, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, auth.ErrNotFound
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	db := s.db.WithContext(ctx)
	known, err := countRows(db, &userRow{}, "id = ?", userID)
	if err != nil {
		return nil, err
	}
	if known == 0 {
		return nil, auth.ErrNotFound
	}
	var rows []tenantRow
	err = db.Table(tableTenants).
		Select("yd_auth_tenants.*").
		Joins("JOIN "+tableUserTenants+" m ON m.tenant_id = yd_auth_tenants.id").
		Where("m.user_id = ?", userID).
		Order("yd_auth_tenants.created_at ASC, yd_auth_tenants.id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]*auth.Tenant, 0, len(rows))
	for i := range rows {
		out = append(out, tenantFromRow(&rows[i]))
	}
	return out, nil
}

// AddUserTenant adds a membership, replacing the roles of an existing one. | AddUserTenant 添加成员关系，已存在时覆盖其角色。
//
// SQL | SQL:
//
//	SELECT count(*) FROM yd_auth_users WHERE id = ?
//	SELECT count(*) FROM yd_auth_tenants WHERE id = ?
//	SELECT * FROM yd_auth_user_tenants WHERE user_id = ? AND tenant_id = ?
//	INSERT / UPDATE yd_auth_user_tenants
func (s *Store) AddUserTenant(ctx context.Context, userID, tenantID string, roles []string) error {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(tenantID) == "" {
		return auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	return s.transaction(ctx, []string{tableUserTenants}, func(tx *gorm.DB) error {
		var user userRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", userID).First(&user).Error; err != nil {
			return notFound(err)
		}
		known, err := existsTenant(tx, tenantID)
		if err != nil {
			return err
		}
		if !known {
			return auth.ErrNotFound
		}
		if err := lockRoleNames(tx, tenantID, roles); err != nil {
			return err
		}
		encoded, err := encodeRoles(roles)
		if err != nil {
			return err
		}
		var membership userTenantRow
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND tenant_id = ?", userID, tenantID).First(&membership).Error
		switch {
		case err == nil:
			return tx.Model(&userTenantRow{}).
				Where("user_id = ? AND tenant_id = ?", userID, tenantID).
				Updates(map[string]any{"roles": encoded, "updated_at": time.Now().UTC()}).Error
		case err != nil && !isNotFound(err):
			return err
		}
		row, err := userTenantToRow(userID, tenantID, roles, false, time.Now().UTC())
		if err != nil {
			return err
		}
		row.IsDefault = user.DefaultTenantID == tenantID
		if err := tx.Create(&row).Error; err != nil {
			if isDuplicate(err) {
				return errs.Wrap(err, auth.ErrConflict)
			}
			return err
		}
		return nil
	})
}

// RemoveUserTenant removes a membership that is neither the home nor the default tenant. | RemoveUserTenant 移除既非身份租户也非默认租户的成员关系。
func (s *Store) RemoveUserTenant(ctx context.Context, userID, tenantID string) error {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(tenantID) == "" {
		return auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	return s.transaction(ctx, []string{tableUserTenants}, func(tx *gorm.DB) error {
		var user userRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", userID).First(&user).Error; err != nil {
			return notFound(err)
		}
		var membership userTenantRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND tenant_id = ?", userID, tenantID).First(&membership).Error; err != nil {
			return notFound(err)
		}
		if tenantID == user.TenantID || membership.IsDefault || user.DefaultTenantID == tenantID {
			return errs.Detail(auth.ErrConflict, "the home and default tenants cannot be removed | 身份租户与默认租户不可移除")
		}
		return tx.Where("user_id = ? AND tenant_id = ?", userID, tenantID).Delete(&userTenantRow{}).Error
	})
}

// SetDefaultTenant makes an existing membership the sign-in default. | SetDefaultTenant 把已有成员关系设为登录默认租户。
func (s *Store) SetDefaultTenant(ctx context.Context, userID, tenantID string) error {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(tenantID) == "" {
		return auth.ErrTenantRequired
	}
	ctx, cancel := s.scoped(ctx)
	defer cancel()
	return s.transaction(ctx, []string{tableUserTenants, tableUsers}, func(tx *gorm.DB) error {
		var user userRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", userID).First(&user).Error; err != nil {
			return notFound(err)
		}
		var membership userTenantRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND tenant_id = ?", userID, tenantID).First(&membership).Error; err != nil {
			return notFound(err)
		}
		// Lock every membership of the user before clearing the flags, so a concurrent membership
		// removal cannot leave default_tenant_id pointing at a row that no longer exists. |
		// 清空标记前锁定该用户的全部成员关系，避免并发移除成员关系后 default_tenant_id 指向已不存在的行。
		var memberships []userTenantRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ?", userID).Order("tenant_id ASC").Find(&memberships).Error; err != nil {
			return err
		}
		if defaultTenantOf(&user) == tenantID && membership.IsDefault {
			return nil
		}
		now := time.Now().UTC()
		if err := tx.Model(&userTenantRow{}).Where("user_id = ?", userID).
			Updates(map[string]any{"is_default": false, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&userTenantRow{}).Where("user_id = ? AND tenant_id = ?", userID, tenantID).
			Updates(map[string]any{"is_default": true, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&userRow{}).Where("id = ?", userID).
			Updates(map[string]any{"default_tenant_id": tenantID, "updated_at": now}).Error
	})
}

// lockRoleNames locks every named role of the tenant in a stable order and reports an unknown name.
// The shared locks serialise role assignment with DeleteRole; sorting prevents two writers with
// different role sets from deadlocking. | lockRoleNames 按稳定顺序锁定租户内的每个命名角色，并报告未知角色。
// 共享锁让角色分配与 DeleteRole 互斥；排序避免两个写入者因角色集合不同而死锁。
func lockRoleNames(tx *gorm.DB, tenantID string, names []string) error {
	seen := make(map[string]struct{}, len(names))
	unique := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		unique = append(unique, name)
	}
	if len(unique) == 0 {
		return nil
	}
	sort.Strings(unique)
	var rows []roleRow
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("name").
		Where("tenant_id = ? AND name IN ?", tenantID, unique).
		Order("name ASC").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) != len(unique) {
		return auth.ErrNotFound
	}
	return nil
}

// isNotFound reports whether err is GORM's record-not-found sentinel. | isNotFound 报告 err 是否为 GORM 的记录不存在哨兵。
func isNotFound(err error) bool {
	return err != nil && err == gorm.ErrRecordNotFound
}
