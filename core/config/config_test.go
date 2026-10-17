package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/spf13/afero"
	"github.com/spf13/pflag"

	"github.com/shibingli/yd/core/config"
)

// watchTimeout is the deadline for waiting on a config file watch callback. | watchTimeout 是等待配置文件监视回调的时限。
// fsnotify delivery latency on Windows is higher than on Linux, so a generous 500ms upper bound is used here. | Windows 上 fsnotify 的投递延迟高于 Linux，这里给出 500ms 的宽松上限。
const watchTimeout = 500 * time.Millisecond

// writeTestFile writes a test fixture on the real filesystem. | writeTestFile 在真实文件系统上写入测试夹具。
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
}

// writeMemFile writes a test fixture on an in-memory filesystem. | writeMemFile 在内存文件系统上写入测试夹具。
func writeMemFile(t *testing.T, fs afero.Fs, path, content string) {
	t.Helper()
	if err := afero.WriteFile(fs, path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入内存文件 %s 失败: %v", path, err)
	}
}

// absentEnvFile returns a guaranteed-nonexistent .env path, | absentEnvFile 返回一个必定不存在的 .env 路径，
// so tests do not accidentally read a developer's local .env in the repository root. | 避免测试意外读取到仓库根目录下开发者本地的 .env。
func absentEnvFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "absent.env")
}

// isolateEnv first clears the given environment variables and restores their original values after the test, | isolateEnv 先清空给定环境变量，并在测试结束后恢复其原有取值，
// keeping assertions between .env injection and real environment variables deterministic. | 使 .env 注入与真实环境变量之间的断言保持确定性。
func isolateEnv(t *testing.T, keys ...string) {
	t.Helper()
	type snapshot struct {
		key   string
		value string
		set   bool
	}
	saved := make([]snapshot, 0, len(keys))
	for _, k := range keys {
		v, ok := os.LookupEnv(k)
		saved = append(saved, snapshot{key: k, value: v, set: ok})
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("清空环境变量 %s 失败: %v", k, err)
		}
	}
	t.Cleanup(func() {
		for _, s := range saved {
			if s.set {
				_ = os.Setenv(s.key, s.value)
				continue
			}
			_ = os.Unsetenv(s.key)
		}
	})
}

// TestPriorityFlagBeatsEnvAndFile covers Review Focus #2: a pflag with a non-empty default value | TestPriorityFlagBeatsEnvAndFile 覆盖 Review Focus #2：带非空默认值的 pflag
// must not override environment variables or config files when not explicitly passed; when explicitly passed, the command line must win. | 在未显式传参时不得覆盖环境变量与配置文件；显式传参时命令行必须最高优先级。
func TestPriorityFlagBeatsEnvAndFile(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "config.yaml"), "server:\n  port: 8080\n")
	t.Setenv("YD_SERVER_PORT", "9090")

	fs := pflag.NewFlagSet("priority", pflag.ContinueOnError)
	fs.Int("port", 8080, "监听端口")

	c, err := config.Load(config.Options{
		Name:       "priority",
		ConfigDir:  dir,
		ConfigName: "config",
		EnvFile:    absentEnvFile(t),
	})
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	mapping := map[string]string{"port": "server.port"}
	if err := c.BindFlags(fs, mapping); err != nil {
		t.Fatalf("BindFlags 失败: %v", err)
	}

	// flag not Changed (value is the default 8080); env must beat both the file and the flag default. | flag 未 Changed（值为默认值 8080），env 必须胜过文件与 flag 默认值。
	if got := c.GetInt("server.port"); got != 9090 {
		t.Fatalf("未传参时 server.port = %d，期望环境变量胜出为 9090", got)
	}

	// After being passed explicitly, the command line has the highest precedence. | 显式传参后命令行最高优先级。
	if err := fs.Set("port", "7070"); err != nil {
		t.Fatalf("设置 flag 失败: %v", err)
	}
	if err := c.BindFlags(fs, mapping); err != nil {
		t.Fatalf("重新 BindFlags 失败: %v", err)
	}
	if got := c.GetInt("server.port"); got != 7070 {
		t.Fatalf("显式传参后 server.port = %d，期望 7070", got)
	}
}

