package cachex

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
)

// Additional sentinel errors for the Redis remote. | Redis 远端的额外哨兵错误。
var (
	// ErrSubscribe indicates the Pub/Sub subscription ended unexpectedly. | ErrSubscribe 表示 Pub/Sub 订阅意外结束。
	ErrSubscribe = errx.New("cachex: subscription failed | cachex：订阅失败")
	// ErrHealthCheck indicates the Pub/Sub connection failed its liveness probe. | ErrHealthCheck 表示 Pub/Sub 连接未通过探活。
	ErrHealthCheck = errx.New("cachex: subscription health check failed | cachex：订阅探活失败")
	// ErrPublish indicates an invalidation event could not be broadcast. | ErrPublish 表示失效事件无法广播。
	ErrPublish = errx.New("cachex: publish failed | cachex：发布失败")
)

// Defaults for RedisOptions. | RedisOptions 的默认值。
const (
	defaultSubscribeBuffer = 64
	eventChunkSize         = 64
	loadManyBatch          = 128
)

// RedisOptions configures the Redis L2 store. | RedisOptions 配置 Redis L2 存储。
type RedisOptions struct {
	// Prefix is prepended verbatim to every logical key. | Prefix 原样加在每个逻辑键之前。
	Prefix string
	// Channel is the Pub/Sub channel; empty means Prefix + "events". | Channel 是 Pub/Sub 频道；空值表示 Prefix + "events"。
	Channel string
	// TombstoneTTL is the default lifetime of a delete marker; <=0 means one hour. | TombstoneTTL 是删除标记的默认存活时间；<=0 表示一小时。
	TombstoneTTL time.Duration
	// SubscribeBuffer is the go-redis channel buffer; <=0 means 64. | SubscribeBuffer 是 go-redis 频道缓冲区；<=0 表示 64。
	SubscribeBuffer int
	// HealthInterval probes the Pub/Sub connection; <=0 means 30 seconds. | HealthInterval 探测 Pub/Sub 连接；<=0 表示 30 秒。
	HealthInterval time.Duration
}

// withDefaults fills empty fields. | withDefaults 填充空字段。
func (o RedisOptions) withDefaults() RedisOptions {
	if o.Prefix == "" {
		o.Prefix = DefaultPrefix
	}
	if o.Channel == "" {
		o.Channel = o.Prefix + "events"
	}
	if o.TombstoneTTL <= 0 {
		o.TombstoneTTL = DefaultTombstoneTTL
	}
	if o.SubscribeBuffer <= 0 {
		o.SubscribeBuffer = defaultSubscribeBuffer
	}
	if o.HealthInterval <= 0 {
		o.HealthInterval = defaultHealthInterval
	}
	return o
}

// RedisRemote is a Redis-backed Remote. | RedisRemote 是基于 Redis 的 Remote。
type RedisRemote struct {
	cli      redis.UniversalClient
	closeCh  chan struct{}
	pubsub   *redis.PubSub
	opts     RedisOptions
	closeOne sync.Once
	subMu    sync.Mutex
	closed   atomic.Bool
}

// NewRedisRemote builds a Redis remote over cli. | NewRedisRemote 在 cli 之上构造 Redis 远端。
//
// The client remains caller-owned: Close stops subscriptions but never closes cli. This is | 客户端仍归调用方所有：Close 只停止订阅，绝不关闭 cli。这样
// what lets one client serve the message bus, the rate limiter and this cache. | 同一个客户端可以同时服务消息总线、限流器与本缓存。
func NewRedisRemote(cli redis.UniversalClient, o RedisOptions) (Remote, error) {
	if cli == nil {
		return nil, ErrNilClient
	}
	return &RedisRemote{
		cli:     cli,
		opts:    o.withDefaults(),
		closeCh: make(chan struct{}),
	}, nil
}

// check reports whether the remote is usable. | check 报告远端是否可用。
func (r *RedisRemote) check() error {
	if r.closed.Load() {
		return ErrClosed
	}
	if r.cli == nil {
		return ErrNilClient
	}
	return nil
}

// key applies the Redis prefix. | key 施加 Redis 前缀。
func (r *RedisRemote) key(k string) string { return r.opts.Prefix + k }

