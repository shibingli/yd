package tools

import (
	"errors"
	"strings"
	"testing"

	"github.com/shibingli/yd/tooling/align"
	"github.com/shibingli/yd/tooling/ko"
)

// TestRegistryOrderAndShape pins the four entries and their order, which is also the order of every
// status table and every report. | TestRegistryOrderAndShape 固定四个条目及其顺序，该顺序也是每张状态表与每份报告的顺序。
func TestRegistryOrderAndShape(t *testing.T) {
	want := []string{toolBetteralign, toolKo, toolTailwindcss, toolDocker}
	got := Registry()
	if len(got) != len(want) {
		t.Fatalf("Registry() 有 %d 个条目，期望 %d 个 | Registry() has %d entries, want %d", len(got), len(want), len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("Registry()[%d].Name = %q，期望 %q | want %q", i, got[i].Name, name, name)
		}
		if got[i].Summary == "" {
			t.Errorf("%s 的 Summary 为空 | Summary is empty", name)
		}
		if got[i].InstallHint == "" {
			t.Errorf("%s 的 InstallHint 为空 | InstallHint is empty", name)
		}
		if got[i].DocURL == "" {
			t.Errorf("%s 的 DocURL 为空 | DocURL is empty", name)
		}
		// Every field a user reads is bilingual, English first - except the two hints that are the
		// bare commands of the align and ko packages. | 每个给用户看的字段都是双语且英文在前——
		// 但复用 align 与 ko 命令原文的两条提示除外。
		if !strings.Contains(got[i].Summary, " | ") {
			t.Errorf("%s 的 Summary 未中英同步 | Summary is not bilingual: %q", name, got[i].Summary)
		}
	}
	for _, name := range []string{toolTailwindcss, toolDocker} {
		if hint := lookupFor(t, name).InstallHint; !strings.Contains(hint, " | ") {
			t.Errorf("%s 的 InstallHint 未中英同步 | InstallHint is not bilingual: %q", name, hint)
		}
	}
	// Exactly the two Go-installable entries carry a target. | 只有两个可用 go install 安装的条目携带目标。
	for _, want := range []struct {
		name      string
		goInstall string
	}{
		{toolBetteralign, "github.com/dkorunic/betteralign/cmd/betteralign@latest"},
		{toolKo, "github.com/google/ko@latest"},
		{toolTailwindcss, ""},
		{toolDocker, ""},
	} {
		if got := lookupFor(t, want.name).GoInstall; got != want.goInstall {
			t.Errorf("%s 的 GoInstall = %q，期望 %q | want %q", want.name, got, want.goInstall, want.goInstall)
		}
	}
}

// TestRegistryHintsReuseResolverConstants is the guard of the plan: the two hints that also exist as
// resolver constants must be that very constant, never a second copy. | TestRegistryHintsReuseResolverConstants
// 是计划的守卫：同时存在于解析器的两个提示必须就是那个常量，而不是第二份副本。
func TestRegistryHintsReuseResolverConstants(t *testing.T) {
	betteralign := lookupFor(t, toolBetteralign)
	if betteralign.InstallHint != align.InstallHint {
		t.Errorf("betteralign 的 InstallHint = %q，期望 align.InstallHint = %q",
			betteralign.InstallHint, align.InstallHint)
	}
	if !strings.Contains(betteralign.InstallHint, betteralign.GoInstall) {
		t.Errorf("betteralign 的 InstallHint 未包含 GoInstall %q | hint does not carry the target: %q",
			betteralign.GoInstall, betteralign.InstallHint)
	}
	koTool := lookupFor(t, toolKo)
	if koTool.InstallHint != ko.InstallHint {
		t.Errorf("ko 的 InstallHint = %q，期望 ko.InstallHint = %q", koTool.InstallHint, ko.InstallHint)
	}
	if !strings.Contains(koTool.InstallHint, koTool.GoInstall) {
		t.Errorf("ko 的 InstallHint 未包含 GoInstall %q | hint does not carry the target: %q",
			koTool.GoInstall, koTool.InstallHint)
	}
	// The hints that cannot use go install point at their delivery route instead of a command. |
	// 无法用 go install 的提示改为指向各自的获取途径。
	if hint := lookupFor(t, toolTailwindcss).InstallHint; !strings.Contains(hint, "yd tools install tailwindcss") {
		t.Errorf("tailwindcss 的 InstallHint 未指向 yd tools install tailwindcss | hint does not point at yd tools install tailwindcss: %q", hint)
	}
	if hint := lookupFor(t, toolDocker).InstallHint; !strings.Contains(hint, "Docker") {
		t.Errorf("docker 的 InstallHint 未提到 Docker | hint does not mention Docker: %q", hint)
	}
}

