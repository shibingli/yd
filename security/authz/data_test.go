package authz_test

import (
	"html/template"
	"testing"

	"github.com/shibingli/yd/security/authz"
	"github.com/shibingli/ydx/json"
	"github.com/shibingli/ydx/render"
)

// TestPermMapMatchesTheRegistry 要求模板数据键与注册表逐条对应。
//
// The map is what a template reads as .Perm, so a missing entry silently hides a control and a
// stale one silently names a capability that no longer exists. | 模板以 .Perm 读取这张映射，
// 因此漏一条会静默地藏起一个控件，多一条会静默地称呼一个已不存在的能力。
func TestPermMapMatchesTheRegistry(t *testing.T) {
	perms := authz.Permissions()
	m := authz.PermMap()
	if len(m) != len(perms) {
		t.Fatalf("PermMap has %[1]d entries for %[2]d permissions | PermMap 有 %[1]d 条，权限有 %[2]d 条", len(m), len(perms))
	}
	for _, p := range perms {
		key := authz.DataKey(p.Name)
		got, ok := m[key]
		if !ok {
			t.Errorf("PermMap is missing the data key %q for %q", key, p.Name)
			continue
		}
		if got != p.Name {
			t.Errorf("PermMap[%q] = %q, want %q", key, got, p.Name)
		}
	}
	// The one spelling the plan pins down, so a rename of the constant cannot pass unnoticed.
	// 计划里写定的那一个写法，好让常量改名无法悄悄通过。
	if got := m["UserCreate"]; got != authz.PermUserCreate {
		t.Errorf(".Perm.UserCreate = %q, want %q | .Perm.UserCreate = %q，期望 %q", got, authz.PermUserCreate, got, authz.PermUserCreate)
	}
}

// TestRoleMapMatchesTheRegistry requires the role data keys to map one-to-one as well. | TestRoleMapMatchesTheRegistry 要求角色数据键同样逐条对应。
func TestRoleMapMatchesTheRegistry(t *testing.T) {
	roles := authz.Roles()
	m := authz.RoleMap()
	if len(m) != len(roles) {
		t.Fatalf("RoleMap has %[1]d entries for %[2]d roles | RoleMap 有 %[1]d 条，角色有 %[2]d 条", len(m), len(roles))
	}
	for _, r := range roles {
		key := authz.RoleDataKey(r.Name)
		if got := m[key]; got != r.Name {
			t.Errorf("RoleMap[%q] = %q, want %q", key, got, r.Name)
		}
	}
	if got := m["Admin"]; got != authz.RoleAdmin {
		t.Errorf(".Role.Admin = %q, want %q", got, authz.RoleAdmin)
	}
}

// TestEnvelopeCarriesNamesAndHoldings 要求载荷同时下发全部名字与本次实际持有的内容。
//
// The name maps go out whole - a page may need to *name* a capability it does not hold, e.g. in a
// tooltip - while the lists are exactly the subject's. | 名字表完整下发——页面可能需要「称呼」一个
// 自己并不持有的能力，例如写在提示里——而列表则正是该主体持有的内容。
func TestEnvelopeCarriesNamesAndHoldings(t *testing.T) {
	f := newFixture(t)
	env := authz.NewEnvelope(authz.NewSubject(f.svc, f.reader))
	if len(env.Perms) != len(authz.Permissions()) || len(env.Roles) != len(authz.Roles()) {
		t.Errorf("the name maps are incomplete: %[1]d perms, %[2]d roles | 名字表不完整：权限 %[1]d，角色 %[2]d", len(env.Perms), len(env.Roles))
	}
	// The documented script spelling, so a rename of the derivation cannot pass unnoticed.
	// 文档里写定的脚本写法，好让派生规则的改动无法悄悄通过。
	if got := env.Perms["USER_CREATE"]; got != authz.PermUserCreate {
		t.Errorf("yd.PERM.USER_CREATE = %q, want %q", got, authz.PermUserCreate)
	}
	if got := env.Roles["ADMIN"]; got != authz.RoleAdmin {
		t.Errorf("yd.ROLE.ADMIN = %q, want %q", got, authz.RoleAdmin)
	}
	if len(env.Granted) != 2 || env.Granted[0] != authz.PermRoleRead || env.Granted[1] != authz.PermUserRead {
		t.Errorf("Granted = %[1]v | Granted = %[1]v", env.Granted)
	}
	if len(env.Mine) != 1 || env.Mine[0] != authz.RoleMember {
		t.Errorf("Mine = %[1]v | Mine = %[1]v", env.Mine)
	}

	// An anonymous visitor still receives the name maps, with two empty lists: that is what lets the
	// front end deny everything without a special case. | 匿名访客同样收到名字表，只是两个列表为空：
	// 正是这一点让前端无需特例即可全部拒绝。
	anon := authz.NewEnvelope(nil)
	if len(anon.Perms) != len(authz.Permissions()) || len(anon.Roles) != len(authz.Roles()) {
		t.Errorf("the anonymous payload lost the name maps | 匿名载荷丢失了名字表")
	}
	if len(anon.Granted) != 0 || len(anon.Mine) != 0 {
		t.Errorf("the anonymous payload granted something: %v %v | 匿名载荷授予了内容：%v %v", anon.Granted, anon.Mine, anon.Granted, anon.Mine)
	}
}

