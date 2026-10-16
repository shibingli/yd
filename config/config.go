// Package config 实现 yd 框架的多源分层配置引擎。
//
// 取值优先级（高 → 低）：
//
//	命令行参数（显式传入的 pflag）> 环境变量（含 .env 注入）> 配置文件 > 默认值
//
// 环境变量前缀固定为 YD，嵌套键通过下划线映射：
// YD_DB_MAXCONN → db.maxconn。配置文件支持 yaml / yml / toml / json，
// 并按 config.<ext> 与 config.local.<ext> 的顺序深合并（local 覆盖 base）。
//
// 职责边界：本包只负责「读取、分层合并、监视变更」，不解析业务语义、不做取值合法性
// 校验，也不装配日志/数据库/服务器等子系统（由各子包通过各自的 OptionsFromConfig 映射）。
//
// 关键正确性约束（Review Focus #2）：带非空默认值的 pflag 在未显式传参时
// 绝不能覆盖环境变量与配置文件。viper 对「未变更的旗标」仍会回落到 DefValue，
// 因此 BindFlags 只对 Changed 的标志调用 BindPFlag；
// 未 Changed 的标志改为把按类型解析后的 DefValue 注入默认值层（最低优先级）。
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/afero"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	// DefaultEnvPrefix 是框架约定的环境变量前缀，YD_DB_MAXCONN 映射到 db.maxconn。
	DefaultEnvPrefix = "YD"
	// DefaultConfigName 是配置文件的基础名（不含扩展名）。
	DefaultConfigName = "config"
	// DefaultConfigDir 是配置文件与 .env 的默认搜索目录。
	DefaultConfigDir = "."
	// DefaultEnvFileName 是默认的 .env 文件名。
	DefaultEnvFileName = ".env"
)

// configExts 是显式支持的配置文件扩展名，靠前者优先。
var configExts = []string{"yaml", "yml", "toml", "json"}

var (
	// ErrUnknownFlag 表示 BindFlags 的映射引用了 FlagSet 中不存在的标志名。
	ErrUnknownFlag = errors.New("config: 未知的命令行标志")
	// ErrWatchUnavailable 表示当前配置实例不具备文件监视条件：
	// 未找到配置文件，或该文件对操作系统不可见（例如内存文件系统）。
	ErrWatchUnavailable = errors.New("config: 无法监视配置文件")
)

// Options 描述一次 Load 的输入参数，零值即采用框架约定默认。
type Options struct {
	// Name 是配置实例名称，仅用于诊断信息（例如监视错误提示）。
	Name string
	// ConfigDir 是配置文件搜索目录，默认为 "."。
	ConfigDir string
	// ConfigName 是配置文件基础名，默认为 "config"；
	// 也可以写成 "config.yaml" 以锁定扩展名。
	ConfigName string
	// EnvPrefix 是环境变量前缀，默认为 "YD"。
	EnvPrefix string
	// EnvFile 是 .env 文件路径，默认为 ".env"。
	EnvFile string
	// FS 是读取配置文件与 .env 的文件系统，默认为 afero.NewOsFs()。
	FS afero.Fs
	// Flags 是可选命令行标志集；BindFlags 传入 nil 时会回退到该值。
	Flags *pflag.FlagSet
}

// Config 是分层配置引擎的句柄。
//
// 内嵌 *viper.Viper，因此 Get/GetInt/GetString/Set 等读取 API 直接可用；
// 本类型额外提供命令行绑定、文件监视与强类型解码能力。
type Config struct {
	*viper.Viper

	name        string
	fs          afero.Fs
	flags       *pflag.FlagSet
	configDir   string
	configName  string
	configType  string
	configFile  string
	configLocal string

	watchMu  sync.RWMutex
	watchErr error
}

