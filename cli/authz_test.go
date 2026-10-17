package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/authz"
)

// authzTestFS writes a project tree into memory and returns it. | authzTestFS 把项目树写入内存并返回。
func authzTestFS(t *testing.T, files map[string]string) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	for name, body := range files {
		if err := fsys.MkdirAll(name[:strings.LastIndex(name, "/")], 0o755); err != nil {
			t.Fatalf("MkdirAll failed: %v", err)
		}
		if err := afero.WriteFile(fsys, name, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) failed: %v", name, err)
		}
	}
	return fsys
}

// TestAuthzCheckPassesOnACleanTree requires a clean tree to pass with a zero exit code and to print a summary CI can archive. | TestAuthzCheckPassesOnACleanTree 要求干净的代码树以零退出码通过，并打印可供 CI 留档的摘要。
func TestAuthzCheckPassesOnACleanTree(t *testing.T) {
	fsys := authzTestFS(t, map[string]string{
		"internal/router/router.go": `package router

import "github.com/shibingli/yd/authz"

func Register(r any) {
	r.Use(authz.Require(authz.PermUserRead))
}
`,
		"web/templates/pages/users.html": `<button data-yd-perm="{{ .Perm.UserCreate }}">new</button>
`,
	})
	te := newTestEnv(t, fsys, "", false)

	if err := Execute(context.Background(), []string{"authz", "check"}, te.env); err != nil {
		t.Fatalf("a clean tree failed the check: %v | 干净的代码树未通过校验：%v\n%s", err, err, te.out.String())
	}
	out := te.out.String()
	for _, want := range []string{"yd authz check", "dir | 目录", "files | 文件", "result | 结论"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "0 error(s)") {
		t.Errorf("the clean report does not say zero errors:\n%s", out)
	}
}

// TestAuthzCheckFailsOnATypo 要求把权限拼错时报错并以非零码退出。
//
// This is the acceptance criterion of the feature: a misspelled capability has to be caught here,
// not after it has silently denied every request in production. | 这是该特性的验收标准：
// 写错的能力必须在这里被抓到，而不是在生产环境静默拒绝每一个请求之后。
func TestAuthzCheckFailsOnATypo(t *testing.T) {
	fsys := authzTestFS(t, map[string]string{
		"internal/router/router.go": `package router

import "github.com/shibingli/yd/auth"

func Register(r any) {
	r.Use(auth.RequirePermission("user:creat"))
}
`,
	})
	te := newTestEnv(t, fsys, "", false)

	err := Execute(context.Background(), []string{"authz", "check"}, te.env)
	if !errors.Is(err, ErrAuthzFailed) {
		t.Fatalf("want ErrAuthzFailed, got %v | 期望 ErrAuthzFailed，实际 %v\n%s", err, err, te.out.String())
	}
	out := te.out.String()
	if !strings.Contains(out, "user:creat") {
		t.Errorf("the report does not name the typo:\n%s", out)
	}
	if !strings.Contains(out, "internal/router/router.go:6") {
		t.Errorf("the report does not point at the line:\n%s", out)
	}
	// The error message is what a CI log shows on the last line, so it has to carry both numbers.
	if !strings.Contains(err.Error(), "1 error(s)") {
		t.Errorf("the error message does not count the errors: %v", err)
	}
}

// TestAuthzCheckFlagsABareLiteral requires a token written as a bare literal to fail the command as well. | TestAuthzCheckFlagsABareLiteral 要求把标识写成裸字面量同样让命令失败。
func TestAuthzCheckFlagsABareLiteral(t *testing.T) {
	fsys := authzTestFS(t, map[string]string{
		"web/static/js/users.js": `export function wire(yd) {
  return yd.can("user:create");
}
`,
	})
	te := newTestEnv(t, fsys, "", false)

	err := Execute(context.Background(), []string{"authz", "check"}, te.env)
	if !errors.Is(err, ErrAuthzFailed) {
		t.Fatalf("want ErrAuthzFailed, got %v | 期望 ErrAuthzFailed，实际 %v", err, err)
	}
	if out := te.out.String(); !strings.Contains(out, "bare[user:create]") {
		t.Errorf("the report does not mark the literal as bare:\n%s", out)
	}
}

