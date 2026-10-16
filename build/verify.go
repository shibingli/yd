package build

import (
	"bytes"
	"io"
	"os"
	"time"

	"github.com/yun-dev/yd/errx"
)

// Sentinel errors of the verification stage. | 校验阶段的哨兵错误。
var (
	// ErrArtifactMissing indicates that the expected artifact does not exist or is empty. | ErrArtifactMissing 表示预期产物不存在或为空。
	ErrArtifactMissing = errx.New("build: artifact missing after build | build：构建后未找到产物")
	// ErrArtifactFormat indicates that the artifact magic does not match the target platform. | ErrArtifactFormat 表示产物魔数与目标平台不符。
	ErrArtifactFormat = errx.New("build: artifact format mismatch | build：产物格式与目标平台不符")
)

// Executable magics recognised by the verifier. | 校验器能识别的可执行文件魔数。
var (
	// magicELF is the ELF magic shared by Linux, the BSDs, Solaris and Android. | magicELF 是 Linux、BSD 系、Solaris 与 Android 共用的 ELF 魔数。
	magicELF = []byte{0x7f, 'E', 'L', 'F'}
	// magicPE is the DOS/PE magic of Windows executables. | magicPE 是 Windows 可执行文件的 DOS/PE 魔数。
	magicPE = []byte{'M', 'Z'}
	// magicWASM is the WebAssembly module magic used by js/wasm and wasip1. | magicWASM 是 js/wasm 与 wasip1 使用的 WebAssembly 模块魔数。
	magicWASM = []byte{0x00, 0x61, 0x73, 0x6d}
)

// executableFormatOf maps a GOOS to the executable format Go emits for it. | executableFormatOf 把 GOOS 映射为 Go 为其产出的可执行格式。
//
// plan9 is left unmodelled ("unknown") because its a.out header varies per
// architecture; unknown formats skip the magic check and are reported by size
// only. | plan9 未建模（返回 "unknown"），因为它的 a.out 头随架构而变；未知格式跳过魔数校验，
// 仅按大小校验。
func executableFormatOf(goos string) string {
	switch goos {
	case "windows":
		return "pe"
	case "darwin", "ios":
		return "macho"
	case "js", "wasip1":
		return "wasm"
	case "aix":
		return "xcoff"
	case "plan9":
		return "unknown"
	default:
		return "elf"
	}
}

// matchesFormat reports whether head carries the magic of the given format. | matchesFormat 判断 head 是否带有指定格式的魔数。
func matchesFormat(head []byte, format string) bool {
	switch format {
	case "pe":
		return bytes.HasPrefix(head, magicPE)
	case "wasm":
		return bytes.HasPrefix(head, magicWASM)
	case "macho":
		// Thin 32/64-bit images both byte orders plus the fat/universal container. | 32/64 位镜像的两种字节序，以及 fat/universal 容器。
		for _, magic := range [][]byte{
			{0xfe, 0xed, 0xfa, 0xce}, // Mach-O 32-bit big-endian | 32 位大端
			{0xce, 0xfa, 0xed, 0xfe}, // Mach-O 32-bit little-endian | 32 位小端
			{0xfe, 0xed, 0xfa, 0xcf}, // Mach-O 64-bit big-endian | 64 位大端
			{0xcf, 0xfa, 0xed, 0xfe}, // Mach-O 64-bit little-endian | 64 位小端
			{0xca, 0xfe, 0xba, 0xbe}, // fat/universal big-endian | fat/universal 大端
			{0xbe, 0xba, 0xfe, 0xca}, // fat/universal little-endian | fat/universal 小端
		} {
			if bytes.HasPrefix(head, magic) {
				return true
			}
		}
		return false
	case "xcoff":
		// XCOFF32 and XCOFF64 both start with 0x01 followed by DF or F7. | XCOFF32 与 XCOFF64 都以 0x01 开头，第二个字节为 DF 或 F7。
		return len(head) >= 2 && head[0] == 0x01 && (head[1] == 0xdf || head[1] == 0xf7)
	default:
		return bytes.HasPrefix(head, magicELF)
	}
}

// verifyArtifact checks that the build produced a non-empty executable carrying the
// magic of the target platform, and reports it as an Artifact. | verifyArtifact 校验构建
// 产出了非空且带有目标平台魔数的可执行文件，并以 Artifact 形式报告。
func verifyArtifact(path string, target Target, d time.Duration) (*Artifact, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrArtifactMissing, "%s | %s", path, path)
	}
	if info.IsDir() || info.Size() == 0 {
		return nil, errx.Detail(ErrArtifactMissing, "%s is empty | %s 为空文件", path, path)
	}
	head, err := readHeader(path, 4)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrArtifactMissing, "%s | %s", path, path)
	}
	if format := executableFormatOf(target.GOOS); !matchesFormat(head, format) {
		return nil, errx.Detail(ErrArtifactFormat,
			"artifact %s is not a %s executable for %s (magic % x) | 产物 %s 不是 %s 平台（%s）的可执行文件（魔数 % x）",
			path, format, target, head, path, target, format, head)
	}
	return &Artifact{
		Path:     path,
		GOOS:     target.GOOS,
		GOARCH:   target.GOARCH,
		Bytes:    info.Size(),
		Duration: d,
	}, nil
}

// readHeader reads the first n bytes of the file at path. | readHeader 读取 path 文件开头的 n 个字节。
func readHeader(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, n)
	if _, err := io.ReadFull(f, head); err != nil {
		return nil, err
	}
	return head, nil
}
