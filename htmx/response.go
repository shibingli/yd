package htmx

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/yun-dev/yd/jsonx"
)

// Htmx 4 响应头名称。
const (
	headerTrigger     = "HX-Trigger"
	headerPushURL     = "HX-Push-Url"
	headerReplaceURL  = "HX-Replace-Url"
	headerRedirect    = "HX-Redirect"
	headerLocation    = "HX-Location"
	headerRefresh     = "HX-Refresh"
	headerRetarget    = "HX-Retarget"
	headerReswap      = "HX-Reswap"
	headerReselect    = "HX-Reselect"
	headerContentType = "Content-Type"
	contentTypeHTML   = "text/html; charset=utf-8"
)

// Event 描述一个回传给浏览器的客户端事件。
//
// 配合模板中的 hx-trigger="<事件名> from:body" 使用，可让任意区域在事件到达时自行刷新，
// 从而避免服务端拼装大段 HTML。
type Event struct {
	// Name 是事件名，不可为空；为空的事件会被忽略。
	Name string
	// Detail 是随事件下发的负载，可为 nil（JSON 形式下写为 null）。
	Detail any
}

// triggerHeaderValue 计算 HX-Trigger 的取值；第二个返回值表示是否需要写出该头。
//
// 仅有一个不含负载的事件时取事件名本身，其余情况取 JSON 对象。JSON 按键的出现顺序
// 拼装（同名事件不会互相覆盖），键名与值均经 jsonx.Marshal 编码；无法编码的负载退化为 null。
func triggerHeaderValue(events []Event) (string, bool) {
	var kept []Event
	for _, ev := range events {
		if ev.Name != "" {
			kept = append(kept, ev)
		}
	}
	if len(kept) == 0 {
		return "", false
	}
	if len(kept) == 1 && kept[0].Detail == nil {
		return kept[0].Name, true
	}

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, ev := range kept {
		if i > 0 {
			buf.WriteByte(',')
		}
		// string 的 jsonx.Marshal 不会失败（非法 UTF-8 也会被替换而不是报错）。
		name, _ := jsonx.Marshal(ev.Name)
		buf.Write(name)
		buf.WriteByte(':')
		detail, err := jsonx.Marshal(ev.Detail)
		if err != nil {
			detail = []byte("null")
		}
		buf.Write(detail)
	}
	buf.WriteByte('}')
	return buf.String(), true
}

// Trigger 写入 HX-Trigger 响应头，触发浏览器端的自定义事件。
//
// 仅有一个不含 Detail 的事件时，头值即事件名（如 "refresh"）；其余情况写出 JSON 对象
// （如 {"refresh":{"id":1}}），Detail 为 nil 的事件其值为 null。
// 输出顺序与传入顺序一致，事件名为空的项被忽略；不传事件时不写任何头。
func Trigger(w http.ResponseWriter, events ...Event) {
	if v, ok := triggerHeaderValue(events); ok {
		w.Header().Set(headerTrigger, v)
	}
}

// TriggerFromHeader 写入 HX-Trigger 响应头触发单个不含负载的事件，
// 等价于 Trigger(w, Event{Name: name})。
func TriggerFromHeader(w http.ResponseWriter, name string) {
	Trigger(w, Event{Name: name})
}

// PushURL 写入 HX-Push-Url，把 url 压入浏览器历史（局部跳转，不刷新整页）。
func PushURL(w http.ResponseWriter, url string) { w.Header().Set(headerPushURL, url) }

// ReplaceURL 写入 HX-Replace-Url，替换浏览器历史栈顶的 URL（不新增历史记录）。
func ReplaceURL(w http.ResponseWriter, url string) { w.Header().Set(headerReplaceURL, url) }

// Redirect 写入 HX-Redirect，让浏览器整页跳转到 url。
//
// 需要「不刷新整页」的 AJAX 跳转时请改用 LocationHeader（HX-Location）。
func Redirect(w http.ResponseWriter, url string) { w.Header().Set(headerRedirect, url) }

