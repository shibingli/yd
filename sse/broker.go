package sse

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/shibingli/yd/router"
)

// defaultQueueSize is the default buffer length of a subscriber's event channel. | defaultQueueSize 是订阅者事件通道的默认缓冲长度。
const defaultQueueSize = 64

// CtxLastEventID is the key that Handler writes into router.Ctx, whose value is the Last-Event-ID carried by the request. | CtxLastEventID 是 Handler 写入 router.Ctx 的键，其值为请求携带的 Last-Event-ID。
//
// It can be read together with LastEventID. | 可配合 LastEventID 读取。
const CtxLastEventID = "sse.last_event_id"

// BrokerOptions holds the construction parameters of Broker. | BrokerOptions 是 Broker 的构造参数。
type BrokerOptions struct {
	// QueueSize is the buffer length of each subscriber's event channel; <=0 means 64. | QueueSize 是每个订阅者事件通道的缓冲长度；<=0 时取 64。
	//
	// A full buffer marks a slow consumer: that frame is dropped and the Dropped counter is incremented, while the publisher never blocks. | 缓冲满即视为慢消费者：该帧被丢弃并累加 Dropped 计数，发布方永不阻塞。
	QueueSize int
}

// subscription is the internal state of a single subscriber. | subscription 是单个订阅者的内部状态。
type subscription struct {
	ch chan Event
}

// topic is the internal state of a single topic. | topic 是单个主题的内部状态。
//
// A topic entry is kept even after its last subscriber leaves, so that its Dropped counter stays queryable. | 主题条目在其最后一个订阅者退出后仍被保留，以便 Dropped 计数持续可查。
type topic struct {
	subs    map[*subscription]struct{}
	dropped atomic.Uint64
}

// Broker is an in-process SSE topic broadcaster that fans events out to every subscriber under the same topic. | Broker 是进程内的 SSE 主题广播器，把事件扇出给同一主题下的全部订阅者。
//
// Delivery is non-blocking: a frame is dropped and counted when a slow consumer's channel is full, without slowing down other subscribers. | 投递是非阻塞的：慢消费者的通道满时丢帧并计数，不会拖慢其它订阅者。
type Broker struct {
	topics map[string]*topic
	opts   BrokerOptions

	mu     sync.RWMutex
	closed bool
}

// NewBroker creates a broadcaster. | NewBroker 创建一个广播器。
func NewBroker(o BrokerOptions) *Broker {
	if o.QueueSize <= 0 {
		o.QueueSize = defaultQueueSize
	}
	return &Broker{
		opts:   o,
		topics: make(map[string]*topic, 8),
	}
}

// Subscribe subscribes to a topic and returns the event channel together with a cancel function. | Subscribe 订阅主题，返回事件通道与取消函数。
//
// The cancel function is idempotent and safe to call repeatedly; once cancelled, the subscription channel is closed and receives no further events. | 取消函数幂等，可安全重复调用；取消后该订阅通道被关闭且不再收到事件。
// Broker.Close closes every subscription channel (and calling the cancel function afterwards is still safe). | Broker.Close 会关闭所有订阅通道（此时再调用取消函数也是安全的）。
// An already closed Broker still returns a closed channel and an empty cancel function. | 已关闭的 Broker 仍返回已关闭的通道与空取消函数。
func (b *Broker) Subscribe(topic string) (<-chan Event, func()) {
	sub := &subscription{ch: make(chan Event, b.opts.QueueSize)}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(sub.ch)
		return sub.ch, func() {}
	}
	t := b.topicLocked(topic)
	t.subs[sub] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			t, ok := b.topics[topic]
			if !ok {
				return
			}
			if _, live := t.subs[sub]; !live {
				return // already cleaned up by Close | 已被 Close 清理
			}
			delete(t.subs, sub)
			close(sub.ch)
		})
	}
	return sub.ch, cancel
}

// Publish delivers an event to all subscribers of a topic in a non-blocking way. | Publish 把事件非阻塞地投递给主题下的所有订阅者。
//
// A slow consumer whose channel is full has that frame dropped and the Dropped counter incremented; this method never blocks the caller, | 通道已满的慢消费者会被丢弃该帧并累加 Dropped 计数；本方法不会阻塞调用方，
// and calling it on a closed Broker or on a topic without subscribers is a no-op. | 对已关闭的 Broker 或无订阅者的主题调用均为无操作。
func (b *Broker) Publish(topic string, e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	t := b.topics[topic]
	if t == nil {
		return
	}
	for sub := range t.subs {
		select {
		case sub.ch <- e:
		default:
			t.dropped.Add(1)
		}
	}
}

