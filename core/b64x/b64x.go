// Package b64x provides an adaptive Base64 codec abstraction layer that hides the differences between underlying implementations. | Package b64x 提供自适应的 Base64 编解码抽象层，隐藏底层实现的差异。
//
// Backend routing (determined by build tags, fully transparent to callers): | 后端分流（由构建标签决定，对调用方完全透明）：
//
//   - amd64 without the b64x_std tag: a size-aware hybrid. Inputs below the
//     measured crossover (32 bytes) use github.com/cristalhq/base64, which has
//     lower SIMD setup cost; larger inputs use the AVX2/SSE-accelerated
//     github.com/cloudwego/base64x. Encode and decode tune independently, and
//     the append forms extend the destination slice directly. Backend() returns
//     "cloudwego/base64x+cristalhq"; | amd64 且未指定 b64x_std 标签：按尺寸分流的混合后端。
//     小于实测交叉点（32 字节）的输入使用 SIMD 启动开销更低的 github.com/cristalhq/base64；
//     更大的输入使用 AVX2/SSE 加速的 github.com/cloudwego/base64x。编码与解码独立调优，
//     追加式接口直接扩展目标切片。Backend() 返回 "cloudwego/base64x+cristalhq"；
//   - other architectures that cristalhq supports (386/arm/arm64/riscv64/ppc64le/s390x/...)
//     without the b64x_std tag: github.com/cristalhq/base64, with encoding/base64 for
//     CR/LF-bearing input and for the allocating string form; Backend() returns "cristalhq/base64"; | 其他
//     cristalhq 支持的架构（386/arm/arm64/riscv64/ppc64le/s390x/...）且未指定 b64x_std 标签：使用
//     github.com/cristalhq/base64；含 CR/LF 的输入与分配式字符串入口回退到 encoding/base64；
//     Backend() 返回 "cristalhq/base64"；
//   - architectures without a third-party backend (currently loong64) or with the b64x_std
//     tag set explicitly: the standard library encoding/base64, Backend() returns
//     "encoding/base64". | 没有第三方后端支持的架构（当前为 loong64）或显式指定 b64x_std 标签：
//     使用标准库 encoding/base64，Backend() 返回 "encoding/base64"。
//
// All backends expose exactly the same symbols and semantics: the four encoding variants | 所有后端暴露完全相同的符号与语义：StdEncoding、RawStdEncoding、URLEncoding、
// StdEncoding, RawStdEncoding, URLEncoding and RawURLEncoding, plus the Encoding methods | RawURLEncoding 四个编码变体，以及 Encoding 的 Encode、Decode、EncodeToString、
// Encode, Decode, EncodeToString, DecodeString, EncodedLen, DecodedLen, AppendEncode and AppendDecode, | DecodeString、EncodedLen、DecodedLen、AppendEncode、AppendDecode 方法，
// with length calculations and encoding results byte-for-byte identical to encoding/base64. | 长度计算与编码结果均与 encoding/base64 逐字节一致。
//
// Zero copy: AppendEncode and AppendDecode reuse the underlying array of the destination buffer when its capacity is sufficient, | 零拷贝：AppendEncode、AppendDecode 在目标缓冲区容量充足时复用其底层数组，
// producing no new allocation; DecodeString allocates only the result. On amd64 the hybrid delivers roughly | 不产生新的分配；DecodeString 只分配结果本身。amd64 混合后端在 1 KiB-64 KiB 载荷上约为
// 8-12x encoding/base64 for 1 KiB-64 KiB payloads while keeping cristalhq's advantage for tiny inputs; | encoding/base64 的 8-12 倍，同时保留 cristalhq 在小输入上的优势；
// cristalhq/base64 is used on the other supported architectures. | 其他受支持架构使用 cristalhq/base64。
// Decode writes directly into dst, and every backend behaves the same. | Decode 直接写入 dst，所有后端行为一致。
//
// Differences from encoding/base64 (all backends behave identically): | 与 encoding/base64 的差异（所有后端行为一致）：
//
//   - on decode failure Decode returns (0, err); like encoding/base64 it may leave partial output in dst. | 解码失败时 Decode 返回 (0, err)；
//     DecodeString returns (nil, err) and AppendDecode returns dst unchanged, so those two never hand a partial result to the caller. | 与 encoding/base64 一样，dst 可能留有部分输出。DecodeString 返回 (nil, err)，AppendDecode 原样返回 dst，这两者绝不会把部分结果交给调用方；
//   - when the destination buffer is shorter than DecodedLen(len(src)) it returns ErrShortBuffer instead of panicking out of bounds; | 目标缓冲区长度小于 DecodedLen(len(src)) 时返回 ErrShortBuffer，而不是越界 panic；
//   - every illegal-data error can be matched with errors.Is(err, ErrInvalidData), | 一切非法数据的错误都可用 errors.Is(err, ErrInvalidData) 判定，
//     while the underlying error (cristalhq's ErrWrongData or the standard library's CorruptInputError) is kept for | 同时保留底层错误（cristalhq 的 ErrWrongData 或标准库的 CorruptInputError）供
//     further matching with errors.Is. | errors.Is 进一步匹配。
package b64x

import "github.com/shibingli/yd/core/errx"

// decodeWriteSlack is spare capacity reserved for SIMD decoders, which may write a
// few bytes past DecodedLen while filling a block. It is never exposed in the
// returned slice. | decodeWriteSlack 是给 SIMD 解码器预留的额外容量：填充块时可能写
// 过 DecodedLen 几个字节，但它绝不会出现在返回切片中。
const decodeWriteSlack = 4

var (
	// ErrShortBuffer indicates the destination buffer is shorter than DecodedLen(len(src)) and cannot hold the decode result. | ErrShortBuffer 表示目标缓冲区长度小于 DecodedLen(len(src))，无法容纳解码结果。
	ErrShortBuffer = errx.New("b64x: destination buffer is too short | b64x：目标缓冲区过短")

	// ErrInvalidData indicates the input is not valid Base64 data; the underlying backend's original error is also annotated | ErrInvalidData 表示输入不是合法的 Base64 数据；底层后端的原始错误通过
	// with errx.Wrap, so it can be matched with errors.Is. | errx.Wrap 一并注解，可用 errors.Is 匹配。
	ErrInvalidData = errx.New("b64x: invalid Base64 data | b64x：非法的 Base64 数据")
)
