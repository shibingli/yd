// Package metricsx defines the runtime metrics abstraction shared by the system-resource sampler and
// the middleware registry. | Package metricsx 定义系统资源采样器与中间件注册表共用的运行时指标抽象。
//
// The package is deliberately free of OS-specific code: a system source decides how CPU and memory are
// sampled, while the registry observes middleware execution. A Collector composes any number of
// independent sources into one Snapshot, so an application can add custom metrics without changing the
// dashboard or the HTTP handlers. | 本包刻意不包含任何平台相关代码：系统来源决定如何采样 CPU 与内存，
// 注册表负责观察中间件执行。Collector 把任意数量的独立来源组合为一份 Snapshot，因此应用可以新增自定义
// 指标而不必改动仪表盘或 HTTP 处理器。
package metricsx

import "time"

// State is the lifecycle state of one observed middleware. | State 是一个被观察中间件的生命周期状态。
type State string

// Middleware lifecycle states. | 中间件生命周期状态。
const (
	// StateRegistered means the middleware is wired but has not served a request yet. | StateRegistered 表示中间件已装配但尚未处理请求。
	StateRegistered State = "registered"
	// StateActive means the application is running or the middleware has observed a request. | StateActive 表示应用正在运行，或中间件已观察过请求。
	StateActive State = "active"
	// StateStopped means the application shut down; counters are retained for inspection. | StateStopped 表示应用已关闭；计数仍保留供查看。
	StateStopped State = "stopped"
)

// Scope tells where a middleware is mounted. | Scope 说明中间件挂载的位置。
type Scope string

// Middleware scopes. | 中间件作用域。
const (
	// ScopeGlobal marks the application-wide chain. | ScopeGlobal 表示应用级全局链。
	ScopeGlobal Scope = "global"
	// ScopeRoute marks a route-group or route-level chain. | ScopeRoute 表示路由组或路由级链。
	ScopeRoute Scope = "route"
	// ScopeCustom marks a middleware observed outside the built-in chain. | ScopeCustom 表示内建链之外的自定义观察项。
	ScopeCustom Scope = "custom"
)

// Descriptor identifies one observed middleware. | Descriptor 标识一个被观察的中间件。
type Descriptor struct {
	// Name is the stable key used to merge the same middleware mounted in several groups. | Name 是稳定键，用于合并挂载在多个分组里的同一个中间件。
	Name string
	// Title is the bilingual fallback label shown when TitleKey is absent. | Title 是缺少 TitleKey 时显示的双语兜底标签。
	Title string
	// TitleKey is the i18n key preferred by templates over Title. | TitleKey 是模板优先于 Title 使用的 i18n 键。
	TitleKey string
	// Scope records where the middleware is mounted. | Scope 记录中间件的挂载位置。
	Scope Scope
}

// SystemSnapshot is one system-resource reading. | SystemSnapshot 是一次系统资源读数。
//
// It is the stable contract between a platform sampler and every dashboard, so all fields carry plain
// values and the two availability flags instead of platform-specific types. | 它是平台采样器与所有仪表盘之间的
// 稳定契约，因此全部字段都是普通值，并通过两个可用性标记替代平台相关类型。
type SystemSnapshot struct {
	// CPUCores is the number of logical CPUs visible to the process. | CPUCores 是进程可见的逻辑 CPU 数。
	CPUCores int
	// CPUPercent is the host-wide busy percentage in [0, 100]. | CPUPercent 是全主机忙碌百分比，取值 [0, 100]。
	CPUPercent float64
	// MemoryTotal is the host memory total in bytes, or 0 when unknown. | MemoryTotal 是宿主机内存总量（字节），未知时为 0。
	MemoryTotal uint64
	// MemoryAvailable is the host memory currently available in bytes. | MemoryAvailable 是宿主机当前可用内存（字节）。
	MemoryAvailable uint64
	// MemoryPercent is the host memory used percentage in [0, 100]. | MemoryPercent 是宿主机内存使用百分比，取值 [0, 100]。
	MemoryPercent float64
	// ProcessCPUPercent is the share of the host CPU used by this process. | ProcessCPUPercent 是本进程占用的宿主 CPU 份额。
	ProcessCPUPercent float64
	// ProcessMemory is the memory the Go runtime obtained from the OS in bytes. | ProcessMemory 是 Go 运行时向操作系统申请的内存（字节）。
	ProcessMemory uint64
	// ProcessMemoryPercent is ProcessMemory as a share of the host total. | ProcessMemoryPercent 是 ProcessMemory 占宿主总量的比例。
	ProcessMemoryPercent float64
	// HostCPU reports whether the platform exposes host CPU times. | HostCPU 报告平台是否提供宿主机 CPU 时间。
	HostCPU bool
	// HostMemory reports whether the platform exposes host memory. | HostMemory 报告平台是否提供宿主机内存。
	HostMemory bool
}

