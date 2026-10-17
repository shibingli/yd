package htmx

import (
	"bytes"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/shibingli/yd/router"
)

// reqWithHeaders builds a test request carrying the given headers. | reqWithHeaders 构造一个带指定请求头的测试请求。
func reqWithHeaders(headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/yd", nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

// errWriter is an io.Writer whose writes always fail, used to verify error propagation. | errWriter 是一个总是写入失败的 io.Writer，用于验证错误传播。
type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

// ---------------------------------------------------------------- Request-side detection | 请求侧判定

func TestIsRequestBoostedAndRequestType(t *testing.T) {
	cases := []struct {
		name        string
		headers     map[string]string
		wantRequest bool
		wantBoosted bool
		wantType    string
	}{
		{name: "无任何 Htmx 头", headers: nil},
		{
			name:        "普通 Htmx 请求",
			headers:     map[string]string{"HX-Request": "true"},
			wantRequest: true,
		},
		{
			name:        "hx-boost 请求",
			headers:     map[string]string{"HX-Request": "true", "HX-Boosted": "true"},
			wantRequest: true,
			wantBoosted: true,
		},
		{
			name:        "局部片段请求",
			headers:     map[string]string{"HX-Request": "true", "HX-Request-Type": "partial"},
			wantRequest: true,
			wantType:    "partial",
		},
		{
			name:        "整页片段请求",
			headers:     map[string]string{"HX-Request": "true", "HX-Request-Type": "full"},
			wantRequest: true,
			wantType:    "full",
		},
		{
			name:    "HX-Request 非 true 不作数",
			headers: map[string]string{"HX-Request": "false", "HX-Boosted": "false"},
		},
		{
			name:        "HX-Request-Type 的首尾空白与大小写被规范化",
			headers:     map[string]string{"HX-Request": "true", "HX-Request-Type": "  FULL  "},
			wantRequest: true,
			wantType:    "full",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := reqWithHeaders(tc.headers)
			if got := IsRequest(r); got != tc.wantRequest {
				t.Errorf("IsRequest = %v, 期望 %v", got, tc.wantRequest)
			}
			if got := IsBoosted(r); got != tc.wantBoosted {
				t.Errorf("IsBoosted = %v, 期望 %v", got, tc.wantBoosted)
			}
			if got := RequestType(r); got != tc.wantType {
				t.Errorf("RequestType = %q, 期望 %q", got, tc.wantType)
			}
		})
	}

	t.Run("nil 请求不 panic", func(t *testing.T) {
		if IsRequest(nil) || IsBoosted(nil) || IsHistoryRestore(nil) || IsPartial(nil) {
			t.Error("a nil request must always report false | nil 请求应一律返回 false")
		}
		if got := RequestType(nil); got != "" {
			t.Errorf("RequestType(nil) = %q, 期望空串", got)
		}
	})
}

func TestTargetSourceAndCurrentURL(t *testing.T) {
	r := reqWithHeaders(map[string]string{
		"HX-Target":      "div#results",
		"HX-Source":      "button#submit",
		"HX-Current-URL": "https://yun-dev.cn/users?page=2",
	})

	if got := Target(r); got != "div#results" {
		t.Errorf("Target = %q, 期望 %q", got, "div#results")
	}
	if got := Source(r); got != "button#submit" {
		t.Errorf("Source = %q, 期望 %q（Htmx 4 的触发元素头）", got, "button#submit")
	}
	if got := CurrentURL(r); got != "https://yun-dev.cn/users?page=2" {
		t.Errorf("CurrentURL = %q", got)
	}

	empty := reqWithHeaders(nil)
	if Target(empty) != "" || Source(empty) != "" || CurrentURL(empty) != "" {
		t.Error("a missing header must yield the empty string | 缺失头时应返回空串")
	}
	if Target(nil) != "" || Source(nil) != "" || CurrentURL(nil) != "" {
		t.Error("a nil request must yield the empty string | nil 请求应返回空串")
	}
}

