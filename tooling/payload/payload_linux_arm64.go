//go:build linux && arm64

// This file embeds the linux/arm64 tool payloads. | 本文件嵌入 linux/arm64 工具 payload。
package payload

import _ "embed"

// The locked payload checksums for this platform. | 本平台的 payload 校验和。
const (
	koSHA256          = "210807bbbc6e3da9f02c93324fc754ff8eebd4063c8cf1f076f1d9f8eee3360d"
	minifySHA256      = "5d4865fe59399d503c943140864e7595427f6de1b9daa0383da203ee4325e310"
	betteralignSHA256 = "718547dc8d559d43909a361f4d6aad12d119c9db2a005b56bf394175297f54ea"
)

// The gzip payloads embedded for this platform. | 本平台嵌入的 gzip payload。
var (
	//go:embed data/ko_linux_arm64.gz
	koData []byte
	//go:embed data/minify_linux_arm64.gz
	minifyData []byte
	//go:embed data/betteralign_linux_arm64.gz
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
