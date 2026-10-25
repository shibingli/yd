package authz

import (
	"html/template"
	"strconv"
	"strings"

	"github.com/shibingli/ydx/json"
	"github.com/shibingli/ydx/log"
	"github.com/shibingli/ydx/render"
)

// AuthzScriptID is the id of the inline JSON payload of the front end. | AuthzScriptID 是前端内联 JSON 载荷的 id。
//
// The layout writes it once, before any script runs:
//
//	<script id="yd-authz" type="application/json">{{.AuthzJSON}}</script>
//
// and web/static/js/yd-authz.js reads exactly that element, so the payload costs no request at all
// and the page works offline. | 布局在任何脚本运行之前写出它一次（见上面的片段），
// 而 web/static/js/yd-authz.js 读取的正是该元素，因此这份载荷不产生任何请求，页面离线也能工作。
const AuthzScriptID = "yd-authz"

// AuthzDataKey is the render data key carrying the whole payload map. | AuthzDataKey 是承载整份载荷映射的渲染数据键。
//
// It is only documentation: the payload is a plain map spread into the page data by Data, not a
// single value under one key. | 它只是文档：载荷是 Data 摊进页面数据的一张普通映射，而不是某个键下的单个值。
const AuthzDataKey = "Authz"

// The selector prefixes of every consumer. | 每个消费方所读取的选择器前缀。
//
// They are constants because four separate languages spell the same name: a template writes
// .Perm.UserCreate, a script writes yd.PERM.USER_CREATE, Go writes PermUserCreate, and
// `yd authz list` prints all of them side by side. Writing the prefixes once is what keeps that
// table honest. | 把它们做成常量，是因为四种不同的语言写的是同一个名字：模板写 .Perm.UserCreate，
// 脚本写 yd.PERM.USER_CREATE，Go 写 PermUserCreate，而 `yd authz list` 把它们并排打印出来。
// 前缀只写一次，那张表才不会说谎。
const (
	// PermSelector is the template prefix of a capability data key. | PermSelector 是能力数据键的模板前缀。
	PermSelector = ".Perm."
	// RoleSelector is the template prefix of a role data key. | RoleSelector 是角色数据键的模板前缀。
	RoleSelector = ".Role."
	// PermScriptSelector is the script prefix of a capability key. | PermScriptSelector 是能力键的脚本前缀。
	PermScriptSelector = "yd.PERM."
	// RoleScriptSelector is the script prefix of a role key. | RoleScriptSelector 是角色键的脚本前缀。
	RoleScriptSelector = "yd.ROLE."
)

// Envelope is the payload the browser receives. | Envelope 是浏览器收到的载荷。
//
// The name maps travel in full - a template or a script may need to *name* a capability it does not
// hold, e.g. to render a tooltip - while the two lists carry what this subject actually holds. | 两张名字表
// 完整下发——模板或脚本可能需要「称呼」一个自己并不持有的能力，例如渲染一条提示——
// 而两个列表承载的是该主体实际持有的内容。
//
// The keys are the JavaScript keys (USER_CREATE, ADMIN), because the documented spelling of a script
// reference is yd.can(yd.PERM.USER_CREATE); the template data keys live in PermMap and never reach
// the browser. | 键是 JavaScript 键（USER_CREATE、ADMIN），因为脚本引用的既定写法是
// yd.can(yd.PERM.USER_CREATE)；模板数据键只存在于 PermMap 中，从不发往浏览器。
type Envelope struct {
	// Perms maps every JavaScript permission key to its token. | Perms 把每个 JavaScript 权限键映射到它的标识。
	Perms map[string]string `json:"perms"`
	// Roles maps every JavaScript role key to its name. | Roles 把每个 JavaScript 角色键映射到它的名字。
	Roles map[string]string `json:"roles"`
	// Granted lists the capabilities of the subject. | Granted 列出该主体持有的能力。
	Granted []string `json:"granted"`
	// Mine lists the roles of the subject. | Mine 列出该主体持有的角色。
	Mine []string `json:"mine"`
}

