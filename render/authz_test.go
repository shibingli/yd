package render_test

import (
	"bytes"
	"html/template"
	"strings"
	"sync"
	"testing"

	"github.com/shibingli/yd/render"
)

// stubAuthz is one subject: the sets of capabilities and roles it holds, over a fixed vocabulary.
// stubAuthz 是一个判定主体：在固定词汇表上它持有的能力集合与角色集合。
//
// It is a stub rather than authz.Subject so this file tests the engine's contract and nothing else:
// what a decision is made of belongs to the authz package's own tests. | 这里用桩而不是 authz.Subject，
// 使本文件只测引擎的契约：判定由什么构成属于 authz 包自己的测试。
type stubAuthz struct {
	grants map[string]bool
	roles  map[string]bool
}

// Can reports whether the stub holds the referenced capability. | Can 报告桩是否持有引用的能力。
//
// It resolves first, exactly as the real authority does: a template passes whatever it has - a data
// key, a constant name or a token - and resolving it is the authority's job, not the engine's.
// 它先解析，与真正的判定主体完全一致：模板传进来的东西是不定的——数据键、常量名或标识——
// 而解析是判定主体的职责，不是引擎的。
func (s stubAuthz) Can(perm string) bool {
	token := s.Perm(perm)
	return token != "" && s.grants[token]
}

// HasRole reports whether the stub holds the referenced role. | HasRole 报告桩是否持有引用的角色。
func (s stubAuthz) HasRole(role string) bool {
	name := s.Role(role)
	return name != "" && s.roles[name]
}

// Perm resolves a capability reference to its token. | Perm 把能力引用解析为标识。
func (s stubAuthz) Perm(ref string) string {
	switch ref {
	case "PermUserCreate", "UserCreate", "user:create":
		return "user:create"
	default:
		return ""
	}
}

// Role resolves a role reference to its name. | Role 把角色引用解析为名字。
func (s stubAuthz) Role(ref string) string {
	switch ref {
	case "RoleAdmin", "Admin", "admin":
		return "admin"
	default:
		return ""
	}
}

// authzTemplate is the fragment every case in this file renders. | authzTemplate 是本文件每个用例渲染的片段。
const authzTemplate = `templates/pages/probe.html`

// authzPage is one line per helper, so a single render shows all four decisions. | authzPage 每个辅助函数一行，一次渲染即可看到全部四项判定。
const authzPage = `create={{ if can "PermUserCreate" }}yes{{ else }}no{{ end }}` +
	`|delete={{ if can .Perm.UserDelete }}yes{{ else }}no{{ end }}` +
	`|admin={{ if hasRole "RoleAdmin" }}yes{{ else }}no{{ end }}` +
	`|perm={{ perm "PermUserCreate" }}|role={{ role .Role.Admin }}`

// probeData is the render data of one probe: the authority plus the two name maps a template reads
// as .Perm and .Role. | probeData 是一次探针的渲染数据：判定主体，以及模板以 .Perm 与 .Role 读取的两张名字表。
func probeData(a stubAuthz) map[string]any {
	return map[string]any{
		render.AuthzKey: a,
		"Perm":          map[string]string{"UserCreate": "user:create", "UserDelete": "user:delete"},
		"Role":          map[string]string{"Admin": "admin"},
	}
}

// newAuthzEngine builds an engine over the one probe template. | newAuthzEngine 基于这一份探针模板构建引擎。
func newAuthzEngine(t *testing.T, extra template.FuncMap) *render.Engine {
	t.Helper()
	e, err := render.New(writeTemplates(t, map[string]string{authzTemplate: authzPage}), render.Options{Funcs: extra})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return e
}

// renderProbe renders the probe with data and returns the body. | renderProbe 用 data 渲染探针并返回正文。
func renderProbe(t *testing.T, e *render.Engine, data any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := e.Render(&buf, "pages/probe.html", data); err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	return buf.String()
}

