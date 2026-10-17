package logx

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The multi-process test re-executes the test binary as several child processes that all append
// to the same file at the same instant. The parent then verifies the exact line count, JSON
// validity, field integrity and one distinct pid per child. | 多进程测试把测试二进制重新执行为
// 若干子进程，让它们在同一个时刻向同一个文件追加。父进程随后校验精确行数、JSON 合法性、
// 字段完整性，以及每个子进程对应一个不同的 pid。

const (
	mpChildEnv = "LOGX_MP_CHILD"
	mpPathEnv  = "LOGX_MP_PATH"
	mpIDEnv    = "LOGX_MP_ID"
	mpLinesEnv = "LOGX_MP_LINES"
	mpStartEnv = "LOGX_MP_START_NS"
	mpAsyncEnv = "LOGX_MP_ASYNC"

	mpProcs   = 8
	mpLines   = 200
	mpRounds  = 5
	mpPadSize = 128
)

// mpRecord is the strongly typed shape every child writes, so a torn or interleaved line fails
// json.Unmarshal instead of slipping through as a valid but incomplete record. | mpRecord 是每个
// 子进程写入的强类型形状，被拆散或交错的行走 json.Unmarshal 会失败，而不会作为合法但不完整的记录漏过。
type mpRecord struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
	PID   int    `json:"pid"`
	Proc  int    `json:"proc"`
	Seq   int    `json:"seq"`
	Pad   string `json:"pad"`
}

// TestMultiProcessAppendPreservesRecords is the real multi-process test. In the parent it runs
// mpRounds rounds; in every round mpProcs children write mpLines records each to one shared file
// through O_APPEND handles, with a wall-clock start barrier so the writes really overlap. Sync
// rounds use the default synchronous writer, async rounds use the opt-in batched writer. | TestMultiProcessAppendPreservesRecords
// 是真实的多进程测试。父进程运行 mpRounds 轮；每轮 mpProcs 个子进程通过 O_APPEND 句柄向同一个
// 共享文件各写 mpLines 条记录，并用墙钟启动屏障保证写入确实重叠。偶数轮使用默认同步写入器，
// 奇数轮使用可选的批量写入器。
func TestMultiProcessAppendPreservesRecords(t *testing.T) {
	if os.Getenv(mpChildEnv) == "1" {
		runMultiProcessChild(t)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() = %v", err)
	}
	dir := t.TempDir()
	for round := 0; round < mpRounds; round++ {
		path := filepath.Join(dir, "mp-"+strconv.Itoa(round)+".log")
		async := round%2 == 1
		start := time.Now().Add(time.Second).UnixNano()

		cmds := make([]*exec.Cmd, mpProcs)
		outputs := make([]*bytes.Buffer, mpProcs)
		for proc := 0; proc < mpProcs; proc++ {
			cmd := exec.Command(exe, "-test.run=^TestMultiProcessAppendPreservesRecords$", "-test.timeout=120s")
			cmd.Env = append(os.Environ(),
				mpChildEnv+"=1",
				mpPathEnv+"="+path,
				mpIDEnv+"="+strconv.Itoa(proc),
				mpLinesEnv+"="+strconv.Itoa(mpLines),
				mpStartEnv+"="+strconv.FormatInt(start, 10),
				mpAsyncEnv+"="+strconv.FormatBool(async),
			)
			out := &bytes.Buffer{}
			cmd.Stdout = out
			cmd.Stderr = out
			if err := cmd.Start(); err != nil {
				t.Fatalf("第 %d 轮启动子进程 %d 失败: %v", round+1, proc, err)
			}
			cmds[proc] = cmd
			outputs[proc] = out
		}
		for proc, cmd := range cmds {
			if err := cmd.Wait(); err != nil {
				t.Fatalf("第 %d 轮子进程 %d 失败: %v\n%s", round+1, proc, err, outputs[proc].String())
			}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("第 %d 轮读取日志失败: %v", round+1, err)
		}
		verifyMultiProcessFile(t, round, string(data), async)
	}
}

