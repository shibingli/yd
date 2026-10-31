package versions

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/shibingli/ydx/errs"
)

const (
	// DefaultSelfReleaseBaseURL is the public release endpoint used by the self updater. | DefaultSelfReleaseBaseURL
	// 是自更新器使用的公开 Release 端点。
	DefaultSelfReleaseBaseURL = "https://github.com/shibingli/yd/releases"
	// selfChecksumsName is the checksum manifest attached to every release. | selfChecksumsName 是每个
	// Release 附带的校验和清单名。
	selfChecksumsName = "checksums.txt"
	// selfArchiveLimit caps the downloaded archive before extraction. | selfArchiveLimit 限制解包前
	// 下载的归档体积。
	selfArchiveLimit = 128 << 20
	// selfChecksumLimit caps the checksum manifest. | selfChecksumLimit 限制校验和清单体积。
	selfChecksumLimit = 1 << 20
	// selfVerifyTimeout bounds the staged binary's --version probe. | selfVerifyTimeout 限制对暂存
	// 二进制执行 --version 的时间。
	selfVerifyTimeout = 30 * time.Second
)

var (
	// ErrSelfRelease reports a failed release lookup. | ErrSelfRelease 表示查询 Release 失败。
	ErrSelfRelease = errs.New("query yd release failed | 查询 yd Release 失败")
	// ErrSelfDownload reports a failed archive or checksum download. | ErrSelfDownload 表示下载归档或
	// 校验和失败。
	ErrSelfDownload = errs.New("download yd release failed | 下载 yd Release 失败")
	// ErrSelfChecksum reports a checksum mismatch. | ErrSelfChecksum 表示校验和不匹配。
	ErrSelfChecksum = errs.New("verify yd release checksum failed | 校验 yd Release 校验和失败")
	// ErrSelfArchive reports an invalid or truncated release archive. | ErrSelfArchive 表示 Release
	// 归档非法或已截断。
	ErrSelfArchive = errs.New("extract yd release binary failed | 解包 yd Release 二进制失败")
	// ErrSelfBinary reports an unusable target binary path. | ErrSelfBinary 表示目标二进制路径不可用。
	ErrSelfBinary = errs.New("yd binary is not replaceable | yd 二进制不可替换")
	// ErrSelfReplace reports a failed atomic replacement. | ErrSelfReplace 表示原子替换失败。
	ErrSelfReplace = errs.New("replace yd binary failed | 替换 yd 二进制失败")
	// ErrSelfVerify reports a staged binary that cannot run or reports the wrong version. | ErrSelfVerify
	// 表示暂存二进制无法运行或版本不符。
	ErrSelfVerify = errs.New("verify staged yd binary failed | 校验暂存 yd 二进制失败")
)

// SelfUpdateOptions configures an in-place CLI update. | SelfUpdateOptions 配置一次原地 CLI 更新。
type SelfUpdateOptions struct {
	// HTTPClient performs release metadata and asset requests; nil is an error. | HTTPClient 执行
	// Release 元数据与资产请求；nil 视为错误。
	HTTPClient *http.Client
	// CurrentVersion is the running binary's tag; empty or a non-version value means unknown. |
	// CurrentVersion 是运行中二进制的标签；为空或非版本值表示未知。
	CurrentVersion string
	// TargetVersion pins the tag to install; empty or "latest" resolves the latest release. |
	// TargetVersion 指定要安装的标签；为空或 "latest" 时解析最新 Release。
	TargetVersion string
	// BinaryPath is the file or directory to replace; empty means the running executable. | BinaryPath
	// 是要替换的文件或目录；为空表示运行中的可执行文件。
	BinaryPath string
	// ReleaseBaseURL overrides the public release endpoint, primarily for tests. | ReleaseBaseURL
	// 覆盖公开 Release 端点，主要供测试使用。
	ReleaseBaseURL string
	// LatestURL overrides the latest-release redirect endpoint, primarily for tests. | LatestURL
	// 覆盖最新 Release 重定向端点，主要供测试使用。
	LatestURL string
	// GOOS and GOARCH select the release asset; empty values use the running platform. | GOOS 与
	// GOARCH 选择 Release 资产；为空时使用运行平台。
	GOOS   string
	GOARCH string
	// Force permits re-installing the current version or downgrading. | Force 允许重装当前版本或降级。
	Force bool
	// SkipVerify disables running the staged binary; tests use it with synthetic payloads. | SkipVerify
	// 关闭对暂存二进制的执行校验；测试用它配合合成载荷。
	SkipVerify bool
}

