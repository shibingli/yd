package protocol

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// connectReq builds a CONNECT request with the given headers and protocol shape. | connectReq 构造带指定头与协议形态的 CONNECT 请求。
func connectReq(headers map[string]string, major int, proto string) *http.Request {
	r := httptest.NewRequest(http.MethodConnect, "/ws", nil)
	r.ProtoMajor = major
	r.Proto = proto
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

// TestIsExtendedConnect pins the H2/H3 request shapes that must be recognized. | TestIsExtendedConnect 固定必须识别的 H2/H3 请求形态。
func TestIsExtendedConnect(t *testing.T) {
	cases := []struct {
		name string
		req  *http.Request
		want bool
	}{
		{"h2 | HTTP/2", connectReq(map[string]string{":protocol": "websocket"}, 2, "HTTP/2.0"), true},
		{"h2 mixed case | HTTP/2 大小写混合", connectReq(map[string]string{":protocol": "WebSocket"}, 2, "HTTP/2.0"), true},
		{"h3 | HTTP/3", connectReq(nil, 3, "websocket"), true},
		{"h3 normal request | HTTP/3 普通请求", connectReq(nil, 3, "HTTP/3.0"), false},
		{"plain connect | 普通 CONNECT", connectReq(nil, 2, "HTTP/2.0"), false},
		{"empty protocol | 空协议 token", connectReq(map[string]string{":protocol": ""}, 2, "HTTP/2.0"), false},
		{"get with header | GET 带协议头", &http.Request{Method: http.MethodGet, Header: http.Header{":protocol": {"websocket"}}}, false},
		{"nil | 空请求", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsExtendedConnect(tc.req); got != tc.want {
				t.Fatalf("IsExtendedConnect = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestH2ExtendedConnectEnabled mirrors the standard library's init-time check. | TestH2ExtendedConnectEnabled 镜像标准库 init 时的判定。
func TestH2ExtendedConnectEnabled(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{"exact | 精确匹配", "http2xconnect=1", true},
		{"among others | 与其他项共存", "gctrace=1,http2xconnect=1", true},
		{"empty | 空值", "", false},
		{"disabled | 显式关闭", "http2xconnect=0", false},
		{"other setting | 其它设置", "gctrace=1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GODEBUG", tc.value)
			if got := H2ExtendedConnectEnabled(); got != tc.want {
				t.Fatalf("H2ExtendedConnectEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMergeGODEBUG pins the environment-merge rules used by every launcher. | TestMergeGODEBUG 固定各启动入口共用的环境合并规则。
func TestMergeGODEBUG(t *testing.T) {
	const setting = "http2xconnect=1"
	cases := []struct {
		name    string
		godebug string
		want    string
	}{
		{"empty | 空值", "", "http2xconnect=1"},
		{"existing setting | 已有其他项", "gctrace=1", "gctrace=1,http2xconnect=1"},
		{"already present | 已包含该项", "gctrace=1,http2xconnect=1", "gctrace=1,http2xconnect=1"},
		{"already present with spaces | 已包含且带空格", " http2xconnect=1 ", " http2xconnect=1 "},
		{"trailing comma | 尾随逗号", "gctrace=1,", "gctrace=1,http2xconnect=1"},
		{"only commas | 仅逗号", " , ", "http2xconnect=1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MergeGODEBUG(tc.godebug, setting); got != tc.want {
				t.Fatalf("MergeGODEBUG(%q) = %q, want %q", tc.godebug, got, tc.want)
			}
		})
	}
}
