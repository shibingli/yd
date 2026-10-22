// Package gormcache is the GORM plugin of cachex. | Package gormcache 是 cachex 的 GORM 插件。
//
// The plugin caches SELECT results by a key over the built SQL, every bind variable and a | 插件按「构建后的 SQL、全部绑定变量与该表的代际」生成键来缓存 SELECT 结果；
// per-table generation. Any write to a table bumps its generation, so every cached query | 对一张表的任何写入都会推进其代际，因此该表的所有缓存查询
// for that table goes cold without a key scan. Generations are also broadcast over the | 立即失效，无需扫描键。代际还会经 cachex 的 Pub/Sub 广播，
// cachex Pub/Sub, which keeps multiple application instances consistent. | 使多个应用实例保持一致。
//
// What it does not do: it does not cache queries executed inside an explicit transaction | 不做什么：不缓存显式事务内执行的查询（避免把未提交数据放进共享缓存），
// (so uncommitted data never enters the shared cache), does not cache raw SQL built | 不缓存手工构建的原始 SQL，也不跟踪绕过 GORM 的写入。
// by hand, and does not track writes that bypass GORM entirely. Call Invalidate after a | 绕过 GORM 的写入之后请调用 Invalidate，或依赖 TTL/租约上界。
// write that bypassed GORM, or rely on the TTL and L1 lease to bound staleness.
package gormcache

import (
	"context"
	"database/sql"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/data/cachex"
)

// Declared sentinel errors, testable with errors.Is. | 已声明的哨兵错误，可用 errors.Is 判定。
var (
	// ErrNilManager indicates Options.Manager was nil. | ErrNilManager 表示 Options.Manager 为 nil。
	ErrNilManager = errx.New("gormcache: nil cache manager | gormcache：缓存管理器为 nil")
	// ErrNilDB indicates Initialize received a nil *gorm.DB. | ErrNilDB 表示 Initialize 收到 nil *gorm.DB。
	ErrNilDB = errx.New("gormcache: nil gorm db | gormcache：GORM 数据库为 nil")
	// ErrCallbackMissing indicates gorm:query was not registered on the DB. | ErrCallbackMissing 表示数据库未注册 gorm:query。
	ErrCallbackMissing = errx.New("gormcache: gorm:query callback missing | gormcache：缺少 gorm:query 回调")
	// ErrAlreadyInitialized indicates the same plugin was installed twice. | ErrAlreadyInitialized 表示同一插件被安装两次。
	ErrAlreadyInitialized = errx.New("gormcache: plugin already initialized | gormcache：插件已初始化")
	// ErrClosed indicates the plugin was closed. | ErrClosed 表示插件已关闭。
	ErrClosed         = errx.New("gormcache: plugin closed | gormcache：插件已关闭")
	errCorruptPayload = errx.New("gormcache: corrupt cached payload | gormcache：缓存的载荷损坏")
)

// defaultTTL is used when Options.TTL is not positive. | defaultTTL 在 Options.TTL 非正时使用。
const defaultTTL = 30 * time.Second

// Options configures the GORM plugin. | Options 配置 GORM 插件。
type Options struct {
	// Manager is the cachex manager that owns L1 and L2. | Manager 是拥有 L1 与 L2 的 cachex 管理器。
	Manager *cachex.Manager
	// Logger receives warnings; nil means logx.Default(). | Logger 接收告警；nil 表示 logx.Default()。
	Logger *logx.Logger
	// Namespace is the cache namespace; empty means "gorm". | Namespace 是缓存命名空间；空值表示 "gorm"。
	Namespace string
	// Tables is an allowlist; empty means every table. | Tables 是白名单；空表示所有表。
	Tables []string
	// IgnoreTables is a denylist applied before Tables. | IgnoreTables 是优先于 Tables 应用的黑名单。
	IgnoreTables []string
	// TTL is the lifetime of a cached query result; <=0 means 30 seconds. | TTL 是查询结果的存活时间；<=0 表示 30 秒。
	TTL time.Duration
}

// pluginStats holds plugin counters. | pluginStats 保存插件计数器。
type pluginStats struct {
	hits    atomic.Uint64
	misses  atomic.Uint64
	corrupt atomic.Uint64
	writes  atomic.Uint64
}

// Stats is a point-in-time view of the plugin and its namespace. | Stats 是插件及其命名空间的时间点视图。
type Stats struct {
	// Hits counts queries served from cache. | Hits 统计由缓存答复的查询。
	Hits uint64
	// Misses counts queries that reached the database. | Misses 统计到达数据库的查询。
	Misses uint64
	// Corrupt counts cached payloads that could not be decoded. | Corrupt 统计无法解码的缓存载荷。
	Corrupt uint64
	// Writes counts table invalidations. | Writes 统计表失效次数。
	Writes uint64
	// Cache is the namespace snapshot. | Cache 是命名空间快照。
	Cache cachex.Stats
}

