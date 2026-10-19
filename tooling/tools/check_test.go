package tools

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/shibingli/yd/tooling/align"
	"github.com/shibingli/yd/tooling/ko"
	"github.com/shibingli/yd/tooling/toolchain"
)

// TestCheckReportsEveryTool pins the shape of the status table: one row per registry entry, in
// registry order. | TestCheckReportsEveryTool 固定状态表的形状：每个注册表条目一行，且按注册表顺序。
func TestCheckReportsEveryTool(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := newFakePATH(t, toolBetteralign, toolTailwindcss, toolDocker)
	usePATH(path)
	exec := &fakeExec{}
	injectExec(t, exec.run)

	statuses := Check(root)
	if len(statuses) != 4 {
		t.Fatalf("Check 返回 %d 行，期望 4 行 | Check returned %d rows, want 4", len(statuses), len(statuses))
	}
	byName := statusesByName(statuses)
	for _, want := range []struct {
		name  string
		found bool
	}{
		{toolBetteralign, true},
		{toolKo, false},
		{toolTailwindcss, true},
		{toolDocker, true},
	} {
		st := byName[want.name]
		if st.Found != want.found {
			t.Errorf("%s.Found = %v，期望 %v | want %v", want.name, st.Found, want.found, want.found)
		}
		if st.Err != nil {
			t.Errorf("%s.Err = %v，期望 nil | want nil", want.name, st.Err)
		}
		// Probing is read-only, so no version is ever queried. | 探测是只读的，因此绝不查询版本。
		if st.Version != "" {
			t.Errorf("%s.Version = %q，探测不应执行工具 | probing must not execute anything", want.name, st.Version)
		}
	}
	if got := byName[toolBetteralign].Path; got != path.known[toolBetteralign] {
		t.Errorf("betteralign.Path = %q，期望 PATH 上的 %q", got, path.known[toolBetteralign])
	}
	if got := byName[toolDocker].Path; got != path.known[toolDocker] {
		t.Errorf("docker.Path = %q，期望 PATH 上的 %q", got, path.known[toolDocker])
	}
	if calls := exec.allCalls(); len(calls) != 0 {
		t.Errorf("Check 执行了 %d 条命令，期望 0 条 | Check ran %d commands, want 0", len(calls), len(calls))
	}
	if missing := Missing(root); len(missing) != 1 || missing[0].Tool.Name != toolKo {
		t.Errorf("Missing = %v，期望只有 ko | want only ko", statusNames(missing))
	}
}

// TestCheckPrefersProjectLocalCopy proves the local segment of the chain wins over PATH. | TestCheckPrefersProjectLocalCopy 证明链条中的本地一段优先于 PATH。
func TestCheckPrefersProjectLocalCopy(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	// PATH knows both tools, and a real project-local copy exists for each. | PATH 认识这两个工具，同时项目内各有一份真实副本。
	path := newFakePATH(t, toolBetteralign, toolKo, toolTailwindcss, toolDocker)
	usePATH(path)
	stubFile(t, localBin(root, toolBetteralign))
	stubFile(t, localBin(root, toolKo))

	byName := statusesByName(Check(root))
	for _, name := range []string{toolBetteralign, toolKo} {
		st := byName[name]
		if !st.Found {
			t.Fatalf("%s 未就绪 | %s is not ready", name, name)
		}
		want, err := filepath.Abs(localBin(root, name))
		if err != nil {
			t.Fatalf("Abs(%q) 失败 | Abs failed: %v", localBin(root, name), err)
		}
		if !samePath(t, st.Path, want) {
			t.Errorf("%s.Path = %q，期望项目内副本 %q", name, st.Path, want)
		}
	}
	// tailwindcss has no local copy, so PATH still serves it. | tailwindcss 没有本地副本，仍由 PATH 提供。
	if got := byName[toolTailwindcss].Path; got != path.known[toolTailwindcss] {
		t.Errorf("tailwindcss.Path = %q，期望 PATH 上的 %q", got, path.known[toolTailwindcss])
	}
	if missing := Missing(root); missing != nil {
		t.Errorf("Missing = %v，期望 nil | want nil", statusNames(missing))
	}
	if hints := Hints(root); hints != nil {
		t.Errorf("Hints = %v，期望 nil | want nil", hints)
	}
}

