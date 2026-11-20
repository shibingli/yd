// Package gormstore is the GORM-backed messaging.Store. | Package gormstore 是基于 GORM 的 messaging.Store。
//
// The schema is owned here: Migrate creates yd_msg_types, yd_msg_tags, yd_msg_conversations,
// yd_msg_members, yd_msg_messages, yd_msg_message_tags, yd_msg_friend_requests, yd_msg_friendships,
// yd_msg_friend_notes, yd_msg_blocks and yd_msg_audit, every one of them tenant-first. Attachments
// live in the separate attachments domain (yd_attachments and yd_attachment_refs). Migrate is
// idempotent and safe to run on every start. | 模式由本包拥有：Migrate 创建上述十一张表，所有索引都以
// 租户为第一列；附件属于独立的附件域（yd_attachments 与 yd_attachment_refs）。Migrate 幂等，可在每次
// 启动时运行。
package gormstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	"github.com/shibingli/yd/messaging"
	"github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Sentinel errors of the store. | 存储层的哨兵错误。
var (
	// ErrMigrate reports a failed schema migration. | ErrMigrate 表示模式迁移失败。
	ErrMigrate = errs.New("message schema migration failed | 消息数据表迁移失败")
	// ErrQuery reports a failed statement. | ErrQuery 表示语句执行失败。
	ErrQuery = errs.New("message statement failed | 消息语句执行失败")
	// ErrConflict reports a unique-key conflict on a message row. | ErrConflict 表示消息记录的唯一键冲突。
	ErrConflict = errs.New("message row already exists | 消息记录已存在")
)

// Store implements messaging.Store over GORM. | Store 基于 GORM 实现 messaging.Store。
type Store struct {
	// invalidator invalidates the GORM query cache after commits; nil disables it. |
	// invalidator 在提交后使 GORM 查询缓存失效；nil 表示禁用。
	invalidator Invalidator
	// db is the shared connection pool. | db 是共享连接池。
	db *gorm.DB
	// log records post-commit invalidation failures. | log 记录提交后失效失败。
	log *ydlog.Logger
	// invalidatorMu guards the late-bound invalidator. | invalidatorMu 保护后置绑定的失效器。
	invalidatorMu sync.RWMutex
}

// New returns a store over db. | New 返回基于 db 的存储。
func New(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, ErrQuery
	}
	return &Store{db: db, log: ydlog.Default()}, nil
}

// Migrate creates or updates every message table. | Migrate 创建或更新全部消息表。
func Migrate(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return ErrMigrate
	}
	models := []any{
		&messaging.MessageType{},
		&messaging.Tag{},
		&messaging.Conversation{},
		&messaging.Member{},
		&messaging.Message{},
		&messaging.MessageTag{},
		&messaging.FriendRequest{},
		&messaging.Friendship{},
		&messaging.FriendNote{},
		&messaging.Block{},
		&messaging.AuditEntry{},
	}
	if err := db.WithContext(ctx).AutoMigrate(models...); err != nil {
		return errs.Wrap(err, ErrMigrate)
	}
	return nil
}

// dbc returns a context-bound handle. | dbc 返回绑定上下文的句柄。
func (s *Store) dbc(ctx context.Context) *gorm.DB { return s.db.WithContext(ctx) }

// notFound maps gorm.ErrRecordNotFound onto messaging.ErrNotFound. | notFound 把 gorm.ErrRecordNotFound 映射为 messaging.ErrNotFound。
func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errs.Wrap(err, messaging.ErrNotFound)
	}
	return nil
}

// queryError maps a statement error, preserving not-found. | queryError 映射语句错误，并保留未找到语义。
func queryError(err error) error {
	if err == nil {
		return nil
	}
	if mapped := notFound(err); mapped != nil {
		return mapped
	}
	return errs.Wrap(err, ErrQuery)
}

// createError maps a statement error, preserving not-found and mapping duplicate keys onto an explicit
// conflict sentinel. | createError 映射语句错误，保留未找到语义，并把重复键映射为明确的冲突哨兵。
func createError(err error) error {
	if err == nil {
		return nil
	}
	if mapped := notFound(err); mapped != nil {
		return mapped
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return errs.Wrap(err, ErrConflict)
	}
	return errs.Wrap(err, ErrQuery)
}

// deadlockRetries bounds the retry of a transaction that lost to a database deadlock. |
// deadlockRetries 限制输给数据库死锁的事务重试次数。
const deadlockRetries = 3

