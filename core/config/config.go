// Package config implements the yd framework's multi-source layered configuration engine. | Package config 实现 yd 框架的多源分层配置引擎。
//
// Value precedence (high to low): | 取值优先级（高 → 低）：
//
//	命令行参数（显式传入的 pflag）> 环境变量（含 .env 注入）> 配置文件 > 默认值
//
// The environment variable prefix is fixed as YD, and nested keys map through underscores: | 环境变量前缀固定为 YD，嵌套键通过下划线映射：
// YD_DB_MAXCONN → db.maxconn. Config files support yaml / yml / toml / json, | YD_DB_MAXCONN → db.maxconn。配置文件支持 yaml / yml / toml / json，
// and are deep-merged in the order config.<ext> then config.local.<ext> (local overrides base). | 并按 config.<ext> 与 config.local.<ext> 的顺序深合并（local 覆盖 base）。
//
// Responsibility boundary: this package only handles "reading, layered merging, watching changes"; it does not parse business semantics or check value validity, | 职责边界：本包只负责「读取、分层合并、监视变更」，不解析业务语义、不做取值合法性
// nor does it wire up subsystems such as logging/database/server (each subpackage maps those through its own OptionsFromConfig). | 校验，也不装配日志/数据库/服务器等子系统（由各子包通过各自的 OptionsFromConfig 映射）。
//
// Key correctness constraint (Review Focus #2): a pflag with a non-empty default value, when not explicitly passed, | 关键正确性约束（Review Focus #2）：带非空默认值的 pflag 在未显式传参时
// must never override environment variables or config files. viper still falls back to DefValue for "unchanged flags", | 绝不能覆盖环境变量与配置文件。viper 对「未变更的旗标」仍会回落到 DefValue，
// so BindFlags calls BindPFlag only for flags that are Changed; | 因此 BindFlags 只对 Changed 的标志调用 BindPFlag；
// for flags that are not Changed it instead injects the type-parsed DefValue into the default layer (lowest precedence). | 未 Changed 的标志改为把按类型解析后的 DefValue 注入默认值层（最低优先级）。
package config

import (
	"bytes"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/shibingli/yd/core/errx"
	"github.com/spf13/afero"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	// DefaultEnvPrefix is the framework's conventional environment variable prefix; YD_DB_MAXCONN maps to db.maxconn. | DefaultEnvPrefix 是框架约定的环境变量前缀，YD_DB_MAXCONN 映射到 db.maxconn。
	DefaultEnvPrefix = "YD"
	// DefaultConfigName is the base name of the config file (without extension). | DefaultConfigName 是配置文件的基础名（不含扩展名）。
	DefaultConfigName = "config"
	// DefaultConfigDir is the default search directory for config files and .env. | DefaultConfigDir 是配置文件与 .env 的默认搜索目录。
	DefaultConfigDir = "."
	// DefaultEnvFileName is the default .env file name. | DefaultEnvFileName 是默认的 .env 文件名。
	DefaultEnvFileName = ".env"
)

// configExts lists the explicitly supported config file extensions; earlier entries take precedence. | configExts 是显式支持的配置文件扩展名，靠前者优先。
var configExts = []string{"yaml", "yml", "toml", "json"}

