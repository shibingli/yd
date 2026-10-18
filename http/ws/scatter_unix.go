//go:build unix

package ws

// scatterWriteThreshold is the payload size from which a server frame is sent
// with a vectored write on Unix. `writev` sends the frame header and the
// payload in one syscall without copying the payload, so the threshold only
// keeps tiny frames on the cheaper buffered path. | scatterWriteThreshold 是
// Unix 上服务端帧改用分散写的载荷阈值。`writev` 用一次系统调用发送帧头与载荷且不
// 复制载荷，因此阈值只用于让极小帧留在更便宜的缓冲路径上。
const scatterWriteThreshold = 256
