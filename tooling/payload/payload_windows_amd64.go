//go:build windows && amd64

// This file embeds the windows/amd64 tool payloads. | 本文件嵌入 windows/amd64 工具 payload。
package payload

import _ "embed"

// The locked payload checksums for this platform. | 本平台的 payload 校验和。
const (
	koSHA256          = "97561a598bad913810507a9753fe06b655e350061cb7bb3ba21e9484a56f75f1"
	minifySHA256      = "0b53c012bb61eb5710e7c83430e9e578a213f1a9a57d6354e318038fc8202e73"
	betteralignSHA256 = "cf827aed262dd9b83983a62a77119daeb48a11751d3174d4f52419f34376fa0a"
)

// The gzip payloads embedded for this platform. | 本平台嵌入的 gzip payload。
var (
	//go:embed data/ko_windows_amd64.gz
	koData []byte
	//go:embed data/minify_windows_amd64.gz
	minifyData []byte
	//go:embed data/betteralign_windows_amd64.gz
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
