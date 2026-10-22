package cachex

import (
	"context"
	"errors"
	"testing"
	"time"
)

// testUser is a representative JSON value. | testUser 是代表性的 JSON 值。
type testUser struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

func TestTypedCacheCRUD(t *testing.T) {
	m := newTestManager(t, nil)
	c, err := New[testUser](m, "typed", WithTTL(time.Minute))
	if err != nil {
		t.Fatalf("New: %v | 构造失败", err)
	}
	if c.Namespace() != "typed" || c.Raw() == nil {
		t.Fatal("accessors returned wrong values | 访问器返回值不符")
	}
	ctx := context.Background()
	want := testUser{ID: 7, Name: "ada"}
	if err := c.Set(ctx, "u:7", want); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	got, ok, err := c.Get(ctx, "u:7")
	if err != nil || !ok || got != want {
		t.Fatalf("Get = %+v,%v,%v | 读取结果不符", got, ok, err)
	}
	var viewed testUser
	found, err := c.View(ctx, "u:7", func(v testUser) error {
		viewed = v
		return nil
	})
	if err != nil || !found || viewed != want {
		t.Fatalf("View = %+v,%v,%v | 视图结果不符", viewed, found, err)
	}
	if err := c.Delete(ctx, "u:7"); err != nil {
		t.Fatalf("Delete: %v | 删除失败", err)
	}
	if _, ok, _ := c.Get(ctx, "u:7"); ok {
		t.Fatal("deleted value still present | 删除后的值仍存在")
	}
}

func TestTypedGetOrLoad(t *testing.T) {
	m := newTestManager(t, nil)
	c, _ := New[testUser](m, "loader", WithNegativeTTL(time.Minute))
	ctx := context.Background()
	want := testUser{ID: 1, Name: "one"}
	got, err := c.GetOrLoad(ctx, "u:1", func(context.Context) (testUser, error) { return want, nil })
	if err != nil || got != want {
		t.Fatalf("GetOrLoad = %+v,%v | 加载结果不符", got, err)
	}
	got, err = c.GetOrLoad(ctx, "u:1", func(context.Context) (testUser, error) {
		t.Fatal("loader called on a hit | 命中时仍调用了 loader")
		return testUser{}, nil
	})
	if err != nil || got != want {
		t.Fatalf("second GetOrLoad = %+v,%v | 第二次加载结果不符", got, err)
	}
	if _, err := c.GetOrLoad(ctx, "u:2", func(context.Context) (testUser, error) {
		return testUser{}, ErrNotFound
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("negative GetOrLoad = %v | 负缓存加载错误不符", err)
	}
	if _, ok, _ := c.Get(ctx, "u:2"); ok {
		t.Fatal("negative entry reported as a hit | 负缓存被报告为命中")
	}
	if _, err := c.GetOrLoad(ctx, "u:3", func(context.Context) (testUser, error) {
		return testUser{}, ErrNotFound
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("negative GetOrLoad = %v | 负缓存加载错误不符", err)
	}
}

func TestTypedCodecs(t *testing.T) {
	m := newTestManager(t, nil)
	ctx := context.Background()
	str, err := NewWithCodec[string](m, "str", String())
	if err != nil {
		t.Fatalf("NewWithCodec: %v | 构造失败", err)
	}
	if err := str.Set(ctx, "k", "hello"); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if got, ok, _ := str.Get(ctx, "k"); !ok || got != "hello" {
		t.Fatalf("string Get = %q,%v | 字符串读取不符", got, ok)
	}
	raw, err := NewWithCodec[[]byte](m, "raw", Bytes())
	if err != nil {
		t.Fatalf("NewWithCodec: %v | 构造失败", err)
	}
	source := []byte("bytes")
	if err := raw.Set(ctx, "k", source); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	source[0] = 'X'
	got, ok, _ := raw.Get(ctx, "k")
	if !ok || string(got) != "bytes" {
		t.Fatalf("bytes Get = %q,%v | 字节读取不符", got, ok)
	}
	got[0] = 'Y'
	again, _, _ := raw.Get(ctx, "k")
	if string(again) != "bytes" {
		t.Fatal("Bytes codec leaked a shared buffer | Bytes 编解码器泄漏了共享缓冲区")
	}
}

func TestTypedCorruptPayloadSelfHeals(t *testing.T) {
	m := newTestManager(t, nil)
	c, _ := New[testUser](m, "heal")
	ctx := context.Background()
	if err := m.local.Set("heal:bad", []byte("not-an-envelope"), time.Minute); err != nil {
		t.Fatalf("seed: %v | 播种失败", err)
	}
	if _, ok, err := c.Get(ctx, "bad"); err != nil || ok {
		t.Fatalf("corrupt envelope Get = %v,%v | 损坏信封读取结果不符", ok, err)
	}
	if err := m.local.Set("heal:json", WireEncode(0, FlagNone, []byte("{")), time.Minute); err != nil {
		t.Fatalf("seed: %v | 播种失败", err)
	}
	if _, ok, err := c.Get(ctx, "json"); err != nil || ok {
		t.Fatalf("corrupt JSON Get = %v,%v | 损坏 JSON 读取结果不符", ok, err)
	}
	if err := m.local.Set("heal:json", WireEncode(0, FlagNone, []byte("{")), time.Minute); err != nil {
		t.Fatalf("seed: %v | 播种失败", err)
	}
	if err := c.Set(ctx, "json", testUser{ID: 1, Name: "ok"}); err != nil {
		t.Fatalf("Set: %v | 写入失败", err)
	}
	if got, ok, _ := c.Get(ctx, "json"); !ok || got.ID != 1 {
		t.Fatalf("repaired Get = %+v,%v | 修复后读取不符", got, ok)
	}
}

func TestTypedInvalidCodec(t *testing.T) {
	m := newTestManager(t, nil)
	if _, err := NewWithCodec[testUser](m, "bad-codec", Codec[testUser]{}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("incomplete codec = %v | 不完整编解码器错误不符", err)
	}
	if _, err := NewWithCodec[testUser](nil, "nil", JSON[testUser]()); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("nil manager = %v | nil 管理器错误不符", err)
	}
}

func TestTypedGetOrLoadView(t *testing.T) {
	m := newTestManager(t, nil)
	c, _ := New[testUser](m, "loader-view")
	ctx := context.Background()
	want := testUser{ID: 9, Name: "nine"}
	var seen testUser
	err := c.GetOrLoadView(ctx, "u:9", 0, func(context.Context) (testUser, error) { return want, nil }, func(v testUser) error {
		seen = v
		return nil
	})
	if err != nil || seen != want {
		t.Fatalf("GetOrLoadView = %+v,%v | 零拷贝读透结果不符", seen, err)
	}
	err = c.GetOrLoadView(ctx, "u:9", 0, func(context.Context) (testUser, error) {
		t.Fatal("loader called on a hit | 命中时仍调用了 loader")
		return testUser{}, nil
	}, func(v testUser) error {
		seen = v
		return nil
	})
	if err != nil || seen != want {
		t.Fatalf("second GetOrLoadView = %+v,%v | 第二次零拷贝读透结果不符", seen, err)
	}
}
