package auth

import "github.com/shibingli/ydx/errs"

// Package-level sentinels. | 包级哨兵错误。
//
// Every failure path in this package references one of these declarations, so
// callers decide with errors.Is and never by matching message text. | 本包所有失败路径都引用
// 下列声明，调用方一律用 errors.Is 判定，绝不匹配文案。
var (
	// ErrNotFound reports an entity that does not exist inside the requested tenant. | ErrNotFound 表示实体在指定租户内不存在。
	//
	// A cross-tenant read deliberately maps onto this sentinel instead of ErrForbidden,
	// so that a probe cannot tell "not yours" from "does not exist". | 跨租户读取刻意归一到本哨兵
	// 而非 ErrForbidden，使探测者无法区分「不属于你」与「不存在」。
	ErrNotFound = errs.New("auth: not found | auth：未找到")

	// ErrForbidden reports a permission check that failed. | ErrForbidden 表示权限判定未通过。
	ErrForbidden = errs.New("auth: forbidden | auth：无权访问")

	// ErrConflict reports an entity that already exists, such as a duplicate slug or username. | ErrConflict 表示实体已存在，例如重复的 Slug 或用户名。
	ErrConflict = errs.New("auth: entity already exists | auth：实体已存在")

	// ErrInvalidInput reports a malformed input field, such as an empty username. | ErrInvalidInput 表示输入字段非法，例如用户名为空。
	ErrInvalidInput = errs.New("auth: invalid input | auth：输入非法")

	// ErrInvalidCredentials reports a failed sign-in. | ErrInvalidCredentials 表示登录凭据无效。
	//
	// A missing user and a wrong password share this single sentinel to prevent
	// user enumeration. | 用户不存在与口令错误共用本哨兵，用于防止用户名枚举。
	ErrInvalidCredentials = errs.New("auth: invalid credentials | auth：凭据无效")

	// ErrTenantRequired reports a call that does not name a tenant. | ErrTenantRequired 表示调用未指定租户。
	ErrTenantRequired = errs.New("auth: tenant id is required | auth：必须提供租户 ID")

	// ErrUserDisabled reports a user whose Status is not StatusActive. | ErrUserDisabled 表示用户状态不是 StatusActive。
	ErrUserDisabled = errs.New("auth: user is disabled | auth：用户已被禁用")

	// ErrWeakPassword reports a password that does not meet the strength policy. | ErrWeakPassword 表示口令不满足强度要求。
	ErrWeakPassword = errs.New("auth: password does not meet the strength policy | auth：口令强度不足")

	// ErrSecKeyMissing reports a user without a usable sec_key. | ErrSecKeyMissing 表示用户缺少可用的 sec_key。
	ErrSecKeyMissing = errs.New("auth: user has no sec_key | auth：用户缺少 sec_key")

	// ErrNilStore reports a nil Store or Service handed to a constructor. | ErrNilStore 表示构造函数收到了空的 Store 或 Service。
	ErrNilStore = errs.New("auth: store is nil | auth：Store 为空")

	// ErrMasterKeyRequired reports a master key shorter than MinMasterKeyLen. | ErrMasterKeyRequired 表示主密钥短于 MinMasterKeyLen。
	ErrMasterKeyRequired = errs.New("auth: master key is required | auth：必须提供主密钥")

	// ErrTokenInvalid reports a token that cannot be trusted. | ErrTokenInvalid 表示令牌不可信。
	//
	// Every failure of jwt.Parse is wrapped into this sentinel, and the underlying
	// jwt sentinel stays reachable through errors.Is. | jwt.Parse 的每一种失败都会包装进本哨兵，
	// 底层 jwt 哨兵仍可用 errors.Is 判定。
	ErrTokenInvalid = errs.New("auth: invalid token | auth：令牌无效")
)
