// This file tests installation: where the script goes, which line loads it, and the one case in which a
// user's startup file is touched - with the backup and the idempotency that case promises. | 本文件测试安装：
// 脚本落到哪里、哪一行加载它，以及唯一会改动用户启动文件的那种情形——连同该情形承诺的备份与幂等。

package completion

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// fakeHome is the home directory every installation test resolves. | fakeHome 是每个安装测试所解析到的主目录。
//
// It is a plain string rather than a real directory: the tests run on an in-memory file system, so nothing
// here can reach the home directory of the machine the test runs on. | 它只是一个字符串而不是真实目录：测试跑在
// 内存文件系统上，因此这里的任何东西都够不到测试所跑机器的主目录。
const fakeHome = "/yd-home"

// installRequest builds a request whose shell, home and platform are all injected. | installRequest 构造一个
// shell、主目录与平台全部注入的请求。
func installRequest(fs afero.Fs, sh Shell, env map[string]string) Request {
	vars := map[string]string{"HOME": fakeHome, "USERPROFILE": fakeHome}
	for k, v := range env {
		vars[k] = v
	}
	return Request{
		Registry: fixtureRegistry(),
		Getenv:   envFunc(vars),
		FS:       fs,
		GOOS:     "linux",
		Shell:    sh,
	}
}

// readFile fails the test when a file cannot be read. | readFile 在文件无法读取时让测试失败。
func readFile(t *testing.T, fs afero.Fs, path string) []byte {
	t.Helper()
	data, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatalf("read %s failed | 读取失败: %v", path, err)
	}
	return data
}

// exists reports whether a path exists. | exists 报告路径是否存在。
func exists(t *testing.T, fs afero.Fs, path string) bool {
	t.Helper()
	ok, err := afero.Exists(fs, path)
	if err != nil {
		t.Fatalf("stat %s failed | 判断存在性失败: %v", path, err)
	}
	return ok
}

// TestResolveDoesNotTouchTheFileSystem 解析阶段不得写入任何东西。
// TestResolveDoesNotTouchTheFileSystem requires resolution to write nothing at all.
//
// The paths are reported before they are written, which is what lets a report or a confirmation prompt
// name the exact files first; a Resolve that already created them would make that promise a lie. | 路径在被写入
// 之前先被报告出来，这正是报告或确认提问可以先点明确切文件的原因；一个已经把文件建出来的 Resolve 会让这句话变成假话。
func TestResolveDoesNotTouchTheFileSystem(t *testing.T) {
	fs := afero.NewMemMapFs()
	plan, err := Resolve(installRequest(fs, Bash, nil))
	if err != nil {
		t.Fatalf("resolve failed | 解析失败: %v", err)
	}
	if dir := filepath.Dir(plan.ScriptPath); exists(t, fs, dir) {
		t.Errorf("resolve created %s | 解析过程创建了目录", dir)
	}
	if exists(t, fs, plan.RCFile) {
		t.Errorf("resolve created %s | 解析过程创建了启动文件", plan.RCFile)
	}
}