// TestAuthzHelpersDecidePerExecution 要求同一份模板集在两次渲染中按两次注入的主体分别判定。
//
// One engine serves every request, so the binding has to happen per execution; if it did not, the
// first request would decide for all of them. | 一个引擎服务所有请求，因此绑定必须按次执行发生；
// 若不是，第一个请求就会替所有请求作出判定。
func TestAuthzHelpersDecidePerExecution(t *testing.T) {
	e := newAuthzEngine(t, nil)
	full := probeData(stubAuthz{
		grants: map[string]bool{"user:create": true},
		roles:  map[string]bool{"admin": true},
	})
	none := probeData(stubAuthz{})

	if got := renderProbe(t, e, full); got != "create=yes|delete=no|admin=yes|perm=user:create|role=admin" {
		t.Errorf("the granted subject rendered %q", got)
	}
	if got := renderProbe(t, e, none); got != "create=no|delete=no|admin=no|perm=user:create|role=admin" {
		t.Errorf("the empty subject rendered %q", got)
	}
	// And the first render is not remembered: the granted subject is granted again.
	if got := renderProbe(t, e, full); !strings.HasPrefix(got, "create=yes") {
		t.Errorf("the binding leaked between executions: %q", got)
	}
}

// TestAuthzHelpersWithoutAnAuthority 要求未注入判定主体时四个辅助函数一律失败关闭。
//
// A page rendered without permissions is a normal page - a sign-in screen, a public listing - so a
// missing wiring must not fail the render; it must only refuse. | 不做权限判定就渲染的页面是正常页面——
// 登录页、公开列表——因此漏装配不得让渲染失败，只应拒绝。
func TestAuthzHelpersWithoutAnAuthority(t *testing.T) {
	e := newAuthzEngine(t, nil)
	got := renderProbe(t, e, map[string]any{"Title": "no authority here"})
	want := "create=no|delete=no|admin=no|perm=PermUserCreate|role="
	if got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}

	// Data that is not a map at all takes the same path: with no authority under AuthzKey the
	// built-in helpers answer, and a quoted constant name still comes back unchanged.
	// 完全不是映射的数据走同一条路径：AuthzKey 下没有判定主体，内建辅助函数作答，
	// 而带引号的常量名仍原样返回。
	plain := map[string]string{
		"templates/pages/plain.html": `create={{ if can "PermUserCreate" }}yes{{ else }}no{{ end }}` +
			`|admin={{ if hasRole "RoleAdmin" }}yes{{ else }}no{{ end }}` +
			`|perm={{ perm "PermUserCreate" }}|role={{ role "RoleAdmin" }}`,
	}
	pe, err := render.New(writeTemplates(t, plain), render.Options{})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	var buf bytes.Buffer
	if err := pe.Render(&buf, "pages/plain.html", struct{ Title string }{"also no authority"}); err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if got := buf.String(); got != "create=no|admin=no|perm=PermUserCreate|role=RoleAdmin" {
		t.Errorf("a struct data value rendered %q", got)
	}

	// A map that carries the key but a value of the wrong type is refused too: the assertion is
	// what decides, so a string under AuthzKey cannot smuggle a decision in.
	// 带键但值类型不对的映射同样拒绝：判定全凭断言，因此 AuthzKey 下放一个字符串塞不进任何判定。
	if got := renderProbe(t, e, map[string]any{render.AuthzKey: "admin"}); got != want {
		t.Errorf("a forged authority rendered %q, want %q", got, want)
	}
}

// TestAuthzHelpersWithoutAReference 要求缺键或非字符串的引用回答「未授予」而不是让渲染失败。
//
// A map lookup that misses hands the helper a zero value, and a template that asks about a
// capability the data does not carry must render, not abort. | 映射取不到键时交给辅助函数的是零值，
// 而询问数据里没有的能力的模板必须照常渲染，而不是中断。
func TestAuthzHelpersWithoutAReference(t *testing.T) {
	files := map[string]string{
		"templates/pages/probe.html": `{{ if can .Perm.Missing }}hidden{{ end }}{{ if hasRole .Role.Missing }}hidden{{ end}}` +
			`[{{ perm .Perm.Missing }}][{{ role .Role.Missing }}]`,
	}
	e, err := render.New(writeTemplates(t, files), render.Options{})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	var buf bytes.Buffer
	data := map[string]any{
		render.AuthzKey: stubAuthz{grants: map[string]bool{"user:create": true}},
		"Perm":          map[string]string{"UserCreate": "user:create"},
		"Role":          map[string]string{},
	}
	if err := e.Render(&buf, "pages/probe.html", data); err != nil {
		t.Fatalf("Render failed on a missing reference: %v | 缺引用时渲染失败：%v", err, err)
	}
	if got := buf.String(); got != "[][]" {
		t.Errorf("rendered %q, want %q", got, "[][]")
	}
}

