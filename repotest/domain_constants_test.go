// Package repotest 的领域字符串守卫：状态、动作与其它机器枚举不得在业务代码、模板或脚本里裸写。
// The domain-string guard of package repotest: states, actions and other machine enumerations must not
// appear as bare literals in business code, templates or scripts.

package repotest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shibingli/ydx/regexpx"
)

// domainLiteralHints maps the machine value to the constant (or family) that should replace it. |
// domainLiteralHints 把机器值映射到应替换它的常量（或常量族）。
var domainLiteralHints = map[string]string{
	"active":       "auth.StatusActive / messaging.ConversationActive / metricsx.StateActive",
	"disabled":     "auth.StatusDisabled / dictionary.StatusDisabled / workflow.DefinitionDisabled",
	"draft":        "workflow.DefinitionDraft / workflow.VersionDraft",
	"published":    "workflow.DefinitionPublished / workflow.VersionPublished",
	"running":      "workflow.InstanceRunning",
	"completed":    "workflow.InstanceCompleted / workflow.TaskCompleted / workflow.TokenCompleted",
	"withdrawn":    "workflow.InstanceWithdrawn",
	"terminated":   "workflow.InstanceTerminated",
	"pending":      "workflow.TaskPending / attachments.StatusPending / messaging.FriendPending",
	"claimed":      "workflow.TaskClaimed",
	"cancelled":    "workflow.TaskCancelled / messaging.FriendCancelled",
	"queued":       "workflow.TaskQueued",
	"waiting":      "workflow.TokenWaiting",
	"dissolved":    "messaging.ConversationDissolved",
	"sent":         "messaging.StatusSent",
	"recalled":     "messaging.StatusRecalled",
	"accepted":     "messaging.FriendAccepted",
	"rejected":     "messaging.FriendRejected / workflow.EventTaskRejected",
	"enabled":      "workflow.GroupEnabled / dictionary.StatusEnabled",
	"archived":     "dictionary lifecycle (ArchivedAt is a timestamp, not a status)",
	"referenced":   "attachments.StatusReferenced",
	"deleted":      "attachments.StatusDeleted",
	"approve":      "workflow.DecisionApprove",
	"reject":       "workflow.DecisionReject",
	"owner":        "authz.RoleOwner / messaging.MemberOwner",
	"member":       "messaging.MemberPlain",
	"notice":       "messaging.KindNotice",
	"direct":       "messaging.KindDirect",
	"normal":       "messaging.TypeNormal",
	"announcement": "messaging.TypeAnnouncement",
	"unread":       "message folder constants",
	"pinned":       "message folder constants",
	"inline":       "attachment disposition constants",
	"local":        "attachment store / redis mode constants",
	"redis":        "ydx/redis mode constants",
	"detection":    "ydx/waf.ModeDetection / waf mode constants",
	"blocking":     "ydx/waf.ModeBlock",
	"todo":         "workflowTabTodo",
	"started":      "workflowTabStarted",
	"done":         "workflowTabDone",
	"edit":         "form mode constants / IsEdit",
	"create":       "form mode constants / IsEdit",
}

// guardedGoPrefixes limits the Go scan to the packages that own these enumerations. Parsers for
// external protocols (driver names, Go types, platform names) are deliberately outside it. |
// guardedGoPrefixes 把 Go 扫描限制在拥有这些枚举的包内。外部协议解析器（驱动名、Go 类型、平台名）
// 被刻意排除。
var guardedGoPrefixes = []string{
	"messaging/",
	"attachments/",
	"security/auth/",
	"security/authz/",
	"dictionary/",
	"runtime/metricsx/",
	"tooling/scaffold/templates/_name_/internal/",
}

// guardedGoAllowlist exempts centralized boundary parsers and seed data, where the literal is the
// external contract rather than an internal decision. |
// guardedGoAllowlist 豁免集中式边界解析器与种子数据：那里的字面量是外部契约，而不是内部判断。
var guardedGoAllowlist = map[string]bool{
	"dictionary/builtin.go":           true, // seeded dictionary data
	"security/auth/middleware.go":     true, // TokenLookup spec parser
	"security/captcha/config.go":      true, // captcha type config parser
	"runtime/app/assets.go":           true, // language default
	"messaging/types.go":              true, // constant declarations and parsing helpers
	"attachments/attachments.go":      true, // constant declarations and validation
	"attachments/gormstore/legacy.go": true, // legacy schema value
	"tooling/scaffold/templates/_name_/internal/controller/controller.go.tmpl":         true, // theme cookie parser
	"tooling/scaffold/templates/_name_/internal/service/attachment.go.tmpl":            true, // store-mode parser
	"tooling/scaffold/templates/_name_/internal/service/message_bus.go.tmpl":           true, // redis mode parser
	"tooling/scaffold/templates/_name_/internal/waf/settings.go.tmpl":                  true, // waf mode parser
	"tooling/scaffold/templates/_name_/internal/controller/workflow_api_types.go.tmpl": true, // action word constants
}

