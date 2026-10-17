package logx

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- Test helpers ---------- | ---------- 测试辅助 ----------

// jsonLines parses each line in buf as JSON and fails immediately if any line is invalid. | jsonLines 把 buf 中的每一行按 JSON 解析，任一行不合法立即失败。
func jsonLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	raw := buf.String()
	if raw == "" {
		return nil
	}
	if !strings.HasSuffix(raw, "\n") {
		t.Fatalf("日志未以换行结尾: %q", raw)
	}
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	recs := make([]map[string]any, 0, len(lines))
	for i, ln := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			t.Fatalf("第 %d 行不是合法 JSON: %v\n%s", i+1, err, ln)
		}
		recs = append(recs, rec)
	}
	return recs
}

// sortedKeys returns the keys of m in ascending order, used only for error messages. | sortedKeys 返回 m 的键（升序），仅用于错误信息。
func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// countingWriter counts Write calls and bytes, used to verify "one Write per line". | countingWriter 统计 Write 调用次数与字节数，用于验证「每行一次 Write」。
type countingWriter struct {
	writes int
	bytes  int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	w.bytes += len(p)
	return len(p), nil
}

// syncingWriter additionally implements Sync() error, used to verify Logger.Sync. | syncingWriter 额外实现 Sync() error，用于验证 Logger.Sync。
type syncingWriter struct {
	buf   bytes.Buffer
	syncs int
}

func (w *syncingWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

func (w *syncingWriter) Sync() error {
	w.syncs++
	return nil
}

// wrappedErr's Error does not contain the inner error message, used to verify that Err really expands the error chain. | wrappedErr 的 Error 不包含内部错误消息，用于验证 Err 真的展开了错误链。
type wrappedErr struct {
	msg string
	err error
}

func (e *wrappedErr) Error() string { return e.msg }
func (e *wrappedErr) Unwrap() error { return e.err }

// ---------- JSON shape ---------- | ---------- JSON 形状 ----------

func TestJSONShapeStable(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Writer: &buf, Level: InfoLevel, Service: "yd"})
	l.Info("hello world",
		Str("user", "alice"),
		Int("count", 3),
		Bool("ok", true),
		Float64("ratio", 0.5),
		Dur("took", 3*time.Millisecond),
		Time("at", time.Unix(0, 0).UTC()),
	)

	raw := buf.String()
	if strings.Count(raw, "\n") != 1 {
		t.Fatalf("单条日志必须只含一个结尾换行: %q", raw)
	}

	recs := jsonLines(t, &buf)
	if len(recs) != 1 {
		t.Fatalf("记录条数 = %d，期望 1", len(recs))
	}
	got := recs[0]

	// The top-level key set must be stable: no extra keys may appear when Caller/Name are not configured. | 顶层键集合必须稳定：不配置 Caller/Name 时不得出现额外键。
	want := []string{"at", "count", "level", "msg", "ok", "pid", "ratio", "service", "took", "ts", "user"}
	if l.host != "" {
		want = append(want, "host")
	}
	sort.Strings(want)
	if ks := sortedKeys(got); !equalStrings(ks, want) {
		t.Fatalf("顶层键 = %v，期望 %v", ks, want)
	}
	if pid, ok := got["pid"].(float64); !ok || int(pid) != os.Getpid() {
		t.Errorf("pid = %#v，期望当前进程号 %d", got["pid"], os.Getpid())
	}
	if l.host != "" && got["host"] != l.host {
		t.Errorf("host = %#v，期望 %q", got["host"], l.host)
	}

	if got["level"] != "info" {
		t.Errorf("level = %#v，期望 \"info\"", got["level"])
	}
	ts, ok := got["ts"].(string)
	if !ok {
		t.Fatalf("ts 不是字符串: %#v", got["ts"])
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("ts = %q 不是 RFC3339: %v", ts, err)
	}
	if got["msg"] != "hello world" {
		t.Errorf("msg = %#v", got["msg"])
	}
	if got["service"] != "yd" {
		t.Errorf("service = %#v，期望 \"yd\"", got["service"])
	}
	if got["user"] != "alice" {
		t.Errorf("user = %#v，期望 \"alice\"", got["user"])
	}
	if got["count"] != float64(3) {
		t.Errorf("count = %#v，期望 3", got["count"])
	}
	if got["ok"] != true {
		t.Errorf("ok = %#v，期望 true", got["ok"])
	}
	if got["ratio"] != 0.5 {
		t.Errorf("ratio = %#v，期望 0.5", got["ratio"])
	}
	// In JSON, Duration is expressed as milliseconds (the documented semantics). | JSON 中 Duration 以毫秒数表达（文档语义）。
	if got["took"] != float64(3) {
		t.Errorf("took = %#v，期望毫秒数 3", got["took"])
	}
	if got["at"] != "1970-01-01T00:00:00.000Z" {
		t.Errorf("at = %#v，期望 1970-01-01T00:00:00.000Z", got["at"])
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------- Levels ---------- | ---------- 级别 ----------

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Writer: &buf, Level: InfoLevel})

	if !l.Enabled(InfoLevel) || !l.Enabled(ErrorLevel) {
		t.Fatal("Info/Error must stay enabled at the InfoLevel threshold | InfoLevel 阈值下 Info/Error 应启用")
	}
	if l.Enabled(DebugLevel) {
		t.Fatal("Debug must be disabled at the InfoLevel threshold | InfoLevel 阈值下 Debug 应禁用")
	}

	l.Debug("must not appear | 不应出现")
	l.Info("shown 1 | 出现 1")
	l.SetLevel(ErrorLevel)
	if l.Enabled(WarnLevel) {
		t.Fatal("Warn must be disabled at the ErrorLevel threshold | ErrorLevel 阈值下 Warn 应禁用")
	}
	l.Warn("must not appear | 不应出现")
	l.Error("shown 2 | 出现 2")

	recs := jsonLines(t, &buf)
	if len(recs) != 2 {
		t.Fatalf("记录条数 = %d，期望 2", len(recs))
	}
	if recs[0]["level"] != "info" || recs[0]["msg"] != "shown 1 | 出现 1" {
		t.Errorf("第 1 条 = %v", recs[0])
	}
	if recs[1]["level"] != "error" || recs[1]["msg"] != "shown 2 | 出现 2" {
		t.Errorf("第 2 条 = %v", recs[1])
	}
}

