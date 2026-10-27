// This file implements the content-addressed cache for extracted tool payloads. | 本文件实现解压后工具的
// 内容寻址缓存。
package payload

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shibingli/ydx/errs"
)

// CacheEnv optionally relocates the payload cache root. | CacheEnv 可选地重定位 payload 缓存根。
const CacheEnv = "YD_TOOLS_CACHE_DIR"

// Key returns the hex SHA-256 of data, used as the content address of one payload. | Key 返回 data 的
// 十六进制 SHA-256，作为 payload 的内容地址。
func Key(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// CacheRoot returns the payload cache root. An explicit YD_TOOLS_CACHE_DIR wins; otherwise the per-user cache
// directory hosts <root>/yd/payloads. The second result is false when neither is available, in which case
// callers extract directly into the project. | CacheRoot 返回 payload 缓存根。显式 YD_TOOLS_CACHE_DIR 优先；否则
// 用户缓存目录下使用 <root>/yd/payloads。两者都不可用时第二个返回值为 false，调用方直接解压到项目内。
func CacheRoot() (string, bool) {
	if override := strings.TrimSpace(os.Getenv(CacheEnv)); override != "" {
		if abs, err := filepath.Abs(override); err == nil {
			override = abs
		}
		return filepath.Join(override, "payloads"), true
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(base, "yd", "payloads"), true
}

// InstallAt writes a payload (gzip or raw) to target, reusing a content-addressed cache when possible. The key is
// the compressed payload's SHA-256; an existing non-empty target always wins, while a zero-byte leftover is
// repaired. A cache hit hard-links the cached file (falling back to a copy across volumes), so the same
// payload is decompressed only once per machine. | InstallAt 把 gzip payload 写到 target，并尽可能复用内容
// 寻址缓存。key 是压缩 payload 的 SHA-256；已存在的非空 target 始终优先，零字节残留则会被修复。缓存命中时
// 硬链接缓存文件（跨卷时回退复制），因此同一 payload 每台机器只解压一次。
func InstallAt(target string, data []byte, key string) error {
	if strings.TrimSpace(key) == "" {
		key = Key(data)
	}
	// A zero-byte leftover is never a usable tool: an interrupted earlier write must be repaired. |
	// 零字节残留绝不是可用的工具：早先中断的写入必须被修复。
	if info, err := os.Stat(target); err == nil && !info.IsDir() && info.Size() > 0 {
		return nil
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errs.WrapDetail(err, ErrExtract, "create %s | 创建 %s", dir, dir)
	}
	if cached, ok := cacheEntry(data, key, filepath.Base(target)); ok {
		if err := linkOrCopy(cached, target); err == nil {
			return nil
		}
	}
	return extractPayload(target, data, "")
}

// cacheEntry returns a validated cache path for data, extracting it on a miss. | cacheEntry 返回 data 的
// 有效缓存路径，未命中时先解压。
func cacheEntry(data []byte, key, name string) (string, bool) {
	root, ok := CacheRoot()
	if !ok {
		return "", false
	}
	dir := filepath.Join(root, key)
	target := filepath.Join(dir, name)
	if validCacheEntry(target) {
		return target, true
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false
	}
	_ = os.Remove(target)
	_ = os.Remove(target + ".sha256")
	if err := extractPayload(target, data, dir); err != nil {
		return "", false
	}
	return target, true
}

// validCacheEntry validates a cached file against its sidecar. The sidecar records the extracted SHA-256,
// size and mtime: a normal hit costs one stat, while an externally touched file is re-hashed before use so a
// same-size corruption is never propagated. | validCacheEntry 依据 sidecar 校验缓存文件。sidecar 记录解压后的
// SHA-256、大小与 mtime：正常命中只需一次 stat；文件被外部改动时会先重新计算哈希，因此同大小损坏也不会被传播。
func validCacheEntry(target string) bool {
	info, err := os.Stat(target)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return false
	}
	sidecar, err := os.ReadFile(target + ".sha256")
	if err != nil {
		return false
	}
	fields := strings.Fields(string(sidecar))
	if len(fields) < 2 {
		return false
	}
	size, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || info.Size() != size {
		return false
	}
	if len(fields) >= 3 {
		if mtime, parseErr := strconv.ParseInt(fields[2], 10, 64); parseErr == nil && mtime == info.ModTime().UnixNano() {
			return true
		}
	}
	sum, err := fileSHA256(target)
	if err != nil || sum != fields[0] {
		return false
	}
	writeSidecar(target, fields[0], size)
	return true
}

// fileSHA256 hashes an existing file. | fileSHA256 计算现有文件的哈希。
func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.CopyBuffer(hash, file, make([]byte, 64*1024)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// extractPayload writes data into target atomically. Gzip payloads are decompressed; raw payloads (such as
// the UPX executables) are copied verbatim. When sidecarDir is non-empty, it also writes the extracted
// SHA-256, size and mtime next to the file. | extractPayload 把 data 原子写入 target。gzip payload 会解压；
// 原始 payload（例如 UPX 可执行文件）原样复制。sidecarDir 非空时，同时在文件旁写入解压后的 SHA-256、大小与 mtime。
func extractPayload(target string, data []byte, sidecarDir string) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errs.WrapDetail(err, ErrExtract, "create %s | 创建 %s", dir, dir)
	}
	temp, err := os.CreateTemp(dir, ".payload-*")
	if err != nil {
		return errs.WrapDetail(err, ErrExtract, "create temporary file in %s | 在 %s 创建临时文件", dir, dir)
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	hash := sha256.New()
	writer := io.MultiWriter(temp, hash)
	buffer := make([]byte, 64*1024)
	var written int64
	var writeErr error
	if isGzip(data) {
		reader, gerr := gzip.NewReader(bytes.NewReader(data))
		if gerr != nil {
			cleanup()
			return errs.WrapDetail(gerr, ErrExtract, "gunzip %s | 解压 %s", filepath.Base(target), filepath.Base(target))
		}
		written, writeErr = io.CopyBuffer(writer, reader, buffer)
		_ = reader.Close()
	} else {
		var n int
		n, writeErr = writer.Write(data)
		written = int64(n)
	}
	if writeErr != nil {
		cleanup()
		return errs.WrapDetail(writeErr, ErrExtract, "write %s | 写入 %s", tempPath, tempPath)
	}
	if err := temp.Chmod(0o755); err != nil {
		cleanup()
		return errs.WrapDetail(err, ErrExtract, "chmod %s | 设置权限 %s", tempPath, tempPath)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return errs.WrapDetail(err, ErrExtract, "close %s | 关闭 %s", tempPath, tempPath)
	}
	if err := os.Rename(tempPath, target); err != nil {
		_ = os.Remove(tempPath)
		// A concurrent extraction may have produced a valid entry; a corrupt or locked file is not accepted. |
		// 并发解压可能已产生有效条目；损坏或被锁定的文件不被接受。
		if validCacheEntry(target) {
			return nil
		}
		// A direct extraction has no sidecar; a same-size concurrent copy is still accepted. |
		// 直接解压没有 sidecar；同大小的并发副本仍然接受。
		if sidecarDir == "" {
			if info, statErr := os.Stat(target); statErr == nil && !info.IsDir() && info.Size() == written {
				return nil
			}
		}
		return errs.WrapDetail(err, ErrExtract, "rename to %s | 重命名到 %s", target, target)
	}
	if sidecarDir != "" {
		writeSidecar(target, hex.EncodeToString(hash.Sum(nil)), written)
	}
	return nil
}

// isGzip reports whether data starts with the gzip magic. | isGzip 报告 data 是否以 gzip 魔数开头。
func isGzip(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

// writeSidecar records the extracted hash, size and mtime; a failure is not fatal because the payload itself
// is already correct. | writeSidecar 记录解压后的哈希、大小与 mtime；失败不致命，因为 payload 本身已经正确。
func writeSidecar(target, sum string, size int64) {
	mtime := int64(0)
	if info, err := os.Stat(target); err == nil {
		mtime = info.ModTime().UnixNano()
	}
	content := sum + " " + strconv.FormatInt(size, 10) + " " + strconv.FormatInt(mtime, 10) + "\n"
	temp, err := os.CreateTemp(filepath.Dir(target), ".sidecar-*")
	if err != nil {
		return
	}
	tempPath := temp.Name()
	if _, err := temp.WriteString(content); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return
	}
	if err := os.Rename(tempPath, target+".sha256"); err != nil {
		_ = os.Remove(tempPath)
	}
}

// linkOrCopy hard-links src to target, falling back to a copy when the file system refuses links. |
// linkOrCopy 把 src 硬链接到 target，文件系统拒绝链接时回退为复制。
func linkOrCopy(src, target string) error {
	// A zero-byte leftover is never a usable tool: an interrupted earlier write must be repaired. |
	// 零字节残留绝不是可用的工具：早先中断的写入必须被修复。
	if info, err := os.Stat(target); err == nil && !info.IsDir() && info.Size() > 0 {
		return nil
	}
	if err := os.Link(src, target); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	temp, err := os.CreateTemp(filepath.Dir(target), ".copy-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	buffer := make([]byte, 64*1024)
	if _, err := io.CopyBuffer(temp, in, buffer); err != nil {
		cleanup()
		return err
	}
	if err := temp.Chmod(0o755); err != nil {
		cleanup()
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, target); err != nil {
		_ = os.Remove(tempPath)
		// Only a same-size concurrent copy is accepted; anything else is reported. | 只有同大小的并发副本会被接受，
		// 其余情况如实上报。
		if srcInfo, statErr := os.Stat(src); statErr == nil {
			if tgtInfo, targetErr := os.Stat(target); targetErr == nil && !tgtInfo.IsDir() && tgtInfo.Size() == srcInfo.Size() {
				return nil
			}
		}
		return err
	}
	return nil
}