func TestIsHistoryRestoreAndIsPartial(t *testing.T) {
	cases := []struct {
		name      string
		headers   map[string]string
		wantResto bool
		wantPart  bool
	}{
		{name: "非 Htmx 请求", headers: nil},
		{
			name:     "普通 Htmx 请求视为局部请求",
			headers:  map[string]string{"HX-Request": "true"},
			wantPart: true,
		},
		{
			name:     "显式 partial",
			headers:  map[string]string{"HX-Request": "true", "HX-Request-Type": "partial"},
			wantPart: true,
		},
		{
			name:    "显式 full 不是局部请求",
			headers: map[string]string{"HX-Request": "true", "HX-Request-Type": "full"},
		},
		{
			name:    "只有 Request-Type 而无 HX-Request",
			headers: map[string]string{"HX-Request-Type": "partial"},
		},
		{
			name:      "历史恢复请求",
			headers:   map[string]string{"HX-Request": "true", "HX-History-Restore-Request": "true"},
			wantResto: true,
			wantPart:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := reqWithHeaders(tc.headers)
			if got := IsHistoryRestore(r); got != tc.wantResto {
				t.Errorf("IsHistoryRestore = %v, 期望 %v", got, tc.wantResto)
			}
			if got := IsPartial(r); got != tc.wantPart {
				t.Errorf("IsPartial = %v, 期望 %v", got, tc.wantPart)
			}
		})
	}
}

// ---------------------------------------------------------------- Response headers | 响应头

func TestTriggerSingleAndJSON(t *testing.T) {
	cases := []struct {
		name   string
		events []Event
		want   string
		absent bool
	}{
		{name: "不传事件时不写头", absent: true},
		{
			name:   "单个无负载事件写事件名",
			events: []Event{{Name: "refresh"}},
			want:   "refresh",
		},
		{
			name:   "单个带负载事件写 JSON",
			events: []Event{{Name: "refresh", Detail: map[string]any{"id": 1}}},
			want:   `{"refresh":{"id":1}}`,
		},
		{
			name:   "单个事件负载为标量",
			events: []Event{{Name: "count", Detail: 3}},
			want:   `{"count":3}`,
		},
		{
			name:   "多个无负载事件写 null",
			events: []Event{{Name: "a"}, {Name: "b"}},
			want:   `{"a":null,"b":null}`,
		},
		{
			name:   "多个事件保持传入顺序",
			events: []Event{{Name: "b", Detail: 2}, {Name: "a", Detail: "x"}},
			want:   `{"b":2,"a":"x"}`,
		},
		{
			name:   "同名事件互不覆盖",
			events: []Event{{Name: "evt", Detail: 1}, {Name: "evt", Detail: 2}},
			want:   `{"evt":1,"evt":2}`,
		},
		{
			name:   "不可编码的负载退化为 null",
			events: []Event{{Name: "evt", Detail: func() {}}},
			want:   `{"evt":null}`,
		},
		{name: "空事件名被忽略", events: []Event{{Name: ""}}, absent: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			Trigger(rec, tc.events...)

			vals := rec.Header().Values("HX-Trigger")
			if tc.absent {
				if len(vals) != 0 {
					t.Fatalf("不应写出 HX-Trigger, 实际 %q", vals)
				}
				return
			}
			if len(vals) != 1 {
				t.Fatalf("HX-Trigger 值个数 = %d, 期望 1 (%q)", len(vals), vals)
			}
			if vals[0] != tc.want {
				t.Fatalf("HX-Trigger = %q, 期望 %q", vals[0], tc.want)
			}
		})
	}
}

func TestPushReplaceRedirectRefreshRetarget(t *testing.T) {
	rec := httptest.NewRecorder()
	PushURL(rec, "/users?page=2")
	ReplaceURL(rec, "/users?page=1")
	Redirect(rec, "/login")
	Refresh(rec)
	Retarget(rec, "#flash")

	cases := []struct {
		header string
		want   string
	}{
		{header: "HX-Push-Url", want: "/users?page=2"},
		{header: "HX-Replace-Url", want: "/users?page=1"},
		{header: "HX-Redirect", want: "/login"},
		{header: "HX-Refresh", want: "true"},
		{header: "HX-Retarget", want: "#flash"},
	}
	for _, tc := range cases {
		if got := rec.Header().Get(tc.header); got != tc.want {
			t.Errorf("%s = %q, 期望 %q", tc.header, got, tc.want)
		}
	}
}

