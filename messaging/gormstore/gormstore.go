// Package gormstore is the GORM-backed messaging.Store. | Package gormstore 是基于 GORM 的 messaging.Store。
//
// The schema is owned here: Migrate creates yd_msg_types, yd_msg_tags, yd_msg_conversations,
// yd_msg_members, yd_msg_messages, yd_msg_message_tags, yd_msg_attachments, yd_msg_friend_requests,
// yd_msg_friendships, yd_msg_friend_notes, yd_msg_blocks and yd_msg_audit, every one of them
// tenant-first. Migrate is idempotent and safe to run on every start. | 模式由本包拥有：
// Migrate 创建上述十二张表，所有索引都以租户为第一列。Migrate 幂等，可在每次启动时运行。
package gormstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/messaging"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Sentinel errors of the store. | 存储层的哨兵错误。
var (
	// ErrMigrate reports a failed schema migration. | ErrMigrate 表示模式迁移失败。
	ErrMigrate = errx.New("message schema migration failed | 消息数据表迁移失败")
	// ErrQuery reports a failed statement. | ErrQuery 表示语句执行失败。
	ErrQuery = errx.New("message statement failed | 消息语句执行失败")
)

// Store implements messaging.Store over GORM. | Store 基于 GORM 实现 messaging.Store。
type Store struct {
	// db is the shared connection pool. | db 是共享连接池。
	db *gorm.DB
}

// New returns a store over db. | New 返回基于 db 的存储。
func New(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, ErrQuery
	}
	return &Store{db: db}, nil
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
		&messaging.Attachment{},
		&messaging.FriendRequest{},
		&messaging.Friendship{},
		&messaging.FriendNote{},
		&messaging.Block{},
		&messaging.AuditEntry{},
	}
	if err := db.WithContext(ctx).AutoMigrate(models...); err != nil {
		return errx.Wrap(err, ErrMigrate)
	}
	return nil
}

// dbc returns a context-bound handle. | dbc 返回绑定上下文的句柄。
func (s *Store) dbc(ctx context.Context) *gorm.DB { return s.db.WithContext(ctx) }

// notFound maps gorm.ErrRecordNotFound onto messaging.ErrNotFound. | notFound 把 gorm.ErrRecordNotFound 映射为 messaging.ErrNotFound。
func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errx.Wrap(err, messaging.ErrNotFound)
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
	return errx.Wrap(err, ErrQuery)
}

// createError maps a statement error, preserving not-found and recognising duplicate keys. |
// createError 映射语句错误，保留未找到语义并识别重复键。
func createError(err error) error {
	if err == nil {
		return nil
	}
	if mapped := notFound(err); mapped != nil {
		return mapped
	}
	return errx.Wrap(err, ErrQuery)
}

// rowsError turns a zero affected-row delete into not-found. | rowsError 把零影响行删除转成未找到。
func rowsError(result *gorm.DB) error {
	if result.Error != nil {
		return queryError(result.Error)
	}
	if result.RowsAffected == 0 {
		return errx.Detail(messaging.ErrNotFound, "row does not exist | 记录不存在")
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

// likePattern escapes LIKE metacharacters and wraps the term in wildcards. | likePattern 转义 LIKE 元字符并在词两侧加通配符。
func likePattern(term string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
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
