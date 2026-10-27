// This file tests the embedded payload selection, checksums and extraction. | 本文件测试嵌入 payload 的选择、
// 校验和与解压。
package payload

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestHostPayloadsMatchChecksums pins the exact bytes shipped inside yd. | TestHostPayloadsMatchChecksums
// 固定 yd 内嵌的确切字节。
func TestHostPayloadsMatchChecksums(t *testing.T) {
	entries := Host()
	if len(entries) == 0 {
		skipOrFailUnsupported(t)
		return
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		sum := sha256.Sum256(entry.Data)
		if got := hex.EncodeToString(sum[:]); got != entry.SHA256 {
			t.Errorf("%s SHA-256 = %s | 期望 %s（payload 漂移，请同步校验和）", entry.Tool, got, entry.SHA256)
		}
		if entry.Version == "" || entry.FileName == "" {
			t.Errorf("%s 的版本或文件名为空 | %s has an empty version or file name", entry.Tool, entry.Tool)
		}
		seen[entry.Tool] = true
	}
	for _, tool := range []string{ToolKo, ToolMinify, ToolBetteralign} {
		if !seen[tool] {
			t.Errorf("当前平台缺少 %s payload | the host payload set misses %s", tool, tool)
		}
	}
}

// TestLookupAndMarkers pins the registry lookups and the read-only marker shape. | TestLookupAndMarkers
// 固定注册表查找与只读标记形态。
func TestLookupAndMarkers(t *testing.T) {
	if entry, ok := Lookup(ToolKo); !ok || entry.Tool != ToolKo {
		t.Fatalf("Lookup(ko) = %+v, %v | 期望找到 ko", entry, ok)
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("未知工具不应返回 payload | an unknown tool must not yield a payload")
	}
	marker := Marker(ToolKo)
	if !IsMarker(marker) || IsMarker(filepath.Join("x", "ko")) {
		t.Errorf("标记判定错误: %q | marker detection is wrong: %q", marker, marker)
	}
}