// TestResolvePathsAndLoadLine 校验解析出的路径与加载行。
// TestResolvePathsAndLoadLine checks the resolved paths and the loading line.
func TestResolvePathsAndLoadLine(t *testing.T) {
	fs := afero.NewMemMapFs()
	plan, err := Resolve(installRequest(fs, Bash, nil))
	if err != nil {
		t.Fatalf("resolve failed | 解析失败: %v", err)
	}
	script := filepath.Join(fakeHome, ".yd", "completion.bash")
	if plan.Dir != fakeHome || plan.ScriptPath != script {
		t.Errorf("paths = %q / %q, want %q / %q | 路径不符", plan.Dir, plan.ScriptPath, fakeHome, script)
	}
	if want := filepath.Join(fakeHome, ".bashrc"); plan.RCFile != want {
		t.Errorf("startup file = %q, want %q | 启动文件不符", plan.RCFile, want)
	}
	if want := LoadLine(Bash, script); plan.LoadLine != want {
		t.Errorf("load line = %q, want %q | 加载行不符", plan.LoadLine, want)
	}
	if plan.Detection.Shell != Bash || plan.Detection.Source != SourceOverride {
		t.Errorf("detection = %+v | 探测结果不符", plan.Detection)
	}

	// --dir moves the script but never the startup file: the startup file belongs to the shell, not to the
	// project. | --dir 只移动脚本，绝不移动启动文件：启动文件属于 shell 而不是项目。
	other := installRequest(fs, Zsh, nil)
	other.Dir = filepath.Join(fakeHome, "work")
	moved, err := Resolve(other)
	if err != nil {
		t.Fatalf("resolve failed | 解析失败: %v", err)
	}
	if want := filepath.Join(fakeHome, "work", ".yd", "completion.zsh"); moved.ScriptPath != want {
		t.Errorf("script = %q, want %q | 脚本路径不符", moved.ScriptPath, want)
	}
	if want := filepath.Join(fakeHome, ".zshrc"); moved.RCFile != want {
		t.Errorf("startup file = %q, want %q | 启动文件不符", moved.RCFile, want)
	}

	// An explicit home wins over the environment. | 显式主目录优先于环境。
	explicit := installRequest(fs, Bash, nil)
	explicit.Home = "/explicit"
	if got, err := Resolve(explicit); err != nil || got.Dir != "/explicit" {
		t.Errorf("explicit home = %q, %v | 显式主目录不符", got.Dir, err)
	}

	// An unknown requested shell is refused; no shell at all falls back to detection and, without $SHELL,
	// fails with the sentinel that names --shell. | 未知的指定 shell 被拒绝；完全不指定则回退到探测，
	// 而没有 $SHELL 时以点明 --shell 的哨兵失败。
	bad := installRequest(fs, "fish", nil)
	if _, err := Resolve(bad); !errors.Is(err, ErrUnknownShell) {
		t.Errorf("want ErrUnknownShell, got %v | 期望哨兵错误", err)
	}
	undetected := installRequest(fs, "", nil)
	if _, err := Resolve(undetected); !errors.Is(err, ErrShellUndetected) {
		t.Errorf("want ErrShellUndetected, got %v | 期望哨兵错误", err)
	}
}

// TestInstallWithoutAppendRCLeavesStartupFileByteIdentical 不传 --append-rc 时启动文件必须逐字节不变。
// TestInstallWithoutAppendRCLeavesStartupFileByteIdentical keeps the startup file byte-identical.
//
// This is the promise of the whole command: installing completion may not edit a dotfile behind the user's
// back, so the file is compared byte for byte rather than by looking for new content. | 这是整条命令的核心承诺：
// 安装补全不得背着用户改动 dotfile，因此这里逐字节比对，而不是只看看有没有多出内容。
func TestInstallWithoutAppendRCLeavesStartupFileByteIdentical(t *testing.T) {
	fs := afero.NewMemMapFs()
	rc := filepath.Join(fakeHome, ".bashrc")
	original := []byte("# my rc | 我的 rc\nexport EDITOR=vim\n")
	if err := afero.WriteFile(fs, rc, original, 0o644); err != nil {
		t.Fatalf("seed failed | 预置失败: %v", err)
	}

	res, err := Install(installRequest(fs, Bash, nil))
	if err != nil {
		t.Fatalf("install failed | 安装失败: %v", err)
	}
	if got := readFile(t, fs, rc); !bytes.Equal(got, original) {
		t.Errorf("startup file changed | 启动文件被改动:\n%q", got)
	}
	if res.RCAppended || res.RCSkipped || res.BackupPath != "" {
		t.Errorf("result claims a startup change: %+v | 结果声称改动过启动文件", res)
	}
	if exists(t, fs, rc+".yd-backup") {
		t.Error("a backup was taken although nothing was written | 未写入却留下了备份")
	}

	script, err := Generate(Bash, fixtureRegistry())
	if err != nil {
		t.Fatalf("generate failed | 生成失败: %v", err)
	}
	if got := readFile(t, fs, res.ScriptPath); string(got) != script {
		t.Error("installed script differs from the generated one | 落盘脚本与生成结果不一致")
	}
	if res.Bytes != len(script) {
		t.Errorf("bytes = %d, want %d | 字节数不符", res.Bytes, len(script))
	}

	// A fresh account has no startup file, and installing must not create one. | 新账号没有启动文件，安装也不该建一个。
	empty := afero.NewMemMapFs()
	fresh, err := Install(installRequest(empty, Bash, nil))
	if err != nil {
		t.Fatalf("install failed | 安装失败: %v", err)
	}
	if exists(t, empty, fresh.RCFile) {
		t.Errorf("startup file %s was created | 启动文件被创建", fresh.RCFile)
	}
}

