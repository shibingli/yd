package render_test

// Differential compatibility tests: every case is rendered twice, once by
// html/template and once by the compiled engine, and the bytes must match.
// | 差分兼容测试：每个用例分别由 html/template 与编译引擎渲染，输出必须逐字节一致。

import (
	"bytes"
	"html/template"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/http/render"
)

// compatCase is one differential template case. | compatCase 是一个差分模板用例。
type compatCase struct {
	name   string
	text   string
	data   any
	files  map[string]string
	delims [2]string
}

// compatFuncs returns the function map shared by both engines. | compatFuncs 返回两个引擎共用的函数表。
func compatFuncs(fsys afero.Fs) template.FuncMap {
	m := render.Funcs(fsys)
	m["shout"] = strings.ToUpper
	m["add"] = func(a, b int) int { return a + b }
	m["fail"] = func() (string, error) { return "", errCompat }
	m["greet"] = func(name string) string { return "hi " + name }
	m["typed"] = func(v any) string { return "typed" }
	return m
}

// errCompat is returned by the fail helper. | errCompat 是 fail 辅助函数返回的错误。
var errCompat = &compatError{}

type compatError struct{}

func (*compatError) Error() string { return "compat failure | 兼容性失败" }

// compatStruct drives the typed field path, which map data does not exercise.
// | compatStruct 驱动类型化字段路径；映射数据不会经过该路径。
type compatStruct struct {
	X string
}

