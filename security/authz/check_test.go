package authz_test

import (
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/security/authz"
)

// checkFS writes every file of a fixture tree into a memory filesystem and runs the checker.
// checkFS 把夹具树的每个文件写入内存文件系统，然后运行检查器。
func checkFS(t *testing.T, files map[string]string) authz.Report {
	t.Helper()
	fsys := afero.NewMemMapFs()
	for name, body := range files {
		dir := name[:strings.LastIndex(name, "/")]
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) failed: %v", dir, err)
		}
		if err := afero.WriteFile(fsys, name, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) failed: %v", name, err)
		}
	}
	rep, err := authz.Check(authz.CheckOptions{FS: fsys, Dir: "app"})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	return rep
}

// finding returns the first error naming ref, and whether one exists. | finding 返回首个指向 ref 的错误，以及是否存在。
func finding(rep authz.Report, ref string) (authz.Issue, bool) {
	for _, is := range rep.Issues {
		if is.Ref == ref && is.Kind.IsError() {
			return is, true
		}
	}
	return authz.Issue{}, false
}

// hint returns the first hint naming ref, and whether one exists. | hint 返回首个指向 ref 的提示，以及是否存在。
func hint(rep authz.Report, ref string) (authz.Issue, bool) {
	for _, is := range rep.Issues {
		if is.Ref == ref && !is.Kind.IsError() {
			return is, true
		}
	}
	return authz.Issue{}, false
}

// TestCheckCatchesATypo 要求把能力标识拼错时报错，并指出文件与行号。
//
// This is the acceptance case of the whole feature: a token misspelled as user:creat compiles, runs,
// and then denies every request forever without a single error in the log. | 这是整个特性的验收用例：
// 把标识拼成 user:creat 之后照样编译、照样运行，然后永远拒绝每一个请求，日志里一个错都不报。
func TestCheckCatchesATypo(t *testing.T) {
	rep := checkFS(t, map[string]string{
		"app/internal/router/router.go": `package router

import "github.com/shibingli/yd/security/auth"

func Register(r any) {
	r.Use(auth.RequirePermission("user:creat"))
}
`,
	})
	is, ok := finding(rep, "user:creat")
	if !ok {
		t.Fatalf("the typo was not reported: %+v | 拼写错误未被报告：%+v", rep.Issues, rep.Issues)
	}
	if !strings.Contains(is.Note, "unregistered") {
		t.Errorf("the finding does not say the token is unregistered: %q", is.Note)
	}
	if is.File != "app/internal/router/router.go" || is.Line != 6 {
		t.Errorf("the finding points at %s:%d, want app/internal/router/router.go:6", is.File, is.Line)
	}
	if !strings.Contains(is.Text, "user:creat") {
		t.Errorf("the finding does not quote the source line: %q", is.Text)
	}
	if rep.OK() {
		t.Errorf("a report with a typo must not be OK | 含拼写错误的报告不得为通过")
	}
}

// TestCheckCatchesABareLiteral requires a bare literal token to be reported, with one spelling per consumer. | TestCheckCatchesABareLiteral 要求把标识写成裸字面量时报错，四处消费方各覆盖一种写法。
func TestCheckCatchesABareLiteral(t *testing.T) {
	rep := checkFS(t, map[string]string{
		"app/internal/router/router.go": `package router

func Register(g *authz.Group) {
	g.Allow("user:create")
	g.Can("tenant:read")
}

var shadow = "user:create"

func pick(k string) string {
	switch k {
	case "role:create":
		return "tenant:update"
	}
	return ""
}
`,
		"app/web/templates/pages/users.html": `<button data-yd-perm="user:update" data-yd-perm-mode="hide">{{ if can "user:delete" }}del{{ end }}</button>
`,
		"app/web/static/js/users.js": `export function wire(yd) {
  return yd.can("role:manage") && yd.hasRole("admin");
}
`,
	})
	for _, ref := range []string{
		authz.PermUserCreate,
		authz.PermTenantRead,
		authz.PermUserUpdate,
		authz.PermUserDelete,
		authz.PermRoleManage,
		authz.PermTenantUpdate,
		authz.PermRoleCreate,
		// A role name in a call that takes one is a bare literal too, even though its shape alone
		// would not identify it: the position is what makes it exact.
		// 在接收角色的调用里写角色名同样是裸字面量，尽管仅凭形态无法识别出来：让它成为精确发现的是位置。
		authz.RoleAdmin,
	} {
		if _, ok := finding(rep, ref); !ok {
			t.Errorf("the bare literal %q was not reported | 裸字面量 %q 未被报告", ref, ref)
		}
	}
	// user:create appears in two shapes here - an argument of Allow and the value of a plain
	// assignment - and both count; the switch case below carries role:create, which the list above
	// also checks for. | user:create 在这里出现两种形态——Allow 的实参、普通赋值的值——两者都要计数；
	// 下面的 switch 分支写的是 role:create，上面的清单同样检查了它。
	if got := countFindings(rep, authz.PermUserCreate); got != 2 {
		t.Errorf("user:create was reported %[1]d time(s), want 2 | user:create 被报告了 %[1]d 次，期望 2 次", got)
	}
	if got := countFindings(rep, authz.PermRoleCreate); got != 1 {
		t.Errorf("role:create was reported %[1]d time(s), want 1 | role:create 被报告了 %[1]d 次，期望 1 次", got)
	}
}

