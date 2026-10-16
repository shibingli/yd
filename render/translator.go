package render

// TranslatorKey is the data key carrying the request translator. | TranslatorKey 是承载请求译者的数据键。
//
// Render looks the key up in a map[string]any data value; only a value satisfying
// Translator makes the `tr` template function resolve through it, for that one
// execution. | Render 在 map[string]any 型数据中查找该键；只有满足 Translator 的值才会让
// `tr` 模板函数在该次执行中经由它解析。
const TranslatorKey = "T"

// Translator resolves a message key into the text of one language. | Translator 把消息键解析为某种语言的文本。
//
// i18n.Translator implements it, and so does any value whose T renders a key and
// fills its placeholders with args. | i18n.Translator 实现了它，任何「T 渲染键并用 args
// 填充占位符」的值也同样实现。
type Translator interface {
	// T returns the message for key, filled with args in order. | T 返回 key 对应的消息，并按顺序用 args 填充。
	T(key string, args ...any) string
}

// tr is the built-in `tr` template function: it returns the key itself. | tr 是内建的 `tr` 模板函数：返回键本身。
//
// The translator is not taken from an argument but injected through the render
// data under TranslatorKey, because a template function never sees the data of
// the execution it runs in; Render binds the injected translator over this
// default for that execution only. | 译者不来自参数，而是通过渲染数据中的 TranslatorKey
// 键注入，因为模板函数看不到所在执行的数据；Render 仅在该次执行中把注入的译者绑定在
// 本默认实现之上。
//
// Without an injected translator the key is returned, so a missing catalog or a
// missing wiring stays visible in the page instead of failing the render:
// nothing panics and nothing is logged. | 未注入译者时返回键本身，因此缺失的语言目录或
// 缺失的装配会在页面上显现，而不是让渲染失败：不 panic，也不记日志。
//
// Extra arguments are forwarded to Translator.T, so `{{ tr "k" "a" "b" }}` fills
// the placeholders of the message. | 多余参数会透传给 Translator.T，因此
// `{{ tr "k" "a" "b" }}` 会填充消息中的占位符。
//
// A caller that wants its own wording replaces "tr" through Options.Funcs; that
// replacement wins over the injected translator, which is then never consulted.
// | 需要自定义措辞的调用方可通过 Options.Funcs 替换 "tr"；该替换优先于注入的译者，
// 此后注入的译者不再被使用。
func tr(key string, _ ...any) string { return key }

// translatorFunc returns the `tr` function bound to t. | translatorFunc 返回绑定 t 的 `tr` 函数。
//
// Its signature matches Translator.T, so the arguments of `{{ tr "k" "a" "b" }}`
// reach the translator unchanged. | 其签名与 Translator.T 一致，
// 因此 `{{ tr "k" "a" "b" }}` 的参数会原样送达译者。
func translatorFunc(t Translator) func(string, ...any) string {
	return func(key string, args ...any) string {
		if t == nil {
			return key
		}
		return t.T(key, args...)
	}
}

// translatorFrom extracts the injected translator from render data. | translatorFrom 从渲染数据中取出注入的译者。
//
// Only a map[string]any holding a Translator under TranslatorKey counts; every
// other data shape (structs, nil, a foreign value under the key) reports nil so
// that `tr` falls back to the key. | 只有「map[string]any 且 TranslatorKey 下存放
// Translator」才算命中；其它数据形态（结构体、nil、该键下存放其它类型的值）一律返回 nil，
// 使 `tr` 退回返回键本身。
func translatorFrom(data any) Translator {
	m, ok := data.(map[string]any)
	if !ok {
		return nil
	}
	t, _ := m[TranslatorKey].(Translator)
	return t
}
