package toolchain

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// collectEvents drains the events produced by a watcher during the given window. | collectEvents 在给定窗口期内收齐监听器产生的事件。
func collectEvents(t *testing.T, w *Watcher, window time.Duration) []Event {
	t.Helper()
	deadline := time.Now().Add(window)
	var got []Event
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return got
		}
		select {
		case ev, ok := <-w.Events():
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-time.After(remaining):
			return got
		}
	}
}

// waitFor polls until the condition holds and fails the test on timeout. | waitFor 轮询等待条件成立，超时即终止测试。
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s | timed out waiting for %s", what, what)
}

// samePath reports whether two paths point at the same location (case-insensitive on Windows). | samePath 比较两个路径是否指向同一位置（Windows 下忽略大小写）。
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func TestDebounceCoalescesRapidEvents(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWatcher(WatchOptions{Roots: []string{dir}, Debounce: 80 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewWatcher 返回错误 | NewWatcher: %v", err)
	}
	defer w.Close()
	// Let the underlying watch settle so the first write is not missed. | 等待底层监听就绪，避免首写事件丢失。
	time.Sleep(200 * time.Millisecond)

	file := filepath.Join(dir, "app.css")
	for i := 0; i < 5; i++ {
		writeFile(t, file, "/* write "+string(rune('a'+i))+" */\n")
		time.Sleep(10 * time.Millisecond)
	}

	events := collectEvents(t, w, 600*time.Millisecond)
	count := 0
	for _, ev := range events {
		if samePath(ev.Path, file) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("抖动合并失败：连续 5 次写入产生了 %d 个事件，期望 1 个；events=%v | debounce failed: want 1 event, got %d (%v)",
			count, events, count, events)
	}
}

func TestIgnoreDirectories(t *testing.T) {
	ignored := []string{".git", "bin", "dist", "node_modules", ".tailwindcss", ".yd"}

	t.Run("默认忽略目录 | default ignore list", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range ignored {
			writeFile(t, filepath.Join(dir, name, "x.go"), "package x\n")
		}
		keep := filepath.Join(dir, "keep", "y.go")
		writeFile(t, keep, "package keep\n")

		w, err := NewWatcher(WatchOptions{Roots: []string{dir}, Debounce: 60 * time.Millisecond})
		if err != nil {
			t.Fatalf("NewWatcher 返回错误 | NewWatcher: %v", err)
		}
		defer w.Close()
		time.Sleep(200 * time.Millisecond)

		if got := w.WatchCount(); got != 2 {
			t.Errorf("WatchCount = %d，期望 2（根目录 + keep）| want 2 watched dirs", got)
		}

		for _, name := range ignored {
			writeFile(t, filepath.Join(dir, name, "x.go"), "package x\n\n")
		}
		writeFile(t, keep, "package keep\n\n")

		events := collectEvents(t, w, 500*time.Millisecond)
		sawKeep := false
		for _, ev := range events {
			rel, err := filepath.Rel(dir, ev.Path)
			if err != nil {
				rel = ev.Path
			}
			rel = filepath.ToSlash(rel)
			for _, name := range ignored {
				if strings.Contains(rel, name+"/") {
					t.Errorf("被忽略目录 %s 产生了事件：%s | ignored dir emitted an event: %s", name, rel, rel)
				}
			}
			if samePath(ev.Path, keep) {
				sawKeep = true
			}
		}
		if !sawKeep {
			t.Errorf("未收到 keep 目录的事件，期望收到 | expected an event for the non-ignored dir")
		}
	})

	t.Run("追加忽略目录名 | extra ignored name", func(t *testing.T) {
		dir := t.TempDir()
		skip := filepath.Join(dir, "skipme", "z.go")
		writeFile(t, skip, "package z\n")
		w, err := NewWatcher(WatchOptions{Roots: []string{dir}, Ignore: []string{"skipme"}, Debounce: 60 * time.Millisecond})
		if err != nil {
			t.Fatalf("NewWatcher 返回错误 | NewWatcher: %v", err)
		}
		defer w.Close()
		time.Sleep(200 * time.Millisecond)

		if got := w.WatchCount(); got != 1 {
			t.Errorf("WatchCount = %d，期望 1 | want 1 watched dir", got)
		}
		writeFile(t, skip, "package z\n\n")
		for _, ev := range collectEvents(t, w, 400*time.Millisecond) {
			if samePath(ev.Path, skip) {
				t.Errorf("自定义忽略目录产生了事件：%s | custom-ignored dir emitted an event", ev.Path)
			}
		}
	})

	t.Run("追加忽略绝对路径 | extra ignored absolute path", func(t *testing.T) {
		dir := t.TempDir()
		sub := filepath.Join(dir, "gen", "out.go")
		writeFile(t, sub, "package gen\n")
		w, err := NewWatcher(WatchOptions{Roots: []string{dir}, Ignore: []string{filepath.Join(dir, "gen")}, Debounce: 60 * time.Millisecond})
		if err != nil {
			t.Fatalf("NewWatcher 返回错误 | NewWatcher: %v", err)
		}
		defer w.Close()
		time.Sleep(200 * time.Millisecond)

		writeFile(t, sub, "package gen\n\n")
		for _, ev := range collectEvents(t, w, 400*time.Millisecond) {
			if samePath(ev.Path, sub) {
				t.Errorf("被忽略的绝对路径产生了事件：%s | ignored absolute path emitted an event", ev.Path)
			}
		}
	})
}

