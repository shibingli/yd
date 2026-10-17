// Package protocol holds HTTP protocol predicates shared by the router, the
// WebSocket layer and middleware. | Package protocol 提供路由器、WebSocket 层与中间件共用的
// HTTP 协议判定。
//
// Keeping these checks in one place avoids three drifting copies of the same
// Extended CONNECT rules. | 把这些判定集中在一处，避免同一套 Extended CONNECT 规则出现三份实现并逐渐漂移。
package protocol

import (
	"net/http"
	"os"
	"strings"
)

// IsExtendedConnect reports whether r is an RFC 8441 (HTTP/2) or RFC 9220
// (HTTP/3) WebSocket extended CONNECT request. | IsExtendedConnect 报告 r 是否为
// RFC 8441（HTTP/2）或 RFC 9220（HTTP/3）的 WebSocket Extended CONNECT 请求。
//
// HTTP/2 exposes the protocol token as the :protocol header; quic-go's HTTP/3
// server removes that header and puts the token in Request.Proto instead. | HTTP/2
// 通过 :protocol 头暴露协议 token；quic-go 的 HTTP/3 服务端会删除该头，把 token 放进
// Request.Proto。
func IsExtendedConnect(r *http.Request) bool {
	if r == nil || r.Method != http.MethodConnect {
		return false
	}
	if p := r.Header.Get(":protocol"); p != "" {
		return strings.EqualFold(p, "websocket")
	}
	return r.ProtoMajor == 3 && strings.EqualFold(r.Proto, "websocket")
}

// H2ExtendedConnectEnabled reports whether the process was started with
// GODEBUG=http2xconnect=1. | H2ExtendedConnectEnabled 报告进程是否以
// GODEBUG=http2xconnect=1 启动。
//
// The check mirrors the standard library exactly: net/http's HTTP/2 package
// reads GODEBUG once in init and only then advertises
// SETTINGS_ENABLE_CONNECT_PROTOCOL. | 该判定与标准库完全一致：net/http 的 HTTP/2
// 包在 init 时只读取一次 GODEBUG，只有读到该项才会通告
// SETTINGS_ENABLE_CONNECT_PROTOCOL。
func H2ExtendedConnectEnabled() bool {
	return strings.Contains(os.Getenv("GODEBUG"), "http2xconnect=1")
}

// MergeGODEBUG returns godebug with setting appended unless it already contains
// that exact setting. | MergeGODEBUG 在 godebug 不含该精确设置时追加 setting，否则原样返回。
//
// Empty and comma-only values collapse to setting; existing settings are
// preserved verbatim, including surrounding whitespace. | 空值与仅含逗号的值会归并为
// setting；已有项原样保留，包括其两侧空白。
func MergeGODEBUG(godebug, setting string) string {
	for _, pair := range strings.Split(godebug, ",") {
		if strings.TrimSpace(pair) == setting {
			return godebug
		}
	}
	trimmed := strings.Trim(godebug, " ,")
	if trimmed == "" {
		return setting
	}
	return trimmed + "," + setting
}