// TestEnsureExtractsAtomicallyAndKeepsExisting pins gzip extraction, the executable mode (outside Windows)
// and the "existing file wins" rule. | TestEnsureExtractsAtomicallyAndKeepsExisting 固定 gzip 解压、可执行权限
// （Windows 之外）与「已有文件优先」规则。
func TestEnsureExtractsAtomicallyAndKeepsExisting(t *testing.T) {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write([]byte("payload-binary")); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	dir := t.TempDir()
	entry := Entry{Tool: ToolKo, Version: VersionKo, FileName: "tool", Data: buf.Bytes()}
	path, ok, err := ensureEntry(dir, entry)
	if err != nil || !ok {
		t.Fatalf("ensureEntry() = %q, %v, %v", path, ok, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "payload-binary" {
		t.Fatalf("内容 = %q, %v | content = %q, %v", data, err, data, err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode()&0o111 == 0 {
			t.Errorf("mode = %v | 期望可执行位", st.Mode())
		}
	}

	// An existing file must win, even when the payload changed. | 已存在的文件必须优先，即使 payload 变了。
	second := Entry{Tool: ToolKo, Version: VersionKo, FileName: "tool", Data: []byte("not-gzip")}
	again, ok, err := ensureEntry(dir, second)
	if err != nil || !ok || again != path {
		t.Fatalf("第二次 ensureEntry() = %q, %v, %v", again, ok, err)
	}
	if data, _ := os.ReadFile(again); string(data) != "payload-binary" {
		t.Errorf("已有文件被覆盖：%q | the existing file was overwritten: %q", data, data)
	}
}

// TestEnsureUnknownTool pins the graceful miss. | TestEnsureUnknownTool 固定优雅缺失。
func TestEnsureUnknownTool(t *testing.T) {
	path, ok, err := Ensure(t.TempDir(), "nope")
	if err != nil || ok || path != "" {
		t.Fatalf("Ensure(nope) = %q, %v, %v | 期望空结果且无错误", path, ok, err)
	}
}

// skipOrFailUnsupported skips only on platforms the package intentionally does not cover, and fails when a
// covered platform is missing its payload. | skipOrFailUnsupported 仅在包刻意不覆盖的平台跳过；覆盖平台缺少
// payload 时直接失败。
func skipOrFailUnsupported(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "windows", "darwin":
		if runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" {
			t.Fatalf("受支持平台没有 payload | a supported platform has no payload")
		}
	}
	t.Skipf("当前平台没有嵌入 payload | no embedded payload on %s/%s", runtime.GOOS, runtime.GOARCH)
}

// TestMain keeps every extraction away from the developer's real cache. | TestMain 让所有解压远离开发机真实缓存。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "yd-payload-cache-*")
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	_ = os.Setenv(CacheEnv, dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// TestInstallAtUsesContentAddressedCache pins the cache path, the hard-link reuse and the corruption repair. |
// TestInstallAtUsesContentAddressedCache 固定缓存路径、硬链接复用与损坏修复。
func TestInstallAtUsesContentAddressedCache(t *testing.T) {
	data := gzipBytes(t, "cached-binary")
	key := Key(data)
	first := filepath.Join(t.TempDir(), "tool")
	if err := InstallAt(first, data, key); err != nil {
		t.Fatalf("InstallAt(first) = %v", err)
	}
	root, ok := CacheRoot()
	if !ok {
		t.Skip("no user cache directory | 没有用户缓存目录")
	}
	cached := filepath.Join(root, key, "tool")
	if _, err := os.Stat(cached); err != nil {
		t.Fatalf("缓存条目不存在: %v | cache entry missing: %v", err, err)
	}
	second := filepath.Join(t.TempDir(), "tool")
	if err := InstallAt(second, data, key); err != nil {
		t.Fatalf("InstallAt(second) = %v", err)
	}
	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatalf("Stat(first) = %v", err)
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		t.Fatalf("Stat(second) = %v", err)
	}
	if !os.SameFile(firstInfo, secondInfo) {
		t.Errorf("两次安装未复用同一缓存 inode | the two installs did not share the cached inode")
	}

	// A damaged cache entry must be re-extracted, never propagated. | 损坏的缓存必须重新解压，绝不能传播。
	if err := os.Truncate(cached, 1); err != nil {
		t.Fatalf("Truncate = %v", err)
	}
	third := filepath.Join(t.TempDir(), "tool")
	if err := InstallAt(third, data, key); err != nil {
		t.Fatalf("InstallAt(third) = %v", err)
	}
	if content, err := os.ReadFile(third); err != nil || string(content) != "cached-binary" {
		t.Fatalf("缓存修复后内容 = %q, %v | repaired content = %q, %v", content, err, content, err)
	}

	// A same-size corruption is caught by the mtime + hash check. | 同大小损坏由 mtime + 哈希校验捕获。
	content, err := os.ReadFile(cached)
	if err != nil {
		t.Fatalf("ReadFile(cached) = %v", err)
	}
	content[0] ^= 0xff
	if err := os.WriteFile(cached, content, 0o755); err != nil {
		t.Fatalf("WriteFile(cached) = %v", err)
	}
	fourth := filepath.Join(t.TempDir(), "tool")
	if err := InstallAt(fourth, data, key); err != nil {
		t.Fatalf("InstallAt(fourth) = %v", err)
	}
	if repaired, err := os.ReadFile(fourth); err != nil || string(repaired) != "cached-binary" {
		t.Fatalf("同大小损坏未修复：%q, %v | a same-size corruption was not repaired: %q, %v", repaired, err, repaired, err)
	}
}

// gzipBytes returns the gzip encoding of value. | gzipBytes 返回 value 的 gzip 编码。
func gzipBytes(t *testing.T, value string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write([]byte(value)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// TestInstallAtRepairsEmptyTarget pins that a zero-byte leftover never counts as an installed tool: an
// interrupted earlier write must be replaced by a valid payload. | TestInstallAtRepairsEmptyTarget 固定零字节
// 残留绝不算已安装：早先中断的写入必须被有效 payload 替换。
func TestInstallAtRepairsEmptyTarget(t *testing.T) {
	data := gzipBytes(t, "repaired-binary")
	target := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(target, nil, 0o755); err != nil {
		t.Fatalf("write empty target: %v", err)
	}
	if err := InstallAt(target, data, Key(data)); err != nil {
		t.Fatalf("InstallAt() = %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "repaired-binary" {
		t.Fatalf("empty target was not repaired: %q, %v | 零字节目标未被修复：%q, %v", content, err, content, err)
	}
}
