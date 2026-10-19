package config

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/spf13/afero"
)

// .env errors, declared once at package level. | .env 错误，在包级声明一次。
var (
	// ErrReadEnvFile indicates the .env file exists but could not be read. | ErrReadEnvFile 表示 .env 文件存在但无法读取。
	ErrReadEnvFile = errx.New("config: failed to read env file | config：读取环境文件失败")
	// ErrInjectEnv indicates an environment variable could not be set from the .env file. | ErrInjectEnv 表示无法从 .env 文件注入环境变量。
	ErrInjectEnv = errx.New("config: failed to inject environment variable | config：注入环境变量失败")
)

// loadEnvFile reads the .env file via afero and injects its key-value pairs into the process environment variables. | loadEnvFile 通过 afero 读取 .env 文件，并把其中的键值注入进程环境变量。
//
// Injection rules: | 注入规则：
//   - only keys carrying the configured environment prefix (YD_ by default) are injected; every
//     other name is ignored, so a checked-out .env cannot inject GOFLAGS, LD_PRELOAD, PATH or a
//     proxy setting into the processes the CLI spawns; |   - 只注入带有所配置环境前缀的键（默认 YD_）；
//     其它名称一律忽略，因此签出的 .env 无法把 GOFLAGS、LD_PRELOAD、PATH 或代理设置注入 CLI 派生的进程；
//   - os.Setenv is called only when a variable of the same name does **not** exist in the real environment, |   - 仅当同名变量在真实环境中**不存在**时才调用 os.Setenv，
//     so real environment variables always outrank .env and are never overwritten; |     因此真实环境变量永远高于 .env，绝不被覆盖；
//   - injected keys are equivalent to ordinary environment variables (e.g. YD_DB_MAXCONN corresponds to db.maxconn), |   - 注入后的键与普通环境变量等价（例如 YD_DB_MAXCONN 对应 db.maxconn），
//     and rank above config files in viper's precedence; |     在 viper 的优先级中位于配置文件之上；
//   - a missing file is normal (a project not using .env) and returns nil. |   - 文件不存在是正常情况（未使用 .env 的项目），返回 nil。
func (c *Config) loadEnvFile(path, prefix string) error {
	data, err := afero.ReadFile(c.fs, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return errx.WrapDetail(err, ErrReadEnvFile, "%s", path)
	}

	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	if prefix == "" {
		prefix = DefaultEnvPrefix
	}
	for key, val := range parseDotEnv(data) {
		// Restrict injection to the framework's own namespace. Without this check an untrusted
		// .env can set GOFLAGS=-toolexec=... and have the next `yd build` execute attacker code.
		// | 只注入框架自己的命名空间。缺少这道检查时，不可信的 .env 可以设置
		// GOFLAGS=-toolexec=...，让下一次 `yd build` 执行攻击者代码。
		if !strings.HasPrefix(strings.ToUpper(key), prefix+"_") {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue // Real environment variables take precedence; overwriting is forbidden. | 真实环境变量优先，禁止覆盖
		}
		if err := os.Setenv(key, val); err != nil {
			return errx.WrapDetail(err, ErrInjectEnv, "%s", key)
		}
	}
	return nil
}

// parseDotEnv parses .env text, supporting: | parseDotEnv 解析 .env 文本，支持：
//
//   - blank lines and whole-line comments starting with # (plus a UTF-8 BOM); |   - 空行与 # 开头的整行注释（以及 UTF-8 BOM）；
//   - the export prefix (export KEY=VALUE); |   - export 前缀（export KEY=VALUE）；
//   - values wrapped in single quotes (kept as-is) or double quotes (with \n \r \t \" \\ escapes parsed); |   - 单引号（原样保留）与双引号（解析 \n \r \t \" \\ 转义）包裹的值；
//   - inline comments in bare values (# must be preceded by whitespace); |   - 裸值中的行内注释（# 前必须有空白）；
//   - CRLF line endings; for duplicate keys the later one overrides the earlier. |   - CRLF 换行；同名键后者覆盖前者。
//
// Unrecognized lines (no = sign, empty key, or key containing whitespace/quotes) are silently skipped, | 无法识别的行（无 = 号、键名为空或含空白/引号）会被静默跳过，
// to keep .env parsing tolerant. | 以保证 .env 的容错性。
func parseDotEnv(data []byte) map[string]string {
	out := make(map[string]string)
	text := string(data)
	// Tolerate a UTF-8 BOM (EF BB BF). | 容忍 UTF-8 BOM（EF BB BF）。
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

// unquoteEnvValue handles the value part of .env: strips wrapping quotes, or truncates an inline comment in a bare value as needed. | unquoteEnvValue 处理 .env 的值部分：剥离包裹引号，或按需截断裸值的行内注释。
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
		// Unclosed quote: fall back to bare-value handling. | 引号未闭合：退化为裸值处理。
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

// unescapeDoubleQuoted parses escape sequences inside a double-quoted value; unknown escapes are kept verbatim. | unescapeDoubleQuoted 解析双引号值中的转义序列，未知转义按原文保留。
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
