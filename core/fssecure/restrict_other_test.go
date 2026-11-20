//go:build !windows

package fssecure

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRestrictAppliesPOSIXMode proves the non-Windows path applies the requested mode. |
// TestRestrictAppliesPOSIXMode 证明非 Windows 路径施加了请求的模式。
func TestRestrictAppliesPOSIXMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.env")
	if err := os.WriteFile(path, []byte("TOKEN=test"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := Restrict(path, 0o600); err != nil {
		t.Fatalf("Restrict = %v | Restrict 返回 %v", err, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 600 | 模式 = %o，期望 600", got, got)
	}
}