// verifyMultiProcessFile proves completeness on the parent side: exactly mpProcs*mpLines lines,
// every line valid JSON with intact msg/pad fields, every (proc, seq) pair present exactly once,
// and exactly mpProcs distinct pids (pid values are process identities, not reused within a round
// because all children are alive at the start barrier). | verifyMultiProcessFile 在父进程侧证明
// 完整性：恰好 mpProcs*mpLines 行，每行都是合法 JSON 且 msg/pad 完整，每个 (proc, seq) 恰好出现一次，
// 且恰好有 mpProcs 个不同的 pid（所有子进程在启动屏障时同时存活，因此 pid 在单轮内不会复用）。
func verifyMultiProcessFile(t *testing.T, round int, raw string, async bool) {
	t.Helper()
	if !strings.HasSuffix(raw, "\n") {
		t.Fatalf("第 %d 轮输出未以换行结尾: len=%d", round+1, len(raw))
	}
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	want := mpProcs * mpLines
	if len(lines) != want {
		t.Fatalf("第 %d 轮记录条数 = %d，期望 %d（async=%v）", round+1, len(lines), want, async)
	}

	seen := make(map[[2]int]bool, want)
	pids := make(map[int]bool, mpProcs)
	for i, ln := range lines {
		var rec mpRecord
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			t.Fatalf("第 %d 轮第 %d 行不是合法 JSON（记录被拆散或交错）: %v\n%s", round+1, i+1, err, ln)
		}
		if rec.Msg != "mp" || rec.Level != "info" {
			t.Fatalf("第 %d 轮第 %d 行 msg/level 被破坏: %+v", round+1, i+1, rec)
		}
		if len(rec.Pad) != mpPadSize {
			t.Fatalf("第 %d 轮第 %d 行 pad 长度 = %d，期望 %d", round+1, i+1, len(rec.Pad), mpPadSize)
		}
		if rec.Proc < 0 || rec.Proc >= mpProcs || rec.Seq < 0 || rec.Seq >= mpLines {
			t.Fatalf("第 %d 轮第 %d 行字段越界: %+v", round+1, i+1, rec)
		}
		key := [2]int{rec.Proc, rec.Seq}
		if seen[key] {
			t.Fatalf("第 %d 轮记录重复: %v", round+1, key)
		}
		seen[key] = true
		pids[rec.PID] = true
		if rec.PID <= 0 {
			t.Fatalf("第 %d 轮第 %d 行 pid 非法: %d", round+1, i+1, rec.PID)
		}
	}
	for proc := 0; proc < mpProcs; proc++ {
		for seq := 0; seq < mpLines; seq++ {
			if !seen[[2]int{proc, seq}] {
				t.Fatalf("第 %d 轮缺少记录 (%d,%d)", round+1, proc, seq)
			}
		}
	}
	if len(pids) != mpProcs {
		t.Fatalf("第 %d 轮不同 pid 数 = %d，期望 %d（多进程标识不完整）", round+1, len(pids), mpProcs)
	}
}

// runMultiProcessChild is the child side: it opens the shared file with O_APPEND, waits for the
// wall-clock barrier, writes its mpLines records, then Syncs and Closes before the process exits. | runMultiProcessChild
// 是子进程侧：以 O_APPEND 打开共享文件，等待墙钟屏障，写入自己的 mpLines 条记录，随后在进程退出前
// 执行 Sync 与 Close。
func runMultiProcessChild(t *testing.T) {
	t.Helper()
	path := os.Getenv(mpPathEnv)
	proc, err := strconv.Atoi(os.Getenv(mpIDEnv))
	if err != nil {
		t.Fatalf("子进程 id 非法: %v", err)
	}
	n, err := strconv.Atoi(os.Getenv(mpLinesEnv))
	if err != nil {
		t.Fatalf("子进程行数非法: %v", err)
	}
	start, err := strconv.ParseInt(os.Getenv(mpStartEnv), 10, 64)
	if err != nil {
		t.Fatalf("子进程启动屏障非法: %v", err)
	}
	async, err := strconv.ParseBool(os.Getenv(mpAsyncEnv))
	if err != nil {
		t.Fatalf("子进程 async 标志非法: %v", err)
	}

	f, err := OpenFile(path)
	if err != nil {
		t.Fatalf("子进程 OpenFile = %v", err)
	}
	l := New(Options{Writer: f, Level: InfoLevel, Async: async, QueueSize: 4096, BatchBytes: PipeBufSize})
	defer func() {
		if err := l.Close(); err != nil {
			t.Errorf("子进程 Close = %v", err)
		}
		if err := f.Close(); err != nil {
			t.Errorf("子进程文件 Close = %v", err)
		}
	}()

	pad := strings.Repeat("x", mpPadSize)
	for time.Now().UnixNano() < start {
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < n; i++ {
		l.Info("mp", Int("proc", proc), Int("seq", i), Str("pad", pad))
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("子进程 Sync = %v", err)
	}
}
