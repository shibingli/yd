// Package b64x 提供自适应的 Base64 编解码抽象层，隐藏底层实现的差异。
//
// 后端分流（由构建标签决定，对调用方完全透明）：
//
//   - amd64 且未指定 b64x_std 标签：使用 SIMD 加速的 github.com/cristalhq/base64，
//     Backend() 返回 "base64x"；
//   - 其他架构（arm64/riscv64/...）或显式指定 b64x_std 标签：使用标准库
//     encoding/base64，Backend() 返回 "encoding/base64"。
//
// 两个后端暴露完全相同的符号与语义：StdEncoding、RawStdEncoding、URLEncoding、
// RawURLEncoding 四个编码变体，以及 Encoding 的 Encode、Decode、EncodeToString、
// DecodeString、EncodedLen、DecodedLen、AppendEncode、AppendDecode 方法，
// 长度计算与编码结果均与 encoding/base64 逐字节一致。
//
// 零拷贝：AppendEncode、AppendDecode 在目标缓冲区容量充足时复用其底层数组，
// 不产生新的分配；DecodeString 只在内部缓冲区中直接产出结果。base64x 后端的
// Decode 因为需要兼容 SIMD 解码器按整块写出的特性，会借助池化缓冲区（容量上限
// 64 KiB，超出不回收）中转并拷贝一次；降级后端直接写入 dst。追求极致零拷贝的
// 热路径应优先使用 AppendDecode。
//
// 与 encoding/base64 的差异（两个后端行为一致）：
//
//   - 解码失败时 Decode 返回 (0, err)、DecodeString 返回 (nil, err)、
//     AppendDecode 原样返回 dst，都不会把部分解码结果交给调用方；
//   - 目标缓冲区长度小于 DecodedLen(len(src)) 时返回 ErrShortBuffer，而不是越界 panic；
//   - 一切非法数据的错误都可用 errors.Is(err, ErrInvalidData) 判定，
//     同时保留底层错误（cristalhq 的 ErrWrongData 或标准库的 CorruptInputError）供
//     errors.Is 进一步匹配。
package b64x

import "errors"

var (
	// ErrShortBuffer 表示目标缓冲区长度小于 DecodedLen(len(src))，无法容纳解码结果。
	ErrShortBuffer = errors.New("b64x: 目标缓冲区过短")

	// ErrInvalidData 表示输入不是合法的 Base64 数据；底层后端的原始错误通过
	// fmt.Errorf 的 %w 一并包装，可用 errors.Is 匹配。
	ErrInvalidData = errors.New("b64x: 非法的 Base64 数据")
)