// Load 按「默认值 → .env → 配置文件」的顺序构建配置实例。
//
// 环境变量（含 .env 注入）与命令行参数不在此处读取，而是由 viper 在取值时
// 按优先级动态解析，从而保证 BindFlags 之后修改 flag 立即生效。
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
	// 让 viper 内部的文件操作（含 WatchConfig 的重新读取）与外部读取共用同一文件系统。
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

	// 1) 默认值层：最低优先级，但保证所有键都存在于 AllKeys 中，
	//    从而使 Unmarshal 能够看到环境变量覆盖后的取值。
	Defaults(c)

	// 2) .env 层：等价于环境变量，绝不覆盖真实环境变量。
	if err := c.loadEnvFile(envFile); err != nil {
		return nil, err
	}

	// 3) 配置文件层：config.<ext> 与 config.local.<ext>（后者覆盖前者）。
	if err := c.loadConfigFiles(); err != nil {
		return nil, err
	}

	return c, nil
}

// BindFlags 按 mapping（形如 {"port": "server.port"}）把命令行标志接入配置层。
//
// 规则：
//   - flag.Changed 为 true：通过 BindPFlag 绑定，命令行参数获得最高优先级；
//   - flag.Changed 为 false：仅把按类型解析后的 DefValue 注入默认值层，
//     绝不登记为旗标层，从而保证环境变量与配置文件仍然胜出；
//   - mapping 引用了 FlagSet 中不存在的标志名时返回包装了 ErrUnknownFlag 的错误。
//
// fs 为 nil 时回退到 Options.Flags 提供的标志集。
func (c *Config) BindFlags(fs *pflag.FlagSet, mapping map[string]string) error {
	if fs == nil {
		fs = c.flags
	}
	if fs == nil {
		if len(mapping) == 0 {
			return nil
		}
		return fmt.Errorf("config: 未提供 FlagSet，无法绑定 %d 个标志: %w", len(mapping), ErrUnknownFlag)
	}

	// 排序后遍历，保证错误信息与注入顺序稳定可复现。
	flagNames := make([]string, 0, len(mapping))
	for flagName := range mapping {
		flagNames = append(flagNames, flagName)
	}
	sort.Strings(flagNames)

	for _, flagName := range flagNames {
		key := strings.ToLower(strings.TrimSpace(mapping[flagName]))
		if key == "" {
			return fmt.Errorf("config: 标志 %q 未指定配置键", flagName)
		}
		fl := fs.Lookup(flagName)
		if fl == nil {
			return fmt.Errorf("config: 标志 %q 不存在于 FlagSet: %w", flagName, ErrUnknownFlag)
		}
		if fl.Changed {
			if err := c.Viper.BindPFlag(key, fl); err != nil {
				return fmt.Errorf("config: 绑定标志 %q 到配置键 %q 失败: %w", flagName, key, err)
			}
			continue
		}
		defValue, err := parseFlagDefault(fl)
		if err != nil {
			return fmt.Errorf("config: 标志 %q 的默认值 %q 无法解析: %w", flagName, fl.DefValue, err)
		}
		c.Viper.SetDefault(key, defValue)
	}
	return nil
}

// parseFlagDefault 把 pflag 的默认值文本按标志类型解析为强类型值，
// 覆盖 bool / int / int64 / float64 / duration / string；
// 其余类型（切片、map 等）退化为原始字符串，交由 viper 在取值时转换。
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

// Unmarshal 把分层合并后的配置强类型解码到 raw 指向的结构体。
func (c *Config) Unmarshal(raw any) error {
	if err := c.Viper.Unmarshal(raw); err != nil {
		return fmt.Errorf("config: 解码配置失败: %w", err)
	}
	return nil
}

// Duration 返回配置键对应的时长值，未设置或无法解析时返回 0。
//
// 统一经由本方法读取时长类配置，便于后续替换底层实现。
func (c *Config) Duration(key string) time.Duration {
	return c.GetDuration(key)
}