// TestInstallWithAppendRCIsIdempotentAndBacksUpFirst 追加必须幂等，且改动之前必须先备份。
// TestInstallWithAppendRCIsIdempotentAndBacksUpFirst requires the append to be idempotent and backed up.
//
// Three assertions are made here on purpose: the backup content is byte-identical to what the file held
// before, the second run changes nothing at all, and the marker keeps the file from growing a second copy of
// the loading line. | 这里刻意做三组断言：备份内容与改动前逐字节一致、第二次运行什么都不改、标记保证加载行不会
// 被追加第二遍。
func TestInstallWithAppendRCIsIdempotentAndBacksUpFirst(t *testing.T) {
	fs := afero.NewMemMapFs()
	rc := filepath.Join(fakeHome, ".bashrc")
	original := []byte("# my rc | 我的 rc\nexport EDITOR=vim\n")
	if err := afero.WriteFile(fs, rc, original, 0o644); err != nil {
		t.Fatalf("seed failed | 预置失败: %v", err)
	}

	req := installRequest(fs, Bash, nil)
	req.AppendRC = true
	first, err := Install(req)
	if err != nil {
		t.Fatalf("install failed | 安装失败: %v", err)
	}
	if !first.RCAppended || first.RCSkipped {
		t.Fatalf("first run did not append: %+v | 首次运行未追加", first)
	}
	if want := rc + ".yd-backup"; first.BackupPath != want {
		t.Fatalf("backup = %q, want %q | 备份路径不符", first.BackupPath, want)
	}
	if got := readFile(t, fs, first.BackupPath); !bytes.Equal(got, original) {
		t.Errorf("backup is not the original file | 备份与原文件不一致:\n%q", got)
	}

	want := string(original) + markerLine + "\n" + first.LoadLine + "\n"
	afterFirst := readFile(t, fs, rc)
	if string(afterFirst) != want {
		t.Fatalf("startup file = %q, want %q | 启动文件内容不符", afterFirst, want)
	}
	if count := strings.Count(string(afterFirst), Marker); count != 1 {
		t.Errorf("marker appears %d times | 标记出现次数不为 1", count)
	}
	if !strings.Contains(string(afterFirst), first.LoadLine) {
		t.Errorf("startup file does not load the script | 启动文件未加载脚本: %q", afterFirst)
	}

	// The script is rewritten on every run, which is how a later release picks up new commands. | 脚本每次都会
	// 被重写，这正是后续版本获取新命令的方式。
	if err := afero.WriteFile(fs, first.ScriptPath, []byte("hand edited | 手工改动"), 0o644); err != nil {
		t.Fatalf("tamper failed | 改动失败: %v", err)
	}

	second, err := Install(req)
	if err != nil {
		t.Fatalf("second install failed | 第二次安装失败: %v", err)
	}
	if second.RCAppended || !second.RCSkipped {
		t.Errorf("second run was not skipped: %+v | 第二次运行未被跳过", second)
	}
	if second.BackupPath != "" {
		t.Errorf("second run took a backup: %q | 第二次运行留下了备份", second.BackupPath)
	}
	if got := readFile(t, fs, rc); !bytes.Equal(got, afterFirst) {
		t.Errorf("second run changed the startup file | 第二次运行改动了启动文件:\n%q", got)
	}
	if exists(t, fs, rc+".yd-backup.2") {
		t.Error("second run created a numbered backup | 第二次运行创建了编号备份")
	}
	script, err := Generate(Bash, fixtureRegistry())
	if err != nil {
		t.Fatalf("generate failed | 生成失败: %v", err)
	}
	if got := readFile(t, fs, second.ScriptPath); string(got) != script {
		t.Error("second run did not refresh the script | 第二次运行未刷新脚本")
	}
}

