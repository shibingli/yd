// This file installs the Tailwind CSS standalone binary, the one optional tool that is neither a
// Go module nor a package manager artifact. | 本文件安装 Tailwind CSS standalone 二进制——唯一既不是
// Go 模块、也不是包管理器产物的可选工具。

package tools

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
)

const (
	// tailwindReleaseBase is the stable GitHub redirect for the newest release. Using
	// releases/latest/download avoids the API (rate limits) and keeps no version to drift. |
	// tailwindReleaseBase 是指向最新发布版的稳定 GitHub 重定向。使用 releases/latest/download 可避开
	// API（限流），也无需维护一个会漂移的版本号。
	tailwindReleaseBase = "https://github.com/tailwindlabs/tailwindcss/releases/latest/download/"
	// tailwindDownloadLimit caps one download so a hostile or broken mirror cannot fill the disk. |
	// tailwindDownloadLimit 限制单次下载，避免恶意或损坏的镜像写满磁盘。
	tailwindDownloadLimit = 256 << 20
	// tailwindDownloadTimeout bounds one download when the caller's context has no deadline. |
	// tailwindDownloadTimeout 在调用方上下文没有期限时为单次下载设限。
	tailwindDownloadTimeout = 10 * time.Minute
)

// tailwindAssetName maps a GOOS/GOARCH pair to the release asset of the standalone compiler. |
// tailwindAssetName 把 GOOS/GOARCH 映射到 standalone 编译器的发布资产名。
//
// The musl build is selected on Alpine because the glibc binary does not run there; every other
// Linux distribution uses the glibc asset. | Alpine 上选择 musl 构建，因为 glibc 二进制在那里无法运行；
// 其余 Linux 发行版使用 glibc 资产。
func tailwindAssetName(goos, goarch string) (string, error) {
	base := ""
	switch goos + "/" + goarch {
	case "linux/amd64":
		base = "tailwindcss-linux-x64"
	case "linux/arm64":
		base = "tailwindcss-linux-arm64"
	case "darwin/amd64":
		base = "tailwindcss-macos-x64"
	case "darwin/arm64":
		base = "tailwindcss-macos-arm64"
	case "windows/amd64":
		return "tailwindcss-windows-x64.exe", nil
	case "windows/arm64":
		return "tailwindcss-windows-arm64.exe", nil
	default:
		return "", errs.Detail(ErrNotInstallable,
			"tailwindcss has no standalone build for %[1]s/%[2]s | tailwindcss 没有 %[1]s/%[2]s 的 standalone 构建",
			goos, goarch)
	}
	if goos == "linux" {
		if _, err := os.Stat("/etc/alpine-release"); err == nil {
			base += "-musl"
		}
	}
	return base, nil
}

// tailwindClient builds the HTTP client used for the download. | tailwindClient 构建下载使用的 HTTP 客户端。
//
// An explicit proxy wins; otherwise the standard transport honours HTTP_PROXY, HTTPS_PROXY and
// NO_PROXY, which is what CI and corporate networks already configure. The proxy URL is never
// logged: it may carry credentials. | 显式代理优先；否则标准传输层会遵循 HTTP_PROXY、HTTPS_PROXY 与
// NO_PROXY，这正是 CI 与企业网络已经配置的变量。代理 URL 绝不写入日志：它可能带凭据。
func tailwindClient(proxy string) (*http.Client, error) {
	timeout := tailwindDownloadTimeout
	if proxy == "" {
		return &http.Client{Timeout: timeout}, nil
	}
	u, err := url.Parse(strings.TrimSpace(proxy))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errs.Detail(ErrInstallFailed, "invalid proxy URL | 非法的代理 URL")
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errs.Detail(ErrInstallFailed, "default HTTP transport is not *http.Transport | 默认 HTTP 传输层不是 *http.Transport")
	}
	transport := base.Clone()
	transport.Proxy = http.ProxyURL(u)
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