// NewEnvelope builds the payload of one subject. | NewEnvelope 构建某个主体的载荷。
//
// A nil subject - an anonymous visitor - yields the full name maps with two empty lists, which is
// what makes the front end able to deny everything without a special case. | 主体为 nil 时
// （匿名访客）得到完整的名字表与两个空列表，这正是前端无需特例即可全部拒绝的原因。
func NewEnvelope(s *Subject) Envelope {
	return Envelope{
		Perms:   PermJSMap(),
		Roles:   RoleJSMap(),
		Granted: s.Permissions(),
		Mine:    s.Roles(),
	}
}

// JS returns the payload as JSON ready for the inline script tag. | JS 把载荷序列化为可直接放入内联脚本标签的 JSON。
//
// The result is template.JS because html/template treats <script type="application/json"> as a
// JavaScript container: any other type would be marshalled again into a quoted JSON *string*. | 结果类型为
// template.JS，因为 html/template 把 <script type="application/json"> 当作 JavaScript 容器：
// 其它类型会被再序列化一次，变成带引号的 JSON *字符串*。
//
// ydx/json escapes the HTML-significant characters, so the body is safe inside a script element. | ydx/json
// 会转义 HTML 敏感字符，因此正文在脚本元素内是安全的。
func (e Envelope) JS() template.JS {
	b, err := json.Marshal(e)
	if err != nil {
		// The shape is maps and slices of strings, so this is unreachable in practice; an empty
		// payload is still the safe answer because the front end then denies everything.
		// 该结构只含字符串的映射与切片，因此实际不可达；即便发生，空载荷仍是安全答案，因为前端会全部拒绝。
		log.Default().Warn("authz: cannot serialize the front-end payload, denying everything | authz：无法序列化前端载荷，将全部拒绝",
			log.Err(err))
		return template.JS("{}")
	}
	return template.JS(b)
}

// PermMap returns the template data key of every capability mapped to its token. | PermMap 返回每条能力的模板数据键到标识的映射。
//
// A template reads it as .Perm, e.g. `{{ if can .Perm.UserCreate }}`, so no template ever spells a
// token out. | 模板以 .Perm 读取它，例如 `{{ if can .Perm.UserCreate }}`，因此模板永远不必写出标识。
func PermMap() map[string]string {
	perms := Permissions()
	out := make(map[string]string, len(perms))
	for _, p := range perms {
		out[DataKey(p.Name)] = p.Name
	}
	return out
}

// RoleMap returns the template data key of every role mapped to its name. | RoleMap 返回每个角色的模板数据键到名字的映射。
func RoleMap() map[string]string {
	roles := Roles()
	out := make(map[string]string, len(roles))
	for _, r := range roles {
		out[RoleDataKey(r.Name)] = r.Name
	}
	return out
}

// PermJSMap returns the JavaScript key of every capability mapped to its token, i.e. the map a
// script reads as yd.PERM. | PermJSMap 返回每条能力的 JavaScript 键到标识的映射，即脚本以
// yd.PERM 读取的那张表。
//
// It is a second *spelling* of the same registry entry and never a second definition: both maps are
// derived from Permissions() on every call, so a capability added in one place appears in all four
// spellings at once. | 它是同一条注册表条目的另一种*写法*，绝不是第二份定义：两张映射每次都由
// Permissions() 派生，因此在任何一处新增能力都会同时出现在全部四种写法里。
func PermJSMap() map[string]string {
	perms := Permissions()
	out := make(map[string]string, len(perms))
	for _, p := range perms {
		out[JSKey(p.Name)] = p.Name
	}
	return out
}