func TestParseLevelRoundTrip(t *testing.T) {
	cases := []struct {
		in   string
		want Level
		ok   bool
	}{
		{"debug", DebugLevel, true},
		{"INFO", InfoLevel, true},
		{" Info ", InfoLevel, true},
		{"warn", WarnLevel, true},
		{"warning", WarnLevel, true},
		{"error", ErrorLevel, true},
		{"fatal", FatalLevel, true},
		{"verbose", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, err := ParseLevel(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("ParseLevel(%q) 出错: %v", c.in, err)
				continue
			}
			if got != c.want {
				t.Errorf("ParseLevel(%q) = %v，期望 %v", c.in, got, c.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("ParseLevel(%q) 期望出错，实际 = %v", c.in, got)
			continue
		}
		if !errors.Is(err, ErrUnknownLevel) {
			t.Errorf("ParseLevel(%q) 错误未包装 ErrUnknownLevel: %v", c.in, err)
		}
	}

	for _, lv := range []Level{DebugLevel, InfoLevel, WarnLevel, ErrorLevel, FatalLevel} {
		got, err := ParseLevel(lv.String())
		if err != nil || got != lv {
			t.Errorf("ParseLevel(%q) = %v, %v；期望 %v, nil", lv.String(), got, err, lv)
		}
	}

	if !(DebugLevel < InfoLevel && InfoLevel < WarnLevel && WarnLevel < ErrorLevel && ErrorLevel < FatalLevel) {
		t.Fatal("level constants must increase strictly | 级别常量必须严格递增")
	}
	if InfoLevel != 0 {
		t.Fatal("InfoLevel must be 0 so that the zero value of Options.Level is InfoLevel | InfoLevel 必须为 0，Options.Level 的零值才是 InfoLevel")
	}
}

// ---------- With / Named ----------

func TestWithAddsPersistentFields(t *testing.T) {
	var buf bytes.Buffer
	root := New(Options{Writer: &buf, Level: DebugLevel})

	child := root.With(Str("req", "42"))
	child.Info("first")
	child.Info("second", Str("extra", "1"))
	root.Info("third")

	recs := jsonLines(t, &buf)
	if len(recs) != 3 {
		t.Fatalf("记录条数 = %d，期望 3", len(recs))
	}
	if recs[0]["req"] != "42" {
		t.Errorf("子 logger 第 1 条缺少持久字段: %v", recs[0])
	}
	if _, ok := recs[0]["extra"]; ok {
		t.Errorf("子 logger 第 1 条不应有 extra: %v", recs[0])
	}
	if recs[1]["req"] != "42" || recs[1]["extra"] != "1" {
		t.Errorf("子 logger 第 2 条字段缺失: %v", recs[1])
	}
	if _, ok := recs[2]["req"]; ok {
		t.Errorf("父 logger 不应被 With 污染: %v", recs[2])
	}
	if len(root.fields) != 0 {
		t.Fatalf("父 logger 的持久字段被修改: %v", root.fields)
	}
	if child.core != root.core {
		t.Fatal("a child logger derived by With must share the core (writer/mutex/level) | With 派生的子 logger 必须共享 core（writer/mutex/level）")
	}

	// Persistent fields must come before the fields of the current call. | 持久字段必须排在本次调用字段之前。
	line := buf.String()
	lines := strings.Split(strings.TrimSuffix(line, "\n"), "\n")
	if strings.Index(lines[1], `"req"`) > strings.Index(lines[1], `"extra"`) {
		t.Errorf("持久字段应出现在调用字段之前: %s", lines[1])
	}
}

func TestNamedLogger(t *testing.T) {
	var buf bytes.Buffer
	root := New(Options{Writer: &buf, Level: DebugLevel}).Named("http").Named("server")
	child := root.With(Str("a", "1"))
	child.Info("x")
	root.Info("y")

	recs := jsonLines(t, &buf)
	if len(recs) != 2 {
		t.Fatalf("记录条数 = %d，期望 2", len(recs))
	}
	for i, rec := range recs {
		if rec["logger"] != "http.server" {
			t.Errorf("第 %d 条 logger = %#v，期望 http.server", i+1, rec["logger"])
		}
	}
	if recs[0]["a"] != "1" {
		t.Errorf("Named 后 With 的字段丢失: %v", recs[0])
	}
	if _, ok := recs[1]["a"]; ok {
		t.Errorf("父 logger 不应被污染: %v", recs[1])
	}
}

func TestSetLevelAffectsDerivedLoggers(t *testing.T) {
	var buf bytes.Buffer
	root := New(Options{Writer: &buf, Level: InfoLevel})
	child := root.With(Str("k", "v"))

	child.SetLevel(ErrorLevel)
	if root.Enabled(WarnLevel) {
		t.Fatal("one Logger family shares the level threshold, so the parent is raised to ErrorLevel too | 同一 Logger 家族共享级别阈值，父级也应被提升到 ErrorLevel")
	}
	child.Warn("must not appear | 不应出现")
	child.Error("shown | 出现")
	if recs := jsonLines(t, &buf); len(recs) != 1 {
		t.Fatalf("记录条数 = %d，期望 1", len(recs))
	}
}

// ---------- Error fields ---------- | ---------- 错误字段 ----------

func TestErrFieldUnwraps(t *testing.T) {
	root := errors.New("connection refused")
	joined := errors.Join(errors.New("a failed"), errors.New("b failed"))

	var buf bytes.Buffer
	l := New(Options{Writer: &buf})
	l.Error("custom", Err(&wrappedErr{msg: "dial failed", err: root}))
	l.Error("wrapped", Err(fmt.Errorf("query failed: %w", root)))
	l.Error("joined", Err(joined))
	l.Info("nil", Err(nil))

	recs := jsonLines(t, &buf)
	if len(recs) != 4 {
		t.Fatalf("记录条数 = %d，期望 4（Err(nil) 不应被跳过整条日志）", len(recs))
	}

	wants := []string{
		"dial failed: connection refused",
		"query failed: connection refused", // The inner message is not repeated. | 不重复内层消息
		joined.Error(),
		"", // Err(nil) → no error key. | Err(nil) → 无 error 键
	}
	for i, want := range wants {
		got, ok := recs[i]["error"]
		if want == "" {
			if ok {
				t.Errorf("第 %d 条不应有 error 键: %v", i+1, got)
			}
			continue
		}
		if got != want {
			t.Errorf("第 %d 条 error = %#v，期望 %q", i+1, got, want)
		}
	}
}

// ---------- console format ---------- | ---------- console 格式 ----------

func TestConsoleFormatHumanReadable(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Writer: &buf, Format: "console", Service: "yd", Level: DebugLevel})
	l.Info("hello", Str("user", "alice"), Int("count", 3), Dur("took", 1500*time.Microsecond), Err(nil))

	raw := buf.String()
	if strings.Count(raw, "\n") != 1 || !strings.HasSuffix(raw, "\n") {
		t.Fatalf("console 每条记录必须以单个换行结尾: %q", raw)
	}
	line := strings.TrimSuffix(raw, "\n")
	if strings.HasPrefix(line, "{") {
		t.Fatalf("console 格式不应是 JSON: %q", line)
	}
	if strings.Contains(line, `"`) {
		t.Fatalf("console 简单值不应带 JSON 引号: %q", line)
	}

	fields := strings.Fields(line)
	if len(fields) < 4 {
		t.Fatalf("console 行字段过少: %q", line)
	}
	if _, err := time.Parse("15:04:05.000", fields[0]); err != nil {
		t.Errorf("console 首个 token 应为 15:04:05.000 时间戳，实际 %q", fields[0])
	}
	if fields[1] != "INFO" {
		t.Errorf("console 第二 token = %q，期望 INFO", fields[1])
	}
	if !strings.Contains(line, "pid="+strconv.Itoa(os.Getpid())) {
		t.Errorf("console 行缺少当前进程号: %q", line)
	}
	if l.host != "" && !strings.Contains(line, "host=") {
		t.Errorf("console 行缺少主机名 %q: %q", l.host, line)
	}
	for _, want := range []string{"yd", "hello", "user=alice", "count=3", "took=1.5ms"} {
		if !strings.Contains(line, want) {
			t.Errorf("console 行缺少 %q: %q", want, line)
		}
	}
	if strings.Index(line, "yd") > strings.Index(line, "hello") {
		t.Errorf("service 应出现在消息之前: %q", line)
	}
	if strings.Contains(line, "error=") {
		t.Errorf("Err(nil) 不应产生 error 字段: %q", line)
	}
}

