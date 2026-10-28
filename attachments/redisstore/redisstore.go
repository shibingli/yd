// Package redisstore stores attachment bytes in Redis so every application node reads the same
// bytes without a shared filesystem. | Package redisstore 把附件字节保存到 Redis，使所有应用节点
// 无需共享文件系统即可读取同一份字节。
//
// It implements attachments.BlobStore. Keys still follow the opaque partition layout produced by
// the attachments package (<tenant hash>/<uploader hash>/<YYYY>/<MM>/<DD>/<attachment id>); this
// package only prefixes them and validates that they cannot collide across deployments. | 它实现
// attachments.BlobStore。键仍沿用 attachments 包生成的不透明分区布局
// （<租户哈希>/<上传者哈希>/<YYYY>/<MM>/<DD>/<附件 ID>），本包只加前缀并校验键不会跨部署冲突。
package redisstore

import (
	"context"
	"strings"

	goredis "github.com/redis/go-redis/v9"
	"github.com/shibingli/ydx/errs"
)

// Sentinel errors of the Redis store. | Redis 存储的哨兵错误。
var (
	// ErrClientInvalid reports a nil Redis client. | ErrClientInvalid 表示 Redis 客户端为空。
	ErrClientInvalid = errs.New("redis blob store client is invalid | Redis 附件存储客户端非法")
	// ErrPrefixInvalid reports an unusable key prefix. | ErrPrefixInvalid 表示键前缀不可用。
	ErrPrefixInvalid = errs.New("redis blob store prefix is invalid | Redis 附件存储前缀非法")
	// ErrKeyInvalid reports a key containing unsafe characters. | ErrKeyInvalid 表示键含不安全字符。
	ErrKeyInvalid = errs.New("attachment key is invalid | 附件键非法")
	// ErrWrite reports a failed write. | ErrWrite 表示写入失败。
	ErrWrite = errs.New("attachment write failed | 附件写入失败")
	// ErrRead reports a failed read. | ErrRead 表示读取失败。
	ErrRead = errs.New("attachment read failed | 附件读取失败")
	// ErrDelete reports a failed delete. | ErrDelete 表示删除失败。
	ErrDelete = errs.New("attachment delete failed | 附件删除失败")
)

// DefaultPrefix is the key namespace used when the caller does not configure one. It keeps the
// historical value so blobs written before the generic attachment domain stay reachable. |
// DefaultPrefix 是调用方未配置时使用的键命名空间；保留历史值，使通用附件域之前的字节仍可读取。
const DefaultPrefix = "yd:msg:blob:"

// Store is a Redis-backed blob store. | Store 是 Redis 支撑的字节存储。
type Store struct {
	// client is the shared Redis client owned by the application. | client 是应用持有的共享 Redis 客户端。
	client goredis.UniversalClient
	// prefix namespaces every key of this deployment. | prefix 是本部署所有键的命名空间。
	prefix string
}

// New returns a store writing under prefix. An empty prefix falls back to DefaultPrefix. The client
// stays owned by the caller; Store never closes it. | New 返回在 prefix 下写入的存储；prefix 为空时
// 回退到 DefaultPrefix。客户端仍由调用方持有，Store 不会关闭它。
func New(client goredis.UniversalClient, prefix string) (*Store, error) {
	if client == nil {
		return nil, ErrClientInvalid
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = DefaultPrefix
	}
	if !validPrefix(prefix) {
		return nil, ErrPrefixInvalid
	}
	return &Store{client: client, prefix: prefix}, nil
}

// Put writes data under key. | Put 把 data 写入 key。
func (s *Store) Put(ctx context.Context, key string, data []byte) error {
	target, err := s.target(key)
	if err != nil {
		return err
	}
	if err := s.client.Set(ctx, target, data, 0).Err(); err != nil {
		return errs.Wrap(err, ErrWrite)
	}
	return nil
}

// Get reads the bytes stored under key. | Get 读取 key 下的字节。
func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	target, err := s.target(key)
	if err != nil {
		return nil, err
	}
	data, err := s.client.Get(ctx, target).Bytes()
	if err != nil {
		return nil, errs.Wrap(err, ErrRead)
	}
	return data, nil
}

// Delete removes key; a missing key is not an error. | Delete 删除 key；键不存在不算错误。
func (s *Store) Delete(ctx context.Context, key string) error {
	target, err := s.target(key)
	if err != nil {
		return err
	}
	if err := s.client.Del(ctx, target).Err(); err != nil {
		return errs.Wrap(err, ErrDelete)
	}
	return nil
}

// Prefix returns the namespace this store writes under. | Prefix 返回本存储写入的命名空间。
func (s *Store) Prefix() string { return s.prefix }

// target validates a key and joins it with the namespace. | target 校验键并与命名空间拼接。
func (s *Store) target(key string) (string, error) {
	key = strings.TrimSpace(key)
	if !validKey(key) {
		return "", ErrKeyInvalid
	}
	return s.prefix + key, nil
}

// validPrefix rejects control characters and whitespace, which would make operational inspection
// and tooling awkward. | validPrefix 拒绝控制字符与空白，避免运维查看与工具处理时产生歧义。
func validPrefix(prefix string) bool {
	for _, r := range prefix {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

// validKey accepts the opaque segment alphabet produced by attachments.attachmentKey and rejects
// empty segments, parent traversal and characters that tools would have to escape. |
// validKey 接受 attachments.attachmentKey 生成的不透明段字母表，并拒绝空段、父目录穿越与需要工具转义
// 的字符。
func validKey(key string) bool {
	if key == "" {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, r := range segment {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
				r == '.' || r == '_' || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}