// ttlMillis converts a duration to Redis milliseconds; <=0 means no expiry. | ttlMillis 把时长转换为 Redis 毫秒；<=0 表示不过期。
func ttlMillis(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	ms := ttl.Milliseconds()
	if ms < 1 {
		return 1
	}
	return ms
}

// Load returns one envelope. | Load 返回一个信封。
func (r *RedisRemote) Load(ctx context.Context, key string) (Item, bool, error) {
	if err := r.check(); err != nil {
		return Item{}, false, err
	}
	full := r.key(key)
	pipe := r.cli.Pipeline()
	get := pipe.Get(ctx, full)
	pttl := pipe.PTTL(ctx, full)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Item{}, false, errx.Wrap(err, ErrRemote)
	}
	raw, err := get.Bytes()
	if errors.Is(err, redis.Nil) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, errx.Wrap(err, ErrRemote)
	}
	return Item{Raw: raw, TTL: remainingTTL(pttl.Val())}, true, nil
}

// LoadMany returns every key that exists. | LoadMany 返回所有存在的键。
func (r *RedisRemote) LoadMany(ctx context.Context, keys []string) (map[string]Item, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	out := make(map[string]Item, len(keys))
	seen := make(map[string]struct{}, len(keys))
	unique := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		unique = append(unique, k)
	}
	for start := 0; start < len(unique); start += loadManyBatch {
		end := start + loadManyBatch
		if end > len(unique) {
			end = len(unique)
		}
		batch := unique[start:end]
		pipe := r.cli.Pipeline()
		gets := make([]*redis.StringCmd, 0, len(batch))
		ttls := make([]*redis.DurationCmd, 0, len(batch))
		for _, k := range batch {
			full := r.key(k)
			gets = append(gets, pipe.Get(ctx, full))
			ttls = append(ttls, pipe.PTTL(ctx, full))
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, errx.Wrap(err, ErrRemote)
		}
		for i, cmd := range gets {
			raw, err := cmd.Bytes()
			if errors.Is(err, redis.Nil) {
				continue
			}
			if err != nil {
				return nil, errx.Wrap(err, ErrRemote)
			}
			out[batch[i]] = Item{Raw: raw, TTL: remainingTTL(ttls[i].Val())}
		}
	}
	return out, nil
}

// remainingTTL converts a PTTL reply to a duration; <=0 means no expiry. go-redis already
// scales the reply by the command precision (milliseconds for PTTL).
// | remainingTTL 把 PTTL 回复转换为时长；<=0 表示不过期。go-redis 已按命令精度（PTTL 为毫秒）换算好。
func remainingTTL(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d
}

// Store writes one envelope and returns its new version. | Store 写入一个信封并返回新版本。
func (r *RedisRemote) Store(ctx context.Context, key string, data []byte, flags uint8, ttl time.Duration) (uint64, error) {
	if err := r.check(); err != nil {
		return 0, err
	}
	v, err := setScript.Run(ctx, r.cli, []string{r.key(key)}, data, int(flags), ttlMillis(ttl)).Uint64()
	if err != nil {
		return 0, errx.Wrap(err, ErrRemote)
	}
	return v, nil
}

// StoreIf performs the versioned write. | StoreIf 执行带版本的写入。
func (r *RedisRemote) StoreIf(ctx context.Context, key string, data []byte, flags uint8, ttl time.Duration, expect uint64) (uint64, bool, error) {
	if err := r.check(); err != nil {
		return 0, false, err
	}
	v, err := casScript.Run(ctx, r.cli, []string{r.key(key)}, data, int(flags), ttlMillis(ttl), expect).Int64()
	if err != nil {
		return 0, false, errx.Wrap(err, ErrRemote)
	}
	if v < 0 {
		return 0, false, nil
	}
	return uint64(v), true, nil
}

// Remove writes a tombstone or negative marker. | Remove 写入墓碑或负缓存标记。
func (r *RedisRemote) Remove(ctx context.Context, key string, flags uint8, ttl time.Duration) (uint64, error) {
	return r.Store(ctx, key, nil, flags, ttl)
}