// ---------- Slow query ---------- | ---------- 慢查询 ----------

func TestSlowQueryFields(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Writer: &buf, Level: InfoLevel})
	SlowQuery(l, "SELECT * FROM users WHERE id = ?", 7, 12*time.Millisecond, nil)
	SlowQuery(l, "UPDATE t SET a = 1", 0, 1500*time.Microsecond, errors.New("deadlock"))
	SlowQuery(nil, "SELECT 1", 0, time.Millisecond, nil) // a nil logger must not panic. | nil logger 不应 panic

	recs := jsonLines(t, &buf)
	if len(recs) != 2 {
		t.Fatalf("记录条数 = %d，期望 2", len(recs))
	}
	first := recs[0]
	if first["slow_query"] != true {
		t.Errorf("slow_query = %#v，期望 true", first["slow_query"])
	}
	if first["sql"] != "SELECT * FROM users WHERE id = ?" {
		t.Errorf("sql = %#v", first["sql"])
	}
	if first["rows"] != float64(7) {
		t.Errorf("rows = %#v，期望 7", first["rows"])
	}
	if first["duration"] != float64(12) { // milliseconds. | 毫秒数
		t.Errorf("duration = %#v，期望毫秒数 12", first["duration"])
	}
	if _, ok := first["error"]; ok {
		t.Errorf("无错误时不应有 error 字段: %v", first)
	}
	if first["level"] != "warn" {
		t.Errorf("慢查询级别 = %#v，期望 warn", first["level"])
	}

	second := recs[1]
	if second["duration"] != 1.5 {
		t.Errorf("duration = %#v，期望毫秒数 1.5", second["duration"])
	}
	if second["error"] != "deadlock" {
		t.Errorf("error = %#v，期望 deadlock", second["error"])
	}

	// Nothing is output when the threshold is above WarnLevel. | 阈值高于 WarnLevel 时零输出。
	l.SetLevel(ErrorLevel)
	SlowQuery(l, "SELECT 1", 1, time.Second, nil)
	if got := jsonLines(t, &buf); len(got) != 2 {
		t.Fatalf("禁用后记录条数 = %d，期望仍为 2", len(got))
	}
}