// TestCheckWithHonoursConfiguredBinaries proves the probe sees the same configured executables the
// real commands do, instead of reporting a tool as missing because it is not on PATH. | TestCheckWithHonoursConfiguredBinaries
// 证明探测看到与真实命令相同的已配置可执行文件，而不会因为工具不在 PATH 上就误报缺失。
func TestCheckWithHonoursConfiguredBinaries(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	// PATH offers nothing; only the configured paths can resolve the three tools. | PATH 上什么都没有，只有配置路径能解析这三个工具。
	usePATH(newFakePATH(t))
	bins := make(map[string]string, 3)
	for _, name := range []string{NameBetteralign, NameKo, NameTailwindcss} {
		p := filepath.Join(root, "configured-"+name)
		stubFile(t, p)
		bins[name] = p
	}

	byName := statusesByName(CheckWith(Options{Dir: root, Binaries: bins}))
	for _, name := range []string{NameBetteralign, NameKo, NameTailwindcss} {
		st := byName[name]
		if !st.Found {
			t.Errorf("%s 未就绪 | %s is not ready: %v", name, name, st.Err)
			continue
		}
		want, err := filepath.Abs(bins[name])
		if err != nil {
			t.Fatalf("Abs(%q) 失败 | Abs failed: %v", bins[name], err)
		}
		if !samePath(t, st.Path, want) {
			t.Errorf("%s.Path = %q，期望配置路径 %q", name, st.Path, want)
		}
	}
	// docker has no configured executable, so it stays missing. | docker 没有配置可执行文件，因此仍然缺失。
	if byName[NameDocker].Found {
		t.Errorf("docker 不应被解析 | docker must not resolve")
	}
	if missing := MissingWith(Options{Dir: root, Binaries: bins}); len(missing) != 1 || missing[0].Tool.Name != NameDocker {
		t.Errorf("Missing = %v，期望只有 docker | want only docker", statusNames(missing))
	}
}

// TestCheckReportsUnexpectedResolverFailures keeps a broken chain apart from a missing tool. | TestCheckReportsUnexpectedResolverFailures 把损坏的解析链与缺失的工具分开。
func TestCheckReportsUnexpectedResolverFailures(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := newFakePATH(t)
	usePATH(path)
	errBoom := errors.New("resolution exploded")
	fallback := resolveTool
	resolveTool = func(tool Tool, o Options) (string, error) {
		switch tool.Name {
		case toolBetteralign:
			return "", errBoom
		case toolKo:
			return "", nil // A resolver that claims success without a path. | 解析器声称成功却没有路径。
		}
		return fallback(tool, o)
	}

	byName := statusesByName(Check(root))
	if st := byName[toolBetteralign]; st.Found || !errors.Is(st.Err, errBoom) {
		t.Errorf("betteralign = %+v，期望如实上报 %v | want the error reported", st, errBoom)
	}
	if st := byName[toolKo]; st.Found || !errors.Is(st.Err, errEmptyPath) {
		t.Errorf("ko = %+v，期望如实上报 errEmptyPath | want errEmptyPath", st)
	}
	// Neither of them counts as missing: reinstalling cannot repair a chain. | 两者都不算缺失：再装一次修不好链。
	missing := Missing(root)
	if got := statusNames(missing); strings.Join(got, ",") != toolTailwindcss+","+toolDocker {
		t.Errorf("Missing = %v，期望 %v", got, []string{toolTailwindcss, toolDocker})
	}
	for _, line := range Hints(root) {
		if strings.Contains(line, toolBetteralign) || strings.Contains(line, "ko:") {
			t.Errorf("提示不该出现解析失败的条目 | hint mentions an entry that is not missing: %q", line)
		}
	}
}

// TestMissingAndHintsListEveryGap pins the hint block: two lines per tool plus the uniform entry. | TestMissingAndHintsListEveryGap 固定提示块：每个工具两行，末尾是统一入口。
func TestMissingAndHintsListEveryGap(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := newFakePATH(t)
	usePATH(path)

	missing := Missing(root)
	want := names()
	if got := statusNames(missing); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Missing = %v，期望 %v", got, want)
	}
	hints := Hints(root)
	if len(hints) != len(want)*2+1 {
		t.Fatalf("Hints 有 %d 行，期望 %d 行 | Hints has %d lines, want %d", len(hints), len(want)*2+1, len(hints), len(want)*2+1)
	}
	for i, st := range missing {
		if !strings.Contains(hints[i*2], st.Tool.Name) || !strings.Contains(hints[i*2], st.Tool.Summary) {
			t.Errorf("第 %d 行为 %q，期望含名称与用途 | want the name and the summary", i*2+1, hints[i*2])
		}
		if !strings.Contains(hints[i*2+1], st.Tool.InstallHint) {
			t.Errorf("第 %d 行为 %q，期望含安装提示 | want the install hint", i*2+2, hints[i*2+1])
		}
	}
	if last := hints[len(hints)-1]; !strings.Contains(last, "yd tools install") || !strings.Contains(last, LocalToolDir) {
		t.Errorf("末行 %q 未给出统一入口 | the last line misses the uniform entry", last)
	}
	for _, line := range hints {
		if !strings.Contains(line, " | ") {
			t.Errorf("提示行未中英同步 | hint line is not bilingual: %q", line)
		}
	}
}

