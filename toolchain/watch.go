package toolchain

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/shibingli/yd/errx"
)

// DefaultDebounce is the default window used to coalesce file events. | DefaultDebounce 是文件事件的默认抖动合并窗口。
const DefaultDebounce = 200 * time.Millisecond

// defaultQueueSize is the buffer length of the public events channel. | defaultQueueSize 是公开事件通道的缓冲长度。
const defaultQueueSize = 256

// DefaultIgnoreDirs returns the directory names that the watcher skips by default, as a copy the caller may modify. | DefaultIgnoreDirs 返回监听器默认跳过的目录名，返回副本以便调用方随意修改。
func DefaultIgnoreDirs() []string {
	return []string{".git", "bin", "dist", "node_modules", ".tailwindcss", ".yd"}
}

// WatchOptions configures a Watcher. | WatchOptions 配置 Watcher。
type WatchOptions struct {
	// Roots are the watched roots; every non-ignored subdirectory is watched recursively. | Roots 是监听根目录；其中每个未被忽略的子目录都会被递归监听。
	Roots []string
	// Ignore adds directory names (".cache") or paths ("gen/out") on top of DefaultIgnoreDirs. | Ignore 在 DefaultIgnoreDirs 之上追加目录名（".cache"）或路径（"gen/out"）。
	Ignore []string
	// Debounce is the coalescing window; <=0 means DefaultDebounce. | Debounce 是抖动合并窗口；<=0 表示 DefaultDebounce。
	Debounce time.Duration
}

// Event is one coalesced file system change. | Event 是一次抖动合并后的文件系统变更。
//
// Op holds one or more change names joined by "+" in the fixed order create, write, remove, rename, chmod. | Op 是一个或多个变更名，按 create、write、remove、rename、chmod 的固定顺序以 "+" 连接。
type Event struct {
	// Path is the absolute path of the changed entry. | Path 是发生变更的条目的绝对路径。
	Path string
	// Op is the merged operation name. | Op 是合并后的操作名。
	Op string
	// At is the time the last change of this path was observed. | At 是该路径最后一次变更被观测到的时间。
	At time.Time
}

// opNames lists the change names in their fixed merge order. | opNames 按固定合并顺序列出变更名。
var opNames = []struct {
	op   fsnotify.Op
	name string
}{
	{fsnotify.Create, "create"},
	{fsnotify.Write, "write"},
	{fsnotify.Remove, "remove"},
	{fsnotify.Rename, "rename"},
	{fsnotify.Chmod, "chmod"},
}

// pending is the merged state of one changed path. | pending 是单个变更路径的合并状态。
type pending struct {
	ops fsnotify.Op
	at  time.Time
}

// Watcher turns raw file system notifications into debounced Event values. | Watcher 把原始文件系统通知转换为抖动合并后的 Event。
//
// Writes arriving inside one debounce window collapse into a single Event per path, so an editor's save-many-files-at-once burst triggers exactly one rebuild. | 同一个抖动窗口内的多次写入会按路径合并为一个 Event，因此编辑器一次保存多个文件只会触发一次重建。
//
// Notification errors reported by the backend are not actionable at this layer (the watched tree stays registered) and are therefore dropped. | 后端报告的通知错误在本层无法处理（被监听的目录树保持注册），因此直接丢弃。
// Close is idempotent, and the events channel is closed once the background loop has stopped. | Close 幂等，事件通道会在后台循环停止后关闭。
type Watcher struct {
	debounce time.Duration
	names    map[string]struct{}
	paths    []string
	roots    []string
	fsw      *fsnotify.Watcher
	timer    *time.Timer
	events   chan Event
	done     chan struct{}
	closeOne sync.Once
	wg       sync.WaitGroup

	// mu guards dirs, which Close and WatchCount read from other goroutines. | mu 保护 dirs：Close 与 WatchCount 会从其它 goroutine 读取它。
	mu   sync.Mutex
	dirs map[string]struct{}

	// pending, order and the timer are only touched by the loop goroutine. | pending、order 与计时器只由循环 goroutine 访问。
	pending map[string]*pending
	order   []string
}

