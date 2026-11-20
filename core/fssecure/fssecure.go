// Package fssecure tightens the permissions of files that may hold secrets. |
// Package fssecure 收紧可能包含密钥的文件权限。
//
// On Unix the requested mode is applied with chmod. On Windows chmod only toggles the read-only
// bit and leaves the inherited ACL intact, so a file created with 0600 is still readable by other
// local users. This package replaces the inherited ACL with an explicit one for the current user,
// SYSTEM and Administrators, which is the Windows equivalent of "owner only". |
// 在 Unix 上通过 chmod 施加请求的模式。Windows 的 chmod 只切换只读位并保留继承 ACL，因此以 0600
// 创建的文件仍可能被同机其他用户读取。本包用「当前用户、SYSTEM 与 Administrators」的显式 ACL
// 替换继承 ACL，这是 Windows 上「仅所有者可读」的等价做法。
package fssecure

import (
	"io/fs"

	"github.com/shibingli/ydx/errs"
)

// ErrRestrict reports that a sensitive path could not be tightened. | ErrRestrict 表示敏感路径的权限
// 无法收紧。
var ErrRestrict = errs.New("sensitive path permissions could not be restricted | 敏感路径权限无法收紧")

// Restrict applies mode on Unix and an owner-only ACL on Windows. A missing path is reported as an
// error; callers that probe optional files should stat first. For a directory the Windows ACL is
// applied recursively so files already inside it are covered as well. |
// Restrict 在 Unix 上施加 mode，在 Windows 上施加仅所有者 ACL。路径不存在时返回错误；探测可选文件的
// 调用方应先 stat。对目录而言，Windows ACL 会递归施加，目录内已有文件同样被覆盖。
func Restrict(path string, mode fs.FileMode) error {
	if err := restrict(path, mode); err != nil {
		return errs.WrapDetail(err, ErrRestrict, "path=%[1]q | 路径=%[1]q", path)
	}
	return nil
}
