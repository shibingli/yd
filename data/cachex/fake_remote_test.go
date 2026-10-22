package cachex

import (
	"context"
	"sync"
	"time"
)

// fakeEntry is one in-memory L2 record. | fakeEntry 是一条内存 L2 记录。
type fakeEntry struct {
	raw     []byte
	expires time.Time
}

// fakeRemote is an in-memory Remote with a synchronous Pub/Sub. | fakeRemote 是带同步 Pub/Sub 的内存 Remote。
type fakeRemote struct {
	items     map[string]fakeEntry
	versions  map[string]uint64
	subs      map[uint64]func(Event)
	published []Event
	closeCh   chan struct{}
	mu        sync.Mutex
	nextSub   uint64
	loads     int
	stores    int
	failLoad  bool
	failStore bool
	drop      bool
	closed    bool
}

// newFakeRemote builds an empty remote. | newFakeRemote 构造空远端。
func newFakeRemote() *fakeRemote {
	return &fakeRemote{
		items:    make(map[string]fakeEntry),
		versions: make(map[string]uint64),
		subs:     make(map[uint64]func(Event)),
		closeCh:  make(chan struct{}),
	}
}

func (f *fakeRemote) load(key string) (Item, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	if f.failLoad {
		return Item{}, false, ErrRemote
	}
	e, ok := f.items[key]
	if !ok {
		return Item{}, false, nil
	}
	ttl := time.Duration(0)
	if !e.expires.IsZero() {
		remaining := time.Until(e.expires)
		if remaining <= 0 {
			delete(f.items, key)
			delete(f.versions, key)
			return Item{}, false, nil
		}
		ttl = remaining
	}
	return Item{Raw: append([]byte(nil), e.raw...), TTL: ttl}, true, nil
}

func (f *fakeRemote) Load(ctx context.Context, key string) (Item, bool, error) {
	return f.load(key)
}

func (f *fakeRemote) LoadMany(_ context.Context, keys []string) (map[string]Item, error) {
	out := make(map[string]Item, len(keys))
	for _, k := range keys {
		if item, ok, err := f.load(k); err != nil {
			return nil, err
		} else if ok {
			out[k] = item
		}
	}
	return out, nil
}

func (f *fakeRemote) store(key string, data []byte, flags uint8, ttl time.Duration, expect uint64, check bool) (uint64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stores++
	if f.failStore {
		return 0, false, ErrRemote
	}
	cur := f.versions[key]
	if check && cur != expect {
		return cur, false, nil
	}
	version := cur + 1
	f.versions[key] = version
	expires := time.Time{}
	if ttl > 0 {
		expires = time.Now().Add(ttl)
	}
	f.items[key] = fakeEntry{raw: WireEncode(version, flags, data), expires: expires}
	return version, true, nil
}

func (f *fakeRemote) Store(_ context.Context, key string, data []byte, flags uint8, ttl time.Duration) (uint64, error) {
	v, _, err := f.store(key, data, flags, ttl, 0, false)
	return v, err
}

func (f *fakeRemote) StoreIf(_ context.Context, key string, data []byte, flags uint8, ttl time.Duration, expect uint64) (uint64, bool, error) {
	return f.store(key, data, flags, ttl, expect, true)
}

func (f *fakeRemote) Remove(_ context.Context, key string, flags uint8, ttl time.Duration) (uint64, error) {
	v, _, err := f.store(key, nil, flags, ttl, 0, false)
	return v, err
}

func (f *fakeRemote) Publish(_ context.Context, ev Event) error {
	f.mu.Lock()
	f.published = append(f.published, ev)
	if f.drop {
		f.mu.Unlock()
		return nil
	}
	subs := make([]func(Event), 0, len(f.subs))
	for _, fn := range f.subs {
		subs = append(subs, fn)
	}
	f.mu.Unlock()
	for _, fn := range subs {
		fn(ev)
	}
	return nil
}

func (f *fakeRemote) Subscribe(ctx context.Context, handler func(Event)) error {
	f.mu.Lock()
	f.nextSub++
	subID := f.nextSub
	f.subs[subID] = handler
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.subs, subID)
		f.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil
	case <-f.closeCh:
		return ErrClosed
	}
}

func (f *fakeRemote) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	close(f.closeCh)
	f.mu.Unlock()
	return nil
}

// setDrop controls whether published events reach subscribers. | setDrop 控制已发布事件是否送达订阅者。
func (f *fakeRemote) setDrop(drop bool) {
	f.mu.Lock()
	f.drop = drop
	f.mu.Unlock()
}

// setFailures controls load/store failures. | setFailures 控制读/写失败。
func (f *fakeRemote) setFailures(load, store bool) {
	f.mu.Lock()
	f.failLoad = load
	f.failStore = store
	f.mu.Unlock()
}

// counts returns load and store call counts. | counts 返回读取与写入调用次数。
func (f *fakeRemote) counts() (loads, stores int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loads, f.stores
}

// value returns the decoded payload and version stored under key. | value 返回 key 下已解码的载荷与版本。
func (f *fakeRemote) value(key string) (data []byte, version uint64, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, exists := f.items[key]
	if !exists {
		return nil, 0, false
	}
	ver, _, payload, valid := WireDecode(e.raw)
	if !valid {
		return nil, 0, false
	}
	return payload, ver, true
}

// events returns a copy of every published event. | events 返回所有已发布事件的副本。
func (f *fakeRemote) events() []Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Event(nil), f.published...)
}
