package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/shibingli/yd/core/errx"
)

// Prompt asks one question and returns the typed answer, or def when the answer is empty. | Prompt 提出一个问题并返回用户输入的答案，答案为空时返回 def。
//
// On a non-TTY (CI, pipes, tests) it returns def immediately without reading a single byte, which is
// what keeps `yd new` scriptable. | 在非 TTY（CI、管道、测试）下它立即返回 def 且不读取任何字节，
// 这正是 `yd new` 可脚本化的原因。
func Prompt(env *Env, question, def string) (string, error) {
	if env == nil {
		return def, ErrNilEnv
	}
	if !env.IsTTY {
		return def, nil
	}
	line, err := readAnswer(env, question, def)
	if err != nil {
		return def, err
	}
	if line == "" {
		return def, nil
	}
	return line, nil
}

// Confirm asks a yes/no question. | Confirm 提出一个是/否问题。
//
// Recognized answers are y/yes/n/no (case-insensitive); anything else, including an empty line, keeps
// the default def. A non-TTY returns def without reading. | 可识别 y/yes/n/no（大小写不敏感）；其它输入
// 包括空行都沿用默认值 def。非 TTY 直接返回 def 且不读取输入。
func Confirm(env *Env, question string, def bool) (bool, error) {
	if env == nil {
		return def, ErrNilEnv
	}
	if !env.IsTTY {
		return def, nil
	}
	defText := "n"
	if def {
		defText = "y"
	}
	line, err := readAnswer(env, question+" (y/n)", defText)
	if err != nil {
		return def, err
	}
	switch strings.ToLower(line) {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return def, nil
	}
}

// Choose asks the user to pick one of options and returns its index. | Choose 让用户从 options 中选择一项并返回其下标。
//
// The returned index is zero-based; def is clamped into range and returned on a non-TTY, on an empty
// answer and on an unparsable or out-of-range answer. An empty option list is a programming error and
// returns ErrEmptyOptions. | 返回的下标从 0 开始；非 TTY、空输入以及无法解析或越界的输入都返回
// 被裁剪到合法区间的 def。选项为空属于编码错误，返回 ErrEmptyOptions。
func Choose(env *Env, question string, options []string, def int) (int, error) {
	if len(options) == 0 {
		return 0, ErrEmptyOptions
	}
	if env == nil {
		return def, ErrNilEnv
	}
	if def < 0 || def >= len(options) {
		def = 0
	}
	if !env.IsTTY {
		return def, nil
	}

	for i, opt := range options {
		fmt.Fprintf(env.Out, "  %d) %s\n", i+1, opt)
	}
	line, err := readAnswer(env, question, strconv.Itoa(def+1))
	if err != nil {
		return def, err
	}
	if line == "" {
		return def, nil
	}
	n, convErr := strconv.Atoi(line)
	if convErr != nil || n < 1 || n > len(options) {
		return def, nil
	}
	return n - 1, nil
}

// readAnswer writes the prompt to env.Out and reads one line from env.In. | readAnswer 把提示语写到 env.Out 并从 env.In 读取一行。
func readAnswer(env *Env, question, def string) (string, error) {
	if env.Out != nil {
		fmt.Fprintf(env.Out, "%s", question)
		if def != "" {
			fmt.Fprintf(env.Out, " [%s]", def)
		}
		fmt.Fprintf(env.Out, ": ")
	}
	if env.In == nil {
		return "", nil
	}

	line, err := bufio.NewReader(env.In).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errx.Wrap(err, ErrInputFailed)
	}
	return strings.TrimSpace(line), nil
}
