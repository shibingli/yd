package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/afero"
)

// loadEnvFile 通过 afero 读取 .env 文件，并把其中的键值注入进程环境变量。
//
// 注入规则：
//   - 仅当同名变量在真实环境中**不存在**时才调用 os.Setenv，
//     因此真实环境变量永远高于 .env，绝不被覆盖；
//   - 注入后的键与普通环境变量等价（例如 YD_DB_MAXCONN 对应 db.maxconn），
//     在 viper 的优先级中位于配置文件之上；
//   - 文件不存在是正常情况（未使用 .env 的项目），返回 nil。
func (c *Config) loadEnvFile(path string) error {
	data, err := afero.ReadFile(c.fs, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("config: 读取环境文件 %s 失败: %w", path, err)
	}

	for key, val := range parseDotEnv(data) {
		if _, exists := os.LookupEnv(key); exists {
			continue // 真实环境变量优先，禁止覆盖
		}
		if err := os.Setenv(key, val); err != nil {
			return fmt.Errorf("config: 注入环境变量 %s 失败: %w", key, err)
		}
	}
	return nil
}

// parseDotEnv 解析 .env 文本，支持：
//
//   - 空行与 # 开头的整行注释（以及 UTF-8 BOM）；
//   - export 前缀（export KEY=VALUE）；
//   - 单引号（原样保留）与双引号（解析 \n \r \t \" \\ 转义）包裹的值；
//   - 裸值中的行内注释（# 前必须有空白）；
//   - CRLF 换行；同名键后者覆盖前者。
//
// 无法识别的行（无 = 号、键名为空或含空白/引号）会被静默跳过，
// 以保证 .env 的容错性。
func parseDotEnv(data []byte) map[string]string {
	out := make(map[string]string)
	text := string(data)
	// 容忍 UTF-8 BOM（EF BB BF）。
	if len(text) >= 3 && text[0] == 0xEF && text[1] == 0xBB && text[2] == 0xBF {
		text = text[3:]
	}

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimSpace(rest)
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t\"'") {
			continue
		}
		out[key] = unquoteEnvValue(strings.TrimSpace(value))
	}
	return out
}

// unquoteEnvValue 处理 .env 的值部分：剥离包裹引号，或按需截断裸值的行内注释。
func unquoteEnvValue(v string) string {
	if v == "" {
		return ""
	}
	if v[0] == '\'' || v[0] == '"' {
		quote := v[0]
		escaped := false
		for i := 1; i < len(v); i++ {
			switch {
			case escaped:
				escaped = false
			case quote == '"' && v[i] == '\\':
				escaped = true
			case v[i] == quote:
				inner := v[1:i]
				if quote == '"' {
					return unescapeDoubleQuoted(inner)
				}
				return inner
			}
		}
		// 引号未闭合：退化为裸值处理。
	}

	for i := 0; i < len(v); i++ {
		if v[i] != '#' {
			continue
		}
		if i == 0 || v[i-1] == ' ' || v[i-1] == '\t' {
			return strings.TrimSpace(v[:i])
		}
	}
	return v
}

// unescapeDoubleQuoted 解析双引号值中的转义序列，未知转义按原文保留。
func unescapeDoubleQuoted(v string) string {
	if !strings.ContainsRune(v, '\\') {
		return v
	}
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' || i+1 >= len(v) {
			b.WriteByte(v[i])
			continue
		}
		i++
		switch v[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(v[i])
		}
	}
	return b.String()
}
