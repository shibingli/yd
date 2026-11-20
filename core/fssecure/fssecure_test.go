package fssecure

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRestrictExistingFileAndDirectory proves the helper accepts both shapes and rejects a missing
// path instead of silently succeeding. The platform-specific ACL/mode assertions live in the
// build-tagged tests. | TestRestrictExistingFileAndDirectory 证明助手接受文件与目录两种形态，并对
// 缺失路径返回错误而不是静默成功。平台相关的 ACL/模式断言放在带构建标签的测试里。
func TestRestrictExistingFileAndDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "secret.env")
	if err := os.WriteFile(file, []byte("TOKEN=test"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := Restrict(file, 0o600); err != nil {
		t.Fatalf("Restrict(file) = %v | Restrict(file) 返回 %v", err, err)
	}
	if err := Restrict(dir, 0o700); err != nil {
		t.Fatalf("Restrict(dir) = %v | Restrict(dir) 返回 %v", err, err)
	}
	if err := Restrict(filepath.Join(dir, "missing"), 0o600); err == nil {
		t.Fatal("Restrict(missing) = nil, want an error | Restrict(missing) 返回 nil，期望错误")
	}
}