// eventWire is the JSON shape published on the channel. | eventWire 是发布到频道的 JSON 形状。
type eventWire struct {
	NS     string   `json:"ns,omitempty"`
	Src    string   `json:"src,omitempty"`
	Keys   []string `json:"keys,omitempty"`
	Tables []string `json:"tables,omitempty"`
	Ver    uint64   `json:"ver,omitempty"`
	At     int64    `json:"at,omitempty"`
	Op     uint8    `json:"op"`
}

// Publish broadcasts an event, chunking keys so one message stays small. | Publish 广播事件，并分块键以保持单条消息较小。
func (r *RedisRemote) Publish(ctx context.Context, ev Event) error {
	if err := r.check(); err != nil {
		return err
	}
	base := eventWire{Op: uint8(ev.Op), NS: ev.Namespace, Tables: ev.Tables, Ver: ev.Version, Src: ev.Source, At: ev.At.UnixNano()}
	if len(ev.Keys) == 0 {
		return r.publishOne(ctx, base)
	}
	for start := 0; start < len(ev.Keys); start += eventChunkSize {
		end := start + eventChunkSize
		if end > len(ev.Keys) {
			end = len(ev.Keys)
		}
		msg := base
		msg.Keys = ev.Keys[start:end]
		if err := r.publishOne(ctx, msg); err != nil {
			return err
		}
	}
	return nil
}

// publishOne marshals and sends one message. | publishOne 序列化并发送一条消息。
func (r *RedisRemote) publishOne(ctx context.Context, msg eventWire) error {
	payload, err := jsonx.Marshal(msg)
	if err != nil {
		return errx.Wrap(err, ErrPublish)
	}
	if err := r.cli.Publish(ctx, r.opts.Channel, string(payload)).Err(); err != nil {
		return errx.Wrap(err, ErrPublish)
	}
	return nil
}

// Subscribe blocks until ctx is cancelled, the Pub/Sub channel closes, or a health probe fails.
// | Subscribe 阻塞，直到 ctx 取消、Pub/Sub 频道关闭或探活失败。
func (r *RedisRemote) Subscribe(ctx context.Context, handler func(Event)) error {
	if err := r.check(); err != nil {
		return err
	}
	pubsub := r.cli.Subscribe(ctx, r.opts.Channel)
	defer func() { _ = pubsub.Close() }()
	r.subMu.Lock()
	r.pubsub = pubsub
	r.subMu.Unlock()
	defer func() {
		r.subMu.Lock()
		if r.pubsub == pubsub {
			r.pubsub = nil
		}
		r.subMu.Unlock()
	}()
	ch := pubsub.Channel(redis.WithChannelSize(r.opts.SubscribeBuffer))
	ticker := time.NewTicker(r.opts.HealthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.closeCh:
			return ErrClosed
		case msg, ok := <-ch:
			if !ok {
				return ErrSubscribe
			}
			if ev, valid := decodeEvent(msg.Payload); valid {
				handler(ev)
			}
		case <-ticker.C:
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := pubsub.Ping(probeCtx)
			cancel()
			if err != nil {
				return errx.Wrap(err, ErrHealthCheck)
			}
		}
	}
}

// decodeEvent parses and bounds one published message. | decodeEvent 解析并限制一条已发布消息。
func decodeEvent(payload string) (Event, bool) {
	var wire eventWire
	if err := jsonx.Unmarshal([]byte(payload), &wire); err != nil {
		return Event{}, false
	}
	ev := Event{
		Op:        Op(wire.Op),
		Namespace: wire.NS,
		Keys:      wire.Keys,
		Tables:    wire.Tables,
		Version:   wire.Ver,
		Source:    wire.Src,
	}
	if wire.At > 0 {
		ev.At = time.Unix(0, wire.At).UTC()
	} else {
		ev.At = time.Now().UTC()
	}
	return ev, validEvent(&ev)
}

// Close stops subscriptions and wakes Subscribe; the client is not closed. | Close 停止订阅并唤醒 Subscribe；不关闭客户端。
func (r *RedisRemote) Close() error {
	r.closeOne.Do(func() {
		r.closed.Store(true)
		close(r.closeCh)
		r.subMu.Lock()
		if r.pubsub != nil {
			_ = r.pubsub.Close()
		}
		r.subMu.Unlock()
	})
	return nil
}