// DownloadTailwind downloads the standalone compiler into <dir>/.yd/tools. | DownloadTailwind 把
// standalone 编译器下载到 <dir>/.yd/tools。
//
// The body is written to a temporary file in the destination directory, given the executable bit and
// renamed into place, so an interrupted download never leaves a half-written binary that later looks
// runnable. URL overrides the release asset (a mirror can be configured where GitHub is blocked);
// Proxy overrides the standard proxy environment. | 响应体先写入目标目录的临时文件，赋予执行位后再改名
// 就位，因此中断的下载不会留下看似可运行的半截二进制。URL 可覆盖发布资产（GitHub 被墙时可配置镜像）；
// Proxy 可覆盖标准代理环境变量。
func DownloadTailwind(ctx context.Context, dir string, o Options) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	target := filepath.Join(toolDir(dir), filepath.FromSlash(LocalToolDir), "tailwindcss"+tailwindExeSuffix())
	assetURL := strings.TrimSpace(o.TailwindURL)
	if assetURL == "" {
		asset, err := tailwindAssetName(runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return "", err
		}
		assetURL = tailwindReleaseBase + asset
	}
	client, err := tailwindClient(o.Proxy)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return "", errs.WrapDetail(err, ErrInstallFailed, "request %s | 请求 %s", assetURL, assetURL)
	}
	req.Header.Set("User-Agent", "yd")
	resp, err := client.Do(req)
	if err != nil {
		return "", errs.WrapDetail(err, ErrInstallFailed, "download %s failed | 下载 %s 失败", assetURL, assetURL)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", errs.Detail(ErrInstallFailed, "download %[1]s: HTTP %[2]d | 下载 %[1]s：HTTP %[2]d",
			assetURL, resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", errs.WrapDetail(err, ErrInstallFailed, "create %s | 创建 %s", filepath.Dir(target), filepath.Dir(target))
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".tailwindcss-*")
	if err != nil {
		return "", errs.WrapDetail(err, ErrInstallFailed, "create temporary file | 创建临时文件")
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	written, err := io.Copy(tmp, io.LimitReader(resp.Body, tailwindDownloadLimit+1))
	if err != nil {
		_ = tmp.Close()
		return "", errs.WrapDetail(err, ErrInstallFailed, "write %s | 写入 %s", tmpName, tmpName)
	}
	if written == 0 {
		_ = tmp.Close()
		return "", errs.Detail(ErrInstallFailed, "tailwindcss download is empty | tailwindcss 下载内容为空")
	}
	if written > tailwindDownloadLimit {
		_ = tmp.Close()
		return "", errs.Detail(ErrInstallFailed, "tailwindcss download exceeds %d bytes | tailwindcss 下载超过 %d 字节",
			tailwindDownloadLimit, tailwindDownloadLimit)
	}
	if err := tmp.Close(); err != nil {
		return "", errs.WrapDetail(err, ErrInstallFailed, "close %s | 关闭 %s", tmpName, tmpName)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return "", errs.WrapDetail(err, ErrInstallFailed, "chmod %s | 修改 %s 权限", tmpName, tmpName)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return "", errs.WrapDetail(err, ErrInstallFailed, "install %s | 安装 %s", target, target)
	}
	cleanup = false
	return target, nil
}

// tailwindExeSuffix returns the platform executable suffix. | tailwindExeSuffix 返回平台可执行文件后缀。
func tailwindExeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// installTailwind downloads the compiler and verifies that the resolver now finds it. | installTailwind
// 下载编译器并验证解析器现在能找到它。
func installTailwind(ctx context.Context, o Options, t Tool, log *ydlog.Logger) (Status, error) {
	path, err := DownloadTailwind(ctx, o.Dir, o)
	if err != nil {
		return Status{}, err
	}
	log.Info("tailwindcss downloaded | tailwindcss 已下载", ydlog.Str("path", path))
	st := probe(t, o)
	if !st.Found {
		return st, errs.Detail(ErrInstallFailed,
			"downloaded %[1]s but it is still not resolvable | 已下载 %[1]s，但仍无法解析", path)
	}
	st.Version = queryVersion(ctx, log, st.Path, t)
	return st, nil
}