// ---------- Concurrency ---------- | ---------- 并发 ----------

func TestConcurrentWritesAreNotInterleaved(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Writer: &buf, Level: DebugLevel, Service: "yd"})

	const goroutines, perG = 8, 64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				// A payload above 1KB triggers buffer growth, covering the growth path. | 超过 1KB 的载荷会触发缓冲扩容，覆盖扩容路径。
				l.Info("concurrent", Int("g", g), Int("i", i), Str("pad", strings.Repeat("x", 1024)))
			}
		}(g)
	}
	wg.Wait()

	recs := jsonLines(t, &buf)
	if len(recs) != goroutines*perG {
		t.Fatalf("记录条数 = %d，期望 %d", len(recs), goroutines*perG)
	}
	seen := make(map[[2]int]bool, goroutines*perG)
	for _, rec := range recs {
		g, okG := rec["g"].(float64)
		i, okI := rec["i"].(float64)
		if !okG || !okI {
			t.Fatalf("记录缺少 g/i 字段，疑似交错: %v", rec)
		}
		key := [2]int{int(g), int(i)}
		if seen[key] {
			t.Fatalf("记录重复: %v", key)
		}
		seen[key] = true
		if pad, _ := rec["pad"].(string); len(pad) != 1024 {
			t.Fatalf("载荷长度 = %d，期望 1024（缓冲被并发破坏？）", len(pad))
		}
	}
}

