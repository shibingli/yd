//go:build !unix

package ws

// scatterWriteThreshold effectively disables the size-triggered scatter path on
// platforms without cheap vectored socket writes (Windows sends multi-buffer
// WSASend through an extra WSABuf pool; TLS and stream transports fall back to
// one Write per buffer). Frames larger than the write buffer still use the
// scatter list, because copying them would be strictly worse. | scatterWriteThreshold
// 在没有廉价分散套接字写的平台上实质关闭「按大小触发」的分散路径（Windows 的多缓冲
// WSASend 要经过额外的 WSABuf 池；TLS 与流传输会退化为每个缓冲一次 Write）。大于写
// 缓冲的帧仍走分散列表，因为复制它们严格更差。
const scatterWriteThreshold = 1 << 30