// renderBoth renders one case with both engines. | renderBoth 用两个引擎渲染同一个用例。
func renderBoth(t *testing.T, c compatCase) (std string, mine string, stdErr, myErr error) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	files := map[string]string{"templates/t.html": c.text}
	for name, body := range c.files {
		files["templates/"+name] = body
	}
	for name, body := range files {
		if err := afero.WriteFile(fsys, name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	funcs := compatFuncs(fsys)

	stdTmpl := template.New("root").Funcs(funcs)
	if c.delims[0] != "" || c.delims[1] != "" {
		stdTmpl = stdTmpl.Delims(c.delims[0], c.delims[1])
	}
	for name, body := range files {
		name = strings.TrimPrefix(name, "templates/")
		if _, err := stdTmpl.New(name).Parse(body); err != nil {
			t.Fatalf("std parse %s: %v", name, err)
		}
	}
	var stdBuf bytes.Buffer
	stdErr = stdTmpl.ExecuteTemplate(&stdBuf, "t.html", c.data)

	engine, err := render.New(fsys, render.Options{
		Root:       "templates",
		LeftDelim:  c.delims[0],
		RightDelim: c.delims[1],
		Funcs: template.FuncMap{
			"shout": strings.ToUpper,
			"add":   func(a, b int) int { return a + b },
			"fail":  func() (string, error) { return "", errCompat },
			"greet": func(name string) string { return "hi " + name },
			"typed": func(v any) string { return "typed" },
		}})
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	var myBuf bytes.Buffer
	myErr = engine.Render(&myBuf, "t.html", c.data)
	return stdBuf.String(), myBuf.String(), stdErr, myErr
}

// TestDifferentialEscaping covers every contextual escaper. | TestDifferentialEscaping 覆盖全部上下文转义器。
func TestDifferentialEscaping(t *testing.T) {
	cases := []compatCase{
		{name: "html text", text: `<p>{{ .X }}</p>`, data: map[string]any{"X": `<script>"a" & 'b' + = \x60`}},
		{name: "quoted attr", text: `<p title="{{ .X }}">x</p>`, data: map[string]any{"X": `" onmouseover="alert(1)`}},
		{name: "unquoted attr", text: `<p class={{ .X }}>x</p>`, data: map[string]any{"X": `a b"c'd>e`}},
		{name: "unquoted attr noncharacter", text: `<p class={{ .X }}>x</p>`, data: map[string]any{"X": "a﷐b"}},
		{name: "unquoted attr private range", text: `<p class={{ .X }}>x</p>`, data: map[string]any{"X": "a￿b"}},
		{name: "unquoted attr invalid utf8", text: `<p class={{ .X }}>x</p>`, data: map[string]any{"X": "a\xffb"}},
		{name: "unquoted attr empty", text: `<p class={{ .X }}>x</p>`, data: map[string]any{"X": ""}},
		{name: "unquoted attr struct empty", text: `<p class={{ .X }}>x</p>`, data: compatStruct{}},
		{name: "unquoted attr struct noncharacter", text: `<p class={{ .X }}>x</p>`, data: compatStruct{X: "a﷐b"}},
		{name: "rcdata", text: `<textarea>{{ .X }}</textarea>`, data: map[string]any{"X": `</textarea><script>x</script>`}},
		{name: "href", text: `<a href="{{ .X }}">x</a>`, data: map[string]any{"X": `javascript:alert(1)`}},
		{name: "href safe", text: `<a href="{{ .X }}">x</a>`, data: map[string]any{"X": `https://example.com/?a=1&b=2`}},
		{name: "href typed", text: `<a href="{{ .X }}">x</a>`, data: map[string]any{"X": template.URL(`javascript:ok()`)}},
		{name: "srcset", text: `<img srcset="{{ .X }}">`, data: map[string]any{"X": `a.png 1x, b.png 2x`}},
		{name: "script value", text: `<script>var x = {{ .X }};</script>`, data: map[string]any{"X": map[string]any{"s": "</script><b>"}}},
		{name: "script string", text: `<script>var x = "{{ .X }}";</script>`, data: map[string]any{"X": `</script>"'`}},
		{name: "inline handler", text: `<button onclick="go('{{ .X }}')">x</button>`, data: map[string]any{"X": `');alert(1);//`}},
		{name: "style value", text: `<div style="color:{{ .X }}">x</div>`, data: map[string]any{"X": `red;background:url(javascript:x)`}},
		{name: "css block", text: `<style>.a{color:{{ .X }}}</style>`, data: map[string]any{"X": `red</style><script>x</script>`}},
		{name: "comment", text: `<!-- {{ .X }} -->`, data: map[string]any{"X": `--><script>x</script>`}},
		{name: "typed html", text: `<div>{{ .X }}</div>`, data: map[string]any{"X": template.HTML(`<b>trusted</b>`)}},
		{name: "typed js", text: `<script>var x = {{ .X }};</script>`, data: map[string]any{"X": template.JS(`{"a":1}`)}},
		{name: "nil", text: `<i>{{ .X }}</i>`, data: map[string]any{"X": nil}},
		{name: "missing", text: `<i>{{ .Missing }}</i>`, data: map[string]any{}},
		{name: "int", text: `<i>{{ .X }}</i>`, data: map[string]any{"X": 42}},
		{name: "float", text: `<i>{{ .X }}</i>`, data: map[string]any{"X": 1.25}},
		{name: "bool", text: `<i>{{ .X }}</i>`, data: map[string]any{"X": true}},
		{name: "pointer", text: `<i>{{ .X }}</i>`, data: map[string]any{"X": func() *string { s := "p"; return &s }()}},
		{name: "nil pointer", text: `<i>{{ .X }}</i>`, data: map[string]any{"X": (*string)(nil)}},
		{name: "named stringer", text: `<i>{{ .X }}</i>`, data: map[string]any{"X": namedString("x")}},
		{name: "named int stringer", text: `<i>{{ .X }}</i>`, data: map[string]any{"X": namedInt(7)}},
		{name: "error value", text: `<i>{{ .X }}</i>`, data: map[string]any{"X": errCompat}},
		{name: "html attr", text: `<input {{ .X }}>`, data: map[string]any{"X": template.HTMLAttr(`disabled`)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			std, mine, stdErr, myErr := renderBoth(t, c)
			if (stdErr != nil) != (myErr != nil) {
				t.Fatalf("error mismatch: std=%v mine=%v", stdErr, myErr)
			}
			if std != mine {
				t.Fatalf("output mismatch:\nstd:  %q\nmine: %q", std, mine)
			}
		})
	}
}

// TestDifferentialControlFlow covers the template language itself. | TestDifferentialControlFlow 覆盖模板语言本身。
func TestDifferentialControlFlow(t *testing.T) {
	type item struct {
		Name  string
		Count int
		Tags  []string
	}
	cases := []compatCase{
		{name: "if else", text: `{{ if .A }}A{{ else if .B }}B{{ else }}C{{ end }}`, data: map[string]any{"A": false, "B": true}},
		{name: "with dot", text: `{{ with .U }}<b>{{ .Name }}</b>{{ else }}none{{ end }}`, data: map[string]any{"U": map[string]any{"Name": "u"}}},
		{name: "with empty", text: `{{ with .U }}<b>{{ .Name }}</b>{{ else }}none{{ end }}`, data: map[string]any{"U": nil}},
		{name: "range slice", text: `{{ range .S }}[{{ . }}]{{ else }}empty{{ end }}`, data: map[string]any{"S": []string{"a", "b"}}},
		{name: "range empty", text: `{{ range .S }}[{{ . }}]{{ else }}empty{{ end }}`, data: map[string]any{"S": []string{}}},
		{name: "range two vars", text: `{{ range $i, $v := .S }}{{ $i }}={{ $v }};{{ end }}`, data: map[string]any{"S": []string{"a", "b"}}},
		{name: "range one var", text: `{{ range $v := .S }}{{ $v }};{{ end }}`, data: map[string]any{"S": []string{"a", "b"}}},
		{name: "range map", text: `{{ range $k, $v := .M }}{{ $k }}={{ $v }};{{ end }}`, data: map[string]any{"M": map[string]int{"b": 2, "a": 1, "c": 3}}},
		{name: "range int map", text: `{{ range $k, $v := .M }}{{ $k }}={{ $v }};{{ end }}`, data: map[string]any{"M": map[int]string{3: "c", 1: "a", 2: "b"}}},
		{name: "range bool map", text: `{{ range $k, $v := .M }}{{ $k }}={{ $v }};{{ end }}`, data: map[string]any{"M": map[bool]int{false: 0, true: 1}}},
		{name: "range int", text: `{{ range $i := .N }}{{ $i }}{{ end }}`, data: map[string]any{"N": 4}},
		{name: "range assignment", text: `{{ $i := -1 }}{{ $v := "" }}{{ range $i, $v = .S }}{{ $i }}={{ $v }};{{ end }}{{ $i }}`, data: map[string]any{"S": []string{"a", "b"}}},
		{name: "range struct", text: `{{ range .S }}{{ .Name }}:{{ .Count }}:{{ range .Tags }}{{ . }},{{ end }};{{ end }}`, data: map[string]any{"S": []item{{Name: "a", Count: 1, Tags: []string{"x"}}, {Name: "b"}}}},
		{name: "nested range break", text: `{{ range .S }}{{ range $i, $v := . }}{{ if eq $i 1 }}{{ break }}{{ end }}{{ $v }}{{ end }};{{ end }}`, data: map[string]any{"S": [][]string{{"a", "b", "c"}, {"d"}}}},
		{name: "nested range continue", text: `{{ range .S }}{{ range $i, $v := . }}{{ if eq $i 1 }}{{ continue }}{{ end }}{{ $v }}{{ end }};{{ end }}`, data: map[string]any{"S": [][]string{{"a", "b", "c"}, {"d"}}}},
		{name: "variables", text: `{{ $x := "a" }}{{ $x }}{{ $x = "b" }}{{ $x }}`, data: nil},
		{name: "variable dot", text: `{{ $root := . }}{{ with .U }}{{ $root.Top }}-{{ .Name }}{{ end }}`, data: map[string]any{"Top": "t", "U": map[string]any{"Name": "u"}}},
		{name: "paren pipeline", text: `{{ (printf "%s-%d" .A .B) | upper }}`, data: map[string]any{"A": "x", "B": 2}},
		{name: "pipeline args", text: `{{ .A | printf "%s!" | upper }}`, data: map[string]any{"A": "go"}},
		{name: "and short circuit", text: `{{ and .A (index .S 9) }}`, data: map[string]any{"A": false, "S": []string{}}},
		{name: "or short circuit", text: `{{ or .A "fallback" }}`, data: map[string]any{"A": ""}},
		{name: "not", text: `{{ not .A }}`, data: map[string]any{"A": true}},
		{name: "len", text: `{{ len .S }}-{{ len .M }}`, data: map[string]any{"S": []int{1, 2}, "M": map[string]int{"a": 1}}},
		{name: "index nested", text: `{{ index .M "a" 1 }}`, data: map[string]any{"M": map[string][]string{"a": {"x", "y"}}}},
		{name: "slice", text: `{{ slice .S 1 3 }}`, data: map[string]any{"S": []int{0, 1, 2, 3}}},
		{name: "slice string", text: `{{ slice .S 1 3 }}`, data: map[string]any{"S": "abcdef"}},
		{name: "index string", text: `{{ index .S 1 }}`, data: map[string]any{"S": "abc"}},
		{name: "index map interface key", text: `{{ index .M "a" }}`, data: map[string]any{"M": map[any]any{"a": 1}}},
		{name: "print", text: `{{ print .A .B }}`, data: map[string]any{"A": "x", "B": 1}},
		{name: "printf", text: `{{ printf "%s:%03d" .A .B }}`, data: map[string]any{"A": "x", "B": 7}},
		{name: "println", text: `{{ println .A }}`, data: map[string]any{"A": "x"}},
		{name: "comparisons", text: `{{ eq 1 1 }}{{ ne 1 2 }}{{ lt 1 2 }}{{ le 2 2 }}{{ gt 3 2 }}{{ ge 2 2 }}`, data: nil},
		{name: "comparison signed unsigned", text: `{{ eq 1 .U }}{{ lt 1 .U }}{{ gt .U 1 }}`, data: map[string]any{"U": uint(2)}},
		{name: "call variadic", text: `{{ call .F 1 2 3 }}`, data: map[string]any{"F": func(nums ...int) int {
			n := 0
			for _, v := range nums {
				n += v
			}
			return n
		}}},
		{name: "with decl", text: `{{ with $u := .U }}{{ $u.Name }}{{ end }}`, data: map[string]any{"U": map[string]any{"Name": "u"}}},
		{name: "comparison mix", text: `{{ eq 1 2 1 }}{{ eq "a" "a" }}`, data: nil},
		{name: "call", text: `{{ call .F "x" }}`, data: map[string]any{"F": func(s string) string { return "f:" + s }}},
		{name: "builtin html", text: `{{ html .X }}`, data: map[string]any{"X": `<b>&`}},
		{name: "builtin js", text: `{{ js .X }}`, data: map[string]any{"X": `a"b`}},
		{name: "builtin urlquery", text: `{{ urlquery .X }}`, data: map[string]any{"X": `a b&c`}},
		{name: "custom funcs", text: `{{ shout .A }}|{{ add .B 2 }}|{{ greet .A }}|{{ typed .A }}`, data: map[string]any{"A": "go", "B": 3}},
		{name: "struct fields", text: `{{ .U.Name }}:{{ .U.Count }}`, data: map[string]any{"U": item{Name: "n", Count: 4}}},
		{name: "struct pointer", text: `{{ .U.Name }}`, data: map[string]any{"U": &item{Name: "p"}}},
		{name: "method", text: `{{ .U.Greet "!" }}`, data: map[string]any{"U": greetStruct{Name: "g"}}},
		{name: "missing chain", text: `{{ .Missing.Deep }}`, data: map[string]any{}},
		{name: "trim markers", text: "a  {{- .X -}}  b", data: map[string]any{"X": "x"}},
		{name: "custom delims", text: `a[[ .X ]]b`, data: map[string]any{"X": "<x>"}, delims: [2]string{"[[", "]]"}},
		{name: "comments", text: `a{{/* hidden */}}b`, data: nil},
		{name: "dict list default", text: `{{ $d := dict "A" 1 "B" 2 }}{{ $d.A }}-{{ $d.B }}-{{ list 1 2 3 }}-{{ default "d" "" }}`, data: nil},
		{name: "json in script", text: `<script>let x = {{ json .X }};</script>`, data: map[string]any{"X": map[string]any{"a": "</script>"}}},
		{name: "human size time", text: `{{ humanSize 2048 }}|{{ humanTime .T }}`, data: map[string]any{"T": time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			std, mine, stdErr, myErr := renderBoth(t, c)
			if (stdErr != nil) != (myErr != nil) {
				t.Fatalf("error mismatch: std=%v mine=%v", stdErr, myErr)
			}
			if stdErr != nil {
				return
			}
			if std != mine {
				t.Fatalf("output mismatch:\nstd:  %q\nmine: %q", std, mine)
			}
		})
	}
}

// namedString and namedInt carry String methods, so the engine must format them
// through fmt rather than their underlying primitive. | namedString 与 namedInt 带
// String 方法，引擎必须经 fmt 而不是底层基础类型格式化它们。
type namedString string

// String renders the named string with a prefix. | String 带前缀渲染具名字符串。
func (n namedString) String() string { return "N:" + string(n) }

type namedInt int

// String renders the named integer with a prefix. | String 带前缀渲染具名整数。
func (n namedInt) String() string { return "I:" + strconv.Itoa(int(n)) }

// greetStruct exercises an exported method. | greetStruct 用于验证导出方法。
type greetStruct struct{ Name string }

// Greet returns a greeting with suffix. | Greet 返回带后缀的问候。
func (g greetStruct) Greet(suffix string) string { return "hello " + g.Name + suffix }

// TestDifferentialLayouts covers define/template/block composition. | TestDifferentialLayouts 覆盖 define/template/block 组合。
func TestDifferentialLayouts(t *testing.T) {
	cases := []compatCase{
		{
			name: "define and template",
			text: `{{ define "row" }}<li>{{ . }}</li>{{ end }}<ul>{{ range .S }}{{ template "row" . }}{{ end }}</ul>`,
			data: map[string]any{"S": []string{"a", "b"}},
		},
		{
			name: "block",
			text: `{{ define "page" }}<main>{{ block "body" . }}default {{ .X }}{{ end }}</main>{{ end }}{{ template "page" . }}`,
			data: map[string]any{"X": "v"},
		},
		{
			name:  "partial in attribute",
			text:  `<a {{ template "attrs" . }}>x</a>`,
			files: map[string]string{"attrs.html": `{{ define "attrs" }}href="{{ .URL }}" title="{{ .T }}"{{ end }}`},
			data:  map[string]any{"URL": "/x?a=1&b=2", "T": `"q"`},
		},
		{
			name:  "partial in script",
			text:  `<script>var x = {{ template "js" . }};</script>`,
			files: map[string]string{"js.html": `{{ define "js" }}{{ .X }}{{ end }}`},
			data:  map[string]any{"X": "</script>"},
		},
		{
			name: "recursive template",
			text: `{{ define "tree" }}{{ .N }}{{ if .Child }}[{{ template "tree" .Child }}]{{ end }}{{ end }}{{ template "tree" .Root }}`,
			data: map[string]any{"Root": map[string]any{"N": 1, "Child": map[string]any{"N": 2}}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			std, mine, stdErr, myErr := renderBoth(t, c)
			if (stdErr != nil) != (myErr != nil) {
				t.Fatalf("error mismatch: std=%v mine=%v", stdErr, myErr)
			}
			if stdErr != nil {
				return
			}
			if std != mine {
				t.Fatalf("output mismatch:\nstd:  %q\nmine: %q", std, mine)
			}
		})
	}
}

// TestDifferentialErrors checks that failures agree on success vs failure. | TestDifferentialErrors 检查失败与否在两个引擎上一致。
func TestDifferentialErrors(t *testing.T) {
	cases := []compatCase{
		{name: "nil field chain", text: `{{ .A.B }}`, data: map[string]any{"A": nil}},
		{name: "missing arg", text: `{{ add 1 }}`, data: nil},
		{name: "wrong type", text: `{{ add "x" 1 }}`, data: nil},
		{name: "func error", text: `{{ fail }}`, data: nil},
		{name: "bad index", text: `{{ index .S 9 }}`, data: map[string]any{"S": []int{1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, stdErr, myErr := renderBoth(t, c)
			if (stdErr != nil) != (myErr != nil) {
				t.Fatalf("error mismatch: std=%v mine=%v", stdErr, myErr)
			}
		})
	}
}