// TestEnvelopeJSMarshal requires the inline JSON to be valid, safe inside a script element and consistent with the registry. | TestEnvelopeJSMarshal 要求内联 JSON 是合法 JSON、可安全放进脚本元素，且与注册表一致。
func TestEnvelopeJSMarshal(t *testing.T) {
	f := newFixture(t)
	raw := string(authz.NewEnvelope(authz.NewSubject(f.svc, f.reader)).JS())
	if !json.Valid([]byte(raw)) {
		t.Fatalf("the payload is not valid JSON: %s | 载荷不是合法 JSON：%s", raw, raw)
	}
	// A script element ends at the first "</script", so an unescaped "<" in a capability name would
	// end the payload early; ydx/json escapes it. | 脚本元素在第一个 "</script" 处结束，
	// 因此能力名里未转义的 "<" 会提前结束载荷；ydx/json 会转义它。
	if got := indexOfScriptEnd(raw); got >= 0 {
		t.Errorf("the payload carries a literal </script at %d | 载荷在第 %d 字节处带有字面量 </script", got, got)
	}

	var env authz.Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	// Every key of both maps must be the JS key of the token it maps to: the script reads
	// yd.PERM.USER_CREATE, so a key that does not derive from its token would be a second definition.
	// 两张映射的每个键都必须是它所映射标识的 JS 键：脚本读的是 yd.PERM.USER_CREATE，
	// 因此不派生自该标识的键就成了第二份定义。
	for key, token := range env.Perms {
		if got := authz.JSKey(token); got != key {
			t.Errorf("perms[%q] = %q, but its JS key is %q", key, token, got)
		}
		if !authz.Registered(token) {
			t.Errorf("perms[%q] names the unregistered token %q", key, token)
		}
	}
	for key, name := range env.Roles {
		if got := authz.RoleJSKey(name); got != key {
			t.Errorf("roles[%q] = %q, but its JS key is %q", key, name, got)
		}
		if !authz.RoleRegistered(name) {
			t.Errorf("roles[%q] names the unregistered role %q", key, name)
		}
	}
	if len(env.Granted) != 2 || len(env.Mine) != 1 {
		t.Errorf("the payload lost the holdings: %v %v", env.Granted, env.Mine)
	}
}

