package ws

import (
	"net/http"
	"testing"
)

// acceptSink keeps the computed accept key observable. | acceptSink 让计算出的 accept 值保持可观测。
var acceptSink string

// TestAcceptKeyAllocations pins that computing the accept value does not
// allocate beyond the returned string. | TestAcceptKeyAllocations 固定「计算
// accept 值除返回字符串外不产生分配」。
func TestAcceptKeyAllocations(t *testing.T) {
	const key = "dGhlIHNhbXBsZSBub25jZQ=="
	allocs := testing.AllocsPerRun(100, func() { acceptSink = acceptKey(key) })
	if allocs > 1 {
		t.Fatalf("acceptKey allocations = %v, want <= 1 | acceptKey 分配次数 = %v，期望 <= 1", allocs, allocs)
	}
}

// subprotocolSink keeps the selected subprotocol observable. | subprotocolSink 让选中的子协议保持可观测。
var subprotocolSink string

// TestSelectSubprotocolDoesNotAllocate pins that parsing the offered
// subprotocols does not allocate. | TestSelectSubprotocolDoesNotAllocate 固定
// 「解析客户端提议的子协议不产生分配」。
func TestSelectSubprotocolDoesNotAllocate(t *testing.T) {
	r := &http.Request{Header: http.Header{
		"Sec-Websocket-Protocol": {"yd.v1, chat", "other"},
	}}
	supported := []string{"yd.v1", "chat"}
	if got := selectSubprotocol(r, supported); got != "yd.v1" {
		t.Fatalf("selectSubprotocol = %q, want %q | selectSubprotocol = %q，期望 %q", got, "yd.v1", got, "yd.v1")
	}
	allocs := testing.AllocsPerRun(100, func() { subprotocolSink = selectSubprotocol(r, supported) })
	if allocs != 0 {
		t.Fatalf("selectSubprotocol allocations = %v, want 0 | selectSubprotocol 分配次数 = %v，期望 0", allocs, allocs)
	}
}