// TestInstallAppendRCCreatesMissingStartupFile 启动文件不存在时会被创建，且不留备份。
// TestInstallAppendRCCreatesMissingStartupFile creates a missing startup file without a backup.
func TestInstallAppendRCCreatesMissingStartupFile(t *testing.T) {
	fs := afero.NewMemMapFs()
	req := installRequest(fs, Zsh, nil)
	req.AppendRC = true
	res, err := Install(req)
	if err != nil {
		t.Fatalf("install failed | 安装失败: %v", err)
	}
	if res.BackupPath != "" {
		t.Errorf("a missing file was backed up to %q | 文件本不存在却留下备份", res.BackupPath)
	}
	if !res.RCAppended {
		t.Errorf("nothing was appended: %+v | 未追加", res)
	}
	if got, want := string(readFile(t, fs, res.RCFile)), markerLine+"\n"+res.LoadLine+"\n"; got != want {
		t.Errorf("startup file = %q, want %q | 启动文件内容不符", got, want)
	}
}

// TestInstallAppendRCSeparatesFromAnUnterminatedLine 最后一行没有换行时必须补一个分隔换行。
// TestInstallAppendRCSeparatesFromAnUnterminatedLine inserts a separator newline when the last line is unterminated.
//
// Without the separator the marker would be glued onto the user's last line, turning it into a different
// line - a file that still parses, but no longer says what it said. | 没有这个分隔换行，标记会粘在用户的最后一行上，
// 把那行变成另一行——文件仍能解析，但说的已经不是原来的话。
func TestInstallAppendRCSeparatesFromAnUnterminatedLine(t *testing.T) {
	fs := afero.NewMemMapFs()
	rc := filepath.Join(fakeHome, ".cshrc")
	if err := afero.WriteFile(fs, rc, []byte("setenv EDITOR vim"), 0o644); err != nil {
		t.Fatalf("seed failed | 预置失败: %v", err)
	}
	req := installRequest(fs, Csh, nil)
	req.AppendRC = true
	res, err := Install(req)
	if err != nil {
		t.Fatalf("install failed | 安装失败: %v", err)
	}
	got := string(readFile(t, fs, rc))
	want := "setenv EDITOR vim\n" + markerLine + "\n" + res.LoadLine + "\n"
	if got != want {
		t.Errorf("startup file = %q, want %q | 启动文件内容不符", got, want)
	}
	if strings.Contains(got, "vim"+Marker) {
		t.Errorf("marker was glued onto the previous line | 标记被粘到上一行: %q", got)
	}
}

// TestInstallBackupDoesNotOverwriteAnOlderBackup 已有备份时必须另起编号。
// TestInstallBackupDoesNotOverwriteAnOlderBackup numbers a new backup instead of replacing an older one.
//
// A backup that gets silently replaced is not a backup, so an existing file at the backup path makes the
// next one land beside it. | 被悄悄替换掉的备份不算备份，因此备份路径上已有文件时，新备份会落在它旁边。
func TestInstallBackupDoesNotOverwriteAnOlderBackup(t *testing.T) {
	fs := afero.NewMemMapFs()
	rc := filepath.Join(fakeHome, ".bashrc")
	if err := afero.WriteFile(fs, rc, []byte("current | 当前\n"), 0o644); err != nil {
		t.Fatalf("seed failed | 预置失败: %v", err)
	}
	older := []byte("older | 更早\n")
	if err := afero.WriteFile(fs, rc+".yd-backup", older, 0o644); err != nil {
		t.Fatalf("seed failed | 预置失败: %v", err)
	}
	req := installRequest(fs, Bash, nil)
	req.AppendRC = true
	res, err := Install(req)
	if err != nil {
		t.Fatalf("install failed | 安装失败: %v", err)
	}
	if want := rc + ".yd-backup.2"; res.BackupPath != want {
		t.Fatalf("backup = %q, want %q | 备份路径不符", res.BackupPath, want)
	}
	if got := readFile(t, fs, rc+".yd-backup"); !bytes.Equal(got, older) {
		t.Errorf("the older backup was overwritten | 更早的备份被覆盖:\n%q", got)
	}
}

// TestInstallReportsWriteFailure 写入失败必须返回哨兵错误。
// TestInstallReportsWriteFailure returns the sentinel when a write fails.
func TestInstallReportsWriteFailure(t *testing.T) {
	fs := afero.NewReadOnlyFs(afero.NewMemMapFs())
	if _, err := Install(installRequest(fs, Bash, nil)); !errors.Is(err, ErrWriteScript) {
		t.Fatalf("want ErrWriteScript, got %v | 期望哨兵错误", err)
	}
}