func TestOneWritePerRecord(t *testing.T) {
	w := &countingWriter{}
	l := New(Options{Writer: w, Level: DebugLevel})
	l.Debug("a")
	l.Info("b", Str("k", "v"))
	l.Error("c")

	if w.writes != 3 {
		t.Fatalf("Write 调用次数 = %d，期望 3（每行一次 Write）", w.writes)
	}
	if w.bytes == 0 {
		t.Fatal("the byte count written is 0 | 写入字节数为 0")
	}
}

// TestHugeRecordIsWrittenIntact covers a huge record above the 64KB pool-return limit: the buffer must grow correctly, | TestHugeRecordIsWrittenIntact 覆盖超过 64KB 回池上限的大记录：缓冲区必须正确扩容，
// and a buffer that is not returned to the pool must not affect subsequent log records. | 且不回池的缓冲不得影响后续日志。
func TestHugeRecordIsWrittenIntact(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Writer: &buf, Level: DebugLevel})
	big := strings.Repeat("y", 70<<10) // 70KB > maxPooledBufSize
	l.Info("huge", Str("pad", big))
	l.Info("after", Int("n", 1))

	recs := jsonLines(t, &buf)
	if len(recs) != 2 {
		t.Fatalf("记录条数 = %d，期望 2", len(recs))
	}
	if recs[0]["pad"] != big {
		t.Fatalf("大字段长度 = %d，期望 %d", len(recs[0]["pad"].(string)), len(big))
	}
	if recs[1]["msg"] != "after" || recs[1]["n"] != float64(1) {
		t.Fatalf("大记录之后的日志异常: %v", recs[1])
	}
}

// ---------- Escaping ---------- | ---------- 转义 ----------