// guardedGoFile reports whether a repo-relative Go file belongs to the guarded set. | guardedGoFile
// 报告仓库相对的 Go 文件是否属于守卫范围。
func guardedGoFile(rel string) bool {
	if guardedGoAllowlist[rel] {
		return false
	}
	for _, prefix := range guardedGoPrefixes {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

// domainCaseLiteral matches a string literal used in a comparison or case branch of a template. |
// domainCaseLiteral 匹配模板比较或分支中的字符串字面量。
var domainCaseLiteral = regexpx.MustCompile(`(?:\b(?:eq|ne)\s+[^"}]*|\bcase\s+)"([a-z][a-z_-]*)"`)

// domainOptionValue matches a machine value rendered into a form option ("Value" "..."). |
// domainOptionValue 匹配渲染进表单选项的机器值（"Value" "..."）。
var domainOptionValue = regexpx.MustCompile(`"Value"\s+"([a-z][a-z_-]*)"`)

// domainJSComparison matches a JavaScript comparison, case branch or prefix probe. |
// domainJSComparison 匹配 JavaScript 比较、分支或前缀判断。
var domainJSComparison = regexpx.MustCompile(`(?:===|!==|==|!=)\s*["']([a-z][a-z_-]*)["']|\bcase\s+["']([a-z][a-z_-]*)["']|\.indexOf\(["']([a-z][a-z_-]*)["']`)

// TestDomainStringsStayConstant enforces the domain-enum rule documented in CLAUDE.md §4.12 and
// docs/developer_guidelines.md: machine values live in typed constants, and code, templates and
// scripts reference those constants instead of repeating the literal. |
// TestDomainStringsStayConstant 执行 CLAUDE.md §4.12 与 docs/developer_guidelines.md 规定的领域枚举规则：
// 机器值只存在于类型化常量中，代码、模板与脚本引用常量，而不是重复字面量。
func TestDomainStringsStayConstant(t *testing.T) {
	root := repoRoot(t)
	scanGoDomainLiterals(t, root)
	scanTemplateDomainLiterals(t, root)
	scanJSDomainLiterals(t, root)
}

// scanGoDomainLiterals reports domain literals in comparison, case, assignment and composite-literal
// values. Const declarations and struct tags are skipped, because they are the declaration point or
// a serialization contract. | scanGoDomainLiterals 报告比较、分支、赋值与复合字面量值中的领域字面量。
// const 声明与结构标签被跳过：前者是声明处，后者是序列化契约。
func scanGoDomainLiterals(t *testing.T, root string) {
	t.Helper()
	fset := token.NewFileSet()
	for _, file := range scanGoFiles(t, root) {
		if file.isTest || !guardedGoFile(file.rel) {
			continue
		}
		parsed, err := parser.ParseFile(fset, file.absPath, file.src, parser.ParseComments)
		if err != nil {
			t.Fatalf("%s: parse: %v", file.rel, err)
		}
		reported := map[token.Pos]bool{}
		flag := func(expr ast.Expr) {
			ast.Inspect(expr, func(node ast.Node) bool {
				lit, ok := node.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || reported[lit.Pos()] {
					return true
				}
				value, unquoteErr := strconv.Unquote(lit.Value)
				if unquoteErr != nil {
					return true
				}
				if hint, ok := domainLiteralHints[value]; ok {
					reported[lit.Pos()] = true
					pos := fset.Position(lit.Pos())
					t.Errorf("%s:%d: bare domain literal %q; use %s | 裸领域字面量 %q，请改用 %s",
						file.rel, pos.Line, value, hint, value, hint)
				}
				return true
			})
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.GenDecl:
				if n.Tok == token.CONST {
					return false
				}
			case *ast.CaseClause:
				for _, expr := range n.List {
					if lit, ok := expr.(*ast.BasicLit); ok {
						flag(lit)
					}
				}
			case *ast.BinaryExpr:
				if n.Op == token.EQL || n.Op == token.NEQ {
					if _, ok := n.X.(*ast.BasicLit); ok {
						flag(n.X)
					}
					if _, ok := n.Y.(*ast.BasicLit); ok {
						flag(n.Y)
					}
				}
			case *ast.AssignStmt:
				for _, expr := range n.Rhs {
					if lit, ok := expr.(*ast.BasicLit); ok {
						flag(lit)
					}
				}
			case *ast.KeyValueExpr:
				flag(n.Value)
			}
			return true
		})
	}
}