// retryTransaction runs fn in a transaction, retrying a bounded number of times when the engine reports
// a deadlock or lock-wait conflict. The classification uses driver-provided interfaces instead of
// matching error strings. | retryTransaction 在事务中执行 fn；引擎报告死锁或锁等待冲突时做有限重试。
// 分类依赖驱动提供的接口，不做错误字符串匹配。
func (s *Store) retryTransaction(ctx context.Context, tables []string, fn func(*gorm.DB) error) error {
	var err error
	for attempt := 0; attempt < deadlockRetries; attempt++ {
		err = s.transaction(ctx, tables, fn)
		if !isRetryableLockConflict(err) {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	return err
}

// isRetryableLockConflict reports engine deadlocks and lock-wait timeouts. MySQL exposes Number(),
// PostgreSQL exposes SQLState() and the pure-Go SQLite driver exposes Code(); none of them requires
// importing the driver into this package. | isRetryableLockConflict 报告引擎死锁与锁等待超时。
// MySQL 暴露 Number()，PostgreSQL 暴露 SQLState()，纯 Go SQLite 驱动暴露 Code()；本包无需引入任何驱动。
func isRetryableLockConflict(err error) bool {
	if err == nil {
		return false
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		switch state.SQLState() {
		case "40P01", "40001": // deadlock_detected, serialization_failure | 死锁、序列化失败
			return true
		}
	}
	var number interface{ Number() uint16 }
	if errors.As(err, &number) {
		switch number.Number() {
		case 1213, 1205: // ER_LOCK_DEADLOCK, ER_LOCK_WAIT_TIMEOUT | MySQL 死锁、锁等待超时
			return true
		}
	}
	var code interface{ Code() int }
	if errors.As(err, &code) {
		switch code.Code() & 0xff {
		case 5, 6: // SQLITE_BUSY, SQLITE_LOCKED | SQLite 忙、被锁
			return true
		}
	}
	return false
}

// rowsError turns a zero affected-row delete into not-found. | rowsError 把零影响行删除转成未找到。
func rowsError(result *gorm.DB) error {
	if result.Error != nil {
		return queryError(result.Error)
	}
	if result.RowsAffected == 0 {
		return errs.Detail(messaging.ErrNotFound, "row does not exist | 记录不存在")
	}
	return nil
}

// updateExisting accepts a no-op update as success but reports a missing row. MySQL reports zero
// affected rows for unchanged values, so existence is checked explicitly. |
// updateExisting 把原样更新视为成功，但记录确实消失时报告未找到。MySQL 对未变化的值报告零影响行，
// 因此需要显式确认存在性。
func (s *Store) updateExisting(ctx context.Context, result *gorm.DB, model any, query string, args ...any) error {
	if result.Error != nil {
		return queryError(result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}
	var count int64
	if err := s.dbc(ctx).Model(model).Where(query, args...).Count(&count).Error; err != nil {
		return queryError(err)
	}
	if count == 0 {
		return errs.Detail(messaging.ErrNotFound, "row does not exist | 记录不存在")
	}
	return nil
}

// inChunks is kept small enough for every dialect's parameter ceiling. | inChunks 保持足够小，适配各数据库的参数上限。
const inChunks = 200

// chunks splits ids into bounded batches. | chunks 把 ID 切分成有界批次。
func chunks(ids []string) [][]string {
	if len(ids) == 0 {
		return nil
	}
	out := make([][]string, 0, (len(ids)+inChunks-1)/inChunks)
	for start := 0; start < len(ids); start += inChunks {
		end := start + inChunks
		if end > len(ids) {
			end = len(ids)
		}
		out = append(out, ids[start:end])
	}
	return out
}

// likePattern escapes LIKE metacharacters and wraps the term in wildcards. The escape character is
// "!" to match every query's ESCAPE '!' clause; escaping with a backslash silently misses literal
// percent/underscore terms. | likePattern 转义 LIKE 元字符并在词两侧加通配符。转义符使用 "!"，
// 与所有查询的 ESCAPE '!' 一致；用反斜杠转义会让含字面 %/_ 的检索静默失配。
func likePattern(term string) string {
	replacer := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	return "%" + strings.ToLower(replacer.Replace(strings.TrimSpace(term))) + "%"
}

// onConflictDoNothing returns the portable clause used for idempotent inserts. | onConflictDoNothing 返回幂等插入使用的可移植子句。
func onConflictDoNothing() clause.OnConflict {
	return clause.OnConflict{DoNothing: true}
}

// newRowID generates the primary key of a join row. The service generates every user-visible id;
// this helper exists only for rows the store creates itself (message-tag links). | newRowID 生成连接行主键。
// 用户可见的 ID 都由 service 生成；本函数只服务存储层自建的记录（消息-标签连接）。
func newRowID(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	return prefix + hex.EncodeToString(raw[:])
}