var (
	// ErrUnknownFlag indicates that a BindFlags mapping referenced a flag name that does not exist in the FlagSet. | ErrUnknownFlag 表示 BindFlags 的映射引用了 FlagSet 中不存在的标志名。
	ErrUnknownFlag = errx.New("config: unknown command-line flag | config：未知的命令行标志")
	// ErrWatchUnavailable indicates that the current config instance cannot watch files: | ErrWatchUnavailable 表示当前配置实例不具备文件监视条件：
	// no config file was found, or the file is not visible to the operating system (e.g. an in-memory filesystem). | 未找到配置文件，或该文件对操作系统不可见（例如内存文件系统）。
	ErrWatchUnavailable = errx.New("config: config file cannot be watched | config：无法监视配置文件")

	// ErrFlagMapping indicates a flag mapping entry is not usable: no FlagSet was provided, or a flag has no config key. | ErrFlagMapping 表示标志映射不可用：未提供 FlagSet，或标志未指定配置键。
	ErrFlagMapping = errx.New("config: invalid flag mapping | config：非法的标志映射")
	// ErrFlagBind indicates viper failed to bind a flag to its config key. | ErrFlagBind 表示 viper 绑定标志到配置键失败。
	ErrFlagBind = errx.New("config: failed to bind flag | config：绑定标志失败")
	// ErrBadFlagDefault indicates a flag's default value cannot be parsed into its declared type. | ErrBadFlagDefault 表示标志的默认值无法按声明类型解析。
	ErrBadFlagDefault = errx.New("config: flag default value cannot be parsed | config：标志默认值无法解析")
	// ErrUnmarshal indicates the layered configuration could not be decoded into the target struct. | ErrUnmarshal 表示分层配置无法解码到目标结构体。
	ErrUnmarshal = errx.New("config: failed to unmarshal config | config：解码配置失败")
	// ErrProbeConfigFile indicates probing the config file path failed with an error other than "not exist". | ErrProbeConfigFile 表示探测配置文件路径失败，且错误并非「不存在」。
	ErrProbeConfigFile = errx.New("config: failed to probe config file | config：探测配置文件失败")
	// ErrReadConfigFile indicates the config file could not be read. | ErrReadConfigFile 表示无法读取配置文件。
	ErrReadConfigFile = errx.New("config: failed to read config file | config：读取配置文件失败")
	// ErrParseConfigFile indicates the config file content could not be parsed. | ErrParseConfigFile 表示无法解析配置文件内容。
	ErrParseConfigFile = errx.New("config: failed to parse config file | config：解析配置文件失败")
)

// Options describes the input parameters of a single Load; the zero value uses the framework's conventional defaults. | Options 描述一次 Load 的输入参数，零值即采用框架约定默认。
type Options struct {
	// FS is the filesystem used to read config files and .env, defaulting to afero.NewOsFs(). | FS 是读取配置文件与 .env 的文件系统，默认为 afero.NewOsFs()。
	FS afero.Fs
	// Flags is the optional command-line flag set; passing nil to BindFlags falls back to this value. | Flags 是可选命令行标志集；BindFlags 传入 nil 时会回退到该值。
	Flags *pflag.FlagSet
	// Name is the config instance name, used only for diagnostics (e.g. watch error messages). | Name 是配置实例名称，仅用于诊断信息（例如监视错误提示）。
	Name string
	// ConfigDir is the config file search directory, defaulting to ".". | ConfigDir 是配置文件搜索目录，默认为 "."。
	ConfigDir string
	// ConfigName is the config file base name, defaulting to "config"; | ConfigName 是配置文件基础名，默认为 "config"；
	// it may also be written as "config.yaml" to lock the extension. | 也可以写成 "config.yaml" 以锁定扩展名。
	ConfigName string
	// EnvPrefix is the environment variable prefix, defaulting to "YD". | EnvPrefix 是环境变量前缀，默认为 "YD"。
	EnvPrefix string
	// EnvFile is the .env file path, defaulting to ".env". | EnvFile 是 .env 文件路径，默认为 ".env"。
	EnvFile string
}

// Config is the handle of the layered configuration engine. | Config 是分层配置引擎的句柄。
//
// It embeds *viper.Viper, so read APIs such as Get/GetInt/GetString/Set are directly available; | 内嵌 *viper.Viper，因此 Get/GetInt/GetString/Set 等读取 API 直接可用；
// this type additionally provides command-line binding, file watching and strongly typed decoding. | 本类型额外提供命令行绑定、文件监视与强类型解码能力。
type Config struct {
	fs       afero.Fs
	watchErr error
	*viper.Viper

	flags *pflag.FlagSet

	name        string
	configDir   string
	configName  string
	configType  string
	configFile  string
	configLocal string

	// watcher is the fsnotify watcher started by Watch; watchMu guards it and watchErr. | watcher
	// 是 Watch 启动的 fsnotify 监视器；watchMu 保护它和 watchErr。
	watcher *fsnotify.Watcher
	watchMu sync.RWMutex

	// mu serialises reads against reloads. viper documents that Get and Set/ReadConfig are not
	// safe to run concurrently, and a live config watch would otherwise crash the process with
	// "concurrent map read and map write". | mu 串行化读取与重新加载。viper 明确说明 Get 与
	// Set/ReadConfig 不可并发，运行中的配置监视否则会以「并发读写 map」直接终止进程。
	mu sync.RWMutex
}

