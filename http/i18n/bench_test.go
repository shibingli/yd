package i18n

import (
	"sync"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/core/afs"
)

// benchI18nSink keeps the benchmark result observable. | benchI18nSink 保持基准结果可观测。
var benchI18nSink string

// BenchmarkT measures the translation paths that matter on a rendered page. | BenchmarkT 测量页面渲染中真正重要的翻译路径。
func BenchmarkT(b *testing.B) {
	bundle := newFixtureBundle(b)
	cases := []struct {
		name string
		lang string
		key  string
		args []any
	}{
		{"hit_no_args", "en", "title", nil},
		{"hit_one_named", "en", "greeting", []any{"Alice"}},
		{"hit_three_named", "en", "summary", []any{"Alice", "Paris", 3}},
		{"verbs", "en", "user.profile.welcome", []any{"Alice", 5}},
		{"fallback_lang", "ja", "only_default", nil},
		{"miss", "en", "missing.key", nil},
	}
	for _, tc := range cases {
		tr := bundle.Translator(tc.lang)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchI18nSink = tr.T(tc.key, tc.args...)
			}
		})
	}
}

// TestNoAllocOnHitWithoutArgs pins the zero-allocation hot path. | TestNoAllocOnHitWithoutArgs 钉住零分配热路径。
func TestNoAllocOnHitWithoutArgs(t *testing.T) {
	bundle := newFixtureBundle(t)
	tr := bundle.Translator("en")
	if n := testing.AllocsPerRun(1000, func() { benchI18nSink = tr.T("title") }); n != 0 {
		t.Fatalf("T(hit, no args) allocs = %v, want 0", n)
	}
	if n := testing.AllocsPerRun(1000, func() { benchI18nSink = tr.T("missing.key") }); n != 0 {
		t.Fatalf("T(miss) allocs = %v, want 0", n)
	}
}

// TestProblemCountersAndHandler covers missing keys and printf mismatches. | TestProblemCountersAndHandler 覆盖缺失键与 printf 不匹配。
func TestProblemCountersAndHandler(t *testing.T) {
	bundle := newFixtureBundle(t)
	tr := bundle.Translator("en")
	var problems []Problem
	bundle.SetProblemHandler(func(p Problem) { problems = append(problems, p) })

	if got := tr.T("missing.key"); got != "missing.key" {
		t.Fatalf("miss = %q, want the key", got)
	}
	raw := "Welcome %s, you have %d new messages"
	if got := tr.T("user.profile.welcome", "Alice", "not-a-number"); got != raw {
		t.Fatalf("format mismatch = %q, want the raw message %q", got, raw)
	}
	if bundle.Misses() != 1 || bundle.FormatErrors() != 1 {
		t.Fatalf("counters = %d/%d, want 1/1", bundle.Misses(), bundle.FormatErrors())
	}
	if len(problems) != 2 || problems[0].Kind != ProblemMiss || problems[1].Kind != ProblemFormat {
		t.Fatalf("problems = %+v", problems)
	}
}

// TestCompiledMessageParity keeps the placeholder contract of the old implementation. | TestCompiledMessageParity 保持旧实现的占位符约定。
func TestCompiledMessageParity(t *testing.T) {
	bundle := newFixtureBundle(t)
	tr := bundle.Translator("zh-CN")
	cases := []struct {
		key  string
		args []any
		want string
	}{
		{"title", nil, "云开发"},
		{"greeting", nil, "你好，{{.Name}}！"},
		{"greeting", []any{"小明"}, "你好，小明！"},
		{"summary", []any{"小明"}, "小明 在 {{.City}} 有 {{.Count}} 条记录"},
		{"summary", []any{"小明", "上海", 2}, "小明 在 上海 有 2 条记录"},
		{"mixed", []any{"小明", "标签"}, "小明 / 标签"},
		{"user.profile.welcome", []any{"小明", 3}, "欢迎 小明，你有 3 条新消息"},
	}
	for _, tc := range cases {
		if got := tr.T(tc.key, tc.args...); got != tc.want {
			t.Errorf("T(%q, %v) = %q, want %q", tc.key, tc.args, got, tc.want)
		}
	}
}

// TestConcurrentReloadAndTranslate keeps readers lock-free across generations. | TestConcurrentReloadAndTranslate 保证读者跨代无锁。
func TestConcurrentReloadAndTranslate(t *testing.T) {
	fsys := afs.NewMem()
	if err := fsys.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCatalog(t, fsys, "en.yaml", `title: "Title A"`)
	bundle, err := New(fsys, fixtureDir, "en")
	if err != nil {
		t.Fatal(err)
	}
	tr := bundle.Translator("en")

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if got := tr.T("title"); got != "Title A" && got != "Title B" {
						t.Errorf("translation = %q, want a whole generation", got)
						return
					}
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		value := "Title A"
		if i%2 == 1 {
			value = "Title B"
		}
		if err := afero.WriteFile(fsys, fixtureDir+"/en.yaml", []byte(`title: "`+value+`"`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := bundle.Reload(); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}
