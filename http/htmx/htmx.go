// Package htmx provides server-side helpers for Htmx 4: request detection, response headers, and multi-region fragment rendering. | Package htmx 提供 Htmx 4 的服务端辅助能力：请求判定、响应头与多区域片段渲染。
//
// Endpoint output is always HTML fragments (never JSON). This package depends only on the standard library and router, | 端点产出物一律是 HTML 片段（不是 JSON）。本包只依赖标准库与 router，
// and header names, attribute names, and event names all follow Htmx 4 conventions: | 头名、属性名、事件名全部遵循 Htmx 4 的约定：
//
//   - The request-side triggering element header is HX-Source (in Htmx 4, HX-Trigger is a response header only; the two are separate); |   - 请求侧触发元素头是 HX-Source（Htmx 4 中 HX-Trigger 仅作响应头，二者已分离）；
//   - Event names uniformly use the colon-segmented "htmx:phase:action" form, e.g. htmx:after:swap; |   - 事件名统一为 "htmx:阶段:动作" 冒号分段形式，如 htmx:after:swap；
//   - Multi-region updates prefer <hx-partial> (see RenderPartials); hx-swap-oob is only a compatibility path; |   - 多区域更新优先使用 <hx-partial>（见 RenderPartials），hx-swap-oob 仅作兼容路径；
//   - Attribute inheritance is explicit: a parent element needs the :inherited modifier before children inherit from it, |   - 属性继承是显式的，父元素需加 :inherited 修饰符才会被子元素继承，
//     so the fragments this package emits never rely on implicit inheritance. |     因此本包输出的片段不会依赖隐式继承。
package htmx

import (
	"net/http"
	"strings"
)

// Htmx 4 request header names. | Htmx 4 请求头名称。
const (
	headerRequest        = "HX-Request"
	headerBoosted        = "HX-Boosted"
	headerRequestType    = "HX-Request-Type"
	headerTarget         = "HX-Target"
	headerSource         = "HX-Source"
	headerCurrentURL     = "HX-Current-URL"
	headerHistoryRestore = "HX-History-Restore-Request"
)

// headerValue returns the value of the request header; it returns an empty string when r is nil. | headerValue 返回请求头的值；r 为 nil 时返回空串。
func headerValue(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	return r.Header.Get(name)
}

// headerIsTrue reports whether the request header is "true" (case-insensitive, tolerating leading and trailing whitespace). | headerIsTrue 报告请求头是否为 "true"（大小写不敏感，容忍首尾空白）。
func headerIsTrue(r *http.Request, name string) bool {
	return strings.EqualFold(strings.TrimSpace(headerValue(r, name)), "true")
}

// IsRequest reports whether the request was issued by Htmx (HX-Request: true). | IsRequest 报告请求是否由 Htmx 发起（HX-Request: true）。
//
// This is the entry point for distinguishing a full-page request from a fragment request: a full-page request returns layout plus content, | 这是区分「整页请求」与「片段请求」的入口：整页请求返回布局 + 内容，
// while a fragment request returns only the content fragment; pair it with IsPartial for finer granularity. | 片段请求只返回内容片段；需要更细的粒度时配合 IsPartial。
func IsRequest(r *http.Request) bool { return headerIsTrue(r, headerRequest) }

// IsBoosted reports whether the request was issued by hx-boost (HX-Boosted: true). | IsBoosted 报告请求是否由 hx-boost 发起（HX-Boosted: true）。
func IsBoosted(r *http.Request) bool { return headerIsTrue(r, headerBoosted) }

// RequestType returns the value of HX-Request-Type, which may be "partial", "full", or the empty string. | RequestType 返回 HX-Request-Type 的取值，可能为 "partial"、"full" 或空串。
//
// "partial" means only the target region is swapped, while "full" means the target is body or hx-select was used. | "partial" 表示只替换目标区域，"full" 表示目标为 body 或使用了 hx-select。
// The returned value is trimmed and lower-cased; it is empty when the header is missing. | 返回值已去除首尾空白并统一为小写；头缺失时返回空串。
func RequestType(r *http.Request) string {
	return strings.ToLower(strings.TrimSpace(headerValue(r, headerRequestType)))
}

// Target returns HX-Target, the swap target of this request, e.g. "div#results". | Target 返回 HX-Target，即本次请求的交换目标，形如 "div#results"。
func Target(r *http.Request) string { return headerValue(r, headerTarget) }

// Source returns HX-Source, the element that triggered this request, e.g. "button#submit". | Source 返回 HX-Source，即触发本次请求的元素，形如 "button#submit"。
//
// In Htmx 4 this request-side header was renamed from HX-Trigger and carries only the triggering element, no longer the form field name. | Htmx 4 中请求侧的该头由 HX-Trigger 改名而来，仅携带触发元素，不再携带表单字段名。
func Source(r *http.Request) string { return headerValue(r, headerSource) }

// CurrentURL returns HX-Current-URL, the current URL in the browser address bar. | CurrentURL 返回 HX-Current-URL，即浏览器地址栏的当前 URL。
func CurrentURL(r *http.Request) string { return headerValue(r, headerCurrentURL) }

// IsHistoryRestore reports whether this request was triggered by a browser back/forward history restore | IsHistoryRestore 报告本次请求是否由浏览器前进/后退的历史恢复触发
// (HX-History-Restore-Request: true). | （HX-History-Restore-Request: true）。
func IsHistoryRestore(r *http.Request) bool { return headerIsTrue(r, headerHistoryRestore) }

// IsPartial reports whether this request should return only a fragment: IsRequest is true and RequestType is not "full". | IsPartial 报告本次请求是否只应返回片段：IsRequest 为真且 RequestType 不为 "full"。
//
// An ordinary Htmx request (one that carries no HX-Request-Type) is treated as a fragment, while "full" is treated as a whole page. | 普通 Htmx 请求（未携带 HX-Request-Type）按片段处理，"full" 则按整页处理。
func IsPartial(r *http.Request) bool {
	return IsRequest(r) && RequestType(r) != "full"
}