// countFindings counts the errors naming ref. | countFindings 统计指向 ref 的错误数。
func countFindings(rep authz.Report, ref string) int {
	n := 0
	for _, is := range rep.Issues {
		if is.Ref == ref && is.Kind.IsError() {
			n++
		}
	}
	return n
}

// TestCheckAcceptsTheSanctionedForms requires all four sanctioned spellings to pass silently. | TestCheckAcceptsTheSanctionedForms 要求四种被认可的写法全部静默通过。
func TestCheckAcceptsTheSanctionedForms(t *testing.T) {
	rep := checkFS(t, map[string]string{
		"app/internal/router/router.go": `package router

import (
	"github.com/shibingli/yd/security/auth"
	"github.com/shibingli/yd/security/authz"
)

func Register(r any) {
	r.Use(authz.Require(authz.PermUserCreate))
	auth.RequirePermission(authz.PermUserRead)
	_ = authz.RoleAdmin
}
`,
		"app/web/templates/pages/users.html": `<button data-yd-perm="{{ .Perm.UserCreate }}">{{ if can .Perm.UserDelete }}del{{ end }}</button>
<a data-yd-role="RoleAdmin" href="/admin">{{ if hasRole "RoleMember" }}me{{ end }}</a>
`,
		"app/web/static/js/users.js": `export function wire(yd) {
  return yd.can(yd.PERM.USER_CREATE) && yd.hasRole(yd.ROLE.ADMIN);
}
`,
	})
	if !rep.OK() {
		t.Fatalf("the sanctioned forms were reported: %+v | 被认可的写法被报告了：%+v", rep.Issues, rep.Issues)
	}
	if rep.Refs < 8 {
		t.Errorf("the scanner found %[1]d references, want at least 8 | 扫描器找到 %[1]d 处引用，期望至少 8 处", rep.Refs)
	}
}

// TestCheckReportsUnusedAsAHint 要求注册了却从未被引用的能力只作提示，不让校验失败。
//
// A capability may be granted to a role through data that no source file mentions, so a zombie
// permission is worth a glance and not a build failure. | 能力也可能经由源码中不存在的数据库授予某个角色，
// 因此僵尸权限值得瞄一眼，但不该让构建失败。
func TestCheckReportsUnusedAsAHint(t *testing.T) {
	rep := checkFS(t, map[string]string{
		"app/internal/router/router.go": `package router

import "github.com/shibingli/yd/security/authz"

func Register(r any) {
	r.Use(authz.Require(authz.PermUserRead))
}
`,
	})
	is, ok := hint(rep, authz.PermUserCreate)
	if !ok {
		t.Fatalf("no unused hint for %s | 没有针对 %s 的未引用提示", authz.PermUserCreate, authz.PermUserCreate)
	}
	if is.Kind != authz.IssueUnused || !strings.Contains(is.Note, "never referenced") {
		t.Errorf("the hint is %+v", is)
	}
	if !rep.OK() {
		t.Errorf("a hint must not fail the check: %+v | 提示不得让校验失败：%+v", rep.Issues, rep.Issues)
	}
	if rep.Hints() == 0 {
		t.Errorf("Hints() = 0")
	}
}