// Load builds a config instance in the order "defaults → .env → config files". | Load 按「默认值 → .env → 配置文件」的顺序构建配置实例。
//
// Environment variables (including .env injection) and command-line arguments are not read here; instead viper resolves them | 环境变量（含 .env 注入）与命令行参数不在此处读取，而是由 viper 在取值时
// dynamically by precedence at lookup time, so that flag changes after BindFlags take effect immediately. | 按优先级动态解析，从而保证 BindFlags 之后修改 flag 立即生效。
func Load(opts Options) (*Config, error) {
	fsys := opts.FS
	if fsys == nil {
		fsys = afero.NewOsFs()
	}
	prefix := opts.EnvPrefix
	if prefix == "" {
		prefix = DefaultEnvPrefix
	}
	dir := opts.ConfigDir
	if dir == "" {
		dir = DefaultConfigDir
	}
	name := opts.ConfigName
	if name == "" {
		name = DefaultConfigName
	}
	envFile := opts.EnvFile
	if envFile == "" {
		envFile = DefaultEnvFileName
	}

	v := viper.New()
	// Make viper's internal file operations (including WatchConfig re-reads) share the same filesystem as external reads. | 让 viper 内部的文件操作（含 WatchConfig 的重新读取）与外部读取共用同一文件系统。
	v.SetFs(fsys)
	v.SetEnvPrefix(prefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	c := &Config{
		Viper:      v,
		name:       opts.Name,
		fs:         fsys,
		flags:      opts.Flags,
		configDir:  dir,
		configName: name,
	}

	// 1) Default layer: lowest precedence, but every key is guaranteed to exist in AllKeys, | 1) 默认值层：最低优先级，但保证所有键都存在于 AllKeys 中，
	//    so that Unmarshal can see the values after environment variable overrides. |    从而使 Unmarshal 能够看到环境变量覆盖后的取值。
	Defaults(c)

	// 2) .env layer: equivalent to environment variables; real environment variables are never overridden. | 2) .env 层：等价于环境变量，绝不覆盖真实环境变量。
	if err := c.loadEnvFile(envFile, prefix); err != nil {
		return nil, err
	}

	// 3) Config file layer: config.<ext> and config.local.<ext> (the latter overrides the former). | 3) 配置文件层：config.<ext> 与 config.local.<ext>（后者覆盖前者）。
	if err := c.loadConfigFiles(); err != nil {
		return nil, err
	}

	return c, nil
}

// BindFlags wires command-line flags into the configuration layer according to mapping (of the form {"port": "server.port"}). | BindFlags 按 mapping（形如 {"port": "server.port"}）把命令行标志接入配置层。
//
// Rules: | 规则：
//   - flag.Changed is true: bind via BindPFlag, giving command-line arguments the highest precedence; |   - flag.Changed 为 true：通过 BindPFlag 绑定，命令行参数获得最高优先级；
//   - flag.Changed is false: only inject the type-parsed DefValue into the default layer, |   - flag.Changed 为 false：仅把按类型解析后的 DefValue 注入默认值层，
//     never registering it as a flag binding, so that environment variables and config files still win; |     绝不登记为旗标层，从而保证环境变量与配置文件仍然胜出；
//   - return an error wrapping ErrUnknownFlag when mapping references a flag name that does not exist in the FlagSet. |   - mapping 引用了 FlagSet 中不存在的标志名时返回包装了 ErrUnknownFlag 的错误。
//
// When fs is nil, it falls back to the flag set provided by Options.Flags. | fs 为 nil 时回退到 Options.Flags 提供的标志集。
func (c *Config) BindFlags(fs *pflag.FlagSet, mapping map[string]string) error {
	if fs == nil {
		fs = c.flags
	}
	if fs == nil {
		if len(mapping) == 0 {
			return nil
		}
		return errx.Detail(ErrFlagMapping, "no FlagSet provided to bind %[1]d flags | 未提供 FlagSet，无法绑定 %[1]d 个标志", len(mapping))
	}

	// Iterate in sorted order so that error messages and injection order stay stable and reproducible. | 排序后遍历，保证错误信息与注入顺序稳定可复现。
	flagNames := make([]string, 0, len(mapping))
	for flagName := range mapping {
		flagNames = append(flagNames, flagName)
	}
	sort.Strings(flagNames)

	for _, flagName := range flagNames {
		key := strings.ToLower(strings.TrimSpace(mapping[flagName]))
		if key == "" {
			return errx.Detail(ErrFlagMapping, "flag %[1]q has no config key | 标志 %[1]q 没有对应的配置键", flagName)
		}
		fl := fs.Lookup(flagName)
		if fl == nil {
			return errx.Detail(ErrUnknownFlag, "flag %[1]q is not in the FlagSet | 标志 %[1]q 不在 FlagSet 中", flagName)
		}
		c.mu.Lock()
		// Bind every flag, not only the ones already Changed: viper consults HasChanged() at lookup
		// time, so binding all of them makes a flag parsed *after* BindFlags effective while an
		// unchanged flag still falls through to env/file/default. | 绑定所有标志而不只是已 Changed 的：
		// viper 在取值时才检查 HasChanged()，因此全部绑定能让 BindFlags 之后解析的标志生效，而未变更的
		// 标志仍会落到环境变量/文件/默认值。
		if err := c.Viper.BindPFlag(key, fl); err != nil {
			c.mu.Unlock()
			return errx.WrapDetail(err, ErrFlagBind, "flag %[1]q to config key %[2]q | 把标志 %[1]q 绑定到配置键 %[2]q", flagName, key)
		}
		if !fl.Changed {
			defValue, err := parseFlagDefault(fl)
			if err != nil {
				c.mu.Unlock()
				return errx.WrapDetail(err, ErrBadFlagDefault, "flag %[1]q default %[2]q | 标志 %[1]q 的默认值 %[2]q", flagName, fl.DefValue)
			}
			c.Viper.SetDefault(key, defValue)
		}
		c.mu.Unlock()
	}
	return nil
}

// The read wrappers below take the configuration lock so a watch reload cannot mutate viper's
// maps while a request reads them. | 下面的读取包装器获取配置锁，使监视触发的重新加载不会在请求读取
// 时修改 viper 的映射。
func (c *Config) Get(key string) any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Viper.Get(key)
}