func TestRetargetReswapReselect(t *testing.T) {
	rec := httptest.NewRecorder()
	Retarget(rec, "closest .card")
	Reswap(rec, "beforeend")
	Reselect(rec, "#results > li")

	cases := []struct {
		header string
		want   string
	}{
		{header: "HX-Retarget", want: "closest .card"},
		{header: "HX-Reswap", want: "beforeend"},
		{header: "HX-Reselect", want: "#results > li"},
	}
	for _, tc := range cases {
		if got := rec.Header().Get(tc.header); got != tc.want {
			t.Errorf("%s = %q, 期望 %q", tc.header, got, tc.want)
		}
	}
}

func TestLocationHeaderBarePathAndJSON(t *testing.T) {
	cases := []struct {
		name   string
		loc    Location
		want   string
		absent bool
	}{
		{name: "零值不写头", absent: true},
		{
			name: "仅 Path 写裸路径",
			loc:  Location{Path: "/users/42"},
			want: "/users/42",
		},
		{
			name: "Path + Target + Swap 写 JSON",
			loc:  Location{Path: "/users/42", Target: "#main", Swap: "outerMorph"},
			want: `{"path":"/users/42","target":"#main","swap":"outerMorph"}`,
		},
		{
			name: "全部字段写 JSON",
			loc: Location{
				Path:    "/users/42",
				Target:  "#main",
				Swap:    "outerMorph",
				Select:  "#content",
				Push:    true,
				Replace: false,
				Values:  map[string]any{"page": 2},
				Headers: map[string]any{"X-YD-From": "htmx"},
			},
			want: `{"path":"/users/42","target":"#main","swap":"outerMorph","select":"#content",` +
				`"push":true,"replace":false,"values":{"page":2},"headers":{"X-YD-From":"htmx"}}`,
		},
		{
			name: "无 Path 但有 Target 也写 JSON",
			loc:  Location{Target: "#main"},
			want: `{"target":"#main"}`,
		},
		{
			name: "不可编码的 Values 退化为裸路径",
			loc:  Location{Path: "/fallback", Values: func() {}},
			want: "/fallback",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			LocationHeader(rec, tc.loc)

			vals := rec.Header().Values("HX-Location")
			if tc.absent {
				if len(vals) != 0 {
					t.Fatalf("不应写出 HX-Location, 实际 %q", vals)
				}
				return
			}
			if len(vals) != 1 {
				t.Fatalf("HX-Location 值个数 = %d, 期望 1 (%q)", len(vals), vals)
			}
			if vals[0] != tc.want {
				t.Fatalf("HX-Location = %q, 期望 %q", vals[0], tc.want)
			}
		})
	}
}

