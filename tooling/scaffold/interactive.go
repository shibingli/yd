package scaffold

import (
	"bufio"
	"errors"
	"io"
	"strings"

	"github.com/shibingli/yd/core/errx"
)

// Fixed prompt and parsing copy for the interview (bilingual line by line). | 交互提示与解析用到的固定文案（逐条中英同步）。
const (
	// promptName asks for the project name. | promptName 询问项目名。
	promptName = "项目名 | Project name"
	// promptModule asks for the module path. | promptModule 询问模块路径。
	promptModule = "模块路径 | Module path"
	// promptDB asks for the database driver. | promptDB 询问数据库驱动。
	promptDB = "数据库 (mysql/postgres/sqlite/none) | Database (mysql/postgres/sqlite/none)"
	// promptRedis asks whether Redis is wanted. | promptRedis 询问是否接入 Redis。
	promptRedis = "启用 Redis 缓存 (y/n) | Enable Redis cache (y/n)"
	// promptAsynq asks whether asynq is wanted. | promptAsynq 询问是否接入 asynq。
	promptAsynq = "启用 Asynq 队列 (y/n) | Enable Asynq queue (y/n)"
	// promptGRPC asks whether gRPC is wanted. | promptGRPC 询问是否开启 gRPC。
	promptGRPC = "启用 gRPC 双通道 (y/n) | Enable gRPC dual channel (y/n)"
	// promptI18n asks whether i18n is wanted. | promptI18n 询问是否开启国际化。
	promptI18n = "启用 i18n (y/n) | Enable i18n (y/n)"
	// promptTailwind asks whether Tailwind is wanted. | promptTailwind 询问是否接入 Tailwind。
	promptTailwind = "启用 Tailwind (y/n) | Enable Tailwind (y/n)"
	// promptExts asks for the Htmx extension list. | promptExts 询问 Htmx 扩展列表。
	promptExts = "Htmx 扩展 (逗号分隔，名称见 `yd new --help`) | Htmx extensions (comma separated, names in `yd new --help`)"
	// promptLicense asks for the licence. | promptLicense 询问开源协议。
	promptLicense = "开源协议 | License"
	// noneAnswer clears list answers (for example: no Htmx extension at all). | noneAnswer 表示清空列表类回答（如不启用任何 Htmx 扩展）。
	noneAnswer = "none"
)

// ErrAnswerInvalid reports an unparsable interactive answer. | ErrAnswerInvalid 表示交互回答无法解析。
var ErrAnswerInvalid = errx.New("invalid answer | 回答无法解析")

// Interactive asks for the missing options one by one and returns the completed Options. | Interactive 逐项询问缺失的选项并返回补齐后的 Options。
//
// When isTTY is false (pipes, CI) nothing is printed and no input is read: base is returned with defaults filled in. | isTTY 为 false 时（管道、CI）不打印任何提示、不读取输入，直接返回补齐默认值后的 base。
// When isTTY is true answers are read line by line from in; an empty line accepts the bracketed default, and EOF stops the interview. | isTTY 为 true 时从 in 逐行读取回答，空行表示接受方括号中的默认值，读到 EOF 即停止询问。
func Interactive(in io.Reader, out io.Writer, base Options, isTTY bool) (Options, error) {
	base = withDefaults(base)
	if !isTTY {
		return base, nil
	}

	reader := bufio.NewReader(in)
	exhausted := false

	ask := func(prompt, def string) (string, error) {
		if exhausted {
			return def, nil
		}
		if _, err := io.WriteString(out, prompt+" ["+def+"]: "); err != nil {
			return "", errx.Wrap(err, ErrInputRead)
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				exhausted = true
			} else {
				return "", errx.Wrap(err, ErrInputRead)
			}
		}
		if answer := strings.TrimSpace(line); answer != "" {
			return answer, nil
		}
		return def, nil
	}

	name, err := ask(promptName, base.Name)
	if err != nil {
		return base, err
	}

	// The module default follows the project name unless it was set explicitly. | 模块路径的默认值跟随项目名（用户显式指定的模块路径保持不变）。
	modDef := base.Module
	if modDef == "" || modDef == defaultModule(base.Name) {
		modDef = defaultModule(name)
	}
	base.Name = name
	module, err := ask(promptModule, modDef)
	if err != nil {
		return base, err
	}
	base.Module = module

	db, err := ask(promptDB, base.DB)
	if err != nil {
		return base, err
	}
	if !containsString(dbValues(), db) {
		return base, errx.Detail(ErrDBUnsupported, "%[1]q (want: %[2]s) | %[1]q（期望: %[2]s）", db, strings.Join(dbValues(), ", "))
	}
	base.DB = db

	for _, toggle := range []struct {
		prompt string
		value  *bool
	}{
		{promptRedis, &base.Redis},
		{promptAsynq, &base.Asynq},
		{promptGRPC, &base.GRPC},
		{promptI18n, &base.I18n},
		{promptTailwind, &base.Tailwind},
	} {
		answer, err := ask(toggle.prompt, yesNo(*toggle.value))
		if err != nil {
			return base, err
		}
		parsed, err := parseYesNo(answer)
		if err != nil {
			return base, errx.WrapDetail(err, ErrAnswerInvalid, "prompt=%[1]s | 提示=%[1]s", toggle.prompt)
		}
		*toggle.value = parsed
	}

	extAnswer, err := ask(promptExts, strings.Join(base.HtmxExts, ","))
	if err != nil {
		return base, err
	}
	exts, err := ParseExtensions(extAnswer)
	if err != nil {
		return base, err
	}
	base.HtmxExts = exts

	license, err := ask(promptLicense, base.License)
	if err != nil {
		return base, err
	}
	base.License = license

	return base, nil
}