// TestDataCarriesTheFourEntries requires one row of render data to feed the template helpers, the selectors and the inline payload at once. | TestDataCarriesTheFourEntries 要求一行渲染数据就能同时喂给模板辅助函数、选择器与内联载荷。
func TestDataCarriesTheFourEntries(t *testing.T) {
	f := newFixture(t)
	data := authz.Data(authz.NewSubject(f.svc, f.reader))
	auth, ok := data[render.AuthzKey].(render.Authz)
	if !ok {
		t.Fatalf("%s does not satisfy render.Authz: %T | %s 不满足 render.Authz：%T", render.AuthzKey, data[render.AuthzKey], render.AuthzKey, data[render.AuthzKey])
	}
	if !auth.Can(authz.PermUserRead) || auth.HasRole(authz.RoleMember) != true {
		t.Errorf("the injected authority decides wrongly | 注入的判定主体判定有误")
	}
	if got := auth.Perm("PermUserCreate"); got != authz.PermUserCreate {
		t.Errorf("Perm(PermUserCreate) = %q", got)
	}
	if got := auth.Role("Admin"); got != authz.RoleAdmin {
		t.Errorf("Role(Admin) = %q", got)
	}
	perms, ok := data["Perm"].(map[string]string)
	if !ok || perms["UserCreate"] != authz.PermUserCreate {
		t.Errorf("data[Perm] = %#v", data["Perm"])
	}
	roles, ok := data["Role"].(map[string]string)
	if !ok || roles["Admin"] != authz.RoleAdmin {
		t.Errorf("data[Role] = %#v", data["Role"])
	}
	payload, ok := data["AuthzJSON"].(template.JS)
	if !ok {
		t.Fatalf("data[AuthzJSON] is %[1]T, want template.JS | data[AuthzJSON] 为 %[1]T，期望 template.JS", data["AuthzJSON"])
	}
	if !json.Valid([]byte(payload)) {
		t.Errorf("the inline payload is not valid JSON: %s", payload)
	}

	// And the one line a handler writes really renders: the four helpers read the injected authority.
	// 而处理器真正写下的那一行确实能渲染：四个辅助函数读到的正是注入的判定主体。
	funcs := decisionFuncs(auth)
	tmpl, err := template.New("t").Funcs(funcs).Parse(`{{ if can .Perm.UserCreate }}create{{ end }}|{{ if hasRole "Admin" }}admin{{ else }}not-admin{{ end }}|{{ perm "PermUserCreate" }}|{{ role .Role.Admin }}`)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	var buf writer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	want := "|not-admin|" + authz.PermUserCreate + "|" + authz.RoleAdmin
	if buf.String() != want {
		t.Errorf("rendered %q, want %q", buf.String(), want)
	}
}

// decisionFuncs wires the four helpers to one authority, the way render does for one execution.
// decisionFuncs 把四个辅助函数接到同一个判定主体上，正是 render 为单次执行所做的接线。
//
// The binding is rebuilt here rather than imported because render's own is internal; what this test
// pins down is the *contract* both sides rely on - the helpers read the render.Authz stored under
// render.AuthzKey in a map[string]any, which is exactly what Data returns. The real binding is
// covered by render's own tests. | 这里重建绑定而不是导入，因为 render 自己的那套是内部的；
// 本测试钉住的是双方共同依赖的那份「契约」——辅助函数从 map[string]any 里的 render.AuthzKey 下
// 取出 render.Authz，而 Data 返回的正是一张这样的映射。真正的绑定由 render 自己的测试覆盖。
func decisionFuncs(a render.Authz) template.FuncMap {
	return template.FuncMap{
		"can":     func(ref any) bool { return a != nil && a.Can(refString(ref)) },
		"hasRole": func(ref any) bool { return a != nil && a.HasRole(refString(ref)) },
		"perm": func(ref any) string {
			if a == nil {
				return refString(ref)
			}
			return a.Perm(refString(ref))
		},
		"role": func(ref any) string {
			if a == nil {
				return refString(ref)
			}
			return a.Role(refString(ref))
		},
	}
}

// refString normalises a helper argument the way render does. | refString 按 render 的方式把辅助函数参数归一化。
func refString(v any) string {
	s, _ := v.(string)
	return s
}

// writer is a minimal io.Writer for the template execution above. | writer 是上面模板执行所需的最小 io.Writer。
type writer struct{ b []byte }

// Write appends p. | Write 追加 p。
func (w *writer) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }

// String returns what was written. | String 返回已写入的内容。
func (w *writer) String() string { return string(w.b) }

// indexOfScriptEnd returns the offset of a literal "</script" in s, or -1. | indexOfScriptEnd 返回 s 中字面量 "</script" 的偏移，没有则返回 -1。
func indexOfScriptEnd(s string) int {
	for i := 0; i+8 <= len(s); i++ {
		if s[i:i+8] == "</script" {
			return i
		}
	}
	return -1
}