// RoleJSMap returns the JavaScript key of every role mapped to its name, i.e. the map a script reads
// as yd.ROLE. | RoleJSMap 返回每个角色的 JavaScript 键到名字的映射，即脚本以 yd.ROLE 读取的那张表。
func RoleJSMap() map[string]string {
	roles := Roles()
	out := make(map[string]string, len(roles))
	for _, r := range roles {
		out[RoleJSKey(r.Name)] = r.Name
	}
	return out
}

// Data returns the render data of one request. | Data 返回单次请求的渲染数据。
//
// It is the one line a page handler needs, and it merges into the data the application already
// passes to the template engine:
//
//	data := authz.Data(subject)
//	data["Users"] = users
//	return c.HTML(http.StatusOK, "pages/users.html", data)
//
// 它是页面处理器唯一需要的一行，并可与应用已有的渲染数据合并（见上面的片段）。
//
// Merge it into the map rather than into a struct: the decided helpers are bound per execution by
// the render package, and that binding reads the data as a map[string]any. | 请把它合并进 map 而不是结构体：
// 判定辅助函数由 render 包按次执行绑定，而该绑定是把数据当作 map[string]any 读取的。
func Data(s *Subject) map[string]any {
	env := NewEnvelope(s)
	data := templateDataOf(env, s)
	data["AuthzJSON"] = env.JS()
	return data
}

// TemplateData returns the render data a fragment needs: the same authority and the two template
// name maps as Data, without building the browser envelope. | TemplateData 返回片段所需的渲染数据：
// 与 Data 相同的判定主体与两张模板名字表，但不构建浏览器信封。
//
// The envelope is only consumed by the document shell's <script id="yd-authz">, and serialising it on
// every history/rail refresh is pure work: a fragment never renders the shell. | 信封只被文档外壳的
// <script id="yd-authz"> 使用；每次历史/会话栏刷新都序列化它纯属浪费，因为片段从不渲染外壳。
func TemplateData(s *Subject) map[string]any {
	return templateDataOf(NewEnvelope(s), s)
}

// templateDataOf builds the shared template data from an envelope already in hand. |
// templateDataOf 用已构建好的信封组装共享模板数据。
func templateDataOf(env Envelope, s *Subject) map[string]any {
	return map[string]any{
		render.AuthzKey: authority{
			subject: s,
			granted: stringSet(env.Granted),
			roles:   stringSet(env.Mine),
			key:     decisionKey(env),
		},
		"Perm": PermMap(),
		"Role": RoleMap(),
	}
}

// stringSet turns a sorted string slice into a lookup set. | stringSet 把已排序的字符串切片转为查找集合。
func stringSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}
	return out
}

// decisionKey renders the decision set as one stable, unambiguous string. | decisionKey 把判定集合渲染为一个稳定且无歧义的字符串。
//
// Every item carries a length prefix ("len:item"), so a role name that itself contains a separator
// byte cannot merge two different sets into the same cache key. (Role names are only required to be
// non-empty, so this must not rely on their charset.) An anonymous subject has an empty key, which
// is itself the correct identity for "deny everything". | 每一项都带长度前缀（"len:item"），因此角色名
// 自身含有分隔字节时也无法把两个不同的集合合并成同一个缓存键。（角色名只要求非空，因此不能依赖其
// 字符集。）匿名主体的键为空串，而空串正是「全部拒绝」这个判定集合的正确身份。
func decisionKey(env Envelope) string {
	if len(env.Granted) == 0 && len(env.Mine) == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(env.Granted)*18 + len(env.Mine)*10)
	for _, p := range env.Granted {
		b.WriteString(strconv.Itoa(len(p)))
		b.WriteByte(':')
		b.WriteString(p)
	}
	b.WriteByte('|')
	for _, r := range env.Mine {
		b.WriteString(strconv.Itoa(len(r)))
		b.WriteByte(':')
		b.WriteString(r)
	}
	return b.String()
}

