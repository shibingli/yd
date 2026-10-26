package versions

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// selfArchive builds a release-shaped tar.gz or zip containing one binary. | selfArchive 构建只含
// 一个二进制的 Release 形状 tar.gz 或 zip。
func selfArchive(t *testing.T, goos, asset string, body []byte) []byte {
	t.Helper()
	name := "yd"
	if goos == "windows" {
		name = "yd.exe"
	}
	var buf bytes.Buffer
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		w, err := zw.Create(asset[:len(asset)-len(".zip")] + "/" + name)
		if err != nil {
			t.Fatalf("创建 zip 条目失败: %v | creating zip entry: %v", err, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("写入 zip 条目失败: %v | writing zip entry: %v", err, err)
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("关闭 zip 失败: %v | closing zip: %v", err, err)
		}
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: asset[:len(asset)-len(".tar.gz")] + "/" + name,
		Mode: 0o755,
		Size: int64(len(body)),
	}); err != nil {
		t.Fatalf("写入 tar 头失败: %v | writing tar header: %v", err, err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("写入 tar 正文失败: %v | writing tar body: %v", err, err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("关闭 tar 失败: %v | closing tar: %v", err, err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("关闭 gzip 失败: %v | closing gzip: %v", err, err)
	}
	return buf.Bytes()
}

// selfReleaseServer serves the latest redirect and one release archive. | selfReleaseServer 提供最新
// 版本重定向与一个 Release 归档。
func selfReleaseServer(t *testing.T, tag, goos, goarch string, body []byte, corruptChecksum bool) (*httptest.Server, string) {
	t.Helper()
	asset := selfAssetName(tag, goos, goarch)
	sum := sha256.Sum256(body)
	checksum := hex.EncodeToString(sum[:])
	if corruptChecksum {
		checksum = strings.Repeat("0", sha256.Size*2)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/releases/latest":
			http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
		case r.URL.Path == "/releases/tag/"+tag:
			_, _ = w.Write([]byte("release"))
		case strings.HasSuffix(r.URL.Path, "/"+selfChecksumsName):
			fmt.Fprintf(w, "%s  %s\n", checksum, asset)
		case strings.HasSuffix(r.URL.Path, "/"+asset):
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, asset
}

// selfTestTarget creates an existing target binary file. | selfTestTarget 创建一个已存在的目标二进制文件。
func selfTestTarget(t *testing.T, goos string) string {
	t.Helper()
	dir := t.TempDir()
	name := "yd"
	if goos == "windows" {
		name = "yd.exe"
	}
	target := filepath.Join(dir, name)
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatalf("写入旧二进制失败: %v | writing old binary: %v", err, err)
	}
	return target
}

// TestUpdateSelfTarGzDownloadsVerifiesAndReplaces covers a normal Linux/macOS release update. |
// TestUpdateSelfTarGzDownloadsVerifiesAndReplaces 覆盖正常的 Linux/macOS Release 更新。
func TestUpdateSelfTarGzDownloadsVerifiesAndReplaces(t *testing.T) {
	const tag = "v9.9.9"
	target := selfTestTarget(t, "linux")
	body := []byte("new binary payload")
	archive := selfArchive(t, "linux", selfAssetName(tag, "linux", "amd64"), body)
	srv, _ := selfReleaseServer(t, tag, "linux", "amd64", archive, false)

	res, err := UpdateSelf(context.Background(), SelfUpdateOptions{
		HTTPClient:     srv.Client(),
		CurrentVersion: "v1.0.0",
		BinaryPath:     target,
		ReleaseBaseURL: srv.URL + "/releases",
		LatestURL:      srv.URL + "/releases/latest",
		GOOS:           "linux",
		GOARCH:         "amd64",
		SkipVerify:     true,
	})
	if err != nil {
		t.Fatalf("UpdateSelf 失败: %v | UpdateSelf: %v", err, err)
	}
	if !res.Updated || res.ToVersion != tag {
		t.Fatalf("结果 = %+v，期望更新到 %s | result = %+v, want %s", res, tag, res, tag)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("读取新二进制失败: %v | reading new binary: %v", err, err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("目标内容 = %q，期望新载荷 | target = %q, want new payload", got, got)
	}
}

// TestUpdateSelfZipReplacesWindowsBinary covers the Windows zip path. | TestUpdateSelfZipReplacesWindowsBinary
// 覆盖 Windows zip 路径。
func TestUpdateSelfZipReplacesWindowsBinary(t *testing.T) {
	const tag = "v9.9.9"
	target := selfTestTarget(t, "windows")
	body := []byte("windows binary payload")
	archive := selfArchive(t, "windows", selfAssetName(tag, "windows", "arm64"), body)
	srv, _ := selfReleaseServer(t, tag, "windows", "arm64", archive, false)

	res, err := UpdateSelf(context.Background(), SelfUpdateOptions{
		HTTPClient:     srv.Client(),
		CurrentVersion: "1.0.0",
		BinaryPath:     target,
		ReleaseBaseURL: srv.URL + "/releases",
		GOOS:           "windows",
		GOARCH:         "arm64",
		SkipVerify:     true,
	})
	if err != nil {
		t.Fatalf("UpdateSelf 失败: %v | UpdateSelf: %v", err, err)
	}
	if !res.Updated {
		t.Fatalf("结果未更新: %+v | result not updated: %+v", res, res)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("目标内容 = %q (err=%v)，期望新载荷 | target = %q (err=%v)", got, err, got, err)
	}
}

// TestUpdateSelfChecksumMismatchKeepsTarget proves a bad digest never replaces the binary. |
// TestUpdateSelfChecksumMismatchKeepsTarget 证明校验和不匹配时绝不替换二进制。
func TestUpdateSelfChecksumMismatchKeepsTarget(t *testing.T) {
	const tag = "v9.9.9"
	target := selfTestTarget(t, "linux")
	archive := selfArchive(t, "linux", selfAssetName(tag, "linux", "amd64"), []byte("payload"))
	srv, _ := selfReleaseServer(t, tag, "linux", "amd64", archive, true)

	_, err := UpdateSelf(context.Background(), SelfUpdateOptions{
		HTTPClient:     srv.Client(),
		CurrentVersion: "v1.0.0",
		TargetVersion:  tag,
		BinaryPath:     target,
		ReleaseBaseURL: srv.URL + "/releases",
		GOOS:           "linux",
		GOARCH:         "amd64",
		SkipVerify:     true,
	})
	if !errors.Is(err, ErrSelfChecksum) {
		t.Fatalf("err = %v，期望 ErrSelfChecksum | want ErrSelfChecksum", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "old binary" {
		t.Fatalf("校验失败后目标被改写: %q | target changed after checksum failure: %q", got, got)
	}
}

// TestUpdateSelfSkipsCurrentVersion proves the network is not touched when already current. |
// TestUpdateSelfSkipsCurrentVersion 证明已是最新时不会触网。
func TestUpdateSelfSkipsCurrentVersion(t *testing.T) {
	target := selfTestTarget(t, "linux")
	res, err := UpdateSelf(context.Background(), SelfUpdateOptions{
		HTTPClient:     &http.Client{Transport: selfFailTransport{t: t}},
		CurrentVersion: "v1.2.3",
		TargetVersion:  "v1.2.3",
		BinaryPath:     target,
		GOOS:           "linux",
		GOARCH:         "amd64",
		SkipVerify:     true,
	})
	if err != nil {
		t.Fatalf("UpdateSelf 失败: %v | UpdateSelf: %v", err, err)
	}
	if res.Updated {
		t.Fatalf("结果不应更新: %+v | result must not update: %+v", res, res)
	}
	if !strings.Contains(res.Reason, "already") {
		t.Errorf("Reason = %q，期望说明已是最新 | want an already-current reason", res.Reason)
	}
}

// TestUpdateSelfRejectsDowngrade proves an older tag requires Force. | TestUpdateSelfRejectsDowngrade
// 证明旧标签需要 Force。
func TestUpdateSelfRejectsDowngrade(t *testing.T) {
	target := selfTestTarget(t, "linux")
	res, err := UpdateSelf(context.Background(), SelfUpdateOptions{
		HTTPClient:     &http.Client{Transport: selfFailTransport{t: t}},
		CurrentVersion: "v2.0.0",
		TargetVersion:  "v1.0.0",
		BinaryPath:     target,
		GOOS:           "linux",
		GOARCH:         "amd64",
		SkipVerify:     true,
	})
	if err != nil {
		t.Fatalf("UpdateSelf 失败: %v | UpdateSelf: %v", err, err)
	}
	if res.Updated {
		t.Fatalf("结果不应降级: %+v | result must not downgrade: %+v", res, res)
	}
	if got, _ := os.ReadFile(target); string(got) != "old binary" {
		t.Fatalf("目标被降级改写: %q | target was replaced: %q", got, got)
	}
}

// TestUpdateSelfDirectoryTarget resolves a directory to yd[.exe]. | TestUpdateSelfDirectoryTarget
// 把目录解析为 yd[.exe]。
func TestUpdateSelfDirectoryTarget(t *testing.T) {
	const tag = "v9.9.9"
	dir := t.TempDir()
	name := "yd"
	target := filepath.Join(dir, name)
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("写入旧二进制失败: %v | writing old binary: %v", err, err)
	}
	body := []byte("directory payload")
	archive := selfArchive(t, "linux", selfAssetName(tag, "linux", "amd64"), body)
	srv, _ := selfReleaseServer(t, tag, "linux", "amd64", archive, false)

	res, err := UpdateSelf(context.Background(), SelfUpdateOptions{
		HTTPClient:     srv.Client(),
		CurrentVersion: "v1.0.0",
		TargetVersion:  tag,
		BinaryPath:     dir,
		ReleaseBaseURL: srv.URL + "/releases",
		GOOS:           "linux",
		GOARCH:         "amd64",
		SkipVerify:     true,
	})
	if err != nil {
		t.Fatalf("UpdateSelf 失败: %v | UpdateSelf: %v", err, err)
	}
	if !res.Updated || !strings.EqualFold(filepath.Clean(res.Binary), filepath.Clean(target)) {
		t.Fatalf("结果 = %+v，期望替换 %s | result = %+v, want %s", res, target, res, target)
	}
	got, _ := os.ReadFile(target)
	if !bytes.Equal(got, body) {
		t.Fatalf("目录目标内容 = %q，期望新载荷 | directory target = %q", got, got)
	}
}

// TestUpdateSelfRejectsArchiveWithoutBinary keeps a malformed archive from replacing the target. |
// TestUpdateSelfRejectsArchiveWithoutBinary 阻止畸形归档替换目标。
func TestUpdateSelfRejectsArchiveWithoutBinary(t *testing.T) {
	const tag = "v9.9.9"
	target := selfTestTarget(t, "linux")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("not the binary")
	if err := tw.WriteHeader(&tar.Header{Name: "../yd", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatalf("写入 tar 头失败: %v | writing tar header: %v", err, err)
	}
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gz.Close()
	archive := buf.Bytes()
	srv, _ := selfReleaseServer(t, tag, "linux", "amd64", archive, false)

	_, err := UpdateSelf(context.Background(), SelfUpdateOptions{
		HTTPClient:     srv.Client(),
		CurrentVersion: "v1.0.0",
		TargetVersion:  tag,
		BinaryPath:     target,
		ReleaseBaseURL: srv.URL + "/releases",
		GOOS:           "linux",
		GOARCH:         "amd64",
		SkipVerify:     true,
	})
	if !errors.Is(err, ErrSelfArchive) {
		t.Fatalf("err = %v，期望 ErrSelfArchive | want ErrSelfArchive", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "old binary" {
		t.Fatalf("畸形归档改写了目标: %q | malformed archive changed target: %q", got, got)
	}
}

// selfFailTransport fails the test if any HTTP request is made. | selfFailTransport 在发生任何 HTTP
// 请求时让测试失败。
type selfFailTransport struct{ t *testing.T }

func (s selfFailTransport) RoundTrip(*http.Request) (*http.Response, error) {
	s.t.Errorf("不应发起网络请求 | unexpected network request")
	return nil, errors.New("unexpected request")
}

// TestSelfExtractWindowsStagedNameEndsInExe locks the extension Windows needs before it will run
// the staged --version probe. | TestSelfExtractWindowsStagedNameEndsInExe 固定 Windows 在运行暂存
// --version 校验前所要求的扩展名。
func TestSelfExtractWindowsStagedNameEndsInExe(t *testing.T) {
	const tag = "v9.9.9"
	target := selfTestTarget(t, "windows")
	body := []byte("staged windows binary")
	archive := selfArchive(t, "windows", selfAssetName(tag, "windows", "amd64"), body)
	path := filepath.Join(t.TempDir(), "release.zip")
	if err := os.WriteFile(path, archive, 0o644); err != nil {
		t.Fatalf("写入归档失败: %v | writing archive: %v", err, err)
	}
	staged, err := selfExtract(path, "windows", target)
	if err != nil {
		t.Fatalf("解包失败: %v | extracting: %v", err, err)
	}
	defer func() { _ = os.Remove(staged) }()
	if !strings.HasSuffix(strings.ToLower(staged), ".exe") {
		t.Fatalf("暂存路径 = %q，期望 .exe 结尾 | staged path = %q, want .exe", staged, staged)
	}
	got, err := os.ReadFile(staged)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("暂存内容 = %q (err=%v) | staged content = %q (err=%v)", got, err, got, err)
	}
}
