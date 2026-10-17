package logx

import (
	"os"

	"github.com/shibingli/yd/core/errx"
)

// ErrOpenLogFile is returned when a log file cannot be opened for appending. | ErrOpenLogFile 表示日志文件无法以追加方式打开。
var ErrOpenLogFile = errx.New("open log file failed | 打开日志文件失败")

// OpenFile opens path for append-only logging and returns the file handle. | OpenFile 以只追加方式打开 path 用于写日志，并返回文件句柄。
//
// The file is opened with O_APPEND|O_CREATE|O_WRONLY: every Write therefore lands at the current | 文件以 O_APPEND|O_CREATE|O_WRONLY 打开：每次 Write 因此都落在文件当前末尾，
// end of the file, so several processes (or several Logger instances) can append to the same log | 于是多个进程（或同一进程内的多个 Logger 实例）可以同时向同一个日志文件追加，
// concurrently without overwriting each other's records. On POSIX systems O_APPEND also makes each | 不会互相覆盖。在 POSIX 系统上 O_APPEND 还使每次 Write 的偏移计算与写入成为原子操作；
// Write's offset calculation and write atomic, which is what keeps whole records from interleaving | 这正是跨进程时整条记录不交错的前提（同步模式一条记录一次 Write，异步模式每个批次一次 Write，
// across processes when each record (sync mode) or batch (async mode) is at most the destination's | 且大小不超过目的地的原子写边界，见 PipeBufSize）。
// atomic-write boundary (see PipeBufSize).
//
// The caller owns the returned file and must close it after calling Logger.Close and Logger.Sync. | 调用方拥有返回的文件，必须在调用 Logger.Close 与 Logger.Sync 之后关闭它。
// When the file cannot be opened the returned error wraps ErrOpenLogFile, so callers can test it | 文件无法打开时返回的错误包装 ErrOpenLogFile，调用方可用
// with errors.Is. | errors.Is 判定。
func OpenFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrOpenLogFile, "path %[1]q | 路径 %[1]q", path)
	}
	return f, nil
}
