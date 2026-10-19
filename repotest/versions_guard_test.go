package repotest

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/tooling/versions"
)

// TestScaffoldVersionsMatchSources 强制脚手架的版本常量始终由 go.mod 与资产锁文件派生。
// TestScaffoldVersionsMatchSources enforces that the scaffold's version constants are always derived
// from go.mod and the asset lock.
//
// 版本常量不是手写常量，而是 `yd versions sync` 的产物。没有这条守卫，一次依赖升级很容易只改 go.mod
// 而忘记模板，`yd new` 随后就会继续生成旧版本的项目——这类漂移不会让任何代码编译失败，只会静默产出过时的项目。
// The version constants are not hand-written: they are produced by `yd versions sync`. Without this
// guard a dependency upgrade can easily touch go.mod while forgetting the templates, and `yd new`
// would keep generating stale projects — a drift that breaks no build and only shows up as an
// outdated project later.
func TestScaffoldVersionsMatchSources(t *testing.T) {
	root := repoRoot(t)
	fsys := afero.NewOsFs()
	if err := versions.Check(fsys, root); err != nil {
		inv, loadErr := versions.Load(fsys, root)
		if loadErr == nil {
			t.Logf("sources: go=%s htmx=%s", inv.GoDirective, inv.HtmxVersion)
		}
		t.Fatalf("脚手架版本与真源不一致，请运行 yd versions sync: %v", err)
	}
}

// TestDocsVersionConstantsMatchSources keeps the package-level identity constants tied to the same
// sources as the generated scaffold constants. docs.go is hand-written, so without this guard a
// dependency upgrade silently leaves `yd.GoVersion` / `yd.HtmxVersion` stale. | TestDocsVersionConstantsMatchSources
// 让包级身份常量与生成脚手架常量同源。docs.go 是手写的，没有这条守卫时一次依赖升级会静默留下
// 过期的 `yd.GoVersion` / `yd.HtmxVersion`。
func TestDocsVersionConstantsMatchSources(t *testing.T) {
	root := repoRoot(t)
	inv, err := versions.Load(afero.NewOsFs(), root)
	if err != nil {
		t.Fatalf("versions.Load: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs.go"))
	if err != nil {
		t.Fatalf("read docs.go: %v", err)
	}
	got := parseStringConstant(string(data))
	if got["GoVersion"] != inv.GoDirective {
		t.Errorf("docs.go GoVersion = %q, want %q", got["GoVersion"], inv.GoDirective)
	}
	if got["HtmxVersion"] != inv.HtmxVersion {
		t.Errorf("docs.go HtmxVersion = %q, want %q", got["HtmxVersion"], inv.HtmxVersion)
	}
}

// TestDockerfileBuilderTagMatchesGoMod enforces §20.3: the build image tag is derived, never
// hand-copied. The root Dockerfile is static, so the guard at least keeps it in lockstep with the
// go directive; the embedded template carries a placeholder substituted at resolution time.
// | TestDockerfileBuilderTagMatchesGoMod 强制 §20.3：构建镜像标签是派生的，绝不手抄。根 Dockerfile
// 是静态文件，因此守卫至少让它与 go 指令同步；内嵌模板带占位符，在解析时替换。
func TestDockerfileBuilderTagMatchesGoMod(t *testing.T) {
	root := repoRoot(t)
	inv, err := versions.Load(afero.NewOsFs(), root)
	if err != nil {
		t.Fatalf("versions.Load: %v", err)
	}
	want := majorMinorOf(inv.GoDirective)
	check := func(name string, wantPlaceholder bool) {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(data)
		if wantPlaceholder {
			if !strings.Contains(text, "{{GO_MAJOR_MINOR}}") {
				t.Errorf("%s no longer carries the Go tag placeholder", name)
			}
			return
		}
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "FROM golang:") {
				continue
			}
			tag := strings.TrimPrefix(line, "FROM golang:")
			tag, _, _ = strings.Cut(tag, " ")
			tag = strings.TrimSuffix(tag, "-alpine")
			if tag != want {
				t.Errorf("%s builder tag = %q, want %q (go.mod)", name, tag, want)
			}
		}
	}
	check("Dockerfile", false)
	check(filepath.Join("tooling", "docker", "templates", "Dockerfile"), true)
}

// parseStringConstant extracts `Name = "value"` lines from a Go source file. | parseStringConstant
// 从 Go 源文件中提取 `Name = "value"` 形式的行。
func parseStringConstant(src string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		name, rest, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		rest = strings.TrimSpace(rest)
		value, err := strconv.Unquote(rest)
		if err != nil {
			continue
		}
		out[name] = value
	}
	return out
}

// majorMinorOf reduces a Go directive to major.minor. | majorMinorOf 把 go 指令归约为主次版本。
func majorMinorOf(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}