// SelfUpdateResult describes one self-update attempt. | SelfUpdateResult 描述一次自更新尝试。
type SelfUpdateResult struct {
	// FromVersion is the running tag, or "dev" when unknown. | FromVersion 是运行中的标签；未知时为 "dev"。
	FromVersion string
	// ToVersion is the resolved release tag. | ToVersion 是解析出的 Release 标签。
	ToVersion string
	// Binary is the replaced absolute path. | Binary 是被替换的绝对路径。
	Binary string
	// Backup is the previous file kept for rollback, empty when it was cleaned. | Backup 是为回滚
	// 保留的旧文件；已清理时为空。
	Backup string
	// Reason explains a skipped update. | Reason 说明跳过更新的原因。
	Reason string
	// Updated reports whether the binary was replaced. | Updated 表示二进制是否已被替换。
	Updated bool
}

// UpdateSelf downloads a released yd binary, verifies its checksum and replaces the current or
// specified installation in place. It supports release archives, go-installed binaries and custom
// paths because all three are ordinary files; a directory argument resolves to yd[.exe] inside it.
// The running process keeps the old image and the new binary takes effect on the next invocation. |
// UpdateSelf 下载已发布的 yd 二进制、校验校验和并原地替换当前或指定安装。Release 归档、go install
// 产物与自定义路径都是普通文件，因此同一路径适用；传入目录时解析为其中的 yd[.exe]。运行中的进程
// 继续使用旧映像，新二进制在下次启动时生效。
func UpdateSelf(ctx context.Context, o SelfUpdateOptions) (*SelfUpdateResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if o.HTTPClient == nil {
		return nil, errs.Detail(ErrSelfDownload, "nil HTTP client | HTTP 客户端为 nil")
	}
	goos, goarch := o.GOOS, o.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	baseURL := strings.TrimRight(strings.TrimSpace(o.ReleaseBaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultSelfReleaseBaseURL
	}
	latestURL := strings.TrimSpace(o.LatestURL)
	if latestURL == "" {
		latestURL = baseURL + "/latest"
	}
	target, err := resolveSelfTarget(o.BinaryPath, goos)
	if err != nil {
		return nil, err
	}
	from := strings.TrimSpace(o.CurrentVersion)
	if from == "" {
		from = "dev"
	}

	tag := strings.TrimSpace(o.TargetVersion)
	if tag == "" || strings.EqualFold(tag, "latest") {
		tag, err = latestSelfTag(ctx, o.HTTPClient, latestURL)
		if err != nil {
			return nil, err
		}
	}
	tag, err = normalizeSelfTag(tag)
	if err != nil {
		return nil, err
	}
	res := &SelfUpdateResult{FromVersion: from, ToVersion: tag, Binary: target}
	cleanupSelfBackups(filepath.Dir(target), filepath.Base(target))
	if !o.Force && !selfIsNewer(from, tag) {
		res.Reason = fmt.Sprintf("yd %s is already at %s | yd %s 已处于 %s", from, tag, from, tag)
		return res, nil
	}

	asset := selfAssetName(tag, goos, goarch)
	want, err := selfChecksum(ctx, o.HTTPClient, baseURL, tag, asset)
	if err != nil {
		return nil, err
	}
	archive, err := selfDownload(ctx, o.HTTPClient, baseURL, tag, asset, want, target)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(archive) }()

	staged, err := selfExtract(archive, goos, target)
	if err != nil {
		return nil, err
	}
	keepStaged := false
	defer func() {
		if !keepStaged {
			_ = os.Remove(staged)
		}
	}()

	if !o.SkipVerify {
		if err := selfVerify(ctx, staged, tag); err != nil {
			return nil, err
		}
	}
	backup, err := selfReplace(target, staged)
	if err != nil {
		return nil, err
	}
	keepStaged = true
	res.Updated = true
	res.Backup = backup
	return res, nil
}

