package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/shibingli/yd/runtime/metricsx"
	"github.com/shibingli/ydx/router"
)

// TestDefaultObservesBuiltInMiddleware proves the framework's default chain is instrumented out of
// the box, in execution order and with global scope. | TestDefaultObservesBuiltInMiddleware 证明框架默认
// 中间件链开箱即被观察，顺序与执行顺序一致，作用域为 global。
func TestDefaultObservesBuiltInMiddleware(t *testing.T) {
	a, err := Default()
	if err != nil {
		t.Fatalf("Default 失败: %[1]v | Default failed: %[1]v", err)
	}

	snaps := a.MiddlewareMetrics().MiddlewareSnapshot()
	want := []string{"recover", "request-id", "access-log", "compress", "cors", "i18n", "static-cache"}
	if len(snaps) != len(want) {
		t.Fatalf("中间件条数 = %d, 期望 %d (%v) | middleware entries = %d, want %d (%v)",
			len(snaps), len(want), want, len(snaps), len(want), want)
	}
	for i, name := range want {
		if snaps[i].Name != name {
			t.Errorf("第 %d 条 = %q, 期望 %q | entry %d = %q, want %q", i, snaps[i].Name, name, i, snaps[i].Name, name)
		}
		if snaps[i].Scope != metricsx.ScopeGlobal {
			t.Errorf("%s 作用域 = %q, 期望 global | %s scope = %q, want global", name, snaps[i].Scope, name, snaps[i].Scope)
		}
		if snaps[i].State != metricsx.StateRegistered {
			t.Errorf("%s 初始状态 = %q, 期望 registered | %s initial state = %q, want registered", name, snaps[i].State, name, snaps[i].State)
		}
		if snaps[i].Title == "" {
			t.Errorf("%s 缺少双语标签 | %s has no bilingual title", name, name)
		}
	}
}

// TestUseObservedRecordsRequests proves a project can instrument a custom middleware through the
// facade without touching the registry directly. | TestUseObservedRecordsRequests 证明项目可以通过门面
// 观察自定义中间件，而不必直接操作注册表。
func TestUseObservedRecordsRequests(t *testing.T) {
	a := newTestApp(t, nil)
	a.UseObserved(metricsx.Descriptor{
		Name:     "custom",
		Title:    "Custom | 自定义",
		TitleKey: "metrics.middleware.custom",
		Scope:    metricsx.ScopeCustom,
	}, func(c *router.Ctx) {
		c.SetHeader("X-Custom", "1")
		c.Next()
	})
	a.GET("/", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") })

	if rec := doRequest(t, a, http.MethodGet, "/", nil); rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200 | status = %d, want 200", rec.Code, rec.Code)
	}
	if rec := doRequest(t, a, http.MethodGet, "/", nil); rec.Header().Get("X-Custom") != "1" {
		t.Error("被观察中间件没有运行 | the observed middleware did not run")
	}

	snaps := a.MiddlewareMetrics().MiddlewareSnapshot()
	if len(snaps) != 1 {
		t.Fatalf("中间件条数 = %d, 期望 1 | middleware entries = %d, want 1", len(snaps), len(snaps))
	}
	got := snaps[0]
	if got.Name != "custom" || got.TitleKey != "metrics.middleware.custom" || got.Scope != metricsx.ScopeCustom {
		t.Errorf("描述符 = %+v, 期望原样保留 | descriptor = %+v, want it preserved", got, got)
	}
	if got.Requests != 2 || got.InFlight != 0 {
		t.Errorf("requests/in-flight = %d/%d, 期望 2/0 | requests/in-flight = %d/%d, want 2/0", got.Requests, got.InFlight, got.Requests, got.InFlight)
	}
	if got.State != metricsx.StateActive {
		t.Errorf("状态 = %q, 期望 active | state = %q, want active", got.State, got.State)
	}
}

// TestShutdownStopsMiddlewareMetrics proves the registry participates in the application lifecycle:
// shutdown moves every entry to stopped and keeps the counters. | TestShutdownStopsMiddlewareMetrics
// 证明注册表参与应用生命周期：关闭把全部条目移到 stopped，并保留计数。
func TestShutdownStopsMiddlewareMetrics(t *testing.T) {
	a := newTestApp(t, nil)
	a.UseObserved(metricsx.Descriptor{Name: "custom"}, func(c *router.Ctx) { c.Next() })
	a.GET("/", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") })
	doRequest(t, a, http.MethodGet, "/", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown 失败: %v | Shutdown failed: %v", err, err)
	}

	snaps := a.MiddlewareMetrics().MiddlewareSnapshot()
	if len(snaps) != 1 || snaps[0].State != metricsx.StateStopped {
		t.Fatalf("快照 = %+v, 期望 stopped | snapshot = %+v, want stopped", snaps, snaps)
	}
	if snaps[0].Requests != 1 {
		t.Errorf("requests = %d, 期望保留 1 | requests = %d, want the retained count 1", snaps[0].Requests, snaps[0].Requests)
	}
}