// TestUnchangedFlagDefaultLosesToConfigFile asserts that an unchanged flag's default value | TestUnchangedFlagDefaultLosesToConfigFile 断言未 Changed 的 flag 默认值
// serves only as the lowest-precedence layer, and the config file still wins. | 只作为最低优先级层，配置文件仍然胜出。
func TestUnchangedFlagDefaultLosesToConfigFile(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "config.yaml"), "server:\n  port: 6080\n")
	isolateEnv(t, "YD_SERVER_PORT")

	fs := pflag.NewFlagSet("file-wins", pflag.ContinueOnError)
	fs.Int("port", 8080, "监听端口")

	c, err := config.Load(config.Options{ConfigDir: dir, EnvFile: absentEnvFile(t)})
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if err := c.BindFlags(fs, map[string]string{"port": "server.port"}); err != nil {
		t.Fatalf("BindFlags 失败: %v", err)
	}
	if got := c.GetInt("server.port"); got != 6080 {
		t.Fatalf("server.port = %d，期望配置文件值 6080 胜过 flag 默认值 8080", got)
	}
}

// TestUnchangedFlagDefaultInjectedAsTypedDefault asserts that an unchanged flag's default value | TestUnchangedFlagDefaultInjectedAsTypedDefault 断言未 Changed 的 flag 默认值
// is parsed by pflag type and injected into the **default layer**, rather than registered as a flag binding: | 按 pflag 类型解析后注入**默认值层**，而不是登记成旗标绑定：
// the retrieved value must be a strong type such as time.Duration (BindPFlag would return the string verbatim), | 取值必须是 time.Duration 这类强类型（BindPFlag 会原样返回字符串），
// and IsSet must be true (an unchanged default value from a flag binding is not visible to IsSet). | 且 IsSet 必须为真（旗标绑定的未变更默认值不会被 IsSet 看到）。
func TestUnchangedFlagDefaultInjectedAsTypedDefault(t *testing.T) {
	fs := pflag.NewFlagSet("typed-defaults", pflag.ContinueOnError)
	fs.Duration("ttl", 30*time.Second, "缓存生存期")
	fs.Int("size", 7, "缓存容量")
	fs.Bool("enabled", true, "是否启用缓存")
	fs.Float64("ratio", 0.5, "命中率阈值")
	fs.String("label", "yd", "标签")

	c, err := config.Load(config.Options{ConfigDir: t.TempDir(), EnvFile: absentEnvFile(t)})
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if err := c.BindFlags(fs, map[string]string{
		"ttl":     "cache.ttl",
		"size":    "cache.size",
		"enabled": "cache.enabled",
		"ratio":   "cache.ratio",
		"label":   "cache.label",
	}); err != nil {
		t.Fatalf("BindFlags 失败: %v", err)
	}

	if got := c.Get("cache.ttl"); got != 30*time.Second {
		t.Fatalf("cache.ttl = %#v，期望 time.Duration(30s)", got)
	}
	if got := c.Duration("cache.ttl"); got != 30*time.Second {
		t.Fatalf("Duration(cache.ttl) = %v，期望 30s", got)
	}
	if !c.IsSet("cache.ttl") {
		t.Fatal("IsSet(cache.ttl) = false; a flag default must reach the defaults layer and stay visible | IsSet(cache.ttl) = false，期望 flag 默认值进入默认值层从而可见")
	}
	if got := c.Get("cache.size"); got != 7 {
		t.Fatalf("cache.size = %#v，期望 int(7)", got)
	}
	if got := c.Get("cache.enabled"); got != true {
		t.Fatalf("cache.enabled = %#v，期望 bool(true)", got)
	}
	if got := c.Get("cache.ratio"); got != 0.5 {
		t.Fatalf("cache.ratio = %#v，期望 float64(0.5)", got)
	}
	if got := c.Get("cache.label"); got != "yd" {
		t.Fatalf("cache.label = %#v，期望 string(\"yd\")", got)
	}
}