// GetString returns the string value of key. | GetString 返回 key 的字符串取值。
func (c *Config) GetString(key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Viper.GetString(key)
}

// GetBool returns the boolean value of key. | GetBool 返回 key 的布尔取值。
func (c *Config) GetBool(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Viper.GetBool(key)
}

// GetInt returns the integer value of key. | GetInt 返回 key 的整数取值。
func (c *Config) GetInt(key string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Viper.GetInt(key)
}

// GetInt64 returns the int64 value of key. | GetInt64 返回 key 的 int64 取值。
func (c *Config) GetInt64(key string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Viper.GetInt64(key)
}

// GetFloat64 returns the float64 value of key. | GetFloat64 返回 key 的 float64 取值。
func (c *Config) GetFloat64(key string) float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Viper.GetFloat64(key)
}

// GetDuration returns the duration value of key. | GetDuration 返回 key 的时长取值。
func (c *Config) GetDuration(key string) time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Viper.GetDuration(key)
}

// GetStringSlice returns the string slice value of key. | GetStringSlice 返回 key 的字符串切片取值。
func (c *Config) GetStringSlice(key string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Viper.GetStringSlice(key)
}

// GetStringMapString returns the string map value of key. | GetStringMapString 返回 key 的字符串映射取值。
func (c *Config) GetStringMapString(key string) map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Viper.GetStringMapString(key)
}

// IsSet reports whether key has a value in any layer. | IsSet 报告 key 在任一层中是否有取值。
func (c *Config) IsSet(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Viper.IsSet(key)
}

// AllKeys returns every known configuration key. | AllKeys 返回全部已知配置键。
func (c *Config) AllKeys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Viper.AllKeys()
}

// AllSettings returns the merged configuration as a map. | AllSettings 以映射返回合并后的配置。
func (c *Config) AllSettings() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Viper.AllSettings()
}