// latestSelfTag follows GitHub's /releases/latest redirect and returns the tag in the final URL. |
// latestSelfTag 跟随 GitHub 的 /releases/latest 重定向，返回最终 URL 中的标签。
func latestSelfTag(ctx context.Context, client *http.Client, latestURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return "", errs.WrapDetail(err, ErrSelfRelease, "build request | 构造请求")
	}
	req.Header.Set("User-Agent", "yd-self-update")
	resp, err := client.Do(req)
	if err != nil {
		return "", errs.WrapDetail(err, ErrSelfRelease, "request %[1]s | 请求 %[1]s", latestURL)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", errs.Detail(ErrSelfRelease, "latest release: HTTP %[1]d | 最新 Release：HTTP 状态 %[1]d", resp.StatusCode)
	}
	final := ""
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.Path
	}
	marker := "/tag/"
	index := strings.LastIndex(final, marker)
	if index < 0 {
		return "", errs.Detail(ErrSelfRelease, "latest release URL has no tag: %[1]q | 最新 Release 地址没有标签：%[1]q", final)
	}
	tag := strings.Trim(strings.TrimSpace(final[index+len(marker):]), "/")
	if tag == "" {
		return "", errs.Detail(ErrSelfRelease, "empty latest release tag | 最新 Release 标签为空")
	}
	return tag, nil
}

// normalizeSelfTag validates a release tag and ensures its leading v. | normalizeSelfTag 校验 Release
// 标签并补上开头的 v。
func normalizeSelfTag(tag string) (string, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", errs.Detail(ErrSelfRelease, "empty release tag | Release 标签为空")
	}
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	for _, r := range tag {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			continue
		}
		return "", errs.Detail(ErrSelfRelease, "invalid release tag %[1]q | 非法的 Release 标签 %[1]q", tag)
	}
	return tag, nil
}

// selfIsNewer reports whether target is newer than current. Unknown versions update; forcing is
// handled by the caller. | selfIsNewer 报告 target 是否比 current 更新。未知版本按需要更新处理；
// 强制更新由调用方处理。
func selfIsNewer(current, target string) bool {
	cur, okCur := parseSemver(current)
	next, okNext := parseSemver(target)
	if !okCur || !okNext {
		return true
	}
	return compareSemver(next, cur) > 0
}

// selfAssetName returns the release asset for one platform. | selfAssetName 返回单个平台的 Release 资产名。
func selfAssetName(tag, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("yd_%s_%s_%s%s", tag, goos, goarch, ext)
}

