package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIsUpgradeRequestIncludesConnect pins CONNECT tunnels and RFC 8441/9220 streams
// as long-lived requests that must not be wrapped or compressed. | TestIsUpgradeRequestIncludesConnect
// 把 CONNECT 隧道与 RFC 8441/9220 流固定为不得被包装或压缩的长连接请求。
func TestIsUpgradeRequestIncludesConnect(t *testing.T) {
	plain := httptest.NewRequest(http.MethodConnect, "/ws", nil)
	if !isUpgradeRequest(plain) {
		t.Fatal("plain CONNECT was not recognized | 普通 CONNECT 未被识别")
	}

	h2 := httptest.NewRequest(http.MethodConnect, "/ws", nil)
	h2.Header.Set(":protocol", "websocket")
	if !isUpgradeRequest(h2) {
		t.Fatal("HTTP/2 extended CONNECT was not recognized | HTTP/2 extended CONNECT 未被识别")
	}

	h3 := httptest.NewRequest(http.MethodConnect, "/ws", nil)
	h3.ProtoMajor = 3
	h3.Proto = "websocket"
	if !isUpgradeRequest(h3) {
		t.Fatal("HTTP/3 extended CONNECT was not recognized | HTTP/3 extended CONNECT 未被识别")
	}

	if isUpgradeRequest(httptest.NewRequest(http.MethodGet, "/ws", nil)) {
		t.Fatal("plain GET was recognized as an upgrade | 普通 GET 被识别为升级请求")
	}
}