// TestCheckUnderstandsProjectVocabulary 要求项目自行声明的能力与角色被吸收进词汇表。
//
// The framework's table is not the project's: a generated project declares its own capabilities next
// to the framework's, and the checker must read that declaration instead of reporting every use of it.
// 框架的表不是项目的表：生成的项目会把自己的能力与框架的并列声明，检查器必须读到那份声明，
// 而不是把它的每一处使用都报出来。
func TestCheckUnderstandsProjectVocabulary(t *testing.T) {
	rep := checkFS(t, map[string]string{
		"app/internal/authz/perms.go": `package authz

import "github.com/shibingli/yd/security/authz"

const PermInvoiceApprove = "invoice:approve"

func init() {
	authz.RegisterAll([]authz.Permission{{Name: PermInvoiceApprove, Description: "Approve | 审批"}}, nil)
}
`,
		"app/internal/router/router.go": `package router

import (
	"github.com/shibingli/yd/security/auth"
	"github.com/shibingli/yd/security/authz"
)

func Register(r any) {
	r.Use(auth.RequirePermission(authz.PermInvoiceApprove))
	r.Use(auth.Require(authz.PermInvoiceApprove))
}
`,
	})
	if !rep.OK() {
		t.Fatalf("the project's own capability was reported: %+v | 项目自己的能力被报告了：%+v", rep.Issues, rep.Issues)
	}
	if _, ok := hint(rep, "invoice:approve"); ok {
		t.Errorf("a referenced capability was reported unused | 被引用的能力被报成未引用")
	}
	// The same declaration with a typo must still be caught: absorbing a vocabulary never means
	// "anything this project writes is fine". | 同一份声明写错仍须抓住：吸收词汇表绝不等于
	// 「这个项目写什么都行」。
	rep = checkFS(t, map[string]string{
		"app/internal/router/router.go": `package router

import "github.com/shibingli/yd/security/auth"

func Register(r any) {
	r.Use(auth.RequirePermission("invoice:aproved"))
}
`,
	})
	if _, ok := finding(rep, "invoice:aproved"); !ok {
		t.Errorf("a typo in the project's own group was not reported | 项目自有分组里的拼写错误未被报告")
	}
}

// TestCheckReportsSelectorsAndKindMismatches requires a selector reading a wrong key and an attribute carrying the wrong kind to both be caught. | TestCheckReportsSelectorsAndKindMismatches 要求选择器读错键与属性写错类别都被抓住。
func TestCheckReportsSelectorsAndKindMismatches(t *testing.T) {
	rep := checkFS(t, map[string]string{
		"app/web/templates/pages/users.html": `<button data-yd-perm="{{ .Perm.UserCreat }}">typo</button>
<a data-yd-perm="{{ .Perm.Admin }}">a role under a permission attribute</a>
<span data-yd-role="{{ .Role.UserRead }}">a permission under a role attribute</span>
`,
	})
	for _, ref := range []string{"UserCreat", "Admin", "UserRead"} {
		if _, ok := finding(rep, ref); !ok {
			t.Errorf("the misread key %q was not reported | 读错的键 %q 未被报告", ref, ref)
		}
	}
	// The two attributes are nested in {{ }}, so each selector is scanned once and reported once:
	// a duplicate would make the report twice as long as the problem. | 两个属性都嵌在 {{ }} 里，
	// 因此每个选择器只被扫描一次、只报告一次：重复报告会让报告比问题长一倍。
	if rep.Errors() != 3 {
		t.Errorf("Errors() = %d, want 3 | Errors() = %d，期望 3", rep.Errors(), rep.Errors())
	}
}

// TestCheckSkipsTestsAndTestdata 要求测试文件与 testdata 不参与判定。
//
// Test files speak about the registry by design - they register throwaway capabilities and assert
// that a typo is refused - so scanning them would report the checker's own test data as breakage.
// 测试文件本来就按设计讨论注册表——它们注册一次性能力并断言拼写错误会被拒绝——
// 因此扫描它们只会把检查器自己的测试数据报成损坏。
func TestCheckSkipsTestsAndTestdata(t *testing.T) {
	rep := checkFS(t, map[string]string{
		"app/internal/router/router_test.go": `package router

func TestGuard(t *testing.T) {
	_ = "user:creat"
}
`,
		"app/testdata/broken/router.go": `package broken

var _ = "user:creat"
`,
		"app/web/static/js/users.test.mjs": `export const broken = "user:creat";
`,
	})
	if !rep.OK() {
		t.Fatalf("test files were scanned: %+v | 测试文件被扫描了：%+v", rep.Issues, rep.Issues)
	}
}

