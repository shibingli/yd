package guomi

import (
	"strings"
	"testing"
)

// TestCipherSuiteInfo checks the self-description of the cipher suite, which feeds /health and audit output. | TestCipherSuiteInfo 校验加密套件自描述信息（供 /health 与审计输出使用）。
func TestCipherSuiteInfo(t *testing.T) {
	want := map[string]string{
		"sm2": "gmsm/sm2 C1C3C2",
		"sm3": "GB/T 32905-2016",
		"sm4": "gmsm/sm4 CBC/GCM",
	}
	info := CipherSuiteInfo()
	for k, v := range want {
		got, ok := info[k]
		if !ok {
			t.Fatalf("CipherSuiteInfo() 缺少键 %q", k)
		}
		if got != v {
			t.Fatalf("CipherSuiteInfo()[%q] = %q, want %q", k, got, v)
		}
	}
	for k, v := range info {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			t.Fatalf("CipherSuiteInfo() 存在空键或空值: %q=%q", k, v)
		}
	}

	// The returned map must be a mutable copy: caller edits may not pollute package state. | 必须返回可变副本，调用方修改不得污染包内状态。
	info["sm2"] = "tampered"
	if got := CipherSuiteInfo()["sm2"]; got != want["sm2"] {
		t.Fatalf("修改返回值后 CipherSuiteInfo()[sm2] = %q, want %q", got, want["sm2"])
	}
}