// TestAuthzCheckTakesADirectory requires --dir to scan only that directory, resolved against the working directory. | TestAuthzCheckTakesADirectory 要求 --dir 只扫描该目录，且相对工作目录解析。
func TestAuthzCheckTakesADirectory(t *testing.T) {
	fsys := authzTestFS(t, map[string]string{
		"app/internal/router/router.go": `package router

import "github.com/shibingli/yd/authz"

func Register(r any) {
	r.Use(authz.Require(authz.PermUserRead))
}
`,
		"other/broken/router.go": `package broken

import "github.com/shibingli/yd/auth"

func Register(r any) {
	r.Use(auth.RequirePermission("user:creat"))
}
`,
	})
	te := newTestEnv(t, fsys, "", false)
	te.env.WorkDir = "."

	if err := Execute(context.Background(), []string{"authz", "check", "--dir", "app"}, te.env); err != nil {
		t.Fatalf("--dir app failed: %v | --dir app 失败：%v\n%s", err, err, te.out.String())
	}
	out := te.out.String()
	if !strings.Contains(out, "dir | 目录: app") {
		t.Errorf("the report does not name the scanned directory:\n%s", out)
	}
	if !strings.Contains(out, "files | 文件: 1") {
		t.Errorf("the scan did not stop at one file:\n%s", out)
	}
	if strings.Contains(out, "error | 错误") {
		t.Errorf("the report carries an error from outside --dir:\n%s", out)
	}

	// And the same command over the whole tree must fail, which proves the flag is what limited the
	// first run rather than the typo being invisible. | 而同一条命令扫整棵树时必须失败，
	// 这证明是标志限定了第一次运行的范围，而不是那个拼写错误根本抓不到。
	err := Execute(context.Background(), []string{"authz", "check"}, te.env)
	if !errors.Is(err, ErrAuthzFailed) {
		t.Fatalf("the whole-tree check passed over a typo: %v | 扫整棵树时拼写错误竟然通过：%v", err, err)
	}
	if out := te.out.String(); !strings.Contains(out, "other/broken/router.go") {
		t.Errorf("the whole-tree report does not point at the typo:\n%s", out)
	}
}

// TestAuthzListIsStableAndBilingual 要求参考表同时给出四种写法与中英对照的分组。
//
// The table is what a developer looks a name up in, so it has to show every spelling of one
// capability side by side - and it has to repeat itself byte for byte, or it cannot be reviewed in
// a diff. | 这张表是开发者查名字的地方，因此同一条能力的每种写法都要并排列出——
// 而且必须逐字节可重复，否则没法在 diff 里审阅。
func TestAuthzListIsStableAndBilingual(t *testing.T) {
	te := newTestEnv(t, afero.NewMemMapFs(), "", false)

	if err := Execute(context.Background(), []string{"authz", "list"}, te.env); err != nil {
		t.Fatalf("list failed: %[1]v | list 失败：%[1]v", err)
	}
	first := te.out.String()
	for _, want := range []string{
		"yd authz list",
		"permissions | 权限:",
		"[tenant | 租户]",
		"user:create",
		"PermUserCreate",
		".Perm.UserCreate",
		"yd.PERM.USER_CREATE",
		"roles | 角色:",
		authz.RoleAdmin,
		"RoleAdmin",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("the table is missing %q:\n%s", want, first)
		}
	}

	te.out.Reset()
	if err := Execute(context.Background(), []string{"authz", "list"}, te.env); err != nil {
		t.Fatalf("second list failed: %v", err)
	}
	if second := te.out.String(); second != first {
		t.Errorf("two listings differ:\n--- first\n%s\n--- second\n%s", first, second)
	}
}

// TestAuthzRejectsBadActions requires an unknown action word and a surplus argument to be refused, both pointing at the usage line. | TestAuthzRejectsBadActions 要求未知动作词与多余参数被拒绝，且都指向用法行。
func TestAuthzRejectsBadActions(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want error
	}{
		{"unknown action | 未知动作", []string{"authz", "verify"}, ErrUnknownCommand},
		{"extra argument | 多余参数", []string{"authz", "check", "app", "extra"}, ErrNoCommand},
		{"list takes no argument | list 不接受参数", []string{"authz", "list", "app"}, ErrNoCommand},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			te := newTestEnv(t, afero.NewMemMapFs(), "", false)
			err := Execute(context.Background(), tc.args, te.env)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Execute(%v) = %v, want %v", tc.args, err, tc.want)
			}
			if got := te.errs.String(); !strings.Contains(got, "yd authz check") {
				t.Errorf("the usage line is missing:\n%s", got)
			}
		})
	}
}

// TestAuthzCheckFailsClosedOnUnreadableSources 要求扫描本身失败时返回扫描错误而不是校验失败。
//
// "The tree has a typo" and "the scan could not run" are two different mornings, and CI decides
// what to do by the sentinel, so the two must never be conflated. | 「树里有拼写错误」与「扫描根本没跑起来」
// 是两个完全不同的早晨，而 CI 正是靠哨兵决定该做什么，因此两者绝不能混为一谈。
func TestAuthzCheckFailsClosedOnUnreadableSources(t *testing.T) {
	te := newTestEnv(t, authzTestFS(t, map[string]string{
		"broken/router.go": `package broken

var _ = "user:create"
`,
	}), "", false)
	te.env.WorkDir = "missing-directory"

	err := Execute(context.Background(), []string{"authz", "check"}, te.env)
	if err == nil {
		t.Fatalf("a scan over a missing directory reported success")
	}
	if errors.Is(err, ErrAuthzFailed) {
		t.Fatalf("a failed scan was reported as a failed check: %v", err)
	}
}