// Plugin implements gorm.Plugin. | Plugin 实现 gorm.Plugin。
type Plugin struct {
	raw         *cachex.Raw
	original    func(*gorm.DB)
	unsub       func()
	gens        map[string]*atomic.Uint64
	opts        Options
	stats       pluginStats
	epoch       atomic.Uint64
	closeOnce   sync.Once
	genMu       sync.Mutex
	initMu      sync.Mutex
	closed      atomic.Bool
	initialized bool
}

// New builds a plugin; call db.Use(plugin) to install it. | New 构造插件；调用 db.Use(plugin) 安装。
func New(o Options) (*Plugin, error) {
	if o.Manager == nil {
		return nil, ErrNilManager
	}
	if o.Namespace == "" {
		o.Namespace = "gorm"
	}
	if o.TTL <= 0 {
		o.TTL = defaultTTL
	}
	if o.Logger == nil {
		o.Logger = logx.Default()
	}
	raw, err := o.Manager.Raw(o.Namespace, cachex.WithTTL(o.TTL))
	if err != nil {
		return nil, err
	}
	return &Plugin{opts: o, raw: raw, gens: make(map[string]*atomic.Uint64)}, nil
}

// Name returns the GORM plugin name. | Name 返回 GORM 插件名。
func (p *Plugin) Name() string { return "cachex:gorm" }

// Initialize installs the query wrapper and the write-invalidation callbacks. | Initialize 安装查询包装器与写入失效回调。
//
// Install one plugin per *gorm.DB. A second Initialize returns ErrAlreadyInitialized. | 每个 *gorm.DB 安装一个插件。第二次 Initialize 返回 ErrAlreadyInitialized。
func (p *Plugin) Initialize(db *gorm.DB) error {
	if db == nil {
		return ErrNilDB
	}
	p.initMu.Lock()
	defer p.initMu.Unlock()
	if p.closed.Load() {
		return ErrClosed
	}
	if p.initialized {
		return ErrAlreadyInitialized
	}
	cb := db.Callback().Query()
	original := cb.Get("gorm:query")
	if original == nil {
		return ErrCallbackMissing
	}
	p.original = original
	if err := cb.Replace("gorm:query", p.query); err != nil {
		return err
	}
	if err := db.Callback().Create().After("gorm:commit_or_rollback_transaction").Register("cachex:invalidate_create", p.invalidateCreate); err != nil {
		return err
	}
	if err := db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("cachex:invalidate_update", p.invalidateUpdate); err != nil {
		return err
	}
	if err := db.Callback().Delete().After("gorm:commit_or_rollback_transaction").Register("cachex:invalidate_delete", p.invalidateDelete); err != nil {
		return err
	}
	unsub, err := p.opts.Manager.Subscribe(p.onEvent)
	if err != nil {
		return err
	}
	p.unsub = unsub
	p.initialized = true
	return nil
}

// query wraps gorm:query with a cache lookup and a best-effort write-back. | query 用缓存查询与尽力回填包装 gorm:query。
func (p *Plugin) query(db *gorm.DB) {
	if !p.cacheable(db) {
		p.original(db)
		return
	}
	callbacks.BuildQuerySQL(db)
	if db.Error != nil || !p.cacheableBuilt(db) {
		p.original(db)
		return
	}
	key, ok := p.queryKey(db)
	if !ok {
		p.original(db)
		return
	}
	ctx := db.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	hit := false
	found, verr := p.raw.View(ctx, key, func(data []byte) error {
		rows, body, valid := splitPayload(data)
		if !valid {
			return errCorruptPayload
		}
		if err := jsonx.Unmarshal(body, db.Statement.Dest); err != nil {
			return err
		}
		db.RowsAffected = rows
		if db.Statement.Result != nil {
			db.Statement.Result.RowsAffected = rows
		}
		hit = true
		return nil
	})
	if found && verr == nil && hit {
		p.stats.hits.Add(1)
		return
	}
	if verr != nil {
		p.stats.corrupt.Add(1)
		_ = p.raw.Delete(ctx, key)
	}
	p.stats.misses.Add(1)
	p.original(db)
	if db.Error != nil || db.RowsAffected <= 0 {
		return
	}
	body, err := jsonx.Marshal(db.Statement.Dest)
	if err != nil {
		return
	}
	payload := make([]byte, 8+len(body))
	binary.BigEndian.PutUint64(payload[:8], uint64(db.RowsAffected))
	copy(payload[8:], body)
	if err := p.raw.SetTTL(ctx, key, payload, p.opts.TTL); err != nil {
		p.opts.Logger.Debug("cachex: gorm cache store failed | cachex：GORM 缓存写入失败", logx.Err(err))
	}
}

