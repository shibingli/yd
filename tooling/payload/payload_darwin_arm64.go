//go:build darwin && arm64

// This file embeds the darwin/arm64 tool payloads. | 本文件嵌入 darwin/arm64 工具 payload。
package payload

import _ "embed"

// The locked payload checksums for this platform. | 本平台的 payload 校验和。
const (
	koSHA256          = "156224605be1870e28e9564ca27c42480284671c5125e597b0dd73d3e9ad20db"
	minifySHA256      = "f40b794cc5ac3bdefd301ebd5a723e0b53b12539192f086dafc0d7801c94ad11"
	betteralignSHA256 = "f3a367d55c225d8da2fa64b916f40c2f1b6f60989ac0434caccc156f4ebe46d1"
)

// The gzip payloads embedded for this platform. | 本平台嵌入的 gzip payload。
var (
	//go:embed data/ko_darwin_arm64.gz
	koData []byte
	//go:embed data/minify_darwin_arm64.gz
	minifyData []byte
	//go:embed data/betteralign_darwin_arm64.gz
	betteralignData []byte
)

// hostEntries returns the payloads for this platform. | hostEntries 返回本平台的 payload。
func hostEntries() []Entry {
	return []Entry{
		{Tool: ToolKo, Version: VersionKo, FileName: "ko", Data: koData, SHA256: koSHA256},
		{Tool: ToolMinify, Version: VersionMinify, FileName: "minify", Data: minifyData, SHA256: minifySHA256},
		{Tool: ToolBetteralign, Version: VersionBetteralign, FileName: "betteralign", Data: betteralignData, SHA256: betteralignSHA256},
	}
}
