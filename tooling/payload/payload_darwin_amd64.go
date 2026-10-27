//go:build darwin && amd64

// This file embeds the darwin/amd64 tool payloads. | 本文件嵌入 darwin/amd64 工具 payload。
package payload

import _ "embed"

// The locked payload checksums for this platform. | 本平台的 payload 校验和。
const (
	koSHA256          = "94d50d08210f0bd710c7f1abd4a7dea0ff9700adb1f8bd5585e5c9b163658d0e"
	minifySHA256      = "cdfef60b4ea792f94dab426c58d952e914b6d887c612d3c5595485b08aabe254"
	betteralignSHA256 = "78eac5da971dcededa818bb42393ca45698f641869236e58cb59477cf8ab670b"
)

// The gzip payloads embedded for this platform. | 本平台嵌入的 gzip payload。
var (
	//go:embed data/ko_darwin_amd64.gz
	koData []byte
	//go:embed data/minify_darwin_amd64.gz
	minifyData []byte
	//go:embed data/betteralign_darwin_amd64.gz
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