// selfChecksum fetches and parses checksums.txt for one asset. | selfChecksum 获取并解析某个资产的
// checksums.txt。
func selfChecksum(ctx context.Context, client *http.Client, baseURL, tag, asset string) ([]byte, error) {
	url := fmt.Sprintf("%s/download/%s/%s", baseURL, tag, selfChecksumsName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errs.WrapDetail(err, ErrSelfDownload, "build checksum request | 构造校验和请求")
	}
	req.Header.Set("User-Agent", "yd-self-update")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errs.WrapDetail(err, ErrSelfDownload, "checksum %[1]s | 校验和 %[1]s", url)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, errs.Detail(ErrSelfDownload, "checksum %[1]s: HTTP %[2]d | 校验和 %[1]s：HTTP 状态 %[2]d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, selfChecksumLimit+1))
	if err != nil {
		return nil, errs.WrapDetail(err, ErrSelfDownload, "read checksum %[1]s | 读取校验和 %[1]s", url)
	}
	if len(data) > selfChecksumLimit {
		return nil, errs.Detail(ErrSelfDownload, "checksum manifest too large | 校验和清单过大")
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != asset {
			continue
		}
		sum, err := hex.DecodeString(fields[0])
		if err != nil || len(sum) != sha256.Size {
			return nil, errs.Detail(ErrSelfChecksum, "asset %[1]q has an invalid checksum | 资产 %[1]q 的校验和非法", asset)
		}
		return sum, nil
	}
	return nil, errs.Detail(ErrSelfChecksum, "asset %[1]q is missing from checksums.txt | 资产 %[1]q 不在 checksums.txt 中", asset)
}

// selfDownload downloads and verifies one archive into the target directory, so replacement stays
// on the same volume. | selfDownload 把归档下载并校验到目标目录，确保替换发生在同一卷上。
func selfDownload(ctx context.Context, client *http.Client, baseURL, tag, asset string, want []byte, target string) (string, error) {
	url := fmt.Sprintf("%s/download/%s/%s", baseURL, tag, asset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", errs.WrapDetail(err, ErrSelfDownload, "build asset request | 构造资产请求")
	}
	req.Header.Set("User-Agent", "yd-self-update")
	resp, err := client.Do(req)
	if err != nil {
		return "", errs.WrapDetail(err, ErrSelfDownload, "asset %[1]s | 资产 %[1]s", url)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", errs.Detail(ErrSelfDownload, "asset %[1]s: HTTP %[2]d | 资产 %[1]s：HTTP 状态 %[2]d", url, resp.StatusCode)
	}
	dir := filepath.Dir(target)
	suffix := ".tar.gz"
	if strings.HasSuffix(asset, ".zip") {
		suffix = ".zip"
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(target)+".download-*"+suffix)
	if err != nil {
		return "", errs.WrapDetail(err, ErrSelfDownload, "create temp file in %[1]s | 在 %[1]s 创建临时文件", dir)
	}
	name := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, selfArchiveLimit+1))
	if err != nil {
		_ = f.Close()
		return "", errs.WrapDetail(err, ErrSelfDownload, "write archive | 写入归档")
	}
	if n > selfArchiveLimit {
		_ = f.Close()
		return "", errs.Detail(ErrSelfDownload, "archive exceeds %[1]d bytes | 归档超过 %[1]d 字节", selfArchiveLimit)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return "", errs.WrapDetail(err, ErrSelfDownload, "sync archive | 同步归档")
	}
	if err := f.Close(); err != nil {
		return "", errs.WrapDetail(err, ErrSelfDownload, "close archive | 关闭归档")
	}
	if !bytes.Equal(hash.Sum(nil), want) {
		return "", errs.Detail(ErrSelfChecksum, "asset %[1]q checksum mismatch | 资产 %[1]q 校验和不匹配", asset)
	}
	ok = true
	return name, nil
}

// selfExtract unpacks the expected binary beside the target and returns the staged path. | selfExtract
// 在目标旁边解包预期二进制并返回暂存路径。
func selfExtract(archive, goos, target string) (string, error) {
	expected := "yd"
	if goos == "windows" {
		expected = "yd.exe"
	}
	dir := filepath.Dir(target)
	suffix := ""
	if goos == "windows" {
		// Windows decides executable format partly by extension; keep the staged name ending in
		// .exe so the --version probe can run it before replacement. | Windows 会依据扩展名判断
		// 可执行格式；让暂存名以 .exe 结尾，替换前的 --version 校验才能启动它。
		suffix = ".exe"
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(target)+".new-*"+suffix)
	if err != nil {
		return "", errs.WrapDetail(err, ErrSelfArchive, "create staged file | 创建暂存文件")
	}
	staged := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(staged)
		}
	}()
	if err := extractSelfBinary(archive, expected, f); err != nil {
		return "", err
	}
	if err := f.Chmod(0o755); err != nil && runtime.GOOS != "windows" {
		return "", errs.WrapDetail(err, ErrSelfArchive, "chmod staged file | 设置暂存文件权限")
	}
	if err := f.Sync(); err != nil {
		return "", errs.WrapDetail(err, ErrSelfArchive, "sync staged file | 同步暂存文件")
	}
	if err := f.Close(); err != nil {
		return "", errs.WrapDetail(err, ErrSelfArchive, "close staged file | 关闭暂存文件")
	}
	ok = true
	return staged, nil
}

