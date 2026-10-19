package router

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// TestClientIP pins the trusted-proxy model. | TestClientIP 钉住可信代理模型。
func TestClientIP(t *testing.T) {
	trusted10 := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	trusted6 := []netip.Prefix{netip.MustParsePrefix("2001:db8::/32")}
	cases := []struct {
		name    string
		remote  string
		xff     string
		xri     string
		trusted []netip.Prefix
		want    string
	}{
		{"untrusted ignores xff", "203.0.113.9:1234", "1.2.3.4", "", nil, "203.0.113.9"},
		{"untrusted ignores xff despite trusted list", "203.0.113.9:1234", "1.2.3.4", "", trusted10, "203.0.113.9"},
		{"trusted takes first untrusted from the right", "10.0.0.5:1234", "1.2.3.4, 203.0.113.7, 10.0.0.6", "", trusted10, "203.0.113.7"},
		{"trusted single hop", "10.0.0.5:1", "198.51.100.7", "", trusted10, "198.51.100.7"},
		{"all hops trusted returns the leftmost", "10.0.0.5:1", "10.1.2.3, 10.0.0.9", "", trusted10, "10.1.2.3"},
		{"malformed xff falls back to peer", "10.0.0.5:1", "nope, 10.0.0.9", "", trusted10, "10.0.0.5"},
		{"empty hop falls back to peer", "10.0.0.5:1", "1.2.3.4,,5.6.7.8", "", trusted10, "10.0.0.5"},
		{"x-real-ip trusted", "10.0.0.5:1", "", "198.51.100.8", trusted10, "198.51.100.8"},
		{"x-real-ip ignored for untrusted peer", "203.0.113.9:1", "", "198.51.100.8", trusted10, "203.0.113.9"},
		{"invalid x-real-ip falls back", "10.0.0.5:1", "", "not-an-ip", trusted10, "10.0.0.5"},
		{"ipv6 peer", "[2001:db8::1]:443", "", "", nil, "2001:db8::1"},
		{"ipv6 trusted proxy", "[2001:db8::1]:443", "2001:db8::99", "", trusted6, "2001:db8::99"},
		{"bare remote address", "192.0.2.7", "", "", nil, "192.0.2.7"},
		{"ipv4-mapped peer is unmapped", "[::ffff:192.0.2.7]:80", "", "", nil, "192.0.2.7"},
		{"empty remote address", "", "", "", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xri != "" {
				req.Header.Set("X-Real-Ip", tc.xri)
			}
			c := &Ctx{Request: req}
			if got := c.ClientIP(tc.trusted); got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestClientIPRejectsOversizedForwardedChain bounds the header before parsing it. | TestClientIPRejectsOversizedForwardedChain 在解析前限制头部长度。
func TestClientIPRejectsOversizedForwardedChain(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Forwarded-For", strings.Repeat("a", maxForwardedBytes+1))
	c := &Ctx{Request: req}
	if got := c.ClientIP(trusted); got != "10.0.0.5" {
		t.Fatalf("oversized chain: ClientIP = %q, want peer 10.0.0.5", got)
	}
}

// TestClientIPRejectsTooManyHops bounds the hop count. | TestClientIPRejectsTooManyHops 限制跳数。
func TestClientIPRejectsTooManyHops(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	hops := make([]string, maxForwardedHops+1)
	for i := range hops {
		hops[i] = "1.2.3.4"
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Forwarded-For", strings.Join(hops, ","))
	c := &Ctx{Request: req}
	if got := c.ClientIP(trusted); got != "10.0.0.5" {
		t.Fatalf("too many hops: ClientIP = %q, want peer 10.0.0.5", got)
	}
}

// TestClientIPJoinsEveryForwardedLine is the regression for Header.Get returning only the first
// X-Forwarded-For line: the walk must see the proxy-appended lines at the right. | TestClientIPJoinsEveryForwardedLine
// 是 Header.Get 只返回第一行 X-Forwarded-For 的回归：从右向左的遍历必须看到代理追加的行。
func TestClientIPJoinsEveryForwardedLine(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Add("X-Forwarded-For", "1.2.3.4")
	req.Header.Add("X-Forwarded-For", "9.9.9.9")
	c := &Ctx{Request: req}
	if got := c.ClientIP(trusted); got != "9.9.9.9" {
		t.Fatalf("multi-line XFF: ClientIP = %q, want the rightmost untrusted hop 9.9.9.9", got)
	}
}