// Unmarshal decodes the layered configuration into raw. | Unmarshal 把分层配置解码到 raw。
func (c *Config) Unmarshal(raw any) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if err := c.Viper.Unmarshal(raw); err != nil {
		return errx.Wrap(err, ErrUnmarshal)
	}
	return nil
}

// Sub returns a sub-tree of the configuration. | Sub 返回配置的一棵子树。
func (c *Config) Sub(key string) *viper.Viper {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Viper.Sub(key)
}

// parseFlagDefault parses pflag's default value text into a strongly typed value according to the flag type, | parseFlagDefault 把 pflag 的默认值文本按标志类型解析为强类型值，
// covering bool / int / int64 / float64 / duration / string; | 覆盖 bool / int / int64 / float64 / duration / string；
// all other types (slices, maps, etc.) degrade to the raw string, leaving conversion to viper at lookup time. | 其余类型（切片、map 等）退化为原始字符串，交由 viper 在取值时转换。
func parseFlagDefault(fl *pflag.Flag) (any, error) {
	switch fl.Value.Type() {
	case "bool":
		return strconv.ParseBool(fl.DefValue)
	case "int":
		return strconv.Atoi(fl.DefValue)
	case "int64":
		return strconv.ParseInt(fl.DefValue, 10, 64)
	case "float64":
		return strconv.ParseFloat(fl.DefValue, 64)
	case "duration":
		return time.ParseDuration(fl.DefValue)
	default:
		return fl.DefValue, nil
	}
}

// Duration returns the duration value for a config key, or 0 when it is unset or cannot be parsed. | Duration 返回配置键对应的时长值，未设置或无法解析时返回 0。
//
// Read all duration-type configuration through this method so the underlying implementation can be replaced later. | 统一经由本方法读取时长类配置，便于后续替换底层实现。
func (c *Config) Duration(key string) time.Duration {
	return c.GetDuration(key)
}

// Watch starts watching the config directory: when the base or local file is written, it reloads
// the layered configuration and calls fn. | Watch 启动配置目录监视：基础文件或 local 文件被写入时，
// 重新加载分层配置并调用 fn。
//
// The reload semantics match Load (config.<ext> first, then config.local.<ext>), so the values
// seen in the callback are the latest file content, including a local override. When fn is nil the
// reload still happens and only the callback is skipped. | 重新加载语义与 Load 一致（先
// config.<ext> 再 config.local.<ext>），因此回调中看到的取值是最新文件内容，local 覆盖也包括在内。
// fn 为 nil 时仍会重新加载，只是不调用回调。
//
// Watching is implemented with fsnotify directly rather than viper.WatchConfig: viper re-reads the
// base file itself (discarding local overrides), mutates its maps outside this package's lock, and
// calls os.Exit when the watcher cannot be created. Directory-watch failures are reported through
// WatchErr and never terminate the process; a reload failure is recorded there too and skips that
// fn invocation. | 监视直接使用 fsnotify 而不是 viper.WatchConfig：后者会自行重读基础文件（丢掉
// local 覆盖）、在本包的锁之外修改映射，并在无法创建监视器时直接 os.Exit。目录监视失败通过
// WatchErr 报告，绝不终止进程；重新加载失败同样记录在那里，并跳过该次 fn 调用。
func (c *Config) Watch(fn func(*Config)) {
	dir := c.configDir
	if dir == "" {
		dir = "."
	}
	// The directory is watched directly. viper's WatchConfig would re-read the base file itself on
	// every event (dropping config.local overrides), mutate its maps outside our lock, and call
	// os.Exit when the watcher cannot be created; owning the watcher avoids all three.
	// | 直接监视目录。viper 的 WatchConfig 会在每次事件时自行重读基础文件（丢掉 config.local 覆盖）、
	// 在锁外修改内部映射，并在无法创建监视器时直接 os.Exit；自己持有监视器可同时避免这三点。
	w, err := fsnotify.NewWatcher()
	if err != nil {
		c.setWatchErr(errx.WrapDetail(err, ErrWatchUnavailable, "dir=%[1]s | 目录=%[1]s", dir))
		return
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		c.setWatchErr(errx.WrapDetail(err, ErrWatchUnavailable, "dir=%[1]s | 目录=%[1]s", dir))
		return
	}

	c.watchMu.Lock()
	old := c.watcher
	c.watcher = w
	c.watchMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	c.setWatchErr(nil)
	go c.watchLoop(w, fn)
}