// extractSelfBinary copies the expected regular file out of a tar.gz or zip archive. | extractSelfBinary
// 从 tar.gz 或 zip 归档中复制预期普通文件。
func extractSelfBinary(archive, expected string, dst io.Writer) error {
	lower := strings.ToLower(archive)
	if strings.HasSuffix(lower, ".zip") {
		return extractSelfZip(archive, expected, dst)
	}
	return extractSelfTarGz(archive, expected, dst)
}

// extractSelfTarGz extracts expected from a gzip-compressed tar. | extractSelfTarGz 从 gzip tar 中解包 expected。
func extractSelfTarGz(archive, expected string, dst io.Writer) error {
	f, err := os.Open(archive)
	if err != nil {
		return errs.WrapDetail(err, ErrSelfArchive, "open archive | 打开归档")
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return errs.WrapDetail(err, ErrSelfArchive, "read gzip | 读取 gzip")
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errs.WrapDetail(err, ErrSelfArchive, "read tar | 读取 tar")
		}
		if hdr.Typeflag != tar.TypeReg || !safeSelfEntry(hdr.Name) || path.Base(hdr.Name) != expected {
			continue
		}
		if hdr.Size < 0 || hdr.Size > selfArchiveLimit {
			return errs.Detail(ErrSelfArchive, "binary entry has invalid size %[1]d | 二进制条目大小非法 %[1]d", hdr.Size)
		}
		if _, err := io.CopyN(dst, tr, hdr.Size); err != nil {
			return errs.WrapDetail(err, ErrSelfArchive, "extract binary | 解包二进制")
		}
		return nil
	}
	return errs.Detail(ErrSelfArchive, "archive has no %[1]s | 归档中没有 %[1]s", expected)
}

// safeSelfEntry reports whether an archive entry stays inside the extraction root. | safeSelfEntry
// 报告归档条目是否仍位于解包根目录之内。
func safeSelfEntry(name string) bool {
	name = strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "./")
	clean := path.Clean(name)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, "../") && !path.IsAbs(clean)
}

// extractSelfZip extracts expected from a zip archive. | extractSelfZip 从 zip 归档中解包 expected。
func extractSelfZip(archive, expected string, dst io.Writer) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return errs.WrapDetail(err, ErrSelfArchive, "open zip | 打开 zip")
	}
	defer func() { _ = zr.Close() }()
	for _, entry := range zr.File {
		if !entry.Mode().IsRegular() || !safeSelfEntry(entry.Name) || path.Base(entry.Name) != expected {
			continue
		}
		if entry.UncompressedSize64 > selfArchiveLimit {
			return errs.Detail(ErrSelfArchive, "binary entry is too large | 二进制条目过大")
		}
		rc, err := entry.Open()
		if err != nil {
			return errs.WrapDetail(err, ErrSelfArchive, "open zip entry | 打开 zip 条目")
		}
		n, err := io.Copy(dst, io.LimitReader(rc, selfArchiveLimit))
		if err != nil {
			_ = rc.Close()
			return errs.WrapDetail(err, ErrSelfArchive, "extract binary | 解包二进制")
		}
		if uint64(n) != entry.UncompressedSize64 {
			_ = rc.Close()
			return errs.Detail(ErrSelfArchive, "binary entry is truncated | 二进制条目已截断")
		}
		if err := rc.Close(); err != nil {
			return errs.WrapDetail(err, ErrSelfArchive, "close zip entry | 关闭 zip 条目")
		}
		return nil
	}
	return errs.Detail(ErrSelfArchive, "archive has no %[1]s | 归档中没有 %[1]s", expected)
}