// authority adapts a possibly anonymous subject to the render helpers. | authority 把可能为匿名的主体适配给模板辅助函数。
//
// The ydx/log warnings live here and not in Subject: a template reference is a hand-written name that
// `yd authz check` is expected to have verified, while a Go reference is checked by the compiler
// against the constants - so warning once per rendered page is the right volume for the first case
// and pure noise for the second. | 日志告警放在这里而不是 Subject 里：模板里的引用是手写的名字，
// 本应由 `yd authz check` 校验；而 Go 里的引用由编译器对着常量校验——
// 因此「每渲染一次页面告警一次」对前者是恰当的量，对后者纯属噪声。
type authority struct {
	// subject is the subject of the page being rendered, or nil when the request is anonymous. | subject 是被渲染页面的主体，匿名请求时为 nil。
	subject *Subject
	// granted is the capability set captured once by Data, so every can call is a map lookup. | granted 是 Data 一次性捕获的能力集合，使每次 can 调用只是一次映射查找。
	granted map[string]struct{}
	// roles is the role set captured once by Data. | roles 是 Data 一次性捕获的角色集合。
	roles map[string]struct{}
	// key identifies the decision set for the render cache. | key 为渲染缓存标识该判定集合。
	key string
}

// Can reports whether the subject holds the referenced capability and never fails the render. | Can 报告主体是否持有引用的能力，且绝不让渲染失败。
//
// An unknown reference renders "not granted" rather than aborting the page: the helper only ever
// decides whether to draw something, and taking a whole page down over a hidden button would turn
// a cosmetic typo into an outage. The typo is reported instead - here, once per render, and by
// `yd authz check` before the code is ever deployed. | 引用未知时渲染为「未授予」而不是中断页面：
// 该辅助函数只决定要不要画出来，为了一个隐藏按钮干掉整页会把排版级的拼写错误升级成故障。
// 拼写错误改由两处上报——这里每渲染一次告警一次，以及代码部署之前的 `yd authz check`。
func (a authority) Can(perm string) bool {
	token, err := ResolvePermission(perm)
	if err != nil {
		return false
	}
	_, ok := a.granted[token]
	return ok
}

// HasRole reports whether the subject holds the referenced role. | HasRole 报告主体是否持有引用的角色。
func (a authority) HasRole(role string) bool {
	name, err := ResolveRole(role)
	if err != nil {
		return false
	}
	_, ok := a.roles[name]
	return ok
}

// DecisionKey returns the stable key of this render's decision set. | DecisionKey 返回本次渲染判定集合的稳定键。
//
// The template engine uses it to cache a bound template set: two renders with the same key answer
// every Can and HasRole identically, so they can share one set instead of cloning the whole parsed
// tree per request. | 模板引擎用它缓存已绑定的模板集：相同键的两次渲染对每个 Can 与 HasRole 的答案
// 都相同，因此可以共享同一个集合，而不必为每个请求克隆整棵解析树。
func (a authority) DecisionKey() string { return a.key }

// Perm resolves a capability reference to its token, or to "" when it is unknown. | Perm 把能力引用解析为标识，未知时解析为空串。
//
// The empty string is the fail-closed answer for the declarative front end: an element whose
// data-yd-perm attribute is empty matches no capability and is therefore hidden. | 空串是声明式前端的失败关闭答案：
// data-yd-perm 属性为空的元素匹配不到任何能力，因此会被隐藏。
func (a authority) Perm(ref string) string {
	token, err := ResolvePermission(ref)
	if err != nil {
		log.Default().Warn("authz: a template references an unregistered permission | authz：模板引用了未注册的权限",
			log.Str("permission", ref))
		return ""
	}
	return token
}

// Role resolves a role reference to its name, or to "" when it is unknown. | Role 把角色引用解析为名字，未知时解析为空串。
func (a authority) Role(ref string) string {
	name, err := ResolveRole(ref)
	if err != nil {
		log.Default().Warn("authz: a template references an unregistered role | authz：模板引用了未注册的角色",
			log.Str("role", ref))
		return ""
	}
	return name
}