// NewWatcher creates a watcher and starts the background loop. | NewWatcher 创建监听器并启动后台循环。
//
// Missing roots are skipped; when no root can be watched ErrNoWatchRoot is returned. | 不存在的根目录会被跳过；当没有任何根目录可监听时返回 ErrNoWatchRoot。
func NewWatcher(o WatchOptions) (*Watcher, error) {
	debounce := o.Debounce
	if debounce <= 0 {
		debounce = DefaultDebounce
	}
	w := &Watcher{
		debounce: debounce,
		names:    make(map[string]struct{}, len(DefaultIgnoreDirs())),
		events:   make(chan Event, defaultQueueSize),
		done:     make(chan struct{}),
		dirs:     make(map[string]struct{}),
		pending:  make(map[string]*pending),
	}
	for _, n := range DefaultIgnoreDirs() {
		w.names[strings.ToLower(n)] = struct{}{}
	}
	for _, n := range o.Ignore {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if strings.ContainsAny(n, `/\`) {
			// 形如 gen/out 或 /abs/path 的忽略项按路径前缀匹配。 | Entries like gen/out or /abs/path match by path prefix.
			abs, err := filepath.Abs(n)
			if err != nil {
				abs = filepath.Clean(n)
			}
			w.paths = append(w.paths, abs)
			continue
		}
		w.names[strings.ToLower(n)] = struct{}{}
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, errx.WrapDetail(err, ErrWatchFailed, "create watcher | 创建监听器")
	}
	w.fsw = fsw
	w.timer = time.NewTimer(debounce)
	w.timer.Stop()

	watched := 0
	for _, root := range o.Roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			abs = filepath.Clean(root)
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			continue
		}
		if err := w.watchTree(abs); err != nil {
			continue
		}
		w.roots = append(w.roots, abs)
		watched++
	}
	if watched == 0 {
		_ = fsw.Close()
		return nil, errx.Detail(ErrNoWatchRoot, "roots %v | 根目录 %v", o.Roots, o.Roots)
	}

	w.wg.Add(1)
	go w.loop()
	return w, nil
}

// Events returns the channel of coalesced events. | Events 返回抖动合并后的事件通道。
//
// The same channel is returned on every call and it is closed after Close returns. | 每次调用返回同一通道，且在 Close 返回后关闭。
func (w *Watcher) Events() <-chan Event { return w.events }

// WatchCount returns how many directories are currently watched. | WatchCount 返回当前正在监听的目录数量。
func (w *Watcher) WatchCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.dirs)
}

// Close stops watching, waits for the loop to finish and closes the events channel. It is safe to call repeatedly. | Close 停止监听、等待循环结束并关闭事件通道；可重复调用。
func (w *Watcher) Close() {
	w.closeOne.Do(func() {
		close(w.done)
		if w.fsw != nil {
			_ = w.fsw.Close()
		}
	})
	w.wg.Wait()
}

// ignore reports whether an absolute path must be skipped. | ignore 判断某个绝对路径是否必须跳过。
func (w *Watcher) ignore(abs string) bool {
	// 目录名只在根目录以下匹配，避免根目录自身的名字误伤整棵树。 | Name matching starts below the roots, so a root named like an ignored dir does not mute the whole tree.
	target := abs
	for _, root := range w.roots {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		target = rel
		break
	}
	for _, seg := range strings.Split(filepath.ToSlash(target), "/") {
		if _, ok := w.names[strings.ToLower(seg)]; ok {
			return true
		}
	}
	for _, p := range w.paths {
		if rel, err := filepath.Rel(p, abs); err == nil {
			if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
				return true
			}
		}
	}
	return false
}

// watchTree registers a directory and all of its non-ignored subdirectories. | watchTree 注册一个目录及其所有未被忽略的子目录。
//
// Only a failure to register the root itself is reported; a subdirectory that cannot be watched (a permission error, a race with a directory delete) is skipped so the rest of the tree keeps working. | 只有根目录本身注册失败才会返回错误；无法监听的子目录（权限错误、与删除目录竞争）会被跳过，以免影响整棵树。
func (w *Watcher) watchTree(dir string) error {
	if err := w.addDir(dir); err != nil {
		return err
	}
	// 递归注册：fsnotify 在 Windows 上不递归，必须逐个目录 Add。 | Register recursively: fsnotify does not recurse on Windows, so every directory is added individually.
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p != dir && w.ignore(p) {
			return filepath.SkipDir
		}
		_ = w.addDir(p)
		return nil
	})
	return nil
}

// addDir registers one directory. | addDir 注册单个目录。
func (w *Watcher) addDir(dir string) error {
	w.mu.Lock()
	_, ok := w.dirs[dir]
	w.mu.Unlock()
	if ok {
		return nil
	}
	if err := w.fsw.Add(dir); err != nil {
		return errx.WrapDetail(err, ErrWatchFailed, "watch %s | 监听 %s", dir, dir)
	}
	w.mu.Lock()
	w.dirs[dir] = struct{}{}
	w.mu.Unlock()
	return nil
}

// dropDir forgets a directory that disappeared. | dropDir 遗忘已消失的目录。
func (w *Watcher) dropDir(dir string) {
	w.mu.Lock()
	delete(w.dirs, dir)
	w.mu.Unlock()
}

// absPath normalizes a notification path. | absPath 归一化通知路径。
func absPath(name string) string {
	if abs, err := filepath.Abs(name); err == nil {
		return abs
	}
	return filepath.Clean(name)
}

// enqueue merges a raw notification into the pending set and restarts the debounce window. | enqueue 把原始通知合并进待定集合并重启抖动窗口。
//
// It is called from the loop goroutine only. | 该方法只由循环 goroutine 调用。
func (w *Watcher) enqueue(ev fsnotify.Event) {
	abs := absPath(ev.Name)
	if w.ignore(abs) {
		return
	}
	now := time.Now()
	p := w.pending[abs]
	if p == nil {
		p = &pending{}
		w.pending[abs] = p
		w.order = append(w.order, abs)
	}
	p.ops |= ev.Op
	p.at = now
	w.timer.Reset(w.debounce)
}

// flush publishes the pending set as Event values. | flush 把待定集合发布为 Event。
//
// It is called from the loop goroutine only. | 该方法只由循环 goroutine 调用。
func (w *Watcher) flush() {
	if len(w.order) == 0 {
		return
	}
	batch := make([]Event, 0, len(w.order))
	for _, p := range w.order {
		if st := w.pending[p]; st != nil {
			batch = append(batch, Event{Path: p, Op: opString(st.ops), At: st.at})
		}
	}
	w.order = w.order[:0]
	clear(w.pending)

	for _, ev := range batch {
		select {
		case w.events <- ev:
		case <-w.done:
			return
		}
	}
}

// opString renders merged operations as "+"-joined names. | opString 把合并后的操作渲染为以 "+" 连接的名字。
func opString(ops fsnotify.Op) string {
	parts := make([]string, 0, len(opNames))
	for _, n := range opNames {
		if ops&n.op != 0 {
			parts = append(parts, n.name)
		}
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, "+")
}

// loop merges raw notifications until the watcher is closed. | loop 持续合并原始通知，直到监听器被关闭。
func (w *Watcher) loop() {
	defer w.wg.Done()
	defer close(w.events)
	for {
		select {
		case <-w.done:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			// 新建目录立即纳入监听，新包与新模板目录里的文件才会被跟踪。 | A new directory is registered immediately so files inside new packages and template dirs are tracked.
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() && !w.ignore(absPath(ev.Name)) {
					_ = w.watchTree(absPath(ev.Name))
				}
			}
			if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				w.dropDir(absPath(ev.Name))
			}
			w.enqueue(ev)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			_ = err // 后端错误在本层不可处理。 | Backend errors are not actionable here.
		case <-w.timer.C:
			w.flush()
		}
	}
}

// listWatched returns the currently watched directories, for diagnostics. | listWatched 返回当前被监听的目录，用于诊断。
func (w *Watcher) listWatched() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.dirs))
	for d := range w.dirs {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