// selfVerify runs the staged binary and checks its reported version. | selfVerify 运行暂存二进制并
// 检查它报告的版本。
func selfVerify(ctx context.Context, staged, tag string) error {
	verifyCtx, cancel := context.WithTimeout(ctx, selfVerifyTimeout)
	defer cancel()
	out, err := exec.CommandContext(verifyCtx, staged, "--version").CombinedOutput()
	if err != nil {
		return errs.WrapDetail(err, ErrSelfVerify, "run %[1]s --version | 运行 %[1]s --version", staged)
	}
	if !strings.Contains(string(out), tag) {
		return errs.Detail(ErrSelfVerify, "staged binary reports %[1]q, want %[2]q | 暂存二进制报告 %[1]q，期望 %[2]q", strings.TrimSpace(string(out)), tag)
	}
	return nil
}

// resolveSelfTarget resolves a file or directory target to an absolute real file. | resolveSelfTarget
// 把文件或目录目标解析为绝对真实路径。
func resolveSelfTarget(binaryPath, goos string) (string, error) {
	p := strings.TrimSpace(binaryPath)
	if p == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", errs.WrapDetail(err, ErrSelfBinary, "resolve running executable | 解析运行中的可执行文件")
		}
		p = exe
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if info, err := os.Stat(p); err == nil && info.IsDir() {
		p = filepath.Join(p, "yd"+selfExeSuffix(goos))
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", errs.WrapDetail(err, ErrSelfBinary, "stat %[1]s | 检查 %[1]s", p)
	}
	if !info.Mode().IsRegular() {
		return "", errs.Detail(ErrSelfBinary, "%[1]s is not a regular file | %[1]s 不是普通文件", p)
	}
	return p, nil
}

// selfExeSuffix returns the executable suffix for a target platform. | selfExeSuffix 返回目标平台的
// 可执行文件后缀。
func selfExeSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// selfReplace renames the current file aside and moves the staged binary into place, rolling back
// if the second rename fails. | selfReplace 把当前文件移到一旁，再把暂存二进制放到目标位置；第二次
// 重命名失败时回滚。
func selfReplace(target, staged string) (string, error) {
	backup := fmt.Sprintf("%s.old-%d", target, os.Getpid())
	for i := 1; ; i++ {
		if _, err := os.Lstat(backup); os.IsNotExist(err) {
			break
		}
		backup = fmt.Sprintf("%s.old-%d-%d", target, os.Getpid(), i)
	}
	if err := os.Rename(target, backup); err != nil {
		return "", errs.WrapDetail(err, ErrSelfReplace, "move %[1]s aside | 移开 %[1]s", target)
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.Rename(backup, target)
		return "", errs.WrapDetail(err, ErrSelfReplace, "install staged binary at %[1]s | 安装暂存二进制到 %[1]s", target)
	}
	if err := os.Chmod(target, 0o755); err != nil && runtime.GOOS != "windows" {
		return "", errs.WrapDetail(err, ErrSelfReplace, "chmod %[1]s | 设置 %[1]s 权限", target)
	}
	// The old image cannot be deleted while this process runs on Windows. Keep the backup when
	// removal fails; cleanupSelfBackups removes it on a later run after the age threshold. |
	// Windows 上当前进程仍在运行时无法删除旧映像。删除失败就保留备份，由后续运行的
	// cleanupSelfBackups 在超过时限后清理。
	if err := os.Remove(backup); err == nil {
		backup = ""
	}
	return backup, nil
}

// cleanupSelfBackups removes stale backups left by earlier updates. | cleanupSelfBackups 清理更早
// 更新留下的过期备份。
func cleanupSelfBackups(dir, base string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := base + ".old-"
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}