func TestErrorWritesStatusAndFragment(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := Error(rec, http.StatusUnprocessableEntity, `<script>alert("x")</script>`); err != nil {
		t.Fatalf("Error 返回错误: %v", err)
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("状态码 = %d, 期望 422", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	want := `<div class="yd-form-error" role="alert">&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;</div>`
	if got := rec.Body.String(); got != want {
		t.Errorf("错误片段 = %q, 期望 %q", got, want)
	}

	bad := httptest.NewRecorder()
	if err := Error(bad, 42, "非法状态码"); err == nil {
		t.Fatal("an invalid status code must return an error | 非法状态码应返回错误")
	}
	if bad.Body.Len() != 0 || bad.Code != http.StatusOK {
		t.Errorf("非法状态码不应写出任何内容, code=%d body=%q", bad.Code, bad.Body.String())
	}
}

func TestTriggerFromCtxAndFromHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	c := &router.Ctx{Writer: rec, Request: httptest.NewRequest(http.MethodPost, "/yd/users", nil)}
	TriggerFromCtx(c, Event{Name: "refresh"})
	if got := rec.Header().Get("HX-Trigger"); got != "refresh" {
		t.Errorf("TriggerFromCtx 单个事件 = %q, 期望 %q", got, "refresh")
	}

	rec2 := httptest.NewRecorder()
	c2 := &router.Ctx{Writer: rec2, Request: httptest.NewRequest(http.MethodPost, "/yd/users", nil)}
	TriggerFromCtx(c2, Event{Name: "table", Detail: map[string]any{"page": 2}})
	if got := rec2.Header().Get("HX-Trigger"); got != `{"table":{"page":2}}` {
		t.Errorf("TriggerFromCtx JSON = %q, 期望 %q", got, `{"table":{"page":2}}`)
	}

	TriggerFromCtx(nil, Event{Name: "refresh"}) // must not panic | 不应 panic

	rec3 := httptest.NewRecorder()
	TriggerFromHeader(rec3, "refresh")
	if got := rec3.Header().Get("HX-Trigger"); got != "refresh" {
		t.Errorf("TriggerFromHeader = %q, 期望 %q", got, "refresh")
	}
}

// ---------------------------------------------------------------- Rendering helpers | 渲染辅助

func TestPartialsRenderExactMarkup(t *testing.T) {
	var buf bytes.Buffer
	parts := []Partial{
		{Target: "#messages", Swap: "beforeend", Content: template.HTML("<div>New message</div>")},
		{Target: "#notifications", Swap: "innerHTML", Content: template.HTML(`<span class="badge">5</span>`)},
		{Target: "#only-target"},
		{Swap: "outerHTML", Content: template.HTML("plain")},
		{Content: template.HTML("<hr>")},
		{Target: "#skeleton", Swap: "none", Content: template.HTML("<div class=\"yd-skeleton\"></div>\n")},
	}
	if err := RenderPartials(&buf, parts...); err != nil {
		t.Fatalf("RenderPartials 返回错误: %v", err)
	}

	want := "" +
		`<hx-partial hx-target="#messages" hx-swap="beforeend"><div>New message</div></hx-partial>` + "\n" +
		`<hx-partial hx-target="#notifications" hx-swap="innerHTML"><span class="badge">5</span></hx-partial>` + "\n" +
		`<hx-partial hx-target="#only-target"></hx-partial>` + "\n" +
		`<hx-partial hx-swap="outerHTML">plain</hx-partial>` + "\n" +
		`<hx-partial><hr></hx-partial>` + "\n" +
		`<hx-partial hx-target="#skeleton" hx-swap="none"><div class="yd-skeleton"></div>` + "\n" +
		`</hx-partial>` + "\n"

	if got := buf.String(); got != want {
		t.Fatalf("输出不一致\n got: %q\nwant: %q", got, want)
	}

	t.Run("无部分时不写出任何内容", func(t *testing.T) {
		var empty bytes.Buffer
		if err := RenderPartials(&empty); err != nil {
			t.Fatalf("RenderPartials 返回错误: %v", err)
		}
		if empty.Len() != 0 {
			t.Fatalf("输出应为空, 实际 %q", empty.String())
		}
	})

	t.Run("属性值被 HTML 转义", func(t *testing.T) {
		var esc bytes.Buffer
		if err := RenderPartials(&esc, Partial{Target: `#a[data-x="y"]`, Swap: "beforeend"}); err != nil {
			t.Fatalf("RenderPartials 返回错误: %v", err)
		}
		wantEsc := `<hx-partial hx-target="#a[data-x=&#34;y&#34;]" hx-swap="beforeend"></hx-partial>` + "\n"
		if got := esc.String(); got != wantEsc {
			t.Fatalf("输出 = %q, 期望 %q", got, wantEsc)
		}
	})

	t.Run("写入失败时返回包装后的错误", func(t *testing.T) {
		wantErr := errors.New("boom")
		err := RenderPartials(errWriter{err: wantErr}, Partial{Target: "#x", Content: template.HTML("hi")})
		if !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, 期望包装 %v", err, wantErr)
		}
	})
}