// cacheable rejects queries that must not be cached. | cacheable 拒绝不得缓存的查询。
func (p *Plugin) cacheable(db *gorm.DB) bool {
	if db == nil || db.Statement == nil || db.Error != nil || db.DryRun {
		return false
	}
	st := db.Statement
	if st.Dest == nil || st.SQL.Len() > 0 {
		return false
	}
	if _, inTx := st.ConnPool.(gorm.TxCommitter); inTx {
		// Uncommitted reads must never enter the shared cache. | 未提交的读取绝不能进入共享缓存。
		return false
	}
	if len(st.Preloads) > 0 {
		return false
	}
	// A join reads tables whose generations are not part of the key, so a write to the joined
	// table could leave a stale row shape cached. | JOIN 会读取键中未包含代际的表，关联表写入后可能缓存陈旧结果。
	if len(st.Joins) > 0 {
		return false
	}
	if from, ok := st.Clauses["FROM"].Expression.(clause.From); ok && len(from.Joins) > 0 {
		return false
	}
	if _, locking := st.Clauses["FOR"]; locking {
		return false
	}
	if _, rows := st.Dest.(*sql.Rows); rows {
		return false
	}
	return true
}

// cacheableBuilt performs the checks that need the built SQL and table. | cacheableBuilt 执行需要已构建 SQL 与表名的检查。
func (p *Plugin) cacheableBuilt(db *gorm.DB) bool {
	st := db.Statement
	if st == nil || st.Dest == nil || st.SQL.Len() == 0 {
		return false
	}
	table := p.tableOf(db)
	if table == "" {
		return false
	}
	return p.tableAllowed(table)
}

// invalidateCreate always invalidates the table after a successful create. | invalidateCreate 在成功创建后总是使表失效。
func (p *Plugin) invalidateCreate(db *gorm.DB) { p.invalidate(db, true) }

// invalidateUpdate invalidates when at least one row changed. | invalidateUpdate 在至少一行变化时失效。
func (p *Plugin) invalidateUpdate(db *gorm.DB) { p.invalidate(db, false) }

// invalidateDelete invalidates when at least one row changed. | invalidateDelete 在至少一行变化时失效。
func (p *Plugin) invalidateDelete(db *gorm.DB) { p.invalidate(db, false) }

// invalidate bumps a table generation and broadcasts it. | invalidate 推进表代际并广播。
func (p *Plugin) invalidate(db *gorm.DB, always bool) {
	if db == nil || db.Statement == nil || db.Error != nil {
		return
	}
	if !always && db.RowsAffected == 0 {
		return
	}
	table := p.tableOf(db)
	if table == "" || !p.tableAllowed(table) {
		return
	}
	p.stats.writes.Add(1)
	p.bump(db.Statement.Context, table)
}

// bump advances one table generation and publishes the new version. | bump 推进一张表的代际并发布新版本。
func (p *Plugin) bump(ctx context.Context, table string) {
	version := p.genFor(table).Add(1)
	if ctx == nil {
		ctx = context.Background()
	}
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	ev := cachex.Event{
		Op:        cachex.OpInvalidate,
		Namespace: p.opts.Namespace,
		Tables:    []string{table},
		Version:   version,
		Source:    p.opts.Manager.NodeID(),
		At:        time.Now().UTC(),
	}
	if err := p.opts.Manager.Publish(pubCtx, ev); err != nil {
		p.opts.Logger.Warn("cachex: gorm invalidation publish failed | cachex：GORM 失效发布失败",
			logx.Err(err), logx.Str("table", table))
	}
}

// onEvent adopts remote generations and resynchronizes after a subscription gap. | onEvent 采纳远端代际，并在订阅缺口后重新同步。
func (p *Plugin) onEvent(ev cachex.Event) {
	// A subscription gap or a whole-cache clear moves every table forward, including tables that
	// have not been queried yet on this process. | 订阅缺口或全缓存清理会推进所有表，包括本进程尚未查询过的表。
	switch {
	case ev.Op == cachex.OpResync:
		p.epoch.Add(1)
		return
	case ev.Op == cachex.OpClear && ev.Namespace == "":
		p.epoch.Add(1)
		return
	}
	if ev.Namespace != p.opts.Namespace {
		return
	}
	if ev.Source == p.opts.Manager.NodeID() {
		return
	}
	switch ev.Op {
	case cachex.OpInvalidate:
		for _, table := range ev.Tables {
			p.adopt(table, ev.Version)
		}
	case cachex.OpClear:
		p.epoch.Add(1)
	}
}

