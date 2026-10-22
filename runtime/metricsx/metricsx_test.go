package metricsx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shibingli/yd/runtime/metricsx"
	"github.com/shibingli/ydx/middleware"
	"github.com/shibingli/ydx/router"
)

// serve runs one request through r and returns the recorder. | serve 通过 r 执行一次请求并返回记录器。
func serve(t *testing.T, r *router.Router, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// TestRegistryRecordsRequestLifecycle proves a wrapped middleware moves from registered to active,
// counts the request, and publishes a non-zero latency. | TestRegistryRecordsRequestLifecycle 证明被包装的
// 中间件会从 registered 进入 active、计数该次请求，并给出非零延迟。
func TestRegistryRecordsRequestLifecycle(t *testing.T) {
	reg := metricsx.NewRegistry()
	r := router.New()
	r.Use(reg.Wrap(metricsx.Descriptor{
		Name:  "access",
		Title: "Access log | 访问日志",
		Scope: metricsx.ScopeGlobal,
	}, func(c *router.Ctx) {
		time.Sleep(time.Millisecond)
		c.Next()
	}))
	r.GET("/", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") })

	before := reg.MiddlewareSnapshot()
	if len(before) != 1 || before[0].State != metricsx.StateRegistered {
		t.Fatalf("初始快照 = %+v, 期望一条 registered 记录 | initial snapshot = %+v, want one registered entry", before, before)
	}

	serve(t, r, "/")

	snaps := reg.MiddlewareSnapshot()
	if len(snaps) != 1 {
		t.Fatalf("快照条数 = %d, 期望 1 | snapshot entries = %d, want 1", len(snaps), len(snaps))
	}
	got := snaps[0]
	if got.Name != "access" || got.Title != "Access log | 访问日志" || got.Scope != metricsx.ScopeGlobal {
		t.Errorf("描述符 = %+v, 期望保留调用方给出的一切 | descriptor = %+v, want the caller's values preserved", got, got)
	}
	if got.State != metricsx.StateActive {
		t.Errorf("状态 = %q, 期望 active | state = %q, want active", got.State, got.State)
	}
	if got.Requests != 1 || got.InFlight != 0 {
		t.Errorf("requests/in-flight = %d/%d, 期望 1/0 | requests/in-flight = %d/%d, want 1/0", got.Requests, got.InFlight, got.Requests, got.InFlight)
	}
	if got.Aborted != 0 || got.Errors != 0 || got.Panics != 0 {
		t.Errorf("aborted/errors/panics = %d/%d/%d, 期望全 0 | aborted/errors/panics = %d/%d/%d, want all zero", got.Aborted, got.Errors, got.Panics, got.Aborted, got.Errors, got.Panics)
	}
	if got.LastSeen.IsZero() {
		t.Error("last seen 未记录 | last seen was not recorded")
	}
	if got.AverageLatency <= 0 || got.P95Latency <= 0 || got.MaxLatency <= 0 {
		t.Errorf("latency = %v/%v/%v, 期望均为正 | latency = %v/%v/%v, want all positive", got.AverageLatency, got.P95Latency, got.MaxLatency, got.AverageLatency, got.P95Latency, got.MaxLatency)
	}
	if got.P95Latency < got.AverageLatency || got.MaxLatency < got.P95Latency {
		t.Errorf("latency 顺序错误: avg=%v p95=%v max=%v | latency order is wrong", got.AverageLatency, got.P95Latency, got.MaxLatency)
	}
}

// TestRegistryCountsAbortedErrorsAndPanics proves the three terminal outcomes are distinguished:
// an aborted chain, a 5xx response and a re-thrown panic. | TestRegistryCountsAbortedErrorsAndPanics 证明
// 三种终止结局被区分计数：终止的链、5xx 响应与重新抛出的 panic。
func TestRegistryCountsAbortedErrorsAndPanics(t *testing.T) {
	reg := metricsx.NewRegistry()
	r := router.New()

	abortMW := reg.Wrap(metricsx.Descriptor{Name: "abort"}, func(c *router.Ctx) {
		c.Abort()
		_ = c.String(http.StatusTooManyRequests, "slow down")
	})
	errorMW := reg.Wrap(metricsx.Descriptor{Name: "error"}, func(c *router.Ctx) {
		_ = c.String(http.StatusInternalServerError, "boom")
	})
	panicMW := reg.Wrap(metricsx.Descriptor{Name: "panic"}, func(*router.Ctx) {
		panic("kaboom")
	})

	r.Use(middleware.Recover(nil))
	r.GET("/abort", func(c *router.Ctx) {}, abortMW)
	r.GET("/error", func(c *router.Ctx) {}, errorMW)
	r.GET("/panic", func(c *router.Ctx) {}, panicMW)

	if rec := serve(t, r, "/abort"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("abort 响应 = %d, 期望 429 | abort status = %d, want 429", rec.Code, rec.Code)
	}
	if rec := serve(t, r, "/error"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("error 响应 = %d, 期望 500 | error status = %d, want 500", rec.Code, rec.Code)
	}
	if rec := serve(t, r, "/panic"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic 响应 = %d, 期望外层 Recover 写出 500 | panic status = %d, want 500 from Recover", rec.Code, rec.Code)
	}

	byName := make(map[string]metricsx.MiddlewareSnapshot)
	for _, snap := range reg.MiddlewareSnapshot() {
		byName[snap.Name] = snap
	}
	if got := byName["abort"]; got.Aborted != 1 || got.Requests != 1 {
		t.Errorf("abort = %+v, 期望 aborted=1 requests=1 | abort = %+v, want aborted=1 requests=1", got, got)
	}
	if got := byName["error"]; got.Errors != 1 || got.Requests != 1 {
		t.Errorf("error = %+v, 期望 errors=1 requests=1 | error = %+v, want errors=1 requests=1", got, got)
	}
	if got := byName["panic"]; got.Panics != 1 || got.Requests != 1 {
		t.Errorf("panic = %+v, 期望 panics=1 requests=1 | panic = %+v, want panics=1 requests=1", got, got)
	}
}

// TestRegistryAttributesPanicToInnermostObserver proves a handler panic is counted once, by the
// observer closest to it, instead of once per middleware the panic unwinds through. | TestRegistryAttributesPanicToInnermostObserver
// 证明处理器 panic 只计数一次，归给最靠近它的观察者，而不是沿着回退路径上的每个中间件各记一次。
func TestRegistryAttributesPanicToInnermostObserver(t *testing.T) {
	reg := metricsx.NewRegistry()
	r := router.New()
	r.Use(middleware.Recover(nil))
	outer := reg.Wrap(metricsx.Descriptor{Name: "outer"}, func(c *router.Ctx) { c.Next() })
	inner := reg.Wrap(metricsx.Descriptor{Name: "inner"}, func(c *router.Ctx) { c.Next() })
	r.GET("/", func(*router.Ctx) { panic("handler boom") }, outer, inner)

	if rec := serve(t, r, "/"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("响应 = %d, 期望外层 Recover 写出 500 | status = %d, want 500 from Recover", rec.Code, rec.Code)
	}

	byName := make(map[string]metricsx.MiddlewareSnapshot)
	for _, snap := range reg.MiddlewareSnapshot() {
		byName[snap.Name] = snap
	}
	if got := byName["outer"]; got.Panics != 0 {
		t.Errorf("outer panics = %d, 期望 0（不是 panic 源头） | outer panics = %d, want 0 (not the origin)", got.Panics, got.Panics)
	}
	if got := byName["inner"]; got.Panics != 1 {
		t.Errorf("inner panics = %d, 期望 1（最靠近处理器） | inner panics = %d, want 1 (closest to the handler)", got.Panics, got.Panics)
	}
}

// TestRegistryAttributesAbortToInnermostObserver proves an aborted chain is counted once, by the
// observer closest to the abort, instead of once per outer middleware that merely saw the flag. | TestRegistryAttributesAbortToInnermostObserver
// 证明被终止的链只计数一次，归给最靠近终止点的观察者，而不是每个看到标志的外层中间件各记一次。
func TestRegistryAttributesAbortToInnermostObserver(t *testing.T) {
	reg := metricsx.NewRegistry()
	r := router.New()
	outer := reg.Wrap(metricsx.Descriptor{Name: "outer"}, func(c *router.Ctx) { c.Next() })
	inner := reg.Wrap(metricsx.Descriptor{Name: "inner"}, func(c *router.Ctx) {
		c.Abort()
		_ = c.String(http.StatusForbidden, "no")
	})
	r.GET("/", func(c *router.Ctx) { _ = c.String(http.StatusOK, "unreachable") }, outer, inner)

	if rec := serve(t, r, "/"); rec.Code != http.StatusForbidden {
		t.Fatalf("响应 = %d, 期望 403 | status = %d, want 403", rec.Code, rec.Code)
	}
	byName := make(map[string]metricsx.MiddlewareSnapshot)
	for _, snap := range reg.MiddlewareSnapshot() {
		byName[snap.Name] = snap
	}
	if got := byName["outer"]; got.Aborted != 0 {
		t.Errorf("outer aborted = %d, 期望 0（不是终止点） | outer aborted = %d, want 0 (not the abort origin)", got.Aborted, got.Aborted)
	}
	if got := byName["inner"]; got.Aborted != 1 {
		t.Errorf("inner aborted = %d, 期望 1（最靠近终止点） | inner aborted = %d, want 1 (closest to the abort)", got.Aborted, got.Aborted)
	}
}

// TestRegistryAggregatesDuplicateDescriptor proves one logical middleware mounted in several
// groups shares one entry instead of splitting its counters. | TestRegistryAggregatesDuplicateDescriptor
// 证明挂载在多个分组里的同一个逻辑中间件共用一个条目，而不是把计数拆散。
func TestRegistryAggregatesDuplicateDescriptor(t *testing.T) {
	reg := metricsx.NewRegistry()
	r := router.New()
	desc := metricsx.Descriptor{Name: "tenant", Title: "Tenant | 租户", Scope: metricsx.ScopeRoute}
	wrapped := reg.Wrap(desc, func(c *router.Ctx) { c.Next() })

	r.GET("/a", func(c *router.Ctx) { _ = c.String(http.StatusOK, "a") }, wrapped)
	r.GET("/b", func(c *router.Ctx) { _ = c.String(http.StatusOK, "b") }, wrapped)

	serve(t, r, "/a")
	serve(t, r, "/b")

	snaps := reg.MiddlewareSnapshot()
	if len(snaps) != 1 {
		t.Fatalf("快照条数 = %d, 期望同名合并为 1 | snapshot entries = %d, want duplicate names merged", len(snaps), len(snaps))
	}
	if snaps[0].Requests != 2 {
		t.Errorf("requests = %d, 期望 2 | requests = %d, want 2", snaps[0].Requests, snaps[0].Requests)
	}
}

// TestEntrySupportsCustomCountersAndGauges proves custom middleware can publish its own values
// through the same entry. | TestEntrySupportsCustomCountersAndGauges 证明自定义中间件可以通过同一条目
// 发布自己的计数与瞬时值。
func TestEntrySupportsCustomCountersAndGauges(t *testing.T) {
	reg := metricsx.NewRegistry()
	entry := reg.Entry(metricsx.Descriptor{Name: "cache", Title: "Cache | 缓存"})
	entry.Incr("hits")
	entry.Add("hits", 2)
	entry.SetGauge("entries", 7)

	snaps := reg.MiddlewareSnapshot()
	if len(snaps) != 1 {
		t.Fatalf("快照条数 = %d, 期望 1 | snapshot entries = %d, want 1", len(snaps), len(snaps))
	}
	if got := snaps[0].Counters["hits"]; got != 3 {
		t.Errorf("hits = %d, 期望 3 | hits = %d, want 3", got, got)
	}
	if got := snaps[0].Gauges["entries"]; got != 7 {
		t.Errorf("entries = %d, 期望 7 | entries = %d, want 7", got, got)
	}
}

// TestRegistryTracksInFlightRequests proves a blocked middleware is visible as in flight and
// disappears once it returns. | TestRegistryTracksInFlightRequests 证明阻塞中的中间件在快照里可见为
// in flight，返回后归零。
func TestRegistryTracksInFlightRequests(t *testing.T) {
	reg := metricsx.NewRegistry()
	r := router.New()
	started := make(chan struct{})
	release := make(chan struct{})
	r.GET("/", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") },
		reg.Wrap(metricsx.Descriptor{Name: "slow"}, func(c *router.Ctx) {
			close(started)
			<-release
			c.Next()
		}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	}()

	<-started
	snaps := reg.MiddlewareSnapshot()
	if len(snaps) != 1 || snaps[0].InFlight != 1 {
		t.Fatalf("in-flight = %+v, 期望 1 | in-flight = %+v, want 1", snaps, snaps)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("请求未在 5 秒内完成 | request did not finish within five seconds")
	}
	// Poll briefly: the counter decrement happens before ServeHTTP returns, but keep the test
	// robust against scheduler timing. | 短暂轮询：计数递减发生在 ServeHTTP 返回之前，但仍要容忍调度时机。
	deadline := time.Now().Add(5 * time.Second)
	for {
		snaps = reg.MiddlewareSnapshot()
		if len(snaps) == 1 && snaps[0].InFlight == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("in-flight 未归零: %+v | in-flight did not settle: %+v", snaps, snaps)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestRegistryStopsAndKeepsHistory proves shutdown is idempotent, marks every entry stopped, and
// retains the counters for post-mortem inspection. | TestRegistryStopsAndKeepsHistory 证明关闭是幂等的、
// 把所有条目标记为 stopped，并保留计数供事后查看。
func TestRegistryStopsAndKeepsHistory(t *testing.T) {
	reg := metricsx.NewRegistry()
	r := router.New()
	r.Use(reg.Wrap(metricsx.Descriptor{Name: "access"}, func(c *router.Ctx) { c.Next() }))
	r.GET("/", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") })
	serve(t, r, "/")

	reg.Stop()
	reg.Stop()
	// A late request after shutdown must not flip the entry back to active. | 关闭后的迟到请求不得把条目翻回 active。
	serve(t, r, "/")

	snaps := reg.MiddlewareSnapshot()
	if len(snaps) != 1 {
		t.Fatalf("快照条数 = %d, 期望 1 | snapshot entries = %d, want 1", len(snaps), len(snaps))
	}
	if snaps[0].State != metricsx.StateStopped {
		t.Errorf("状态 = %q, 期望 stopped | state = %q, want stopped", snaps[0].State, snaps[0].State)
	}
	if snaps[0].Requests != 2 {
		t.Errorf("requests = %d, 期望 2 | requests = %d, want 2", snaps[0].Requests, snaps[0].Requests)
	}
}

// TestRegistryStartMarksRegisteredEntriesActive proves the application lifecycle can activate
// entries before the first request. | TestRegistryStartMarksRegisteredEntriesActive 证明应用生命周期可以在
// 首个请求之前激活已注册条目。
func TestRegistryStartMarksRegisteredEntriesActive(t *testing.T) {
	reg := metricsx.NewRegistry()
	reg.Entry(metricsx.Descriptor{Name: "idle"})
	reg.Start()

	snaps := reg.MiddlewareSnapshot()
	if len(snaps) != 1 || snaps[0].State != metricsx.StateActive {
		t.Fatalf("快照 = %+v, 期望 active | snapshot = %+v, want active", snaps, snaps)
	}
}

// fakeSystem implements SystemSource. | fakeSystem 实现 SystemSource。
type fakeSystem struct{ snap metricsx.SystemSnapshot }

func (f fakeSystem) SystemSnapshot() metricsx.SystemSnapshot { return f.snap }

// fakeMiddleware implements MiddlewareSource. | fakeMiddleware 实现 MiddlewareSource。
type fakeMiddleware struct{ snaps []metricsx.MiddlewareSnapshot }

func (f fakeMiddleware) MiddlewareSnapshot() []metricsx.MiddlewareSnapshot { return f.snaps }

// TestCollectorComposesSystemAndMiddleware proves the provider abstraction composes independent
// sources, ignores nil sources, and stamps the snapshot time. | TestCollectorComposesSystemAndMiddleware
// 证明提供者抽象能组合独立来源、忽略 nil 来源，并记录快照时间。
func TestCollectorComposesSystemAndMiddleware(t *testing.T) {
	first := fakeMiddleware{snaps: []metricsx.MiddlewareSnapshot{{Name: "a", Requests: 1}}}
	second := fakeMiddleware{snaps: []metricsx.MiddlewareSnapshot{{Name: "b", Requests: 2}}}
	collector := metricsx.NewCollector(
		fakeSystem{snap: metricsx.SystemSnapshot{CPUCores: 8, HostCPU: true}},
		first, nil, second,
	)

	got := collector.Snapshot()
	if got.System.CPUCores != 8 || !got.System.HostCPU {
		t.Errorf("system = %+v, 期望来自 system source | system = %+v, want values from the source", got.System, got.System)
	}
	if len(got.Middleware) != 2 || got.Middleware[0].Name != "a" || got.Middleware[1].Name != "b" {
		t.Errorf("middleware = %+v, 期望按来源顺序拼接 | middleware = %+v, want sources concatenated in order", got.Middleware, got.Middleware)
	}
	if got.Collected.IsZero() {
		t.Error("collected 未记录 | collected time was not recorded")
	}
}

// TestCollectorWithNilSystem proves a project without a system sampler still gets middleware
// metrics. | TestCollectorWithNilSystem 证明没有系统采样器时中间件指标仍然可用。
func TestCollectorWithNilSystem(t *testing.T) {
	reg := metricsx.NewRegistry()
	reg.Entry(metricsx.Descriptor{Name: "custom"})
	got := metricsx.NewCollector(nil, reg).Snapshot()
	if len(got.Middleware) != 1 || got.Middleware[0].Name != "custom" {
		t.Fatalf("middleware = %+v, 期望包含 custom | middleware = %+v, want custom", got.Middleware, got.Middleware)
	}
}

// TestRegistryOrdering proves descriptors stay in registration order, which mirrors the order
// the middleware runs. | TestRegistryOrdering 证明描述符按注册顺序排列，与中间件的执行顺序一致。
func TestRegistryOrdering(t *testing.T) {
	reg := metricsx.NewRegistry()
	reg.Entry(metricsx.Descriptor{Name: "second"})
	reg.Entry(metricsx.Descriptor{Name: "first"})
	reg.Entry(metricsx.Descriptor{Name: "third"})

	got := reg.MiddlewareSnapshot()
	want := []string{"second", "first", "third"}
	if len(got) != len(want) {
		t.Fatalf("快照条数 = %d, 期望 %d | snapshot entries = %d, want %d", len(got), len(want), len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("第 %d 条 = %q, 期望 %q | entry %d = %q, want %q", i, got[i].Name, name, i, got[i].Name, name)
		}
	}
}