// MiddlewareSnapshot is the runtime view of one observed middleware. | MiddlewareSnapshot 是一个被观察中间件的运行时视图。
type MiddlewareSnapshot struct {
	// LastSeen is when the middleware last handled a request; zero before the first one. | LastSeen 是中间件最近处理请求的时刻；首次请求前为零值。
	LastSeen time.Time
	// Counters holds caller-defined counters. | Counters 保存调用方定义的计数器。
	Counters map[string]uint64
	// Gauges holds caller-defined instantaneous values. | Gauges 保存调用方定义的瞬时值。
	Gauges map[string]int64
	// Name is the stable descriptor key. | Name 是稳定的描述符键。
	Name string
	// Title is the bilingual fallback label. | Title 是双语兜底标签。
	Title string
	// TitleKey is the preferred i18n key. | TitleKey 是首选的 i18n 键。
	TitleKey string
	// Scope is where the middleware is mounted. | Scope 是中间件的挂载位置。
	Scope Scope
	// State is the lifecycle state. | State 是生命周期状态。
	State State
	// Requests is the number of invocations observed. | Requests 是已观察到的调用次数。
	Requests uint64
	// InFlight is the number of invocations still running. | InFlight 是仍在执行的调用数。
	InFlight uint64
	// Aborted is the number of invocations that aborted the chain. | Aborted 是终止调用链的次数。
	Aborted uint64
	// Errors is the number of invocations that ended with a 5xx status. | Errors 是以 5xx 状态结束的次数。
	Errors uint64
	// Panics is the number of invocations that panicked and were re-thrown to the outer recovery. | Panics 是发生 panic 并重新抛给外层恢复的次数。
	Panics uint64
	// AverageLatency is the arithmetic mean of the observed durations. | AverageLatency 是已观察时长的算术平均。
	AverageLatency time.Duration
	// P95Latency is the 95th percentile from a bounded histogram. | P95Latency 是有限桶直方图给出的 95 分位。
	P95Latency time.Duration
	// MaxLatency is the longest observed duration. | MaxLatency 是已观察到的最长时长。
	MaxLatency time.Duration
}

// Snapshot is one composed view of every registered source. | Snapshot 是全部已注册来源的组合视图。
type Snapshot struct {
	// Collected is when the snapshot was taken. | Collected 是快照的采集时刻。
	Collected time.Time
	// Middleware lists every observed middleware in registration order. | Middleware 按注册顺序列出全部被观察中间件。
	Middleware []MiddlewareSnapshot
	// System is the system-resource reading. | System 是系统资源读数。
	System SystemSnapshot
}

// SystemSource supplies system-resource readings. | SystemSource 提供系统资源读数。
type SystemSource interface {
	// SystemSnapshot returns the current system-resource reading. | SystemSnapshot 返回当前系统资源读数。
	SystemSnapshot() SystemSnapshot
}

// MiddlewareSource supplies middleware runtime readings. | MiddlewareSource 提供中间件运行时读数。
type MiddlewareSource interface {
	// MiddlewareSnapshot returns every observed middleware. | MiddlewareSnapshot 返回全部被观察中间件。
	MiddlewareSnapshot() []MiddlewareSnapshot
}

// Provider supplies one complete runtime snapshot. | Provider 提供一份完整的运行时快照。
//
// Dashboards depend on this interface rather than on concrete samplers, so a project can swap the
// registry or add sources without touching the HTTP handlers. | 仪表盘依赖该接口而不是具体采样器，因此项目可以替换
// 注册表或新增来源，而不必改动 HTTP 处理器。
type Provider interface {
	// Snapshot returns one composed runtime view. | Snapshot 返回一份组合后的运行时视图。
	Snapshot() Snapshot
}

// Collector composes one system source and any number of middleware sources. | Collector 组合一个系统来源与任意数量的中间件来源。
//
// Nil sources are ignored, so an application without a system sampler still exposes middleware
// metrics and vice versa. | nil 来源会被忽略，因此没有系统采样器的应用仍能提供中间件指标，反之亦然。
type Collector struct {
	system     SystemSource
	middleware []MiddlewareSource
}

// NewCollector builds a collector; the system source may be nil. | NewCollector 构造收集器；系统来源可以为 nil。
func NewCollector(system SystemSource, sources ...MiddlewareSource) *Collector {
	c := &Collector{system: system}
	for _, source := range sources {
		if source != nil {
			c.middleware = append(c.middleware, source)
		}
	}
	return c
}

// Snapshot composes every source into one view. | Snapshot 把全部来源组合为一份视图。
func (c *Collector) Snapshot() Snapshot {
	out := Snapshot{Collected: time.Now()}
	if c == nil {
		return out
	}
	if c.system != nil {
		out.System = c.system.SystemSnapshot()
	}
	out.Middleware = c.MiddlewareSnapshot()
	return out
}

// MiddlewareSnapshot concatenates every middleware source in registration order. | MiddlewareSnapshot
// 按来源注册顺序拼接全部中间件快照。
func (c *Collector) MiddlewareSnapshot() []MiddlewareSnapshot {
	if c == nil || len(c.middleware) == 0 {
		return nil
	}
	var out []MiddlewareSnapshot
	for _, source := range c.middleware {
		out = append(out, source.MiddlewareSnapshot()...)
	}
	return out
}