// TestAuthzHelpersYieldToOptionsFuncs 要求经 Options.Funcs 注册的同名函数优先于按次绑定。
//
// An application that registered its own `can` - with different semantics, or from another
// authorization model - must keep it; the binding is a default, not an override. | 自行注册过 `can` 的应用
// （语义不同，或来自另一套授权模型）必须保住它；按次绑定是默认值而不是强制覆盖。
func TestAuthzHelpersYieldToOptionsFuncs(t *testing.T) {
	own := template.FuncMap{
		"can":     func(any) bool { return true },
		"perm":    func(ref any) string { return "own:" + ref.(string) },
		"hasRole": func(any) bool { return false },
	}
	e := newAuthzEngine(t, own)
	got := renderProbe(t, e, probeData(stubAuthz{}))
	// The application's `can` answers every call, so both of its uses are granted; `hasRole` and
	// `role` were not registered by the application, so the binding answers those two.
	// 应用自己的 can 对每个调用都答「是」，因此它的两处用法都放行；hasRole 与 role 未由应用注册，
	// 这两处仍由按次绑定作答。
	want := "create=yes|delete=yes|admin=no|perm=own:PermUserCreate|role=admin"
	if got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
}

// TestAuthzBindingIsConcurrencySafe 要求并发请求各自看到自己的主体。
//
// This is the whole reason the binding clones: a template set is shared between goroutines, so an
// in-place function swap would let one request decide for another. The race detector is what makes
// this test worth running. | 这正是绑定必须克隆的全部原因：模板集在多个 goroutine 间共享，
// 就地替换函数表会让一个请求替另一个请求作出判定。让这个测试值得跑的是竞态检测器。
func TestAuthzBindingIsConcurrencySafe(t *testing.T) {
	e := newAuthzEngine(t, nil)
	const rounds = 32
	var wg sync.WaitGroup
	failures := make(chan string, rounds*2)
	for i := range rounds {
		allowed := i%2 == 0
		grant := stubAuthz{grants: map[string]bool{"user:create": allowed}}
		want := "create=no"
		if allowed {
			want = "create=yes"
		}
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var buf bytes.Buffer
				if err := e.Render(&buf, "pages/probe.html", probeData(grant)); err != nil {
					failures <- err.Error()
					return
				}
				if !strings.HasPrefix(buf.String(), want) {
					failures <- buf.String()
				}
			}()
		}
	}
	wg.Wait()
	close(failures)
	for got := range failures {
		t.Errorf("a concurrent render crossed subjects: %q", got)
	}
}

// TestAuthzPayloadStaysRawInsideAScript 要求内联载荷在脚本元素内原样输出。
//
// html/template treats <script type="application/json"> as a JavaScript container, which is what
// makes an unquoted template.JS payload land as JSON rather than as a JSON *string*; the payload is
// what the front end reads with zero extra requests. | html/template 把 <script type="application/json">
// 当作 JavaScript 容器，因此未加引号的 template.JS 载荷会以 JSON 落地，而不是变成一段 JSON *字符串*；
// 前端正是以零额外请求读取这份载荷。
func TestAuthzPayloadStaysRawInsideAScript(t *testing.T) {
	files := map[string]string{
		"templates/layouts/base.html": `<html><body><script id="yd-authz" type="application/json">{{ .AuthzJSON }}</script></body></html>`,
	}
	e, err := render.New(writeTemplates(t, files), render.Options{})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	payload := `{"perms":{"USER_CREATE":"user:create"},"roles":{"ADMIN":"admin"},"granted":["user:create"],"mine":["admin"]}`
	var buf bytes.Buffer
	if err := e.Render(&buf, "layouts/base.html", map[string]any{"AuthzJSON": template.JS(payload)}); err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if !strings.Contains(buf.String(), payload) {
		t.Fatalf("the payload was escaped or quoted: %s", buf.String())
	}
	// A plain string under the same key is quoted, which is exactly the failure the template.JS
	// type exists to prevent - pinned here so the two are distinguishable. | 同一个键下放普通字符串会被引号包住，
	// 而这正是 template.JS 类型存在的意义所在——这里把它钉住，好让两种形态可区分。
	buf.Reset()
	if err := e.Render(&buf, "layouts/base.html", map[string]any{"AuthzJSON": payload}); err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if strings.Contains(buf.String(), payload) {
		t.Errorf("a plain string payload landed verbatim, so the type carries no meaning")
	}
}