// Watch 启动配置文件监视：文件被写入时重新加载分层配置并调用 fn。
//
// 重新加载语义与 Load 一致（先 config.<ext> 再 config.local.<ext>），
// 因此回调中看到的取值为最新文件内容。fn 为 nil 时只注册监视不回调。
//
// 说明：viper 的 WatchConfig 不返回错误（内部仅记录日志，创建 watcher 失败时甚至
// 直接 os.Exit），因此本方法在调用前自行校验前置条件——必须存在对操作系统可见的
// 配置文件。校验失败时不会启动监视，原因通过 WatchErr 暴露；
// 回调中的重新加载失败同样记录到 WatchErr，并跳过该次 fn 调用。
func (c *Config) Watch(fn func(*Config)) {
	if c.configFile == "" {
		label := c.name
		if label == "" {
			label = c.configName
		}
		c.setWatchErr(fmt.Errorf("%w: %s 未找到任何配置文件", ErrWatchUnavailable, label))
		return
	}
	if _, err := os.Stat(c.configFile); err != nil {
		c.setWatchErr(fmt.Errorf("%w: 配置文件 %s 对操作系统不可见（内存文件系统不支持文件监视）: %v",
			ErrWatchUnavailable, c.configFile, err))
		return
	}

	if fn != nil {
		c.Viper.OnConfigChange(func(fsnotify.Event) {
			if err := c.reload(); err != nil {
				c.setWatchErr(err)
				return
			}
			c.setWatchErr(nil)
			fn(c)
		})
	}
	c.Viper.WatchConfig()
}

// WatchErr 返回最近一次监视过程中的错误；nil 表示监视正常或尚未启动监视。
func (c *Config) WatchErr() error {
	c.watchMu.RLock()
	defer c.watchMu.RUnlock()
	return c.watchErr
}

// setWatchErr 记录监视错误，供 WatchErr 并发安全地读取。
func (c *Config) setWatchErr(err error) {
	c.watchMu.Lock()
	defer c.watchMu.Unlock()
	c.watchErr = err
}

// loadConfigFiles 读取 config.<ext> 与 config.local.<ext>；两者都不存在不是错误。
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

// findConfigFiles 在当前文件系统上按扩展名优先级定位基础配置文件与 local 覆盖文件。
// 仅存在 local 文件时同样会被加载；两者都不存在时返回空路径且不报错。
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
		return false, fmt.Errorf("config: 探测配置文件 %s 失败: %w", p, statErr)
	}

	for _, e := range exts {
		p := filepath.Join(c.configDir, base+"."+e)
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
			p := filepath.Join(c.configDir, base+".local."+e)
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

	lp := filepath.Join(c.configDir, base+".local."+ext)
	ok, probeErr := probe(lp)
	if probeErr != nil {
		return "", "", "", probeErr
	}
	if ok {
		localPath = lp
	}
	return basePath, localPath, ext, nil
}

// readConfigFile 通过 afero 读取文件字节后交给 viper 解析，
// 从而支持 MemFS 等非操作系统文件系统。
// merge 为 false 时整体替换配置树，为 true 时深合并（local 覆盖 base）。
func (c *Config) readConfigFile(path string, merge bool) error {
	data, err := afero.ReadFile(c.fs, path)
	if err != nil {
		return fmt.Errorf("config: 读取配置文件 %s 失败: %w", path, err)
	}
	c.Viper.SetConfigType(c.configType)

	reader := bytes.NewReader(data)
	if merge {
		if err := c.Viper.MergeConfig(reader); err != nil {
			return fmt.Errorf("config: 解析配置文件 %s 失败: %w", path, err)
		}
		return nil
	}
	if err := c.Viper.ReadConfig(reader); err != nil {
		return fmt.Errorf("config: 解析配置文件 %s 失败: %w", path, err)
	}
	return nil
}

// reload 重新读取配置文件（基础 + local 覆盖）并刷新分层结果，供 Watch 回调使用。
// 配置文件被整体删除时保留当前内存取值，不做破坏性清空。
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

// splitConfigName 把配置名拆成「基础名 + 已锁定的扩展名」；
// 未显式给出受支持扩展名时返回空扩展名，表示按 configExts 逐个探测。
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