// TestNestedEnvMappingFromDotEnv asserts that YD_DB_MAXCONN in .env maps to | TestNestedEnvMappingFromDotEnv 断言 .env 中的 YD_DB_MAXCONN 能映射到
// the nested key db.maxconn, and that parsing supports comments, blank lines, quotes and the export prefix. | 嵌套键 db.maxconn，并且解析支持注释、空行、引号与 export 前缀。
func TestNestedEnvMappingFromDotEnv(t *testing.T) {
	isolateEnv(t,
		"YD_DB_MAXCONN",
		"YD_LOG_LEVEL",
		"YD_APP_ENV",
		"YD_REDIS_ADDR",
	)

	mem := afero.NewMemMapFs()
	writeMemFile(t, mem, ".env", `# yd 本地环境变量
export YD_DB_MAXCONN=42

YD_LOG_LEVEL='debug'
YD_APP_ENV="staging"
YD_REDIS_ADDR=127.0.0.1:6380 # 行内注释
`)

	c, err := config.Load(config.Options{
		Name:      "dotenv",
		ConfigDir: ".",
		EnvFile:   ".env",
		FS:        mem,
	})
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	if got := c.GetInt("db.maxconn"); got != 42 {
		t.Fatalf("db.maxconn = %d，期望 .env 注入的 42", got)
	}
	if got := c.GetString("log.level"); got != "debug" {
		t.Fatalf("log.level = %q，期望单引号值 debug", got)
	}
	if got := c.GetString("app.env"); got != "staging" {
		t.Fatalf("app.env = %q，期望双引号值 staging", got)
	}
	if got := c.GetString("redis.addr"); got != "127.0.0.1:6380" {
		t.Fatalf("redis.addr = %q，期望剥除行内注释后的 127.0.0.1:6380", got)
	}
}

// TestTypedUnmarshal asserts that the layered merge result can be strongly-typed decoded into a struct. | TestTypedUnmarshal 断言分层合并结果可以强类型解码到结构体。
func TestTypedUnmarshal(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "config.yaml"), "server:\n  port: 8080\n")
	t.Setenv("YD_SERVER_PORT", "9090")

	c, err := config.Load(config.Options{ConfigDir: dir, EnvFile: absentEnvFile(t)})
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	// Note: snake_case config keys must be mapped to Go field names via mapstructure tags. | 注意：snake_case 的配置键必须通过 mapstructure tag 映射到 Go 字段名。
	var got struct {
		Server struct {
			Port        int           `mapstructure:"port"`
			ReadTimeout time.Duration `mapstructure:"read_timeout"`
			H2C         bool          `mapstructure:"h2c"`
		} `mapstructure:"server"`
		Log struct {
			Level  string `mapstructure:"level"`
			Format string `mapstructure:"format"`
		} `mapstructure:"log"`
		DB struct {
			Driver        string        `mapstructure:"driver"`
			MaxConn       int           `mapstructure:"maxconn"`
			SlowThreshold time.Duration `mapstructure:"slow_threshold"`
			PrepareStmt   bool          `mapstructure:"prepare_stmt"`
		} `mapstructure:"db"`
	}
	if err := c.Unmarshal(&got); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}

	if got.Server.Port != 9090 {
		t.Fatalf("Server.Port = %d，期望环境变量覆盖后的 9090", got.Server.Port)
	}
	if got.Server.ReadTimeout != 15*time.Second {
		t.Fatalf("Server.ReadTimeout = %v，期望 15s", got.Server.ReadTimeout)
	}
	if !got.Server.H2C {
		t.Fatalf("Server.H2C = false，期望默认 true")
	}
	if got.Log.Level != "info" || got.Log.Format != "json" {
		t.Fatalf("Log = %+v，期望 {info json}", got.Log)
	}
	if got.DB.Driver != "sqlite" || got.DB.MaxConn != 100 {
		t.Fatalf("DB = %+v，期望 sqlite/100", got.DB)
	}
	if got.DB.SlowThreshold != 200*time.Millisecond {
		t.Fatalf("DB.SlowThreshold = %v，期望 200ms", got.DB.SlowThreshold)
	}
	if !got.DB.PrepareStmt {
		t.Fatalf("DB.PrepareStmt = false，期望默认 true")
	}
}