// isolate clears every source except the stub PATH, so a probe sees only what the test provides. | isolate
// 清空 stub PATH 之外的每个来源，使探测只看到测试提供的东西。
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv(align.EnvToolBin, "")
	t.Setenv(ko.EnvBinary, "")
	t.Setenv(toolchain.EnvTailwindBin, "")
	prevPath, prevKoPath, prevResolve := PathLookup, ko.PathLookup, resolveTool
	t.Cleanup(func() {
		PathLookup, ko.PathLookup, resolveTool = prevPath, prevKoPath, prevResolve
	})
}

// fakePATH is a PathLookup over a fixed command set that also records what was asked for. | fakePATH 是在固定命令集合上的 PathLookup，同时记录被询问过的命令。
type fakePATH struct {
	known map[string]string
	asked []string
	mu    sync.Mutex
}

// newFakePATH builds a lookup that knows exactly names. | newFakePATH 构建一个只认识 names 的查找器。
func newFakePATH(t *testing.T, names ...string) *fakePATH {
	t.Helper()
	known := make(map[string]string, len(names))
	for _, name := range names {
		known[name] = filepath.Join(os.TempDir(), "yd-stub", name+exeSuffix())
	}
	return &fakePATH{known: known}
}

// lookup resolves a bare command name against the fixed set. | lookup 在固定集合上解析裸命令名。
func (f *fakePATH) lookup(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, name)
	if p, ok := f.known[name]; ok {
		return p, nil
	}
	return "", os.ErrNotExist
}

// askedFor reports whether name was ever looked up. | askedFor 报告 name 是否被查找过。
func (f *fakePATH) askedFor(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, asked := range f.asked {
		if asked == name {
			return true
		}
	}
	return false
}

// usePATH points the framework lookup and ko's own lookup at the stub, since ko owns no per-call
// lookup. | usePATH 把框架查找与 ko 自己的查找都指向 stub，因为 ko 不提供按次注入的查找。
func usePATH(f *fakePATH) {
	PathLookup, ko.PathLookup = f.lookup, f.lookup
}

// localBin is the project-local path every resolver tries before PATH. | localBin 是各解析器在 PATH 之前尝试的项目内路径。
func localBin(dir, name string) string {
	return filepath.Join(dir, ".yd", "tools", name+exeSuffix())
}

// exeSuffix mirrors the executable suffix of the current platform. | exeSuffix 与当前平台的可执行文件后缀一致。
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// stubFile writes the placeholder executable that the resolvers check for. | stubFile 写入解析器所检查的占位可执行文件。
func stubFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) 失败 | MkdirAll(%q) failed: %v", filepath.Dir(path), filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) 失败 | WriteFile(%q) failed: %v", path, path, err)
	}
}

// samePath compares two paths by file identity, which survives symlinks and short names. | samePath
// 按文件标识比较两个路径，可穿越符号链接与短名。
func samePath(t *testing.T, got, want string) bool {
	t.Helper()
	gotInfo, err := os.Stat(got)
	if err != nil {
		t.Fatalf("Stat(%q) 失败 | Stat(%q) failed: %v", got, got, err)
	}
	wantInfo, err := os.Stat(want)
	if err != nil {
		t.Fatalf("Stat(%q) 失败 | Stat(%q) failed: %v", want, want, err)
	}
	return os.SameFile(gotInfo, wantInfo)
}

// statusesByName indexes a status list by tool name. | statusesByName 按工具名索引状态列表。
func statusesByName(statuses []Status) map[string]Status {
	out := make(map[string]Status, len(statuses))
	for _, st := range statuses {
		out[st.Tool.Name] = st
	}
	return out
}

// statusNames returns the tool names of a status list in order. | statusNames 按顺序返回状态列表的工具名。
func statusNames(statuses []Status) []string {
	out := make([]string, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, st.Tool.Name)
	}
	return out
}

// failureNames returns the tool names of a failure list in order. | failureNames 按顺序返回失败列表的工具名。
func failureNames(failures []Failure) []string {
	out := make([]string, 0, len(failures))
	for _, f := range failures {
		out = append(out, f.Name)
	}
	return out
}