// Location 描述 HX-Location 的内容，即一次不刷新整页的 AJAX 跳转。
type Location struct {
	// Path 是目标 URL。
	Path string `json:"path,omitempty"`
	// Target 覆盖本次跳转的交换目标（CSS 选择器）。
	Target string `json:"target,omitempty"`
	// Swap 覆盖本次跳转的交换策略（如 beforeend、outerMorph）。
	Swap string `json:"swap,omitempty"`
	// Select 覆盖 hx-select，从响应中挑选要交换的内容。
	Select string `json:"select,omitempty"`
	// Push 是否把 URL 压入历史，可以是 bool 或 "true"/"false" 字符串。
	Push any `json:"push,omitempty"`
	// Replace 是否替换历史栈顶 URL，取值同 Push。
	Replace any `json:"replace,omitempty"`
	// Values 是随跳转携带的额外值。
	Values any `json:"values,omitempty"`
	// Headers 是随跳转携带的额外请求头。
	Headers any `json:"headers,omitempty"`
}

// isZero 报告 l 的所有字段均为零值。注意不能用 == 比较整个结构体：
// 接口字段装载 map 等不可比较类型时，结构体比较会 panic。
func (l Location) isZero() bool { return l.Path == "" && !l.hasExtras() }

// hasExtras 报告除 Path 之外是否还有字段被设置。
func (l Location) hasExtras() bool {
	return l.Target != "" || l.Swap != "" || l.Select != "" ||
		l.Push != nil || l.Replace != nil || l.Values != nil || l.Headers != nil
}

// LocationHeader 写入 HX-Location 响应头。
//
// 只设置了 Path 时写入裸路径（最常用的形式）；设置了其它字段时写入 JSON 对象
// （如 {"path":"/users/42","target":"#main","swap":"outerMorph"}）；loc 为零值时什么都不写。
// JSON 序列化失败时（如 Values 含不可编码的值）退化为写入裸 Path，Path 也为空则不写。
func LocationHeader(w http.ResponseWriter, loc Location) {
	if loc.isZero() {
		return
	}
	if !loc.hasExtras() {
		w.Header().Set(headerLocation, loc.Path)
		return
	}
	buf, err := jsonx.Marshal(loc)
	if err != nil {
		if loc.Path != "" {
			w.Header().Set(headerLocation, loc.Path)
		}
		return
	}
	w.Header().Set(headerLocation, string(buf))
}

// Refresh 写入 HX-Refresh: true，让浏览器整页刷新。
func Refresh(w http.ResponseWriter) { w.Header().Set(headerRefresh, "true") }

// Retarget 写入 HX-Retarget，用 CSS 选择器覆盖本次响应的交换目标。
func Retarget(w http.ResponseWriter, selector string) { w.Header().Set(headerRetarget, selector) }

// Reswap 写入 HX-Reswap，覆盖本次响应的交换策略（如 beforeend、innerMorph）。
func Reswap(w http.ResponseWriter, swap string) { w.Header().Set(headerReswap, swap) }

// Reselect 写入 HX-Reselect，覆盖 hx-select，从响应中挑选要交换的内容。
func Reselect(w http.ResponseWriter, selector string) { w.Header().Set(headerReselect, selector) }

// Error 以指定状态码写出错误片段（text/html），供前端用 hx-status:4xx/5xx 声明式接管。
//
// Htmx 4 默认对所有状态码执行交换（204/304 除外），因此校验失败时直接返回 422 与错误片段
// 即可，无需额外的响应头。msg 会被 HTML 转义；status 必须是合法的 HTTP 状态码，
// 否则返回错误且不写出任何内容。
func Error(w http.ResponseWriter, status int, msg string) error {
	if status < 100 || status > 999 {
		return fmt.Errorf("htmx: 非法 HTTP 状态码 %d", status)
	}
	w.Header().Set(headerContentType, contentTypeHTML)
	w.WriteHeader(status)
	if _, err := io.WriteString(w, string(ErrorFragment(msg))); err != nil {
		return fmt.Errorf("htmx: 写出错误片段失败: %w", err)
	}
	return nil
}
