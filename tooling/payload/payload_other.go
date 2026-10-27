//go:build !(linux && amd64) && !(linux && arm64) && !(windows && amd64) && !(windows && arm64) && !(darwin && amd64) && !(darwin && arm64)

// This file covers platforms without an embedded payload. | 本文件覆盖没有嵌入 payload 的平台。
package payload

// hostEntries returns no payloads on unsupported platforms, leaving every tool to the normal resolution
// chain. | hostEntries 在不支持的平台上返回空集合，让每个工具都走正常解析链。
func hostEntries() []Entry { return nil }