func TestJSONEscaping(t *testing.T) {
	tricky := "quote\" back\\slash nl\n tab\t cr\r ctrl\x01 esc\x1b delim中文\""

	var buf bytes.Buffer
	l := New(Options{Writer: &buf})
	l.Info(tricky,
		Str("k", tricky),
		Str("multi", "line1\nline2"),
		Strs("arr", []string{"a\"b", "中\n文"}),
	)

	raw := buf.String()
	if strings.Count(raw, "\n") != 1 {
		t.Fatalf("控制字符未被转义（出现裸换行）: %q", raw)
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < 0x20 && raw[i] != '\n' {
			t.Fatalf("输出含未转义控制字节 %#x: %q", raw[i], raw)
		}
	}
	for _, want := range []string{`\"`, `\\`, `\n`, `\t`, `\u0001`, `\u001b`, `中文`} {
		if !strings.Contains(raw, want) {
			t.Errorf("输出缺少转义序列 %q: %s", want, raw)
		}
	}

	recs := jsonLines(t, &buf)
	got := recs[0]
	if got["msg"] != tricky {
		t.Errorf("msg 往返失败:\n got %q\nwant %q", got["msg"], tricky)
	}
	if got["k"] != tricky {
		t.Errorf("k 往返失败: %q", got["k"])
	}
	if got["multi"] != "line1\nline2" {
		t.Errorf("multi 往返失败: %q", got["multi"])
	}
	arr, ok := got["arr"].([]any)
	if !ok || len(arr) != 2 || arr[0] != "a\"b" || arr[1] != "中\n文" {
		t.Errorf("arr 往返失败: %#v", got["arr"])
	}
}

// ---------- Other field types ---------- | ---------- 其他字段类型 ----------

func TestBytesFieldIsBase64(t *testing.T) {
	payload := []byte{0x00, 0x01, 0xff, 'h', 'i'}
	var buf bytes.Buffer
	l := New(Options{Writer: &buf})
	l.Info("bytes", Bytes("payload", payload))

	recs := jsonLines(t, &buf)
	want := base64.StdEncoding.EncodeToString(payload)
	if recs[0]["payload"] != want {
		t.Errorf("payload = %#v，期望 base64 %q", recs[0]["payload"], want)
	}
}

func TestRawFieldVerbatim(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Writer: &buf})
	l.Info("raw",
		Raw([]byte(`"extra":{"a":1}`)),
		RawField("user", []byte(`{"id":7}`)),
	)

	recs := jsonLines(t, &buf)
	extra, ok := recs[0]["extra"].(map[string]any)
	if !ok || extra["a"] != float64(1) {
		t.Errorf("Raw 原样注入失败: %#v", recs[0]["extra"])
	}
	user, ok := recs[0]["user"].(map[string]any)
	if !ok || user["id"] != float64(7) {
		t.Errorf("RawField 原样注入失败: %#v", recs[0]["user"])
	}
}

func TestAnyFieldSlowPath(t *testing.T) {
	type point struct {
		X int
		Y int
	}
	var buf bytes.Buffer
	l := New(Options{Writer: &buf})
	l.Info("any",
		Any("pt", point{X: 1, Y: 2}),
		Any("n", 7),
		Any("s", "text"),
		Any("nil", nil),
		Any("d", 250*time.Millisecond),
		Any("t", time.Unix(0, 0)),
		Any("e", errors.New("boom")),
		Any("b", []byte("hi")),
	)

	recs := jsonLines(t, &buf)
	got := recs[0]
	pt, ok := got["pt"].(map[string]any)
	if !ok || pt["X"] != float64(1) || pt["Y"] != float64(2) {
		t.Errorf("Any 结构体编码失败: %#v", got["pt"])
	}
	if got["n"] != float64(7) || got["s"] != "text" {
		t.Errorf("Any 标量编码失败: n=%#v s=%#v", got["n"], got["s"])
	}
	if _, ok := got["nil"]; ok {
		t.Errorf("Any(nil) 应被跳过: %#v", got["nil"])
	}
	if got["d"] != float64(250) {
		t.Errorf("Any(Duration) = %#v，期望毫秒数 250", got["d"])
	}
	if got["t"] != "1970-01-01T00:00:00.000Z" {
		t.Errorf("Any(time.Time) = %#v", got["t"])
	}
	if got["e"] != "boom" {
		t.Errorf("Any(error) = %#v，期望 boom", got["e"])
	}
	if got["b"] != base64.StdEncoding.EncodeToString([]byte("hi")) {
		t.Errorf("Any([]byte) = %#v", got["b"])
	}
}

