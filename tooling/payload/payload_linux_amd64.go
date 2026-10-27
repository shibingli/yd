//go:build linux && amd64

// This file embeds the linux/amd64 tool payloads. | 本文件嵌入 linux/amd64 工具 payload。
package payload

import _ "embed"

// The locked payload checksums for this platform. | 本平台的 payload 校验和。
const (
	koSHA256          = "9d65096c6b743146e31a2a479f196e954f3d54355ac8df874fc544950e6985f0"
	minifySHA256      = "1c04547869c421a16a8bde29e956f8084e02d418d601cb0b7323a05a0099fde8"
	betteralignSHA256 = "5193376acd70dc465ac02df8f6253a1d5674e2219e42b61b3a6a4f5d22ea39d1"
)

// The gzip payloads embedded for this platform. | 本平台嵌入的 gzip payload。
var (
	//go:embed data/ko_linux_amd64.gz
	koData []byte
	//go:embed data/minify_linux_amd64.gz
	minifyData []byte
	//go:embed data/betteralign_linux_amd64.gz
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
