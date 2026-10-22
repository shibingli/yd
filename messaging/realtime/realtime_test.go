package realtime_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/shibingli/yd/messaging"
	"github.com/shibingli/yd/messaging/realtime"
	ydlog "github.com/shibingli/ydx/log"
)

// newRedisClient returns a client against a disposable miniredis. | newRedisClient 返回指向一次性 miniredis 的客户端。
func newRedisClient(t *testing.T, addr string) redis.UniversalClient {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// waitEvent receives one event or fails after the timeout. | waitEvent 在超时前收到一条事件，否则失败。
func waitEvent(t *testing.T, ch <-chan messaging.Event) messaging.Event {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a realtime event | 等待实时事件超时")
		return messaging.Event{}
	}
}

// TestDistributedHubCrossesNodes proves one publish reaches a subscriber on another node exactly once. |
// TestDistributedHubCrossesNodes 证明一次发布恰好到达另一节点上的订阅者一次。
func TestDistributedHubCrossesNodes(t *testing.T) {
	server := miniredis.RunT(t)
	log := ydlog.Default()
	bus1, err := realtime.NewRedisBus(newRedisClient(t, server.Addr()), "yd:test:events", log)
	if err != nil {
		t.Fatalf("bus1: %v", err)
	}
	bus2, err := realtime.NewRedisBus(newRedisClient(t, server.Addr()), "yd:test:events", log)
	if err != nil {
		t.Fatalf("bus2: %v", err)
	}
	hub1 := realtime.NewDistributed(log, bus1, nil, "node-1")
	hub2 := realtime.NewDistributed(log, bus2, nil, "node-2")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub1.Start(ctx)
	hub2.Start(ctx)
	defer hub1.Close()
	defer hub2.Close()

	first := make(chan messaging.Event, 4)
	second := make(chan messaging.Event, 4)
	hub1.Subscribe(realtime.Subscription{TenantID: "tn_1", UserID: "us_1", Deliver: func(event messaging.Event) { first <- event }})
	hub2.Subscribe(realtime.Subscription{TenantID: "tn_1", UserID: "us_1", Deliver: func(event messaging.Event) { second <- event }})

	if err := hub1.Publish(ctx, messaging.Event{
		Type:           messaging.EventMessageCreated,
		TenantID:       "tn_1",
		ConversationID: "cv_1",
		Audience:       []string{"us_1"},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	local := waitEvent(t, first)
	remote := waitEvent(t, second)
	if local.ID == "" || local.Origin != "node-1" {
		t.Fatalf("local event = %+v", local)
	}
	if remote.ID != local.ID || remote.Origin != "node-1" {
		t.Fatalf("remote event = %+v, local = %+v", remote, local)
	}
	// The origin node must not deliver its own bus echo a second time. | 源节点不得再次投递自己的总线回传。
	select {
	case duplicate := <-first:
		t.Fatalf("origin received a duplicate event: %+v", duplicate)
	case <-time.After(250 * time.Millisecond):
	}
}

// failingBus always fails to publish and never subscribes. | failingBus 总是发布失败且从不订阅。
type failingBus struct{}

// Publish fails. | Publish 失败。
func (failingBus) Publish(context.Context, messaging.Event) error {
	return errors.New("bus unavailable")
}

// Subscribe does nothing. | Subscribe 什么也不做。
func (failingBus) Subscribe(context.Context, func(messaging.Event)) (func(), error) {
	return func() {}, nil
}

// Close does nothing. | Close 什么也不做。
func (failingBus) Close() error { return nil }

// recordingRetry records enqueued events. | recordingRetry 记录入队事件。
type recordingRetry struct {
	mu     sync.Mutex
	events []messaging.Event
}

// Enqueue records one event. | Enqueue 记录一条事件。
func (r *recordingRetry) Enqueue(_ context.Context, event messaging.Event) error {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
	return nil
}

// count returns the recorded event count. | count 返回已记录事件数。
func (r *recordingRetry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// TestFailedBroadcastGoesToRetryQueue proves local delivery survives a bus failure and the event is queued. |
// TestFailedBroadcastGoesToRetryQueue 证明总线故障时本地投递仍然成功，且事件进入重试队列。
func TestFailedBroadcastGoesToRetryQueue(t *testing.T) {
	log := ydlog.Default()
	retry := &recordingRetry{}
	hub := realtime.NewDistributed(log, failingBus{}, retry, "node-1")
	defer hub.Close()

	delivered := make(chan messaging.Event, 1)
	hub.Subscribe(realtime.Subscription{TenantID: "tn_1", UserID: "us_1", Deliver: func(event messaging.Event) { delivered <- event }})

	if err := hub.Publish(context.Background(), messaging.Event{
		Type:     messaging.EventMessageCreated,
		TenantID: "tn_1",
		Audience: []string{"us_1"},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	event := waitEvent(t, delivered)
	if event.ID == "" {
		t.Fatal("local event has no id | 本地事件没有 ID")
	}
	if retry.count() != 1 {
		t.Fatalf("retry queue count = %d, want 1 | 重试队列数量 = %d，期望 1", retry.count(), 1)
	}
}

// TestAudienceAndTenantFiltering proves a subscriber only receives its own events. |
// TestAudienceAndTenantFiltering 证明订阅者只收到自己的事件。
func TestAudienceAndTenantFiltering(t *testing.T) {
	hub := realtime.New(ydlog.Default())
	defer hub.Close()
	received := make(chan messaging.Event, 2)
	hub.Subscribe(realtime.Subscription{TenantID: "tn_1", UserID: "us_1", Deliver: func(event messaging.Event) { received <- event }})

	if err := hub.Publish(context.Background(), messaging.Event{Type: "x", TenantID: "tn_2", Audience: []string{"us_1"}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := hub.Publish(context.Background(), messaging.Event{Type: "y", TenantID: "tn_1", Audience: []string{"us_2"}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := hub.Publish(context.Background(), messaging.Event{Type: "z", TenantID: "tn_1", Audience: []string{"us_1"}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	event := waitEvent(t, received)
	if event.Type != "z" {
		t.Fatalf("received %q, want z | 收到 %q，期望 z", event.Type, event.Type)
	}
	select {
	case extra := <-received:
		t.Fatalf("received an unrelated event: %+v", extra)
	case <-time.After(250 * time.Millisecond):
	}
}