// TestConfigLocalOverridesBase asserts that config.local.<ext> overrides config.<ext>, | TestConfigLocalOverridesBase 断言 config.local.<ext> 覆盖 config.<ext>，
// and that keys which are not overridden keep their base values (deep merge rather than wholesale replacement). | 且未被覆盖的键保持基础值（深合并而非整体替换）。
func TestConfigLocalOverridesBase(t *testing.T) {
	mem := afero.NewMemMapFs()
	writeMemFile(t, mem, "config.yaml", "server:\n  port: 8080\napp:\n  name: base\n")
	writeMemFile(t, mem, "config.local.yaml", "server:\n  port: 8181\napp:\n  local: true\n")

	c, err := config.Load(config.Options{
		Name:       "local",
		ConfigDir:  ".",
		ConfigName: "config",
		EnvFile:    "absent.env",
		FS:         mem,
	})
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	if got := c.GetInt("server.port"); got != 8181 {
		t.Fatalf("server.port = %d，期望 config.local.yaml 的 8181", got)
	}
	if got := c.GetString("app.name"); got != "base" {
		t.Fatalf("app.name = %q，期望保留基础文件的 base", got)
	}
	if got := c.GetBool("app.local"); !got {
		t.Fatalf("app.local = false，期望 config.local.yaml 新增的 true")
	}
}

// TestWatchReload asserts that the watch callback fires after a file change and that it sees the reloaded values. | TestWatchReload 断言文件变更后监视回调被触发，且回调能看到重新加载后的取值。
func TestWatchReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	writeTestFile(t, path, "server:\n  port: 8080\n")

	c, err := config.Load(config.Options{
		Name:      "watch",
		ConfigDir: dir,
		EnvFile:   absentEnvFile(t),
	})
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if got := c.GetInt("server.port"); got != 8080 {
		t.Fatalf("初次加载 server.port = %d，期望 8080", got)
	}

	var (
		mu    sync.Mutex
		calls int
		port  int
	)
	fired := make(chan struct{}, 1)

	c.Watch(func(cc *config.Config) {
		mu.Lock()
		calls++
		port = cc.GetInt("server.port")
		mu.Unlock()
		select {
		case fired <- struct{}{}:
		default:
		}
	})

	if err := c.WatchErr(); err != nil {
		t.Fatalf("Watch 前置条件失败: %v", err)
	}

	writeTestFile(t, path, "server:\n  port: 8181\n  read_timeout: 9s\n")

	select {
	case <-fired:
	case <-time.After(watchTimeout):
		mu.Lock()
		got := calls
		mu.Unlock()
		t.Fatalf("%v 内未收到文件变更回调（calls=%d, WatchErr=%v）", watchTimeout, got, c.WatchErr())
	}

	// Drain possibly duplicated fsnotify events: all writes happen inside the watcher goroutine. | 排空可能重复的 fsnotify 事件：所有写操作都发生在监视 goroutine 内。
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if calls < 1 {
		t.Fatalf("回调调用次数 = %d，期望至少 1 次", calls)
	}
	if port != 8181 {
		t.Fatalf("回调内 server.port = %d，期望重新加载后的 8181", port)
	}
}

// TestBindFlagsFallsBackToOptionsFlags asserts that passing nil to BindFlags | TestBindFlagsFallsBackToOptionsFlags 断言 BindFlags 传入 nil 时
// falls back to the flag set provided by Options.Flags. | 回退到 Options.Flags 提供的标志集。
func TestBindFlagsFallsBackToOptionsFlags(t *testing.T) {
	fs := pflag.NewFlagSet("options-flags", pflag.ContinueOnError)
	fs.String("host", "0.0.0.0", "监听地址")

	c, err := config.Load(config.Options{
		ConfigDir: t.TempDir(),
		EnvFile:   absentEnvFile(t),
		Flags:     fs,
	})
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if err := c.BindFlags(nil, map[string]string{"host": "server.host"}); err != nil {
		t.Fatalf("BindFlags 失败: %v", err)
	}
	if got := c.GetString("server.host"); got != "0.0.0.0" {
		t.Fatalf("server.host = %q，期望 Options.Flags 的默认值 0.0.0.0", got)
	}
}

