package authz

import (
	"strings"
	"unicode"

	"github.com/shibingli/ydx/errs"
)

// Every name a capability is known by is derived from its token here, so the four spellings of one
// capability - the token, the Go constant, the template data key and the JavaScript key - cannot
// drift apart: they are one token run through four deterministic functions.
// 一个能力的四种写法——标识本身、Go 常量名、模板数据键、JavaScript 键——都在这里由标识推导，
// 因此它们不可能漂移：它们是同一个标识经过四个确定性函数的结果。

// ConstantName returns the Go constant name of a capability token. | ConstantName 返回能力标识对应的 Go 常量名。
//
// ConstantName("user:create") is "PermUserCreate", the very name of the constant exported by this
// package. A project declares its own capabilities with the same shape, which is what lets
// `yd authz check` recognise a reference to any of them. | ConstantName("user:create") 等于
// "PermUserCreate"，也就是本包导出的那个常量名。项目按同一形态声明自己的能力，
// 正是这一点让 `yd authz check` 能识别对它们当中任何一个的引用。
func ConstantName(token string) string { return "Perm" + camel(token) }

// RoleConstantName returns the Go constant name of a role. | RoleConstantName 返回角色对应的 Go 常量名。
func RoleConstantName(role string) string { return "Role" + camel(role) }

// DataKey returns the template data key of a capability. | DataKey 返回能力对应的模板数据键。
//
// DataKey("user:create") is "UserCreate", i.e. the field a template reads as
// `{{ if can .Perm.UserCreate }}`, so no template ever writes the token itself. |
// DataKey("user:create") 等于 "UserCreate"，即模板写作 `{{ if can .Perm.UserCreate }}` 的那个字段，
// 因此模板永远不必写出标识本身。
func DataKey(token string) string { return camel(token) }

// RoleDataKey returns the template data key of a role. | RoleDataKey 返回角色对应的模板数据键。
func RoleDataKey(role string) string { return camel(role) }

// JSKey returns the JavaScript key of a capability. | JSKey 返回能力对应的 JavaScript 键。
//
// JSKey("user:create") is "USER_CREATE", which is what `yd.PERM.USER_CREATE` reads. |
// JSKey("user:create") 等于 "USER_CREATE"，也就是 `yd.PERM.USER_CREATE` 读取的那个键。
func JSKey(token string) string { return upperSnake(token) }

// RoleJSKey returns the JavaScript key of a role. | RoleJSKey 返回角色对应的 JavaScript 键。
func RoleJSKey(role string) string { return upperSnake(role) }

// ResolvePermission resolves a reference to a capability token. | ResolvePermission 把一处引用解析为能力标识。
//
// The four accepted forms are the token itself ("user:create"), the Go constant name
// ("PermUserCreate"), the template data key ("UserCreate") and the JavaScript key ("USER_CREATE");
// every one of them is derivable from the token, so a caller that receives one of them from a
// template or from JavaScript never has to normalise it by hand. | 可接受的形式有四种：
// 标识本身（"user:create"）、Go 常量名（"PermUserCreate"）、模板数据键（"UserCreate"）与
// JavaScript 键（"USER_CREATE"）；它们都能由标识推导，因此从模板或 JavaScript 收到其中之一的调用方
// 不必手工归一化。
//
// An unknown reference is ErrUnknownPermission, and the caller is expected to fail closed rather
// than to treat it as a capability nobody happens to hold: the two are indistinguishable at run
// time, and only `yd authz check` can tell a typo from a deliberate denial. | 引用未知时返回
// ErrUnknownPermission，调用方应当失败关闭，而不是把它当成「恰好没人持有」的能力：
// 两者在运行时无法区分，只有 `yd authz check` 才能分辨拼写错误与刻意拒绝。
func ResolvePermission(ref string) (string, error) {
	if Registered(ref) {
		return ref, nil
	}
	for _, p := range Permissions() {
		if ConstantName(p.Name) == ref || DataKey(p.Name) == ref || JSKey(p.Name) == ref {
			return p.Name, nil
		}
	}
	return "", errs.Detail(ErrUnknownPermission, "ref=%[1]q | 引用=%[1]q", ref)
}

// ResolveRole resolves a reference to a role name. | ResolveRole 把一处引用解析为角色名。
//
// It accepts the name itself ("admin"), the Go constant name ("RoleAdmin"), the template data key
// ("Admin") and the JavaScript key ("ADMIN"), exactly like ResolvePermission does for capabilities.
// | 它接受名字本身（"admin"）、Go 常量名（"RoleAdmin"）、模板数据键（"Admin"）与
// JavaScript 键（"ADMIN"），与 ResolvePermission 对能力的处理完全一致。
func ResolveRole(ref string) (string, error) {
	if RoleRegistered(ref) {
		return ref, nil
	}
	for _, r := range Roles() {
		if RoleConstantName(r.Name) == ref || RoleDataKey(r.Name) == ref || RoleJSKey(r.Name) == ref {
			return r.Name, nil
		}
	}
	return "", errs.Detail(ErrUnknownRole, "ref=%[1]q | 引用=%[1]q", ref)
}

// camel turns a token or a role into a Go-style identifier. | camel 把标识或角色名转为 Go 风格标识符。
//
// The separators are the ones the capability shape allows: ':' between resource and action, and
// '_' or '-' inside either half. | 分隔符就是能力形态所允许的那些：资源与动作之间的 ':'，
// 以及任一半段内部的 '_' 或 '-'。
func camel(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	upper := true
	for _, r := range s {
		switch {
		case r == ':' || r == '_' || r == '-' || r == '.':
			upper = true
		case upper:
			b.WriteRune(unicode.ToUpper(r))
			upper = false
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// upperSnake turns a token or a role into an upper snake case key. | upperSnake 把标识或角色名转为全大写下划线键。
func upperSnake(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ':', '_', '-', '.':
			b.WriteByte('_')
		default:
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}
