// Package jsonx provides an adaptive JSON codec abstraction layer. | Package jsonx 提供自适应的 JSON 编解码抽象层。
//
// The same API automatically selects the best backend under different architectures and build tags, with no need for callers to be aware of the underlying implementation: | 同一套 API 在不同架构与构建标签下自动选择最优后端，调用方无需感知底层实现：
//
//   - amd64 without the jsonx_std tag: github.com/bytedance/sonic (JIT + SIMD, see jsonx_amd64.go) | amd64 且未设置 jsonx_std 标签：github.com/bytedance/sonic（JIT + SIMD，见 jsonx_amd64.go）
//   - non-amd64 without the jsonx_std tag: github.com/goccy/go-json (pure-Go high performance, see jsonx_generic.go) | 非 amd64 且未设置 jsonx_std 标签：github.com/goccy/go-json（纯 Go 高性能，见 jsonx_generic.go）
//   - with the jsonx_std tag: the standard library encoding/json (troubleshooting and compatibility reference, see jsonx_std.go) | 设置 jsonx_std 标签：标准库 encoding/json（排障与兼容性对照，见 jsonx_std.go）
//
// All three backends export exactly the same symbol set (Marshal / Unmarshal / MarshalIndent / Valid / | 三个后端导出完全相同的符号集（Marshal / Unmarshal / MarshalIndent / Valid /
// NewEncoder / NewDecoder / Backend), and each carries its own build constraint, compiling mutually exclusively with unique symbols, | NewEncoder / NewDecoder / Backend），且都配备对应的构建约束，互斥编译、符号唯一，
// so any supported target platform can be built directly. The currently active backend name can be queried via Backend(). | 因此任何受支持的目标平台都能直接构建。当前生效的后端名称可用 Backend() 查询。
//
// The semantic baseline is encoding/json: strings are HTML-escaped (& < > are escaped to & < >), | 语义基准是 encoding/json：字符串按 HTML 转义（& < > 转义为 & < >）、
// map keys are output in sorted order, []byte goes through StdEncoding base64, time.Time goes through RFC3339Nano, | map 的 key 排序输出、[]byte 走 StdEncoding base64、time.Time 走 RFC3339Nano、
// and json.Marshaler / json.TextMarshaler / json.RawMessage / omitempty are supported. | 兼容 json.Marshaler / json.TextMarshaler / json.RawMessage / omitempty。
// The sonic backend always uses sonic.ConfigStd, the configuration that is "fully compatible with encoding/json", | sonic 后端固定使用 sonic.ConfigStd 这一「与 encoding/json 完全兼容」的配置，
// so switching backends never changes the output shape; go-json and the standard library are themselves drop-in replacements. | 因此切换后端不会改变输出形状；go-json 与标准库本身即为 drop-in 关系。
//
// Typical usage: | 典型用法：
//
//	b, err := jsonx.Marshal(v)
//	if err != nil {
//		return err
//	}
//	if err := jsonx.Unmarshal(b, &out); err != nil {
//		return err
//	}
//
// To determine errors stably across backends, combine errors.Is with ErrSyntax / ErrTypeMismatch: | 需要跨后端稳定判定错误时，可用 errors.Is 配合 ErrSyntax / ErrTypeMismatch：
//
//	if err := jsonx.Unmarshal(b, &out); errors.Is(err, jsonx.ErrSyntax) {
//		// 输入不是合法 JSON
//	}
//
// Note: Backend() is provided by each backend implementation file (jsonx_amd64.go / jsonx_generic.go / | 说明：Backend() 由各后端实现文件提供（jsonx_amd64.go / jsonx_generic.go /
// jsonx_std.go); this file only carries the package documentation, the shared error variables and the error classification helper. | jsonx_std.go），本文件只承载包级文档、共享错误变量与错误归类辅助函数。
//
// Known backend differences: sonic's Valid only performs structural validation, and is more lenient than encoding/json | 已知后端差异：sonic 的 Valid 只做结构校验，对字符串中的非法转义序列（如 \x、\u12）
// about illegal escape sequences (such as \x, \u12) and unescaped control characters (it treats them as valid); go-json matches the standard library exactly. | 与未转义控制字符比 encoding/json 宽松（会判为合法）；go-json 与标准库完全一致。
// For strict validation, use encoding/json explicitly, or set the jsonx_std build tag. | 需要严格校验时请显式使用 encoding/json，或设置 jsonx_std 构建标签。
package jsonx

import (
	"errors"
	"fmt"
)

var (
	// ErrSyntax indicates the input is not valid JSON text. | ErrSyntax 表示输入不是合法的 JSON 文本。
	//
	// The syntax error types of the backends (sonic / go-json / encoding/json) are mutually incompatible, so jsonx | 各后端（sonic / go-json / encoding/json）的语法错误类型互不兼容，jsonx 会在
	// classifies them uniformly on the error path; therefore errors.Is(err, jsonx.ErrSyntax) holds under any backend; | 错误路径上统一归类，因此 errors.Is(err, jsonx.ErrSyntax) 在任意后端下都成立；
	// the original backend error is still preserved in the error chain through %w, and can be further inspected with errors.Is/As. | 后端原始错误依旧通过 %w 保留在错误链中，可用 errors.Is/As 进一步判定。
	ErrSyntax = errors.New("jsonx: invalid JSON syntax")

	// ErrTypeMismatch indicates the JSON text itself is valid but does not match the target Go type | ErrTypeMismatch 表示 JSON 文本本身合法，但与目标 Go 类型不匹配
	// (for example, parsing a string into an int, or an array into a struct). | （例如把字符串解析进 int、把数组解析进 struct）。
	//
	// Like ErrSyntax, this classification stays consistent across all backends. | 与 ErrSyntax 一样，该判定在各后端间保持一致。
	ErrTypeMismatch = errors.New("jsonx: JSON value does not match target Go type")
)

// wrapDecodeError performs a one-time cross-backend classification of the parse error returned by a backend on the error path: | wrapDecodeError 在错误路径上对后端返回的解析错误做一次跨后端归类：
// when the input is not valid JSON it is wrapped as ErrSyntax, otherwise as ErrTypeMismatch. | 输入不是合法 JSON 时包装为 ErrSyntax，否则包装为 ErrTypeMismatch。
//
// The classification is based on the shared Valid re-check (performed only on the error path, so normal-path performance is unaffected), | 归类依据是共享的 Valid 复检（仅在出错时执行，不影响正常路径性能），因此不依赖
// so it depends on no backend-private error type. Both errors are wrapped with %w, leaving the original backend error intact in the error chain. | 任何后端私有错误类型。两个错误均以 %w 包装，原始后端错误保持不变地保留在错误链中。
func wrapDecodeError(data []byte, err error) error {
	if err == nil {
		return nil
	}
	if !Valid(data) {
		return fmt.Errorf("%w: %w", ErrSyntax, err)
	}
	return fmt.Errorf("%w: %w", ErrTypeMismatch, err)
}
