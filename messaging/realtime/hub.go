// Package realtime provides the subscription hub and the cross-node bus behind the message centre's
// WebSocket endpoint. | Package realtime 提供消息中心 WebSocket 端点背后的订阅中心与跨节点总线。
//
// Single-node deployments use the in-process Hub alone. Multi-node deployments attach a Bus (Redis
// Pub/Sub) and optionally a RetryQueue (asynq): an event is delivered locally first, then broadcast;
// a failed broadcast is handed to the durable queue so another node can republish it. | 单节点部署只使用
// 进程内 Hub。多节点部署会挂上 Bus（Redis Pub/Sub）与可选的 RetryQueue（asynq）：事件先在本地投递，
// 再广播；广播失败时交给持久队列，由另一节点重发。
package realtime

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/messaging"
)

// Subscription is one live connection's interest. | Subscription 是一条活连接的订阅。
type Subscription struct {
	// Deliver receives events that match the subscription. | Deliver 接收匹配订阅的事件。
	Deliver func(messaging.Event)
	// TenantID is the tenant of the connection. | TenantID 是连接所属租户。
	TenantID string
	// UserID is the signed-in user. | UserID 是登录用户。
	UserID string
	// ConversationID is the thread currently open in the browser; empty means none. | ConversationID 是浏览器当前打开的线程；为空表示没有。
	ConversationID string
	// Lang is the negotiated language of the connection, used to render pushed fragments. | Lang 是连接的协商语言，用于渲染推送片段。
	Lang string
}

// Hub fans events out to live subscriptions and, when a Bus is attached, to the other nodes. |
// Hub 把事件分发给活订阅；挂上 Bus 时也会发给其它节点。
type Hub struct {
	// bus is the cross-node transport; nil means single-node. | bus 是跨节点传输；nil 表示单节点。
	bus Bus
	// retry is the durable fallback for a failed broadcast; may be nil. | retry 是广播失败时的持久回退；可为 nil。
	retry RetryQueue
	// log records delivery panics and bus failures. | log 记录投递 panic 与总线故障。
	log *logx.Logger
	// subs maps a subscription token to its description. | subs 把订阅令牌映射到描述。
	subs map[uint64]Subscription
	// cancel stops the bus subscription. | cancel 停止总线订阅。
	cancel func()
	// nodeID identifies this process in the bus; empty in a single-node deployment. | nodeID 在总线中标识本进程；单节点部署为空。
	nodeID string
	// mu guards subs, next, closed and started. | mu 保护 subs、next、closed 与 started。
	mu sync.RWMutex
	// next is the next subscription token. | next 是下一个订阅令牌。
	next uint64
	// counter disambiguates events created in the same nanosecond. | counter 区分同一纳秒内创建的事件。
	counter uint64
	// closed stops new deliveries. | closed 阻止新的投递。
	closed bool
	// started records whether the bus subscription has been started. | started 记录总线订阅是否已启动。
	started bool
}

// New constructs a single-node hub. | New 构造单节点 Hub。
func New(log *logx.Logger) *Hub { return NewDistributed(log, nil, nil, "") }

// NewDistributed constructs a hub with optional cross-node delivery. | NewDistributed 构造带可选跨节点投递的 Hub。
//
// A nil bus keeps the hub local; a nil retry queue only means a failed broadcast is logged. |
// bus 为 nil 时保持本地；retry 为 nil 只表示广播失败仅记录日志。
func NewDistributed(log *logx.Logger, bus Bus, retry RetryQueue, nodeID string) *Hub {
	if log == nil {
		log = logx.Default()
	}
	return &Hub{bus: bus, retry: retry, log: log, nodeID: nodeID, subs: make(map[uint64]Subscription)}
}

// Start subscribes to the bus and delivers remote events locally. Calling it without a bus is a
// no-op, and repeated calls start at most one subscription. | Start 订阅总线并在本地投递远端事件。
// 没有总线时是空操作，重复调用最多启动一次订阅。
func (h *Hub) Start(ctx context.Context) {
	if h == nil || h.bus == nil {
		return
	}
	h.mu.Lock()
	if h.started || h.closed {
		h.mu.Unlock()
		return
	}
	h.started = true
	h.mu.Unlock()

	cancel, err := h.bus.Subscribe(ctx, func(event messaging.Event) {
		// This node already delivered the event locally before publishing it. | 本节点在发布之前已在本地投递过该事件。
		if event.Origin != "" && event.Origin == h.nodeID {
			return
		}
		h.deliverLocal(event)
	})
	if err != nil {
		h.log.Warn("message bus subscription failed, continuing with local delivery | 消息总线订阅失败，继续本地投递",
			logx.Err(err),
		)
		return
	}
	h.mu.Lock()
	h.cancel = cancel
	h.mu.Unlock()
}

