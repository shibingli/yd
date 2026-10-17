package render

import "html/template"

// AuthzKey is the data key carrying the decision subject of the page. | AuthzKey 是承载页面判定主体的数据键。
//
// Render looks the key up in a map[string]any data value; only a value satisfying Authz makes the
// `can`, `hasRole`, `perm` and `role` helpers decide through it, for that one execution - the same
// mechanism TranslatorKey uses for `tr`. | Render 在 map[string]any 型数据中查找该键；
// 只有满足 Authz 的值才会让 `can`、`hasRole`、`perm` 与 `role` 辅助函数在该次执行中经由它判定——
// 与 TranslatorKey 对 `tr` 的机制完全相同。
//
// The package that owns the registry fills this key in; render defines the contract and never
// imports it, so the template engine stays independent of any particular authorization model. | 该键由
// 拥有注册表的包填入；render 只定义契约而从不导入它，因此模板引擎不依赖任何特定的授权模型。
const AuthzKey = "Authz"

// Authz decides what the subject of one render may do. | Authz 判定某次渲染的主体可以做什么。
//
// authz.Subject implements it. | authz.Subject 实现了它。
type Authz interface {
	// Can reports whether the subject holds a capability. | Can 报告主体是否持有某能力。
	Can(perm string) bool
	// HasRole reports whether the subject holds a role. | HasRole 报告主体是否持有某角色。
	HasRole(role string) bool
	// Perm resolves a capability reference to its token, or to "" when it is unknown. | Perm 把能力引用解析为标识，未知时返回空串。
	Perm(ref string) string
	// Role resolves a role reference to its name, or to "" when it is unknown. | Role 把角色引用解析为名字，未知时返回空串。
	Role(ref string) string
}

// The red line, written where the helpers are defined so it cannot be forgotten | 红线写在辅助函数的定义处，以免被遗忘：
//
// `can` and `hasRole` exist to keep a page from offering an action the server would refuse. They
// are not an authorization check: whoever receives the page can call the endpoint behind a hidden
// button directly, and whoever writes the page can decide differently. Every protected route still
// carries its own server-side guard. | `can` 与 `hasRole` 的存在只是为了让页面不提供「服务端本来就会拒绝」
// 的操作，它们不是授权检查：拿到页面的人可以直接调用隐藏按钮背后的端点，写页面的人也可以另作决定。
// 每个受保护路由仍然必须有自己的服务端守卫。
//
// Without an injected Authz the helpers fall back to the behaviour of `tr` without a translator:
// nothing is granted, and a reference is returned as it was written, so a missing wiring stays
// visible in the page instead of failing the render. | 未注入 Authz 时，辅助函数退回到「无译者的 tr」
// 的行为：不授予任何能力，引用原样返回，因此漏装配会在页面上显现，而不是让渲染失败。

// can is the built-in `can` template function: without an authority it grants nothing. | can 是内建的 can 模板函数：没有判定主体时不授予任何能力。
func can(any) bool { return false }

// hasRole is the built-in `hasRole` template function: without an authority it grants nothing. | hasRole 是内建的 hasRole 模板函数：没有判定主体时不授予任何能力。
func hasRole(any) bool { return false }

// perm is the built-in `perm` template function: it returns the reference unchanged. | perm 是内建的 perm 模板函数：原样返回引用。
func perm(ref any) string { return refString(ref) }

// role is the built-in `role` template function: it returns the reference unchanged. | role 是内建的 role 模板函数：原样返回引用。
func role(ref any) string { return refString(ref) }

// authzFuncs returns the four decision helpers bound to a. | authzFuncs 返回绑定到 a 的四个判定辅助函数。
//
// The reference argument is declared as any because a template passes whatever it has: a data key
// of the injected maps (.Perm.UserCreate), a quoted constant name, or nothing at all when the key
// is missing from a map. A value that is not a string answers "not granted" rather than failing
// the render, which is the same fail-closed answer an unknown name gets. | 引用参数声明为 any，
// 因为模板传进来的东西是不定的：注入映射里的数据键（.Perm.UserCreate）、带引号的常量名，
// 或者映射里缺键时的空值。非字符串的值回答「未授予」而不是让渲染失败，与未知名字得到的失败关闭答案一致。
func authzFuncs(a Authz) template.FuncMap {
	return template.FuncMap{
		"can":     func(ref any) bool { return a.Can(refString(ref)) },
		"hasRole": func(ref any) bool { return a.HasRole(refString(ref)) },
		"perm":    func(ref any) string { return a.Perm(refString(ref)) },
		"role":    func(ref any) string { return a.Role(refString(ref)) },
	}
}

// refString normalises a helper argument into a reference string. | refString 把辅助函数参数归一化为引用字符串。
func refString(v any) string {
	s, _ := v.(string)
	return s
}

// authzFrom extracts the injected authority from render data. | authzFrom 从渲染数据中取出注入的判定主体。
//
// Like translatorFrom, only a map[string]any holding an Authz under AuthzKey counts; every other
// data shape reports nil so that the helpers fall back to their built-in answer. | 与 translatorFrom
// 一样，只有「map[string]any 且 AuthzKey 下存放 Authz」才算命中；其它数据形态一律返回 nil，
// 使辅助函数退回内建答案。
func authzFrom(data any) Authz {
	m, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	a, _ := m[AuthzKey].(Authz)
	return a
}
