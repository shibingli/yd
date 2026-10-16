package redisx_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/yun-dev/yd/data/redisx"
)

// newMiniRedis starts an in-process Redis server for the test. | newMiniRedis 为测试启动进程内 Redis 服务。
//
// Using miniredis keeps every case free of external dependencies: it registers its own cleanup, | 使用 miniredis 使每个用例零外部依赖：它自行注册清理逻辑，
// so no case can leak a listener or a connection. | 因此不会有用例泄漏监听端口或连接。
func newMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	return miniredis.RunT(t)
}

// openSingle opens a single-node client against mr and closes it when the test ends. | openSingle 针对 mr 打开单机客户端，并在测试结束时关闭。
func openSingle(t *testing.T, mr *miniredis.Miniredis) redis.UniversalClient {
	t.Helper()
	cli, err := redisx.Open(redisx.Options{
		Mode:        redisx.ModeSingle,
		Addr:        mr.Addr(),
		DialTimeout: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open(single) 失败: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

// TestOpenSingleSentinelClusterModes asserts the three modes construct the matching go-redis client type. | TestOpenSingleSentinelClusterModes 断言三种模式各自构造出对应的 go-redis 客户端类型。
func TestOpenSingleSentinelClusterModes(t *testing.T) {
	mr := newMiniRedis(t)

	single, err := redisx.Open(redisx.Options{Mode: redisx.ModeSingle, Addr: mr.Addr()})
	if err != nil {
		t.Fatalf("单机模式 Open 失败: %v", err)
	}
	defer func() { _ = single.Close() }()
	if _, ok := single.(*redis.Client); !ok {
		t.Fatalf("单机模式客户端类型 = %T，期望 *redis.Client", single)
	}

	sentinel, err := redisx.Open(redisx.Options{
		Mode:       redisx.ModeSentinel,
		Addrs:      []string{mr.Addr()},
		MasterName: "mymaster",
	})
	if err != nil {
		t.Fatalf("哨兵模式 Open 失败: %v", err)
	}
	defer func() { _ = sentinel.Close() }()
	if _, ok := sentinel.(*redis.Client); !ok {
		t.Fatalf("哨兵模式客户端类型 = %T，期望 *redis.Client（failover 客户端）", sentinel)
	}

	cluster, err := redisx.Open(redisx.Options{Mode: redisx.ModeCluster, Addrs: []string{mr.Addr()}})
	if err != nil {
		t.Fatalf("集群模式 Open 失败: %v", err)
	}
	defer func() { _ = cluster.Close() }()
	if _, ok := cluster.(*redis.ClusterClient); !ok {
		t.Fatalf("集群模式客户端类型 = %T，期望 *redis.ClusterClient", cluster)
	}

	// 空 Mode 回落到单机（与 config.Defaults 的 redis.mode 默认值一致）。 | An empty Mode falls back to single, matching config.Defaults.
	fallback, err := redisx.Open(redisx.Options{Addr: mr.Addr()})
	if err != nil {
		t.Fatalf("空 Mode Open 失败: %v", err)
	}
	defer func() { _ = fallback.Close() }()
	if _, ok := fallback.(*redis.Client); !ok {
		t.Fatalf("空 Mode 客户端类型 = %T，期望 *redis.Client", fallback)
	}
}

// TestOpenRejectsInvalidOptions asserts every malformed option set is reported through a declared sentinel. | TestOpenRejectsInvalidOptions 断言每种非法参数都通过已声明的哨兵错误上报。
func TestOpenRejectsInvalidOptions(t *testing.T) {
	cases := []struct {
		name string
		o    redisx.Options
		want error
	}{
		{"未知模式", redisx.Options{Mode: "replica", Addr: "127.0.0.1:6379"}, redisx.ErrUnknownMode},
		{"单机缺地址", redisx.Options{Mode: redisx.ModeSingle}, redisx.ErrMissingAddr},
		{"哨兵缺地址", redisx.Options{Mode: redisx.ModeSentinel, MasterName: "m"}, redisx.ErrMissingAddr},
		{"哨兵缺主节点名", redisx.Options{Mode: redisx.ModeSentinel, Addrs: []string{"127.0.0.1:26379"}}, redisx.ErrMissingMasterName},
		{"集群缺地址", redisx.Options{Mode: redisx.ModeCluster}, redisx.ErrMissingAddr},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cli, err := redisx.Open(c.o)
			if cli != nil {
				_ = cli.Close()
				t.Fatalf("非法参数下返回了非 nil 客户端")
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("Open 错误 = %v，期望 errors.Is(err, %v)", err, c.want)
			}
		})
	}
}

// TestHealthReportsConnectionState asserts Health succeeds on a live server and fails after it goes away. | TestHealthReportsConnectionState 断言 Health 在服务存活时成功、在服务消失后失败。
func TestHealthReportsConnectionState(t *testing.T) {
	mr := newMiniRedis(t)
	cli := openSingle(t, mr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := redisx.Health(ctx, cli); err != nil {
		t.Fatalf("存活服务 Health 失败: %v", err)
	}

	mr.Close()
	if err := redisx.Health(ctx, cli); !errors.Is(err, redisx.ErrHealthCheckFailed) {
		t.Fatalf("服务关闭后 Health 错误 = %v，期望包装 ErrHealthCheckFailed", err)
	}

	if err := redisx.Health(ctx, nil); !errors.Is(err, redisx.ErrNilClient) {
		t.Fatalf("nil 客户端 Health 错误 = %v，期望 ErrNilClient", err)
	}
}

// TestOptionsFromConfigReadsRedisSection asserts OptionsFromConfig maps every redis.* key. | TestOptionsFromConfigReadsRedisSection 断言 OptionsFromConfig 映射全部 redis.* 键。
func TestOptionsFromConfigReadsRedisSection(t *testing.T) {
	const yaml = `
redis:
  mode: cluster
  addr: 127.0.0.1:7000
  addrs:
    - 127.0.0.1:7000
    - 127.0.0.1:7001
  username: app
  password: secret
  db: 3
  master_name: mymaster
  pool_size: 64
  min_idle_conns: 8
  dial_timeout: 2s
  read_timeout: 3s
  write_timeout: 4s
  key_prefix: "app:"
`
	cfg := loadConfig(t, yaml)
	got := redisx.OptionsFromConfig(cfg)
	want := redisx.Options{
		Mode:         "cluster",
		Addr:         "127.0.0.1:7000",
		Addrs:        []string{"127.0.0.1:7000", "127.0.0.1:7001"},
		Username:     "app",
		Password:     "secret",
		DB:           3,
		MasterName:   "mymaster",
		PoolSize:     64,
		MinIdleConns: 8,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 4 * time.Second,
		KeyPrefix:    "app:",
	}
	assertOptionsEqual(t, got, want)

	// 空配置回落到 config.Defaults 注册的 redis 默认值。 | An empty config falls back to the redis defaults registered by config.Defaults.
	def := redisx.OptionsFromConfig(loadConfig(t, ""))
	if def.Mode != redisx.ModeSingle || def.Addr != "127.0.0.1:6379" {
		t.Fatalf("默认 Options = %+v，期望 single/127.0.0.1:6379", def)
	}

	// nil 配置返回零值 Options 而不是 panic。 | A nil config yields zero Options instead of panicking.
	if z := redisx.OptionsFromConfig(nil); z != (redisx.Options{}) {
		t.Fatalf("nil 配置 Options = %+v，期望零值", z)
	}
}

// assertOptionsEqual compares two option sets field by field (Options holds a slice, so it is not comparable). | assertOptionsEqual 逐字段比较两组 Options（含切片，struct 本身不可比较）。
func assertOptionsEqual(t *testing.T, got, want redisx.Options) {
	t.Helper()
	if strings.Join(got.Addrs, ",") != strings.Join(want.Addrs, ",") {
		t.Fatalf("Addrs = %v，期望 %v", got.Addrs, want.Addrs)
	}
	got.Addrs, want.Addrs = nil, nil
	if got != want {
		t.Fatalf("Options = %+v，期望 %+v", got, want)
	}
}
