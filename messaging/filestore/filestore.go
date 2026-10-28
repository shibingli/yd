// Package filestore is kept for source compatibility; the implementation moved to
// github.com/shibingli/yd/attachments/filestore. | Package filestore 仅为源码兼容保留；
// 实现已迁移到 github.com/shibingli/yd/attachments/filestore。
//
// Deprecated: import github.com/shibingli/yd/attachments/filestore instead. | 已弃用：请改用
// github.com/shibingli/yd/attachments/filestore。
package filestore

import (
	attachments "github.com/shibingli/yd/attachments/filestore"
)

// Store is the attachments/filestore store. | Store 是 attachments/filestore 的存储。
type Store = attachments.Store

// Sentinel errors forwarded from the new package. | 从新包转发的哨兵错误。
var (
	ErrRootInvalid = attachments.ErrRootInvalid
	ErrKeyInvalid  = attachments.ErrKeyInvalid
	ErrWrite       = attachments.ErrWrite
	ErrRead        = attachments.ErrRead
	ErrDelete      = attachments.ErrDelete
)

// New forwards to attachments/filestore.New. | New 转发到 attachments/filestore.New。
func New(root string) (*Store, error) { return attachments.New(root) }
