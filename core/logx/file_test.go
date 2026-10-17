package logx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenFileAppendsWithTwoHandles verifies that two independent handles opened through OpenFile
// append instead of overwriting each other's records: O_APPEND puts every Write at the current
// end of the file, which is the multi-process append contract. | TestOpenFileAppendsWithTwoHandles
// 验证通过 OpenFile 打开的两个独立句柄是追加而不是互相覆盖：O_APPEND 让每次 Write 都落在
// 文件当前末尾，这正是多进程追加写入的约定。
func TestOpenFileAppendsWithTwoHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "append.log")
	f1, err := OpenFile(path)
	if err != nil {
		t.Fatalf("OpenFile(first) = %v", err)
	}
	defer func() { _ = f1.Close() }()
	f2, err := OpenFile(path)
	if err != nil {
		t.Fatalf("OpenFile(second) = %v", err)
	}
	defer func() { _ = f2.Close() }()

	l1 := New(Options{Writer: f1, Level: DebugLevel})
	l2 := New(Options{Writer: f2, Level: DebugLevel})
	for i := 0; i < 20; i++ {
		l1.Info("first handle", Int("seq", i))
		l2.Info("second handle", Int("seq", i))
	}
	if err := l1.Sync(); err != nil {
		t.Fatalf("l1.Sync() = %v", err)
	}
	if err := l2.Sync(); err != nil {
		t.Fatalf("l2.Sync() = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile = %v", err)
	}
	recs := decodeAsyncLines(t, string(data))
	if len(recs) != 40 {
		t.Fatalf("记录条数 = %d，期望 40", len(recs))
	}
	for _, rec := range recs {
		if msg := rec["msg"]; msg != "first handle" && msg != "second handle" {
			t.Fatalf("记录被覆盖或交错: %v", rec)
		}
	}
}

// TestOpenFileErrorIsSentinel verifies the errx sentinel contract: a path that cannot be opened
// returns an error matching ErrOpenLogFile via errors.Is. | TestOpenFileErrorIsSentinel 验证 errx
// 哨兵约定：无法打开的路径返回的错误可用 errors.Is 匹配 ErrOpenLogFile。
func TestOpenFileErrorIsSentinel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "nested", "x.log")
	f, err := OpenFile(path)
	if err == nil {
		_ = f.Close()
		t.Fatal("OpenFile on a nonexistent directory must fail | 不存在的目录下 OpenFile 必须失败")
	}
	if !errors.Is(err, ErrOpenLogFile) {
		t.Fatalf("错误未包装 ErrOpenLogFile: %v", err)
	}
}