func TestOOBExactMarkup(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		swap    string
		content template.HTML
		want    string
	}{
		{
			name:    "swap 与 target 都给出",
			target:  "#contacts-table",
			swap:    "beforeend",
			content: template.HTML("<tr><td>x</td></tr>"),
			want:    `<div hx-swap-oob="beforeend:#contacts-table"><tr><td>x</td></tr></div>`,
		},
		{
			name:    "省略 swap",
			target:  "#x",
			content: template.HTML("hi"),
			want:    `<div hx-swap-oob="#x">hi</div>`,
		},
		{
			name:    "省略 target",
			swap:    "innerHTML",
			content: template.HTML("hi"),
			want:    `<div hx-swap-oob="innerHTML">hi</div>`,
		},
		{
			name: "两者都省略时按 id 匹配",
			want: `<div hx-swap-oob="true"></div>`,
		},
		{
			name:    "内容原样输出（不转义）",
			target:  "#x",
			swap:    "innerHTML",
			content: template.HTML(`<b a="1">1 & 2</b>`),
			want:    `<div hx-swap-oob="innerHTML:#x"><b a="1">1 & 2</b></div>`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(OOB(tc.target, tc.swap, tc.content)); got != tc.want {
				t.Fatalf("OOB = %q, 期望 %q", got, tc.want)
			}
		})
	}
}

func TestErrorFragmentEscapesHTML(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want string
	}{
		{
			name: "脚本标签被转义",
			msg:  `<script>alert("x")</script>`,
			want: `<div class="yd-form-error" role="alert">&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;</div>`,
		},
		{
			name: "与号和尖括号被转义",
			msg:  `a & b <c>`,
			want: `<div class="yd-form-error" role="alert">a &amp; b &lt;c&gt;</div>`,
		},
		{
			name: "中文原样保留",
			msg:  "邮箱格式不正确",
			want: `<div class="yd-form-error" role="alert">邮箱格式不正确</div>`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(ErrorFragment(tc.msg))
			if got != tc.want {
				t.Fatalf("ErrorFragment = %q, 期望 %q", got, tc.want)
			}
			if strings.Contains(got, "<script>") {
				t.Fatalf("错误片段不得包含未转义的 <script>: %q", got)
			}
			if !strings.Contains(got, `role="alert"`) {
				t.Fatalf("错误片段应带 role=alert 便于无障碍读取: %q", got)
			}
		})
	}
}

// ---------------------------------------------------------------- Naming guard | 命名守卫

// TestNoHtmx2OnlyNamesAppear asserts that no Htmx 2 only identifier appears anywhere in the package source, | TestNoHtmx2OnlyNamesAppear 断言整个包的源码中不出现 Htmx 2 专有标识，
// guarding later changes against misusing the old naming (Htmx 4 has renamed or removed these identifiers). | 防止后续改动误用旧版本命名（Htmx 4 已改名或移除这些标识）。
func TestNoHtmx2OnlyNamesAppear(t *testing.T) {
	// Split concatenation: keeps this test file itself from matching the strings being scanned. | 分片拼接：避免本测试文件自身命中被扫描的字符串。
	forbidden := []string{
		"hx-disabled" + "-elt",
		"hx" + "-vars",
		"HX-Trigger" + "-Name",
		"HX-Trigger-After" + "-Swap",
		"HX-Trigger-After" + "-Settle",
		"hx-prompt" + "=",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败: %v", err)
	}

	scanned := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		src, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", entry.Name(), err)
		}
		scanned++
		for _, bad := range forbidden {
			if bytes.Contains(src, []byte(bad)) {
				t.Errorf("%s 含 Htmx 2 专有标识 %q，请改用 Htmx 4 命名", entry.Name(), bad)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("no Go source file was scanned, the naming guard is not effective | 未扫描到任何 Go 源文件，命名守卫失效")
	}
}
