package ws

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSelectSubprotocolIsCaseSensitive pins the RFC 7936 rule: subprotocol names
// are compared with a case-sensitive string match. | TestSelectSubprotocolIsCaseSensitive
// 固定 RFC 7936 的规则：子协议名按大小写敏感的字符串比较。
func TestSelectSubprotocolIsCaseSensitive(t *testing.T) {
	offer := httptest.NewRequest(http.MethodGet, "/ws", nil)
	offer.Header.Set("Sec-WebSocket-Protocol", "Chat")
	if got := selectSubprotocol(offer, []string{"chat"}); got != "" {
		t.Fatalf("selectSubprotocol = %q, want empty (RFC 7936 is case-sensitive) | selectSubprotocol = %q，期望为空（RFC 7936 大小写敏感）", got, got)
	}

	exact := httptest.NewRequest(http.MethodGet, "/ws", nil)
	exact.Header.Set("Sec-WebSocket-Protocol", "chat")
	if got := selectSubprotocol(exact, []string{"chat"}); got != "chat" {
		t.Fatalf("selectSubprotocol = %q, want %q | selectSubprotocol = %q，期望 %q", got, "chat", got, "chat")
	}
}