// TestRegistryVersionQueries pins the version queries, including docker's deliberate absence. | TestRegistryVersionQueries 固定版本查询，包含 docker 刻意的缺席。
func TestRegistryVersionQueries(t *testing.T) {
	for _, want := range []struct {
		name string
		args []string
	}{
		{toolBetteralign, []string{"-V=full"}},
		{toolKo, []string{"version"}},
		{toolTailwindcss, []string{"--version"}},
		{toolDocker, nil},
	} {
		got := lookupFor(t, want.name).VersionArgs
		if strings.Join(got, " ") != strings.Join(want.args, " ") {
			t.Errorf("%s 的 VersionArgs = %v，期望 %v", want.name, got, want.args)
		}
	}
}

// TestRegistryReturnsACopy proves a caller cannot tamper with the registry. | TestRegistryReturnsACopy 证明调用方无法改动注册表。
func TestRegistryReturnsACopy(t *testing.T) {
	first := Registry()
	first[0].Name = "tampered"
	first[0].InstallHint = "tampered"
	first[0].VersionArgs[0] = "tampered"
	first[3].VersionArgs = []string{"tampered"}

	second := Registry()
	if second[0].Name != toolBetteralign {
		t.Errorf("Registry()[0].Name = %q，注册表被调用方改动 | the registry was mutated by a caller", second[0].Name)
	}
	if second[0].InstallHint != align.InstallHint {
		t.Errorf("Registry()[0].InstallHint = %q，注册表被调用方改动 | the registry was mutated", second[0].InstallHint)
	}
	if strings.Join(second[0].VersionArgs, " ") != "-V=full" {
		t.Errorf("Registry()[0].VersionArgs = %v，注册表被调用方改动 | the registry was mutated", second[0].VersionArgs)
	}
	if second[3].VersionArgs != nil {
		t.Errorf("Registry()[3].VersionArgs = %v，期望 nil | want nil", second[3].VersionArgs)
	}
}

// TestLookupAcceptsCaseAndSurroundingBlanks documents the tolerance of the command word. | TestLookupAcceptsCaseAndSurroundingBlanks 记录命令词的大小写与空白容差。
func TestLookupAcceptsCaseAndSurroundingBlanks(t *testing.T) {
	tool, err := Lookup("  BetterAlign ")
	if err != nil {
		t.Fatalf("Lookup(\"  BetterAlign \") 返回错误 %v | unexpected error", err)
	}
	if tool.Name != toolBetteralign {
		t.Errorf("Lookup 返回 %q，期望 %q | want %q", tool.Name, toolBetteralign, toolBetteralign)
	}
}

// TestLookupUnknownToolNamesTheAlternatives keeps the error actionable. | TestLookupUnknownToolNamesTheAlternatives 让错误保持可用。
func TestLookupUnknownToolNamesTheAlternatives(t *testing.T) {
	_, err := Lookup("golangci-lint")
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("err = %v，期望 ErrUnknownTool | want ErrUnknownTool", err)
	}
	if !strings.Contains(err.Error(), "golangci-lint") {
		t.Errorf("错误未回显输入 %q | the error does not echo the input: %v", "golangci-lint", err)
	}
	for _, name := range names() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("错误未列出可用工具 %q | the error does not list %q: %v", name, name, err)
		}
	}
}

// lookupFor returns the registry entry of name or fails the test. | lookupFor 返回 name 的注册表条目，找不到即判失败。
func lookupFor(t *testing.T, name string) Tool {
	t.Helper()
	tool, err := Lookup(name)
	if err != nil {
		t.Fatalf("Lookup(%q) 失败 | Lookup(%q) failed: %v", name, name, err)
	}
	return tool
}