// watchLoop reloads the layered configuration after a debounced config-file change. | watchLoop
// 在配置变更去抖后重新加载分层配置。
func (c *Config) watchLoop(w *fsnotify.Watcher, fn func(*Config)) {
	const debounce = 100 * time.Millisecond
	var timer *time.Timer
	var pending <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if !c.isConfigEvent(ev.Name) {
				continue
			}
			if timer == nil {
				timer = time.NewTimer(debounce)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(debounce)
			}
			pending = timer.C
		case <-pending:
			pending = nil
			c.mu.Lock()
			err := c.reload()
			c.mu.Unlock()
			if err != nil {
				c.setWatchErr(err)
				continue
			}
			c.setWatchErr(nil)
			if fn != nil {
				fn(c)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			c.setWatchErr(errx.Wrap(err, ErrWatchUnavailable))
		}
	}
}

// isConfigEvent reports whether path is the base or local config file of this instance. | isConfigEvent
// 报告 path 是否为本实例的基础配置文件或 local 覆盖文件。
func (c *Config) isConfigEvent(path string) bool {
	base, lockedExt := splitConfigName(filepath.Base(c.configName))
	exts := configExts
	if lockedExt != "" {
		exts = []string{lockedExt}
	}
	name := strings.ToLower(filepath.Base(path))
	for _, ext := range exts {
		if name == strings.ToLower(base+"."+ext) || name == strings.ToLower(base+".local."+ext) {
			return true
		}
	}
	return false
}

// WatchErr returns the error from the most recent watch operation; nil means watching is healthy or has not started. | WatchErr 返回最近一次监视过程中的错误；nil 表示监视正常或尚未启动监视。
func (c *Config) WatchErr() error {
	c.watchMu.RLock()
	defer c.watchMu.RUnlock()
	return c.watchErr
}

// setWatchErr records a watch error for WatchErr to read concurrency-safely. | setWatchErr 记录监视错误，供 WatchErr 并发安全地读取。
func (c *Config) setWatchErr(err error) {
	c.watchMu.Lock()
	defer c.watchMu.Unlock()
	c.watchErr = err
}

// loadConfigFiles reads config.<ext> and config.local.<ext>; neither existing is not an error. | loadConfigFiles 读取 config.<ext> 与 config.local.<ext>；两者都不存在不是错误。
func (c *Config) loadConfigFiles() error {
	basePath, localPath, ext, err := c.findConfigFiles()
	if err != nil {
		return err
	}
	if basePath == "" && localPath == "" {
		return nil
	}

	base, _ := splitConfigName(c.configName)
	c.Viper.SetConfigName(base)
	c.Viper.AddConfigPath(c.configDir)

	c.configType = ext
	if basePath != "" {
		if err := c.readConfigFile(basePath, false); err != nil {
			return err
		}
		c.configFile = basePath
		c.Viper.SetConfigFile(basePath)
	}
	if localPath != "" {
		if err := c.readConfigFile(localPath, basePath != ""); err != nil {
			return err
		}
		c.configLocal = localPath
	}
	return nil
}

// joinVirtual joins a configuration file name under dir using slash separators. | joinVirtual 使用斜杠
// 分隔符把配置文件名拼到 dir 之下。
//
// Configuration can be read from an io/fs backend (a read-only embedded catalogue, for instance);
// such backends reject the backslash filepath.Join produces on Windows, while OsFs and MemMapFs
// accept forward slashes on every platform. | 配置可能从 io/fs 后端读取（例如只读的内嵌目录）；
// 这类后端会在 Windows 上拒绝 filepath.Join 产生的反斜杠，而 OsFs 与 MemMapFs 在所有平台上都接受斜杠。
func joinVirtual(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return path.Join(filepath.ToSlash(dir), name)
}

