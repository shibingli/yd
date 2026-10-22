package authz

import (
	"github.com/shibingli/yd/security/auth"
	"github.com/shibingli/ydx/errs"
)

// Package-level sentinels. | 包级哨兵错误。
//
// Every failure path references one of these declarations, so callers decide with
// errors.Is and never by matching message text. | 本包所有失败路径都引用下列声明，
// 调用方一律用 errors.Is 判定，绝不匹配文案。
var (
	// ErrForbidden reports a refused decision. | ErrForbidden 表示判定未通过。
	//
	// It is the very sentinel of the auth package, so an error returned by Allow is
	// rendered as 403 by the framework's error renderers without any translation
	// layer in between. | 它就是 auth 包的哨兵，因此 Allow 返回的错误无需任何转换层，
	// 框架的错误渲染器就会把它渲染为 403。
	ErrForbidden = auth.ErrForbidden

	// ErrDuplicatePermission reports a permission name that is already registered. | ErrDuplicatePermission 表示权限名已注册。
	ErrDuplicatePermission = errs.New("permission already registered | 权限已注册")

	// ErrUnknownPermission reports a permission name that the registry does not know. | ErrUnknownPermission 表示注册表不认识该权限名。
	ErrUnknownPermission = errs.New("unknown permission | 未注册的权限")

	// ErrInvalidPermission reports a permission name outside the "resource:action" shape. | ErrInvalidPermission 表示权限名不符合 "resource:action" 形态。
	ErrInvalidPermission = errs.New("invalid permission name | 非法的权限名")

	// ErrDuplicateRole reports a role name that is already registered. | ErrDuplicateRole 表示角色名已注册。
	ErrDuplicateRole = errs.New("role already registered | 角色已注册")

	// ErrUnknownRole reports a role name that the registry does not know. | ErrUnknownRole 表示注册表不认识该角色名。
	ErrUnknownRole = errs.New("unknown role | 未注册的角色")

	// ErrInvalidRole reports a role name that is empty or carries unsupported characters. | ErrInvalidRole 表示角色名为空或含有不支持的字符。
	ErrInvalidRole = errs.New("invalid role name | 非法的角色名")

	// ErrNoAuthority reports a template render without any authority in the data, which is
	// why the perm and role helpers cannot resolve a name. | ErrNoAuthority 表示模板渲染的数据里没有判定主体，
	// 因此 perm 与 role 辅助函数无法解析名字。
	ErrNoAuthority = errs.New("no authz authority in the render data | 渲染数据中没有权限判定主体")

	// ErrScanDir reports an unreadable scan root for the static checker. | ErrScanDir 表示静态检查的扫描根不可读。
	ErrScanDir = errs.New("cannot scan the directory | 无法扫描目录")

	// ErrReadSource reports an unreadable source file for the static checker. | ErrReadSource 表示静态检查无法读取源文件。
	ErrReadSource = errs.New("cannot read the source file | 无法读取源文件")
)
