package app

import (
	"os"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/http/protocol"
	"github.com/shibingli/yd/runtime/server"
)

// bootstrapEnv prevents an endless re-exec loop. | bootstrapEnv 防止无限重启循环。
const bootstrapEnv = "YD_HTTP2XCONNECT_BOOTSTRAPPED"

// ErrReexecFailed reports that the process could not be restarted with the
// merged GODEBUG value. | ErrReexecFailed 表示无法带合并后的 GODEBUG 值重新启动进程。
var ErrReexecFailed = errx.New("app: failed to re-exec with GODEBUG | app：带 GODEBUG 重新执行失败")

// reexecFunc is the process-replacement function; tests inject a fake. | reexecFunc 是进程替换函数；测试可注入假实现。
var reexecFunc = platformReexec

// isTestProcess reports whether the current binary is a go test binary. | isTestProcess 报告当前二进制是否为 go test 二进制。
var isTestProcess = func() bool { return isTestBinary(os.Args[0]) }

// isTestBinary reports whether path names a go test binary. | isTestBinary 报告 path 是否为 go test 二进制。
func isTestBinary(path string) bool {
	return strings.HasSuffix(path, ".test") || strings.HasSuffix(path, ".test.exe")
}

// http2Possible reports whether the server can expose HTTP/2 at all. | http2Possible 报告服务器是否可能暴露 HTTP/2。
func http2Possible(o server.Options) bool {
	return o.H2C || o.TLS.Enabled || o.ACME.Enabled || o.HTTP3.Enabled
}

// ensureHTTP2ExtendedConnect implements the force switch: when the operator
// asked for RFC 8441 and the process did not start with GODEBUG=http2xconnect=1,
// restart the process image with the merged environment. | ensureHTTP2ExtendedConnect
// 实现强制开关：使用方要求 RFC 8441 且进程启动时没有 GODEBUG=http2xconnect=1 时，
// 带合并后的环境重新启动进程镜像。
//
// The standard library reads GODEBUG in package init, so setting it in-process is
// impossible; re-exec is the only correct point. Tests never re-exec. | 标准库在包
// init 时读取 GODEBUG，进程内设置无效；重启是唯一正确的时机。测试永不重启。
func ensureHTTP2ExtendedConnect(opts server.Options, reexec func([]string) error) error {
	if !opts.ForceHTTP2ExtendedConnect || !http2Possible(opts) {
		return nil
	}
	if protocol.H2ExtendedConnectEnabled() {
		return nil
	}
	if os.Getenv(bootstrapEnv) == "1" {
		return server.ErrHTTP2ExtendedConnectRequired
	}
	if isTestProcess() {
		return nil
	}

	env := mergeEnvValue(os.Environ(), "GODEBUG", protocol.MergeGODEBUG(os.Getenv("GODEBUG"), "http2xconnect=1"))
	env = append(env, bootstrapEnv+"=1")
	return reexec(env)
}

// mergeEnvValue replaces every case-insensitive match of key and appends one
// merged entry at the end. | mergeEnvValue 删除所有大小写不敏感的 key 条目，并在末尾追加一条合并结果。
func mergeEnvValue(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(k, key) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, key+"="+value)
}
