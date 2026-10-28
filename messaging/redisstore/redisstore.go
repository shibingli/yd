// Package redisstore is kept for source compatibility; the implementation moved to
// github.com/shibingli/yd/attachments/redisstore. | Package redisstore 仅为源码兼容保留；
// 实现已迁移到 github.com/shibingli/yd/attachments/redisstore。
//
// Deprecated: import github.com/shibingli/yd/attachments/redisstore instead. | 已弃用：请改用
// github.com/shibingli/yd/attachments/redisstore。
package redisstore

import (
	goredis "github.com/redis/go-redis/v9"

	attachments "github.com/shibingli/yd/attachments/redisstore"
)

// DefaultPrefix is forwarded from the new package. | DefaultPrefix 从新包转发。
const DefaultPrefix = attachments.DefaultPrefix

// Store is the attachments/redisstore store. | Store 是 attachments/redisstore 的存储。
type Store = attachments.Store

// Sentinel errors forwarded from the new package. | 从新包转发的哨兵错误。
var (
	ErrClientInvalid = attachments.ErrClientInvalid
	ErrPrefixInvalid = attachments.ErrPrefixInvalid
	ErrKeyInvalid    = attachments.ErrKeyInvalid
	ErrWrite         = attachments.ErrWrite
	ErrRead          = attachments.ErrRead
	ErrDelete        = attachments.ErrDelete
)

// New forwards to attachments/redisstore.New. | New 转发到 attachments/redisstore.New。
func New(client goredis.UniversalClient, prefix string) (*Store, error) {
	return attachments.New(client, prefix)
}