// scanTemplateDomainLiterals reports comparisons and option values in both template trees: the
// generated project's templates and the framework's root demo templates. Template comments are
// skipped; the demo templates read machine values from .Values or declare them once at the top of
// a fragment. | scanTemplateDomainLiterals 报告两棵模板树中的比较与选项值：生成项目模板与框架根目录的
// 演示模板。模板注释被跳过；演示模板从 .Values 读取机器值，或在片段顶部声明一次。
func scanTemplateDomainLiterals(t *testing.T, root string) {
	t.Helper()
	scanTree := func(dir string) {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() || !strings.HasSuffix(path, ".html") {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			inComment := false
			for i, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
				if inComment {
					if strings.Contains(line, "*/}}") {
						inComment = false
					}
					continue
				}
				if strings.Contains(line, "{{/*") {
					if !strings.Contains(line, "*/}}") {
						inComment = true
					}
					continue
				}
				for _, match := range domainCaseLiteral.FindAllStringSubmatch(line, -1) {
					if hint, ok := domainLiteralHints[match[1]]; ok {
						t.Errorf("%s:%d: bare domain literal %q in a template branch; use %s | 模板分支中的裸领域字面量 %q，请改用 %s",
							rel, i+1, match[1], hint, match[1], hint)
					}
				}
				for _, match := range domainOptionValue.FindAllStringSubmatch(line, -1) {
					if hint, ok := domainLiteralHints[match[1]]; ok {
						t.Errorf("%s:%d: bare domain literal %q in a form option; use %s | 表单选项中的裸领域字面量 %q，请改用 %s",
							rel, i+1, match[1], hint, match[1], hint)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan templates under %s: %v", dir, err)
		}
	}
	scanTree(filepath.Join(root, "tooling", "scaffold", "templates", "_name_", "web", "templates"))
	scanTree(filepath.Join(root, "web", "templates"))
}

// scanJSDomainLiterals reports comparisons, case branches and prefix probes in the shared and
// scaffold scripts. Comments and minified files are skipped. |
// scanJSDomainLiterals 报告共享脚本与脚手架脚本中的比较、分支与前缀判断。注释与压缩文件被跳过。
func scanJSDomainLiterals(t *testing.T, root string) {
	t.Helper()
	roots := []string{
		filepath.Join(root, "web", "static", "js"),
		filepath.Join(root, "tooling", "scaffold", "templates", "_name_", "web", "static", "js"),
	}
	for _, dir := range roots {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !(strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".js.tmpl")) ||
				strings.HasSuffix(name, ".min.js") || strings.HasSuffix(name, ".test.mjs") {
				continue
			}
			path := filepath.Join(dir, name)
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("read %s: %v", path, readErr)
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			inBlockComment := false
			for i, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
				trimmed := strings.TrimSpace(line)
				if inBlockComment {
					if strings.Contains(trimmed, "*/") {
						inBlockComment = false
					}
					continue
				}
				if strings.HasPrefix(trimmed, "/*") {
					if !strings.Contains(trimmed, "*/") {
						inBlockComment = true
					}
					continue
				}
				if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
					continue
				}
				// ARIA and DOM disabled state is not a domain status. | ARIA 与 DOM 的 disabled 状态不是领域状态。
				if strings.Contains(line, "aria-disabled") || strings.Contains(line, "STATE_ATTR") ||
					strings.Contains(line, "setAttribute('disabled'") || strings.Contains(line, `setAttribute("disabled"`) {
					continue
				}
				for _, match := range domainJSComparison.FindAllStringSubmatch(line, -1) {
					value := firstNonEmptyMatch(match)
					if hint, ok := domainLiteralHints[value]; ok {
						t.Errorf("%s:%d: bare domain literal %q in script logic; use a constants object (%s) | 脚本逻辑中的裸领域字面量 %q，请改用常量对象（%s）",
							rel, i+1, value, hint, value, hint)
					}
				}
			}
		}
	}
}

// firstNonEmptyMatch returns the first non-empty capture of a regexp match. | firstNonEmptyMatch
// 返回正则匹配中的第一个非空捕获。
func firstNonEmptyMatch(match []string) string {
	for _, value := range match[1:] {
		if value != "" {
			return value
		}
	}
	return ""
}
