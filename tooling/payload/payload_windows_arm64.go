//go:build windows && arm64

// This file embeds the windows/arm64 tool payloads. | 本文件嵌入 windows/arm64 工具 payload。
package payload

import _ "embed"

// The locked payload checksums for this platform. | 本平台的 payload 校验和。
const (
	koSHA256          = "5ee52f9ba61b948b7af64a4c9a60299059a56a4da73ff6aff91edd5fde44200d"
	minifySHA256      = "d2692507049b0cbc205b418ac9072a73374dbb75bf4ea186ff5be156144ee7cb"
	betteralignSHA256 = "e0551082e2fdce3344543c7a70fda9ac42b6299794f13a5cb06a3582371cfc2d"
)

// The gzip payloads embedded for this platform. | 本平台嵌入的 gzip payload。
var (
	//go:embed data/ko_windows_arm64.gz
	koData []byte
	//go:embed data/minify_windows_arm64.gz
	minifyData []byte
	//go:embed data/betteralign_windows_arm64.gz
	betteralignData []byte
)

// hostEntries returns the payloads for this platform. | hostEntries 返回本平台的 payload。
func hostEntries() []Entry {
	return []Entry{
		{Tool: ToolKo, Version: VersionKo, FileName: "ko.exe", Data: koData, SHA256: koSHA256},
		{Tool: ToolMinify, Version: VersionMinify, FileName: "minify.exe", Data: minifyData, SHA256: minifySHA256},
		{Tool: ToolBetteralign, Version: VersionBetteralign, FileName: "betteralign.exe", Data: betteralignData, SHA256: betteralignSHA256},
	}
}
