//go:build windows || unix

package versions

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// copySelfFile copies one executable file preserving its mode. | copySelfFile 复制一个可执行文件并
// 保留权限位。
func copySelfFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// TestSelfReplaceRunningHelper is the child used by TestSelfReplaceRunningExecutable: it signals
// readiness and then keeps the copied executable image running. | TestSelfReplaceRunningHelper 是
// TestSelfReplaceRunningExecutable 使用的子进程：它发出就绪信号，然后持续运行被复制的可执行映像。
func TestSelfReplaceRunningHelper(t *testing.T) {
	if os.Getenv("YD_SELF_REPLACE_HELPER") != "1" {
		t.Skip("非替换辅助进程调用 | not the replacement helper invocation")
	}
	if ready := os.Getenv("YD_SELF_REPLACE_READY"); ready != "" {
		if err := os.WriteFile(ready, []byte("ready"), 0o644); err != nil {
			t.Fatalf("写入就绪标记失败: %v | writing ready marker: %v", err, err)
		}
	}
	for {
		time.Sleep(time.Hour)
	}
}

// TestSelfReplaceRunningExecutable proves the running image can be renamed aside and replaced on
// Windows and Unix, which is what makes `yd versions upgrade --self` work for custom downloads,
// go-installed binaries and explicit --self-binary paths alike. |
// TestSelfReplaceRunningExecutable 证明 Windows 与 Unix 都能把运行中的映像移到一旁并替换，
// 这正是 `yd versions upgrade --self` 能同时适配自定义下载、go install 产物与
// --self-binary 指定路径的原因。
func TestSelfReplaceRunningExecutable(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("取测试二进制路径失败: %v | os.Executable: %v", err, err)
	}
	dir := t.TempDir()
	name := "yd"
	if runtime.GOOS == "windows" {
		name = "yd.exe"
	}
	target := filepath.Join(dir, name)
	if err := copySelfFile(exe, target); err != nil {
		t.Fatalf("复制测试二进制失败: %v | copying test binary: %v", err, err)
	}
	ready := filepath.Join(dir, "ready")
	cmd := exec.Command(target, "-test.run=^TestSelfReplaceRunningHelper$")
	cmd.Env = append(os.Environ(),
		"YD_SELF_REPLACE_HELPER=1",
		"YD_SELF_REPLACE_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动辅助进程失败: %v | starting helper: %v", err, err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("辅助进程未就绪 | helper never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	staged := filepath.Join(dir, "staged")
	body := []byte("replacement binary")
	if err := os.WriteFile(staged, body, 0o755); err != nil {
		t.Fatalf("写入暂存二进制失败: %v | writing staged binary: %v", err, err)
	}
	backup, err := selfReplace(target, staged)
	if err != nil {
		t.Fatalf("替换运行中的可执行文件失败（pid %d）: %v | replacing running executable (pid %d): %v", pid, err, pid, err)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("替换后内容 = %q (err=%v)，期望新载荷 | target = %q (err=%v)", got, err, got, err)
	}
	if backup != "" {
		t.Cleanup(func() { _ = os.Remove(backup) })
	}
}
