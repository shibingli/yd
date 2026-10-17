package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/shibingli/yd/http/router"
)

// keyCtx builds a request context for key-extractor tests. | keyCtx 为键提取器测试构造请求上下文。
func keyCtx(method, target string, hdr map[string]string, remote string) *router.Ctx {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remote
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return &router.Ctx{Request: req}
}

// TestKeyPath covers path extraction and the missing-value marker. | TestKeyPath 覆盖路径提取与缺失值标记。
func TestKeyPath(t *testing.T) {
	k := KeyPath()
	if got := k(keyCtx(http.MethodGet, "/api/users?x=1", nil, "10.0.0.1:1")); got != "/api/users" {
		t.Errorf("KeyPath = %q, want /api/users", got)
	}
	empty := &router.Ctx{Request: &http.Request{URL: &url.URL{}}}
	if got := k(empty); got != missingKeyPart {
		t.Errorf("KeyPath(empty) = %q, want %q", got, missingKeyPart)
	}
}

// TestKeyQuery covers the named parameter, trimming and the missing marker. | TestKeyQuery 覆盖指定参数、去空白与缺失标记。
func TestKeyQuery(t *testing.T) {
	k := KeyQuery("user")
	if got := k(keyCtx(http.MethodGet, "/?user=alice&user=bob", nil, "10.0.0.1:1")); got != "alice" {
		t.Errorf("KeyQuery = %q, want alice", got)
	}
	if got := k(keyCtx(http.MethodGet, "/?user=%20alice%20", nil, "10.0.0.1:1")); got != "alice" {
		t.Errorf("KeyQuery trimmed = %q, want alice", got)
	}
	if got := k(keyCtx(http.MethodGet, "/?other=1", nil, "10.0.0.1:1")); got != missingKeyPart {
		t.Errorf("KeyQuery(missing) = %q, want %q", got, missingKeyPart)
	}
}

// TestKeyHeader covers case-insensitive lookup, trimming and the missing marker. | TestKeyHeader 覆盖大小写不敏感查找、去空白与缺失标记。
func TestKeyHeader(t *testing.T) {
	k := KeyHeader("X-Api-Key")
	if got := k(keyCtx(http.MethodGet, "/", map[string]string{"x-api-key": "  secret  "}, "10.0.0.1:1")); got != "secret" {
		t.Errorf("KeyHeader = %q, want secret", got)
	}
	if got := k(keyCtx(http.MethodGet, "/", nil, "10.0.0.1:1")); got != missingKeyPart {
		t.Errorf("KeyHeader(missing) = %q, want %q", got, missingKeyPart)
	}
}

// TestKeyBasicAuthUser covers the username and the missing marker. | TestKeyBasicAuthUser 覆盖用户名与缺失标记。
func TestKeyBasicAuthUser(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("alice", "pw")
	if got := KeyBasicAuthUser()(&router.Ctx{Request: req}); got != "alice" {
		t.Errorf("KeyBasicAuthUser = %q, want alice", got)
	}
	if got := KeyBasicAuthUser()(keyCtx(http.MethodGet, "/", nil, "10.0.0.1:1")); got != missingKeyPart {
		t.Errorf("KeyBasicAuthUser(missing) = %q, want %q", got, missingKeyPart)
	}
}

// TestKeyRemoteIPResistsSpoofing proves that an untrusted peer cannot pick its bucket with a header.
// | TestKeyRemoteIPResistsSpoofing 证明不可信对端无法用头部挑选自己的桶。
func TestKeyRemoteIPResistsSpoofing(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	untrusted := keyCtx(http.MethodGet, "/", map[string]string{"X-Forwarded-For": "1.2.3.4"}, "203.0.113.9:1234")
	if got := KeyRemoteIP(trusted)(untrusted); got != "203.0.113.9" {
		t.Errorf("untrusted peer key = %q, want 203.0.113.9", got)
	}
	trustedPeer := keyCtx(http.MethodGet, "/", map[string]string{"X-Forwarded-For": "198.51.100.7"}, "10.0.0.5:1234")
	if got := KeyRemoteIP(trusted)(trustedPeer); got != "198.51.100.7" {
		t.Errorf("trusted peer key = %q, want 198.51.100.7", got)
	}
}

// TestKeyFingerprint covers stability, ordering, missing parts and privacy. | TestKeyFingerprint 覆盖稳定性、顺序、缺失部分与隐私。
func TestKeyFingerprint(t *testing.T) {
	fp := KeyFingerprint(KeyRemoteIP(nil), KeyHeader("User-Agent"))
	a := keyCtx(http.MethodGet, "/", map[string]string{"User-Agent": "agent/1.0"}, "203.0.113.9:1")
	b := keyCtx(http.MethodGet, "/", map[string]string{"User-Agent": "agent/1.0"}, "203.0.113.9:2")
	if ka, kb := fp(a), fp(b); ka != kb {
		t.Errorf("same shape produced different keys: %q vs %q", ka, kb)
	}
	c := keyCtx(http.MethodGet, "/", map[string]string{"User-Agent": "agent/2.0"}, "203.0.113.9:1")
	if fp(a) == fp(c) {
		t.Error("different user agents produced the same key")
	}
	if !strings.HasPrefix(fp(a), "fp:") {
		t.Errorf("fingerprint = %q, want fp: prefix", fp(a))
	}
	if strings.Contains(fp(a), "agent/1.0") || strings.Contains(fp(a), "203.0.113.9") {
		t.Errorf("fingerprint leaked a raw value: %q", fp(a))
	}

	// A missing part and an explicit marker value must agree. | 缺失部分与显式标记值必须一致。
	missing := keyCtx(http.MethodGet, "/", nil, "203.0.113.9:1")
	marked := keyCtx(http.MethodGet, "/", map[string]string{"User-Agent": missingKeyPart}, "203.0.113.9:1")
	if fp(missing) != fp(marked) {
		t.Errorf("missing part = %q, marked = %q; want equal", fp(missing), fp(marked))
	}

	// Part order is part of the key. | 部分顺序是键的一部分。
	swapped := KeyFingerprint(KeyHeader("User-Agent"), KeyRemoteIP(nil))
	if fp(a) == swapped(a) {
		t.Error("swapping parts produced the same key")
	}
	if got := KeyFingerprint()(a); got != missingKeyPart {
		t.Errorf("empty fingerprint = %q, want %q", got, missingKeyPart)
	}
	if got := KeyFingerprint(nil)(a); got == "" || strings.Contains(got, "203.0.113.9") {
		t.Errorf("nil part key = %q", got)
	}
}