// adopt advances one table generation by at least one step and never below version. | adopt 把一张表的代际至少推进一格，且不低于 version。
//
// Advancing even when the remote version is lower is deliberate: after a missed event a node's
// local counter can be ahead, and only a changed generation makes its old L2 query keys
// unreachable. | 即使远端版本更低也推进是刻意的：漏掉事件后本地计数器可能领先，只有代际变化
// 才能让旧的 L2 查询键不可达。
func (p *Plugin) adopt(table string, version uint64) {
	if table == "" {
		return
	}
	g := p.genFor(table)
	for {
		cur := g.Load()
		next := version
		if next <= cur {
			next = cur + 1
		}
		if g.CompareAndSwap(cur, next) {
			return
		}
	}
}

// genFor returns the generation cell of a table. | genFor 返回一张表的代际单元。
func (p *Plugin) genFor(table string) *atomic.Uint64 {
	p.genMu.Lock()
	defer p.genMu.Unlock()
	g := p.gens[table]
	if g == nil {
		g = &atomic.Uint64{}
		p.gens[table] = g
	}
	return g
}

// generations returns the global epoch and the table generation. | generations 返回全局纪元与该表代际。
func (p *Plugin) generations(table string) (epoch, generation uint64) {
	return p.epoch.Load(), p.genFor(table).Load()
}

// tableOf returns the physical table name. | tableOf 返回物理表名。
func (p *Plugin) tableOf(db *gorm.DB) string {
	if db == nil || db.Statement == nil {
		return ""
	}
	if db.Statement.Table != "" {
		return db.Statement.Table
	}
	if db.Statement.Schema != nil {
		return db.Statement.Schema.Table
	}
	return ""
}

// tableAllowed applies the denylist then the allowlist. | tableAllowed 先应用黑名单再应用白名单。
func (p *Plugin) tableAllowed(table string) bool {
	for _, t := range p.opts.IgnoreTables {
		if t == table {
			return false
		}
	}
	if len(p.opts.Tables) == 0 {
		return true
	}
	for _, t := range p.opts.Tables {
		if t == table {
			return true
		}
	}
	return false
}

// Invalidate manually invalidates tables; with no arguments it invalidates every table. | Invalidate 手动使表失效；不带参数时使所有表失效。
//
// Use it after writes that bypass GORM, or after an explicit transaction commits. | 在绕过 GORM 的写入之后，或显式事务提交之后使用。
func (p *Plugin) Invalidate(ctx context.Context, tables ...string) error {
	if p.closed.Load() {
		return ErrClosed
	}
	if len(tables) == 0 {
		p.epoch.Add(1)
		ev := cachex.Event{
			Op:        cachex.OpClear,
			Namespace: p.opts.Namespace,
			Source:    p.opts.Manager.NodeID(),
			At:        time.Now().UTC(),
		}
		return p.opts.Manager.Publish(ctx, ev)
	}
	for _, table := range tables {
		p.bump(ctx, table)
	}
	return nil
}

// Stats returns plugin counters and the namespace snapshot. | Stats 返回插件计数器与命名空间快照。
func (p *Plugin) Stats() Stats {
	return Stats{
		Hits:    p.stats.hits.Load(),
		Misses:  p.stats.misses.Load(),
		Corrupt: p.stats.corrupt.Load(),
		Writes:  p.stats.writes.Load(),
		Cache:   p.raw.Stats(),
	}
}

// Close unregisters the namespace and stops event delivery. It is idempotent. | Close 注销命名空间并停止事件投递。它是幂等的。
func (p *Plugin) Close() error {
	p.closeOnce.Do(func() {
		p.closed.Store(true)
		if p.unsub != nil {
			p.unsub()
		}
		if err := p.raw.Close(); err != nil {
			p.opts.Logger.Warn("cachex: gorm namespace close failed | cachex：GORM 命名空间关闭失败", logx.Err(err))
		}
	})
	return nil
}

// splitPayload splits the row count prefix from the JSON body. | splitPayload 从 JSON 正文中拆出行数前缀。
func splitPayload(data []byte) (rows int64, body []byte, ok bool) {
	if len(data) < 8 {
		return 0, nil, false
	}
	return int64(binary.BigEndian.Uint64(data[:8])), data[8:], true
}