// PublishHTML publishes an HTML partial update using Htmx SSE semantics. | PublishHTML 以 Htmx SSE 语义发布一段 HTML 局部更新。
//
// The event name is fixed to the topic name (clients can trigger it with sse-swap="<topic>"), and the data is | 事件名固定为主题名（客户端可用 sse-swap="<topic>" 触发），数据为
// `<hx-partial hx-target="…" hx-swap="…">…</hx-partial>`; | `<hx-partial hx-target="…" hx-swap="…">…</hx-partial>`；
// the hx-target / hx-swap attribute values are HTML-attribute-escaped, while the html fragment itself is transmitted as is. | hx-target / hx-swap 的属性值会做 HTML 属性转义，html 片段本身按原样传输。
func (b *Broker) PublishHTML(topic, target, swap, html string) {
	var sb strings.Builder
	sb.Grow(len(html) + len(target) + len(swap) + 48)
	sb.WriteString(`<hx-partial hx-target="`)
	writeAttrEscaped(&sb, target)
	sb.WriteString(`" hx-swap="`)
	writeAttrEscaped(&sb, swap)
	sb.WriteString(`">`)
	sb.WriteString(html)
	sb.WriteString(`</hx-partial>`)
	b.Publish(topic, Event{Name: topic, Data: []byte(sb.String())})
}

// Clients returns the current number of subscribers under a topic. | Clients 返回主题下当前的订阅者数量。
func (b *Broker) Clients(topic string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	t := b.topics[topic]
	if t == nil {
		return 0
	}
	return len(t.subs)
}

// Dropped returns the cumulative number of events dropped under a topic because of slow consumers. | Dropped 返回主题下因慢消费者而被丢弃的事件累计数。
func (b *Broker) Dropped(topic string) uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	t := b.topics[topic]
	if t == nil {
		return 0
	}
	return t.dropped.Load()
}

// Close shuts the broadcaster down: every subscription channel is closed and later Publish calls become no-ops; calling it repeatedly is safe. | Close 关闭广播器：所有订阅通道被关闭，后续 Publish 成为无操作；重复调用是安全的。
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for _, t := range b.topics {
		for sub := range t.subs {
			close(sub.ch)
		}
		clear(t.subs)
	}
}

// topicLocked returns the topic, creating it when it does not exist; it must be called while holding the write lock. | topicLocked 返回主题（不存在时创建）；必须在持有写锁时调用。
func (b *Broker) topicLocked(name string) *topic {
	t := b.topics[name]
	if t == nil {
		t = &topic{subs: make(map[*subscription]struct{}, 4)}
		b.topics[name] = t
	}
	return t
}

// writeAttrEscaped escapes s according to HTML attribute rules and writes it into sb. | writeAttrEscaped 按 HTML 属性规则转义 s 并写入 sb。
func writeAttrEscaped(sb *strings.Builder, s string) {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		case '"':
			sb.WriteString("&#34;")
		case '\'':
			sb.WriteString("&#39;")
		default:
			sb.WriteByte(c)
		}
	}
}

// Handler returns an out-of-the-box SSE endpoint handler. | Handler 返回一个开箱即用的 SSE 端点处理器。
//
// It establishes an event stream according to o (starting a `: ping` heartbeat when Heartbeat > 0), subscribes to topic, and writes events into the stream; | 它按 o 建立事件流（Heartbeat > 0 时启动 `: ping` 心跳），订阅 topic 并把事件写入流；
// the Last-Event-ID request header is written into the router.Ctx key CtxLastEventID (readable with LastEventID), | 请求头 Last-Event-ID 会被写入 router.Ctx 的 CtxLastEventID 键（可用 LastEventID 读取），
// so that upper-layer middleware or handlers can make resumption decisions. | 供上层中间件或处理器做断点续传判断。
// The handler automatically cancels the subscription and returns when the client disconnects (r.Context().Done() closes), the broadcaster closes, or a write fails. | 客户端断开（r.Context().Done() 关闭）、广播器关闭或写出失败时，处理器自动取消订阅并返回。
func Handler(b *Broker, topic string, o Options) router.HandlerFunc {
	return func(c *router.Ctx) {
		lastID := c.Header("Last-Event-ID")
		c.Set(CtxLastEventID, lastID)

		w, err := NewWriter(c.Writer, o)
		if err != nil {
			c.SetStatus(http.StatusInternalServerError)
			_ = c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.SetStatus(http.StatusOK)
		defer func() { _ = w.Close() }()

		if o.Heartbeat > 0 {
			w.Heartbeat(o.Heartbeat)
		}

		ch, cancel := b.Subscribe(topic)
		defer cancel()

		done := c.Request.Context().Done()
		for {
			select {
			case <-done:
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				if err := w.WriteEvent(e); err != nil {
					return
				}
			}
		}
	}
}

// LastEventID returns the Last-Event-ID carried by this request (an empty string when it is absent). | LastEventID 返回本次请求携带的 Last-Event-ID（不存在时为空串）。
//
// The value is written into router.Ctx by Handler when it establishes the event stream. | 该值由 Handler 在建立事件流时写入 router.Ctx。
func LastEventID(c *router.Ctx) string {
	if v, ok := c.Get(CtxLastEventID); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
