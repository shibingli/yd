package realtime

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shibingli/yd/messaging"
	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/json"
	ydlog "github.com/shibingli/ydx/log"
)

// DefaultRedisChannel is the pub/sub channel shared by every node of one deployment. |
// DefaultRedisChannel 是一次部署中所有节点共享的发布/订阅频道。
const DefaultRedisChannel = "yd:message:events"

// Sentinel errors of the distributed bus. | 分布式总线的哨兵错误。
var (
	// ErrBusPublish reports a failed broadcast. | ErrBusPublish 表示广播失败。
	ErrBusPublish = errs.New("realtime bus publish failed | 实时总线发布失败")
	// ErrBusSubscribe reports a failed subscription. | ErrBusSubscribe 表示订阅失败。
	ErrBusSubscribe = errs.New("realtime bus subscribe failed | 实时总线订阅失败")
)

// Bus broadcasts events between nodes. The in-process Hub is complete without one; a Bus only
// extends delivery beyond the current process. | Bus 在节点之间广播事件。没有 Bus 时进程内 Hub 已经完整；
// Bus 只把投递扩展到当前进程之外。
type Bus interface {
	// Publish broadcasts one event. | Publish 广播一条事件。
	Publish(ctx context.Context, event messaging.Event) error
	// Subscribe calls handler for every event received until the returned function is called. |
	// Subscribe 对收到的每条事件调用 handler，直到调用返回的取消函数。
	Subscribe(ctx context.Context, handler func(messaging.Event)) (func(), error)
	// Close releases the subscription resources. | Close 释放订阅资源。
	Close() error
}

// RetryQueue makes a failed broadcast durable. An implementation may enqueue the event into a
// task queue and republish it later; it must not deliver locally. | RetryQueue 让失败的广播变得持久。
// 实现可以把事件放入任务队列稍后重发；它不得在本地投递。
type RetryQueue interface {
	// Enqueue stores one event for a later PublishRemote. | Enqueue 保存一条事件供稍后 PublishRemote。
	Enqueue(ctx context.Context, event messaging.Event) error
}

// RedisBus implements Bus over Redis Pub/Sub. | RedisBus 基于 Redis 发布/订阅实现 Bus。
type RedisBus struct {
	// client is the shared Redis client. | client 是共享 Redis 客户端。
	client redis.UniversalClient
	// log records reconnect attempts. | log 记录重连尝试。
	log *ydlog.Logger
	// channel is the pub/sub channel. | channel 是发布/订阅频道。
	channel string
}

// NewRedisBus returns a Redis-backed bus. | NewRedisBus 返回基于 Redis 的总线。
func NewRedisBus(client redis.UniversalClient, channel string, log *ydlog.Logger) (*RedisBus, error) {
	if client == nil {
		return nil, ErrBusSubscribe
	}
	if channel == "" {
		channel = DefaultRedisChannel
	}
	if log == nil {
		log = ydlog.Default()
	}
	return &RedisBus{client: client, channel: channel, log: log}, nil
}

// Publish broadcasts one event as JSON. | Publish 以 JSON 广播一条事件。
func (b *RedisBus) Publish(ctx context.Context, event messaging.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return errs.Wrap(err, ErrBusPublish)
	}
	if err := b.client.Publish(ctx, b.channel, data).Err(); err != nil {
		return errs.Wrap(err, ErrBusPublish)
	}
	return nil
}

// Subscribe starts one goroutine that reconnects until ctx is cancelled or the returned function is
// called. | Subscribe 启动一个协程，在 ctx 取消或调用返回函数之前持续重连。
func (b *RedisBus) Subscribe(ctx context.Context, handler func(messaging.Event)) (func(), error) {
	if handler == nil {
		return nil, ErrBusSubscribe
	}
	runCtx, cancel := context.WithCancel(ctx)
	pubsub := b.client.Subscribe(runCtx, b.channel)
	go func() {
		defer func() { _ = pubsub.Close() }()
		for {
			message, err := pubsub.ReceiveMessage(runCtx)
			if err != nil {
				if runCtx.Err() != nil {
					return
				}
				b.log.Warn("message bus subscription interrupted, retrying | 消息总线订阅中断，正在重试",
					ydlog.Err(err),
					ydlog.Str("channel", b.channel),
				)
				select {
				case <-runCtx.Done():
					return
				case <-time.After(time.Second):
					continue
				}
			}
			var event messaging.Event
			if err := json.Unmarshal([]byte(message.Payload), &event); err != nil {
				b.log.Warn("message bus payload is invalid | 消息总线载荷非法", ydlog.Err(err))
				continue
			}
			handler(event)
		}
	}()
	return cancel, nil
}

// Close is a no-op: every subscription owns and closes its own PubSub. | Close 是空操作：每个订阅拥有并关闭自己的 PubSub。
func (b *RedisBus) Close() error { return nil }