// TestUnknownFlagReturnsError asserts that referencing a nonexistent flag in the mapping returns a checkable error. | TestUnknownFlagReturnsError 断言映射引用了不存在的 flag 时返回可判定的错误。
func TestUnknownFlagReturnsError(t *testing.T) {
	fs := pflag.NewFlagSet("unknown", pflag.ContinueOnError)
	fs.String("port", "", "监听端口")

	c, err := config.Load(config.Options{ConfigDir: t.TempDir(), EnvFile: absentEnvFile(t)})
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	err = c.BindFlags(fs, map[string]string{"nope": "server.host"})
	if err == nil {
		t.Fatal("BindFlags returned no error, want ErrUnknownFlag | BindFlags 未返回错误，期望 ErrUnknownFlag")
	}
	if !errors.Is(err, config.ErrUnknownFlag) {
		t.Fatalf("错误 %v 未包装 ErrUnknownFlag", err)
	}
}

// TestDefaultsAppliedWhenNothingSet asserts that all safety-net defaults take effect with no file, no env and no flag. | TestDefaultsAppliedWhenNothingSet 断言无文件、无 env、无 flag 时全部安全底线默认值生效。
func TestDefaultsAppliedWhenNothingSet(t *testing.T) {
	c, err := config.Load(config.Options{
		Name:      "defaults",
		ConfigDir: t.TempDir(),
		EnvFile:   absentEnvFile(t),
	})
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	cases := []struct {
		key  string
		want any
	}{
		{"server.host", ""},
		{"server.port", 8080},
		{"server.read_timeout", 15 * time.Second},
		{"server.write_timeout", 15 * time.Second},
		{"server.idle_timeout", 60 * time.Second},
		{"server.shutdown_timeout", 10 * time.Second},
		{"server.h2c", true},
		{"server.http3.enabled", false},
		{"server.acme.enabled", false},
		{"log.level", "info"},
		{"log.format", "json"},
		{"app.name", "yd"},
		{"app.env", "development"},
		{"db.driver", "sqlite"},
		{"db.maxconn", 100},
		{"db.maxidle", 10},
		{"db.slow_threshold", "200ms"},
		{"db.prepare_stmt", true},
		{"redis.mode", "single"},
		{"redis.addr", "127.0.0.1:6379"},
		{"asynq.queues", "critical:3,default:2,low:1"},
		{"asynq.concurrency", 10},
		{"grpc.enabled", false},
		{"grpc.mode", "same-port"},
		{"i18n.default_lang", "zh-CN"},
		{"tailwind.enabled", true},
	}
	for _, tc := range cases {
		if got := c.Get(tc.key); got != tc.want {
			t.Errorf("默认值 %s = %#v，期望 %#v", tc.key, got, tc.want)
		}
	}

	if got := c.Duration("server.read_timeout"); got != 15*time.Second {
		t.Errorf("Duration(server.read_timeout) = %v，期望 15s", got)
	}
	if got := c.Duration("db.slow_threshold"); got != 200*time.Millisecond {
		t.Errorf("Duration(db.slow_threshold) = %v，期望 200ms", got)
	}
}

// TestEnvDoesNotOverrideRealEnvVar asserts that .env never overrides an already existing real environment variable. | TestEnvDoesNotOverrideRealEnvVar 断言 .env 永不覆盖已存在的真实环境变量。
func TestEnvDoesNotOverrideRealEnvVar(t *testing.T) {
	t.Setenv("YD_APP_NAME", "from-real-env")

	mem := afero.NewMemMapFs()
	writeMemFile(t, mem, ".env", "YD_APP_NAME=from-dotenv\n")

	c, err := config.Load(config.Options{
		Name:      "dotenv-guard",
		ConfigDir: ".",
		EnvFile:   ".env",
		FS:        mem,
	})
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	if got := c.GetString("app.name"); got != "from-real-env" {
		t.Fatalf("app.name = %q，期望真实环境变量胜出为 from-real-env", got)
	}
	if got := os.Getenv("YD_APP_NAME"); got != "from-real-env" {
		t.Fatalf("真实环境变量被改写为 %q，期望保持 from-real-env", got)
	}
}