func TestCustomTimeFormat(t *testing.T) {
	var buf bytes.Buffer
	at := time.Date(2026, 10, 16, 8, 30, 0, 0, time.UTC)
	l := New(Options{Writer: &buf, TimeFormat: "2006-01-02 15:04:05"})
	l.Info("x", Time("at", at))

	recs := jsonLines(t, &buf)
	ts, _ := recs[0]["ts"].(string)
	parsed, err := time.Parse("2006-01-02 15:04:05", ts)
	if err != nil {
		t.Fatalf("ts = %#v 不符合自定义布局: %v", ts, err)
	}
	if d := time.Since(parsed); d < 0 || d > time.Minute {
		t.Errorf("ts = %#v 与当前 UTC 时间相差 %v，期望时间戳按 UTC 渲染", ts, d)
	}
	if recs[0]["at"] != "2026-10-16 08:30:00" {
		t.Errorf("at = %#v，期望 2026-10-16 08:30:00", recs[0]["at"])
	}
}

// ---------- Caller / Fatal / Default Logger ---------- | ---------- Caller / Fatal / 默认 Logger ----------

func TestCallerAttribution(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Writer: &buf, Caller: true})
	l.Info("with caller")

	recs := jsonLines(t, &buf)
	caller, _ := recs[0]["caller"].(string)
	if !strings.Contains(caller, "logx_test.go:") {
		t.Fatalf("caller = %q，期望包含 logx_test.go:<行号>", caller)
	}
	if strings.ContainsAny(caller, `/\`) {
		t.Fatalf("caller 应只含文件名: %q", caller)
	}
}

func TestFatalCallsExitFunc(t *testing.T) {
	var buf bytes.Buffer
	var code = -1
	l := New(Options{Writer: &buf, ExitFunc: func(c int) { code = c }})
	l.Fatal("boom", Str("k", "v"))

	if code != 1 {
		t.Fatalf("ExitFunc 收到退出码 = %d，期望 1", code)
	}
	recs := jsonLines(t, &buf)
	if len(recs) != 1 || recs[0]["level"] != "fatal" || recs[0]["msg"] != "boom" {
		t.Fatalf("Fatal 未先写日志: %v", recs)
	}
}

func TestSyncAndWriter(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Writer: &buf})
	if l.Writer() != &buf {
		t.Fatal("Writer() did not return the configured writer | Writer() 未返回配置的写入器")
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync() = %v，期望 nil（bytes.Buffer 无 Sync）", err)
	}

	w := &syncingWriter{}
	l2 := New(Options{Writer: w})
	if err := l2.Sync(); err != nil {
		t.Fatalf("Sync() = %v，期望 nil", err)
	}
	if w.syncs != 1 {
		t.Fatalf("底层 Sync 调用次数 = %d，期望 1", w.syncs)
	}
}

func TestDefaultLogger(t *testing.T) {
	def := Default()
	if def == nil {
		t.Fatal("Default() = nil")
	}
	if !def.Enabled(InfoLevel) || def.Enabled(DebugLevel) {
		t.Fatal("the default Logger must be InfoLevel | 默认 Logger 应为 InfoLevel")
	}

	var buf bytes.Buffer
	other := New(Options{Writer: &buf, Level: DebugLevel})
	SetDefault(other)
	defer SetDefault(def)
	if Default() != other {
		t.Fatal("SetDefault did not take effect | SetDefault 未生效")
	}
	Default().Debug("hi")
	if buf.Len() == 0 {
		t.Fatal("the replaced default Logger is not the one in use | 替换后的默认 Logger 未被使用")
	}

	SetDefault(nil)
	if Default() != other {
		t.Fatal("SetDefault(nil) must not change the default Logger | SetDefault(nil) 不应改变默认 Logger")
	}
}