// findConfigFiles locates the base config file and the local override file on the current filesystem, by extension precedence. | findConfigFiles 在当前文件系统上按扩展名优先级定位基础配置文件与 local 覆盖文件。
// A lone local file is loaded as well; when neither exists it returns empty paths without error. | 仅存在 local 文件时同样会被加载；两者都不存在时返回空路径且不报错。
func (c *Config) findConfigFiles() (basePath, localPath, ext string, err error) {
	base, lockedExt := splitConfigName(c.configName)
	exts := configExts
	if lockedExt != "" {
		exts = []string{lockedExt}
	}

	probe := func(p string) (bool, error) {
		_, statErr := c.fs.Stat(p)
		if statErr == nil {
			return true, nil
		}
		if errors.Is(statErr, os.ErrNotExist) {
			return false, nil
		}
		return false, errx.WrapDetail(statErr, ErrProbeConfigFile, "%s", p)
	}

	for _, e := range exts {
		p := joinVirtual(c.configDir, base+"."+e)
		ok, probeErr := probe(p)
		if probeErr != nil {
			return "", "", "", probeErr
		}
		if ok {
			basePath, ext = p, e
			break
		}
	}
	if basePath == "" {
		for _, e := range exts {
			p := joinVirtual(c.configDir, base+".local."+e)
			ok, probeErr := probe(p)
			if probeErr != nil {
				return "", "", "", probeErr
			}
			if ok {
				return "", p, e, nil
			}
		}
		return "", "", "", nil
	}

	lp := joinVirtual(c.configDir, base+".local."+ext)
	ok, probeErr := probe(lp)
	if probeErr != nil {
		return "", "", "", probeErr
	}
	if ok {
		localPath = lp
	}
	return basePath, localPath, ext, nil
}

// readConfigFile reads the file bytes via afero and hands them to viper for parsing, | readConfigFile 通过 afero 读取文件字节后交给 viper 解析，
// thereby supporting non-OS filesystems such as MemFS. | 从而支持 MemFS 等非操作系统文件系统。
// When merge is false it replaces the whole config tree; when true it deep-merges (local overrides base). | merge 为 false 时整体替换配置树，为 true 时深合并（local 覆盖 base）。
func (c *Config) readConfigFile(path string, merge bool) error {
	data, err := afero.ReadFile(c.fs, path)
	if err != nil {
		return errx.WrapDetail(err, ErrReadConfigFile, "%s", path)
	}
	c.Viper.SetConfigType(c.configType)

	reader := bytes.NewReader(data)
	if merge {
		if err := c.Viper.MergeConfig(reader); err != nil {
			return errx.WrapDetail(err, ErrParseConfigFile, "%s", path)
		}
		return nil
	}
	if err := c.Viper.ReadConfig(reader); err != nil {
		return errx.WrapDetail(err, ErrParseConfigFile, "%s", path)
	}
	return nil
}

// reload re-reads the config files (base + local override) and refreshes the layered result, for use by the Watch callback. | reload 重新读取配置文件（基础 + local 覆盖）并刷新分层结果，供 Watch 回调使用。
// When the config files are all deleted it keeps the current in-memory values, without destructively clearing them. | 配置文件被整体删除时保留当前内存取值，不做破坏性清空。
func (c *Config) reload() error {
	basePath, localPath, ext, err := c.findConfigFiles()
	if err != nil {
		return err
	}
	if basePath == "" && localPath == "" {
		return nil
	}

	c.configType = ext
	switch {
	case basePath == "":
		if err := c.readConfigFile(localPath, false); err != nil {
			return err
		}
	default:
		if err := c.readConfigFile(basePath, false); err != nil {
			return err
		}
		if localPath != "" {
			if err := c.readConfigFile(localPath, true); err != nil {
				return err
			}
		}
	}

	c.configFile, c.configLocal = basePath, localPath
	if basePath != "" {
		c.Viper.SetConfigFile(basePath)
	}
	return nil
}

// splitConfigName splits a config name into "base name + locked extension"; | splitConfigName 把配置名拆成「基础名 + 已锁定的扩展名」；
// when no supported extension is given explicitly it returns an empty extension, meaning configExts are probed one by one. | 未显式给出受支持扩展名时返回空扩展名，表示按 configExts 逐个探测。
func splitConfigName(name string) (base, ext string) {
	if e := strings.TrimPrefix(filepath.Ext(name), "."); e != "" {
		lower := strings.ToLower(e)
		for _, supported := range configExts {
			if lower == supported {
				return strings.TrimSuffix(name, filepath.Ext(name)), lower
			}
		}
	}
	return name, ""
}