// TestCheckIgnoresNonTokenLiterals 要求与能力无关的字符串不进入报告。
//
// The checker has to live in a real project: MIME types, SSE event names, CSS classes and markdown
// documentation are everywhere, and a report that mentions them is a report nobody reads.
// 检查器必须能待在一个真实项目里：媒体类型、SSE 事件名、CSS 类名与 markdown 文档无处不在，
// 一份会提到它们的报告就是没人会读的报告。
// TestCheckAcceptsAbsoluteUnixDir proves an absolute Unix directory is scanned
// as written instead of being turned into a relative path. | TestCheckAcceptsAbsoluteUnixDir
// 证明 Unix 绝对目录会按原样扫描，而不会被改成相对路径。
func TestCheckAcceptsAbsoluteUnixDir(t *testing.T) {
	fsys := afero.NewMemMapFs()
	if err := fsys.MkdirAll("/work/app", 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v | 建目录失败", err)
	}
	body := "package main\n\nvar _ = auth.RequirePermission(\"user:creat\")\n"
	if err := afero.WriteFile(fsys, "/work/app/main.go", []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v | 写文件失败", err)
	}
	rep, err := authz.Check(authz.CheckOptions{FS: fsys, Dir: "/work/app"})
	if err != nil {
		t.Fatalf("Check failed: %v | 检查失败", err)
	}
	if rep.Files != 1 {
		t.Fatalf("Files = %d, want 1 | 扫描文件数不符", rep.Files)
	}
	// The reported path must start with the root as written, so a reader can paste it into an
	// editor; trimming the leading "/" produced a different, non-openable path. | 报告路径必须以原样传入的
	// 根开头，读者才能直接粘进编辑器；裁掉开头的 "/" 会得到另一个无法打开的路径。
	is, ok := finding(rep, "user:creat")
	if !ok {
		t.Fatal("the typo was not reported | 拼写错误未被报告")
	}
	if !strings.HasPrefix(is.File, "/work/app/") {
		t.Errorf("issue path = %q，期望以 /work/app/ 开头 | want a path below /work/app/", is.File)
	}
}

func TestCheckIgnoresNonTokenLiterals(t *testing.T) {
	rep := checkFS(t, map[string]string{
		"app/internal/sse/broker.go": `package sse

const event = "yd:close"

func write(w any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = "application/json"
}
`,
		"app/web/templates/partials/row.html": `<tr style="display:none" hx-sse:close="yd:close"><td>{{ .Name }}</td></tr>
`,
		"app/docs/permissions.md": "A page writes `user:create` in its prose.\n",
		"app/AGENTS.md.tmpl":      "The project registers its capabilities with `authz.RegisterAll`.\n",
	})
	if !rep.OK() {
		t.Fatalf("unrelated strings were reported: %+v | 无关字符串被报告了：%+v", rep.Issues, rep.Issues)
	}
}

// TestFrameworkTreePassesItsOwnCheck 要求本仓自身通过校验。
//
// The framework is the first project the checker has to survive: its own registry, its own
// templates and the templates it generates must all be free of unregistered references and bare
// literals, or the tool ships broken. | 框架是检查器必须活下来的第一个项目：它自己的注册表、
// 自己的模板，以及它生成的模板，都不得出现未注册引用与裸字面量，否则这个工具就是带病发布的。
func TestFrameworkTreePassesItsOwnCheck(t *testing.T) {
	rep, err := authz.Check(authz.CheckOptions{Dir: ".."})
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("%[1]d error(s) in the framework tree | 框架树中有 %[1]d 个错误\n%+[1]v", rep.Errors(), rep.Issues)
	}
	if rep.Files == 0 || rep.Refs == 0 {
		t.Fatalf("the walk found nothing: %+v | 遍历什么也没找到：%+v", rep, rep)
	}
}
