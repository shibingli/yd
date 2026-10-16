// Package jsonx 提供自适应的 JSON 编解码抽象层。
//
// 同一套 API 在不同架构与构建标签下自动选择最优后端，调用方无需感知底层实现：
//
//   - amd64 且未设置 jsonx_std 标签：github.com/bytedance/sonic（JIT + SIMD，见 jsonx_amd64.go）
//   - 非 amd64 且未设置 jsonx_std 标签：github.com/goccy/go-json（纯 Go 高性能，见 jsonx_generic.go）
//   - 设置 jsonx_std 标签：标准库 encoding/json（排障与兼容性对照，见 jsonx_std.go）
//
// 三个后端导出完全相同的符号集（Marshal / Unmarshal / MarshalIndent / Valid /
// NewEncoder / NewDecoder / Backend），且都配备对应的构建约束，互斥编译、符号唯一，
// 因此任何受支持的目标平台都能直接构建。当前生效的后端名称可用 Backend() 查询。
//
// 语义基准是 encoding/json：字符串按 HTML 转义（& < > 转义为 & < >）、
// map 的 key 排序输出、[]byte 走 StdEncoding base64、time.Time 走 RFC3339Nano、
// 兼容 json.Marshaler / json.TextMarshaler / json.RawMessage / omitempty。
// sonic 后端固定使用 sonic.ConfigStd 这一「与 encoding/json 完全兼容」的配置，
// 因此切换后端不会改变输出形状；go-json 与标准库本身即为 drop-in 关系。
//
// 典型用法：
//
//	b, err := jsonx.Marshal(v)
//	if err != nil {
//		return err
//	}
//	if err := jsonx.Unmarshal(b, &out); err != nil {
//		return err
//	}
//
// 需要跨后端稳定判定错误时，可用 errors.Is 配合 ErrSyntax / ErrTypeMismatch：
//
//	if err := jsonx.Unmarshal(b, &out); errors.Is(err, jsonx.ErrSyntax) {
//		// 输入不是合法 JSON
//	}
//
// 说明：Backend() 由各后端实现文件提供（jsonx_amd64.go / jsonx_generic.go /
// jsonx_std.go），本文件只承载包级文档、共享错误变量与错误归类辅助函数。
//
// 已知后端差异：sonic 的 Valid 只做结构校验，对字符串中的非法转义序列（如 \x、\u12）
// 与未转义控制字符比 encoding/json 宽松（会判为合法）；go-json 与标准库完全一致。
// 需要严格校验时请显式使用 encoding/json，或设置 jsonx_std 构建标签。
package jsonx

import (
	"errors"
	"fmt"
)

var (
	// ErrSyntax 表示输入不是合法的 JSON 文本。
	//
	// 各后端（sonic / go-json / encoding/json）的语法错误类型互不兼容，jsonx 会在
	// 错误路径上统一归类，因此 errors.Is(err, jsonx.ErrSyntax) 在任意后端下都成立；
	// 后端原始错误依旧通过 %w 保留在错误链中，可用 errors.Is/As 进一步判定。
	ErrSyntax = errors.New("jsonx: invalid JSON syntax")

	// ErrTypeMismatch 表示 JSON 文本本身合法，但与目标 Go 类型不匹配
	// （例如把字符串解析进 int、把数组解析进 struct）。
	//
	// 与 ErrSyntax 一样，该判定在各后端间保持一致。
	ErrTypeMismatch = errors.New("jsonx: JSON value does not match target Go type")
)

// wrapDecodeError 在错误路径上对后端返回的解析错误做一次跨后端归类：
// 输入不是合法 JSON 时包装为 ErrSyntax，否则包装为 ErrTypeMismatch。
//
// 归类依据是共享的 Valid 复检（仅在出错时执行，不影响正常路径性能），因此不依赖
// 任何后端私有错误类型。两个错误均以 %w 包装，原始后端错误保持不变地保留在错误链中。
func wrapDecodeError(data []byte, err error) error {
	if err == nil {
		return nil
	}
	if !Valid(data) {
		return fmt.Errorf("%w: %w", ErrSyntax, err)
	}
	return fmt.Errorf("%w: %w", ErrTypeMismatch, err)
}