func TestWatcherSeesNestedDirectories(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "web", "templates", "partials")
	// Directories that exist before the watch starts must be registered recursively. | 监听开始前就存在的嵌套目录必须被递归注册。
	writeFile(t, filepath.Join(deep, "keep.html"), "<div></div>\n")

	w, err := NewWatcher(WatchOptions{Roots: []string{dir}, Debounce: 60 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewWatcher 返回错误 | NewWatcher: %v", err)
	}
	defer w.Close()
	time.Sleep(200 * time.Millisecond)

	file := filepath.Join(deep, "card.html")
	writeFile(t, file, "<div></div>\n")
	event := false
	for _, ev := range collectEvents(t, w, 500*time.Millisecond) {
		if samePath(ev.Path, file) {
			event = true
			if ev.Op == "" {
				t.Errorf("事件缺少 Op | event has no Op: %+v", ev)
			}
			if ev.At.IsZero() {
				t.Errorf("事件缺少时间戳 | event has no timestamp: %+v", ev)
			}
		}
	}
	if !event {
		t.Fatalf("未收到嵌套目录 %s 的事件 | no event for the nested file %s", file, file)
	}

	// A directory created while running must be picked up automatically. | 运行期新建的目录也必须被自动纳入监听。
	newPkg := filepath.Join(dir, "internal", "newpkg")
	writeFile(t, filepath.Join(newPkg, "doc.go"), "package newpkg\n")
	waitFor(t, 5*time.Second, "新增目录被监听 | the new directory being watched", func() bool {
		for _, d := range w.listWatched() {
			if samePath(d, newPkg) {
				return true
			}
		}
		return false
	})
	created := filepath.Join(newPkg, "svc.go")
	writeFile(t, created, "package newpkg\n\nfunc Svc() {}\n")
	found := false
	for _, ev := range collectEvents(t, w, 500*time.Millisecond) {
		if samePath(ev.Path, created) {
			found = true
		}
	}
	if !found {
		t.Fatalf("未收到新建目录中 %s 的事件 | no event for %s inside the new directory", created, created)
	}
}

func TestWatcherLifecycle(t *testing.T) {
	t.Run("缺少可监听根目录返回哨兵 | no watchable root returns the sentinel", func(t *testing.T) {
		dir := t.TempDir()
		_, err := NewWatcher(WatchOptions{Roots: []string{filepath.Join(dir, "nope")}})
		if err == nil {
			t.Fatal("期望错误，实际为 nil | want an error, got nil")
		}
		if !strings.Contains(err.Error(), ErrNoWatchRoot.Error()) {
			t.Fatalf("err = %v，期望包装 ErrNoWatchRoot | want ErrNoWatchRoot", err)
		}
	})

	t.Run("空 Roots 返回哨兵 | empty Roots returns the sentinel", func(t *testing.T) {
		if _, err := NewWatcher(WatchOptions{}); err == nil {
			t.Fatal("期望错误，实际为 nil | want an error, got nil")
		}
	})

	t.Run("默认抖动 200ms | default debounce is 200ms", func(t *testing.T) {
		dir := t.TempDir()
		w, err := NewWatcher(WatchOptions{Roots: []string{dir}})
		if err != nil {
			t.Fatalf("NewWatcher 返回错误 | NewWatcher: %v", err)
		}
		defer w.Close()
		if w.debounce != DefaultDebounce {
			t.Fatalf("debounce = %v，期望 %v | want %v", w.debounce, DefaultDebounce, DefaultDebounce)
		}
	})

	t.Run("Close 幂等且关闭事件通道 | Close is idempotent and closes the channel", func(t *testing.T) {
		dir := t.TempDir()
		w, err := NewWatcher(WatchOptions{Roots: []string{dir}})
		if err != nil {
			t.Fatalf("NewWatcher 返回错误 | NewWatcher: %v", err)
		}
		w.Close()
		w.Close()
		select {
		case _, ok := <-w.Events():
			if ok {
				t.Fatal("Close 之后仍能读到事件 | events are still delivered after Close")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Close 之后事件通道未关闭 | the events channel was not closed")
		}
	})
}

func TestDefaultIgnoreDirs(t *testing.T) {
	got := DefaultIgnoreDirs()
	want := []string{".git", "bin", "dist", "node_modules", ".tailwindcss", ".yd"}
	if len(got) != len(want) {
		t.Fatalf("DefaultIgnoreDirs = %v，期望 %v | want %v", got, want, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DefaultIgnoreDirs = %v，期望 %v | want %v", got, want, want)
		}
	}
	// The returned slice is a copy: mutating it must not affect the defaults. | 返回值是副本：改动它不得影响默认列表。
	got[0] = "mutated"
	if DefaultIgnoreDirs()[0] != ".git" {
		t.Fatal("DefaultIgnoreDirs 返回了内部切片 | DefaultIgnoreDirs leaked its internal slice")
	}
}

func TestClassifyChanges(t *testing.T) {
	cases := []struct {
		path string
		want changeKind
	}{
		{"main.go", kindGo},
		{"internal/router/router.go", kindGo},
		{"go.mod", kindGo},
		{"configs/app.toml", kindConfig},
		{".env", kindConfig},
		{"config.yaml", kindConfig},
		{"web/templates/index.html", kindTemplate},
		{"web/static/css/app.css", kindWeb},
		{"web/static/js/app.js", kindWeb},
		{"README.md", kindNone},
		{"data/cache.bin", kindNone},
	}
	for _, tc := range cases {
		if got := classify(tc.path); got != tc.want {
			t.Errorf("classify(%q) = %q，期望 %q | want %q", tc.path, got, tc.want, tc.want)
		}
	}
}
