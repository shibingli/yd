// Package htmx 提供 Htmx 4 的服务端辅助能力：请求判定、响应头与多区域片段渲染。
//
// 端点产出物一律是 HTML 片段（不是 JSON）。本包只依赖标准库与 router，
// 头名、属性名、事件名全部遵循 Htmx 4 的约定：
//
//   - 请求侧触发元素头是 HX-Source（Htmx 4 中 HX-Trigger 仅作响应头，二者已分离）；
//   - 事件名统一为 "htmx:阶段:动作" 冒号分段形式，如 htmx:after:swap；
//   - 多区域更新优先使用 <hx-partial>（见 RenderPartials），hx-swap-oob 仅作兼容路径；
//   - 属性继承是显式的，父元素需加 :inherited 修饰符才会被子元素继承，
//     因此本包输出的片段不会依赖隐式继承。
package htmx

import (
	"net/http"
	"strings"
)

// Htmx 4 请求头名称。
const (
	headerRequest        = "HX-Request"
	headerBoosted        = "HX-Boosted"
	headerRequestType    = "HX-Request-Type"
	headerTarget         = "HX-Target"
	headerSource         = "HX-Source"
	headerCurrentURL     = "HX-Current-URL"
	headerHistoryRestore = "HX-History-Restore-Request"
)

// headerValue 返回请求头的值；r 为 nil 时返回空串。
func headerValue(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	return r.Header.Get(name)
}

// headerIsTrue 报告请求头是否为 "true"（大小写不敏感，容忍首尾空白）。
func headerIsTrue(r *http.Request, name string) bool {
	return strings.EqualFold(strings.TrimSpace(headerValue(r, name)), "true")
}

// IsRequest 报告请求是否由 Htmx 发起（HX-Request: true）。
//
// 这是区分「整页请求」与「片段请求」的入口：整页请求返回布局 + 内容，
// 片段请求只返回内容片段；需要更细的粒度时配合 IsPartial。
func IsRequest(r *http.Request) bool { return headerIsTrue(r, headerRequest) }

// IsBoosted 报告请求是否由 hx-boost 发起（HX-Boosted: true）。
func IsBoosted(r *http.Request) bool { return headerIsTrue(r, headerBoosted) }

// RequestType 返回 HX-Request-Type 的取值，可能为 "partial"、"full" 或空串。
//
// "partial" 表示只替换目标区域，"full" 表示目标为 body 或使用了 hx-select。
// 返回值已去除首尾空白并统一为小写；头缺失时返回空串。
func RequestType(r *http.Request) string {
	return strings.ToLower(strings.TrimSpace(headerValue(r, headerRequestType)))
}

// Target 返回 HX-Target，即本次请求的交换目标，形如 "div#results"。
func Target(r *http.Request) string { return headerValue(r, headerTarget) }

// Source 返回 HX-Source，即触发本次请求的元素，形如 "button#submit"。
//
// Htmx 4 中请求侧的该头由 HX-Trigger 改名而来，仅携带触发元素，不再携带表单字段名。
func Source(r *http.Request) string { return headerValue(r, headerSource) }

// CurrentURL 返回 HX-Current-URL，即浏览器地址栏的当前 URL。
func CurrentURL(r *http.Request) string { return headerValue(r, headerCurrentURL) }

// IsHistoryRestore 报告本次请求是否由浏览器前进/后退的历史恢复触发
// （HX-History-Restore-Request: true）。
func IsHistoryRestore(r *http.Request) bool { return headerIsTrue(r, headerHistoryRestore) }

// IsPartial 报告本次请求是否只应返回片段：IsRequest 为真且 RequestType 不为 "full"。
//
// 普通 Htmx 请求（未携带 HX-Request-Type）按片段处理，"full" 则按整页处理。
func IsPartial(r *http.Request) bool {
	return IsRequest(r) && RequestType(r) != "full"
}