// ParseExtensions 解析逗号分隔的 Htmx 扩展列表：去空、去重、小写化，none 表示清空。
// ParseExtensions parses a comma-separated Htmx extension list: blanks dropped, duplicates removed, lowered; none clears it.
func ParseExtensions(s string) ([]string, error) {
	answer := strings.TrimSpace(s)
	if answer == "" || strings.EqualFold(answer, noneAnswer) {
		return nil, nil
	}

	exts := make([]string, 0, len(extNames))
	for _, part := range strings.Split(answer, ",") {
		ext := strings.ToLower(strings.TrimSpace(part))
		if ext == "" || containsString(exts, ext) {
			continue
		}
		if !containsString(extNames, ext) {
			return nil, errx.Detail(ErrHtmxExtUnknown, "%[1]q (want: %[2]s) | %[1]q（期望: %[2]s）", ext, strings.Join(extNames, ", "))
		}
		exts = append(exts, ext)
	}
	return exts, nil
}

// withDefaults fills empty fields without touching explicitly given boolean switches. | withDefaults 补齐空字段的默认值（不改动已显式给定的布尔开关）。
func withDefaults(o Options) Options {
	if strings.TrimSpace(o.DB) == "" {
		o.DB = DBDefault
	}
	if strings.TrimSpace(o.License) == "" {
		o.License = LicenseApache2
	}
	if strings.TrimSpace(o.Module) == "" {
		o.Module = defaultModule(o.Name)
	}
	return o
}

// dbValues returns the supported drivers in a fixed order for hints and tests. | dbValues 返回支持的数据库驱动取值（固定顺序，便于提示与测试）。
func dbValues() []string {
	return []string{DBMySQL, DBPostgres, DBSQLite, DBNone}
}

// yesNo renders a boolean switch as the y/n hint. | yesNo 把布尔开关渲染为提示用的 y/n。
func yesNo(v bool) string {
	if v {
		return "y"
	}
	return "n"
}

// parseYesNo parses a y/n answer; empty answers are handled by the caller. | parseYesNo 解析 y/n 形式的回答，空回答由调用方先行处理。
func parseYesNo(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "true", "1", "on", "是":
		return true, nil
	case "n", "no", "false", "0", "off", "否":
		return false, nil
	default:
		return false, errx.Detail(ErrAnswerInvalid, "%[1]q (want y/n) | %[1]q（期望 y/n）", s)
	}
}

// defaultModule returns the default module path of a project name. | defaultModule 返回项目名对应的默认模块路径。
func defaultModule(name string) string {
	return "github.com/yun-dev/" + name
}