// Subscribe registers a connection and returns an idempotent unsubscribe function. | Subscribe 注册连接并返回幂等的取消订阅函数。
func (h *Hub) Subscribe(sub Subscription) func() {
	if h == nil || sub.Deliver == nil {
		return func() {}
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return func() {}
	}
	h.next++
	token := h.next
	h.subs[token] = sub
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, token)
			h.mu.Unlock()
		})
	}
}

// Publish delivers one event locally and broadcasts it when a bus is attached. A failed broadcast is
// handed to the retry queue; it never fails the write that produced the event. |
// Publish 在本地投递一条事件，并在挂上总线时广播。广播失败交给重试队列；绝不让产生事件的写入失败。
func (h *Hub) Publish(ctx context.Context, event messaging.Event) error {
	if h == nil {
		return nil
	}
	event = h.stamp(event)
	h.deliverLocal(event)
	if h.bus == nil {
		return nil
	}
	if err := h.bus.Publish(ctx, event); err != nil {
		h.log.Warn("message broadcast failed | 消息广播失败",
			logx.Err(err),
			logx.Str("event", event.Type),
			logx.Str("tenant", event.TenantID),
		)
		if h.retry != nil {
			if retryErr := h.retry.Enqueue(ctx, event); retryErr != nil {
				h.log.Warn("message broadcast retry enqueue failed | 消息广播重试入队失败", logx.Err(retryErr))
			}
		}
	}
	return nil
}

// PublishRemote republishes an event received from the durable retry queue. It never delivers
// locally: every node, including this one, receives it through the bus. | PublishRemote 重发来自持久
// 重试队列的事件。它绝不在本地投递：包括本节点在内的所有节点都经总线收到它。
func (h *Hub) PublishRemote(ctx context.Context, event messaging.Event) error {
	if h == nil || h.bus == nil {
		return nil
	}
	return h.bus.Publish(ctx, event)
}

// deliverLocal fans an event out to every matching subscription. Delivery runs in its own goroutine
// so a slow socket can never stall the HTTP write that produced the message, and a panic in one
// subscriber cannot take down the hub. | deliverLocal 把事件分发给每个匹配的订阅。投递在独立协程中执行，
// 因此慢套接字绝不会阻塞产生消息的 HTTP 写入，单个订阅者的 panic 也不会拖垮 Hub。
func (h *Hub) deliverLocal(event messaging.Event) {
	if h == nil {
		return
	}
	h.mu.RLock()
	if h.closed {
		h.mu.RUnlock()
		return
	}
	targets := make([]Subscription, 0, len(h.subs))
	for _, sub := range h.subs {
		if matches(sub, event) {
			targets = append(targets, sub)
		}
	}
	h.mu.RUnlock()
	for _, sub := range targets {
		go h.deliver(sub, event)
	}
}

// deliver invokes one subscriber with panic recovery. | deliver 带 panic 恢复地调用一个订阅者。
func (h *Hub) deliver(sub Subscription, event messaging.Event) {
	defer func() {
		if recovered := recover(); recovered != nil {
			h.log.Error("message subscriber panicked | 消息订阅者发生 panic",
				logx.Any("panic", recovered),
				logx.Str("event", event.Type),
			)
		}
	}()
	sub.Deliver(event)
}

// stamp fills the event id, origin and time without mutating the caller's value. | stamp 补全事件 ID、来源与时间，且不修改调用方取值。
func (h *Hub) stamp(event messaging.Event) messaging.Event {
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if event.Origin == "" {
		event.Origin = h.nodeID
	}
	if event.ID == "" {
		h.mu.Lock()
		h.counter++
		counter := h.counter
		h.mu.Unlock()
		event.ID = event.Origin + "-" + strconv.FormatInt(event.At.UnixNano(), 36) + "-" + strconv.FormatUint(counter, 36)
	}
	return event
}

// Close stops future deliveries, cancels the bus subscription and closes the bus. | Close 停止后续投递，取消总线订阅并关闭总线。
func (h *Hub) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.closed = true
	h.subs = make(map[uint64]Subscription)
	cancel := h.cancel
	h.cancel = nil
	bus := h.bus
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if bus != nil {
		if err := bus.Close(); err != nil {
			h.log.Warn("message bus close failed | 消息总线关闭失败", logx.Err(err))
		}
	}
}

// matches reports whether a subscription should receive an event. | matches 报告订阅是否应收到事件。
//
// A non-empty audience is authoritative: only those users receive the event. An empty audience is a
// tenant-wide catalog notification, which every connection in the tenant may observe. | 非空受众是权威条件：
// 只有其中列出的用户会收到事件。空受众表示租户级目录通知，租户内每条连接都可以观察。
func matches(sub Subscription, event messaging.Event) bool {
	if sub.TenantID != "" && event.TenantID != "" && sub.TenantID != event.TenantID {
		return false
	}
	if len(event.Audience) == 0 {
		return true
	}
	for _, id := range event.Audience {
		if id == sub.UserID {
			return true
		}
	}
	return false
}
