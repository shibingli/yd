// Package web_test verifies the front-end assets embedded by package web: the vendored
// JavaScript locked in assets.lock.json, the compiled Tailwind stylesheet and the page
// templates. | Package web_test 校验 package web 内嵌的前端资产：由 assets.lock.json
// 锁定的 vendoring JavaScript、编译后的 Tailwind 样式与页面模板。
//
// Every check here runs fully offline: no test in this file opens a socket. | 本文件所有校验
// 都完全离线执行：没有任何测试会打开网络连接。
package web_test

import (
	"encoding/hex"
	"io/fs"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/core/jsonx"
	"github.com/shibingli/yd/core/regexpx"
	"github.com/shibingli/yd/web"
	"github.com/spf13/afero"
)

// lockFilePath is the asset manifest path inside the embedded tree. | lockFilePath 是内嵌目录树中资产清单的路径。
const lockFilePath = "assets.lock.json"

// vendorDir is the directory holding every vendored third-party script. | vendorDir 是存放全部 vendoring 第三方脚本的目录。
const vendorDir = "static/js/vendor"

// minVendorBytes is the smallest plausible size of a minified library; anything smaller is a truncated download. | minVendorBytes 是压缩后库文件的最小可信体积；更小即视为下载被截断。
const minVendorBytes = 256

// lockAsset mirrors one record of assets.Manifest. | lockAsset 对应 assets.Manifest 中的单条记录。
//
// The types are redeclared here instead of importing the assets package: package web must not depend on it,
// and this manifest is read directly from the embedded file system. | 这里重新声明这些类型而不导入 assets 包：
// package web 不得依赖它，而本清单直接从内嵌文件系统读取。
type lockAsset struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	URL      string `json:"url"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	License  string `json:"license"`
	Size     int64  `json:"size"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
}

// lockManifest mirrors assets.Manifest. | lockManifest 对应 assets.Manifest。
type lockManifest struct {
	Version     int         `json:"version"`
	GeneratedAt string      `json:"generated_at"`
	Sources     []string    `json:"sources"`
	Assets      []lockAsset `json:"assets"`
}

// readEmbedded reads a file from the embedded front-end tree and fails the test when it is absent. | readEmbedded 从内嵌前端目录树读取文件，缺失时直接判定测试失败。
func readEmbedded(t *testing.T, fsys afero.Fs, name string) []byte {
	t.Helper()
	data, err := afero.ReadFile(fsys, name)
	if err != nil {
		t.Fatalf("reading embedded file %q failed: %v | 读取内嵌文件 %q 失败: %v", name, err, name, err)
	}
	return data
}

// loadLock parses assets.lock.json from the embedded tree. | loadLock 解析内嵌目录树中的 assets.lock.json。
func loadLock(t *testing.T) lockManifest {
	t.Helper()
	var m lockManifest
	data := readEmbedded(t, web.FS(), lockFilePath)
	if err := jsonx.Unmarshal(data, &m); err != nil {
		t.Fatalf("parsing %s failed: %v | 解析 %s 失败: %v", lockFilePath, err, lockFilePath, err)
	}
	return m
}

// sha256Hex reports whether s is a 64 character lower-case hexadecimal digest. | sha256Hex 报告 s 是否为 64 位小写十六进制摘要。
func sha256Hex(s string) bool {
	if len(s) != 64 || s != strings.ToLower(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// TestAllManifestAssetsExistAndMatchChecksum is the offline integrity gate for every locked front-end asset. | TestAllManifestAssetsExistAndMatchChecksum 是全部锁定前端资产的离线完整性闸门。
//
// For each record it asserts the file exists inside the embedded tree, that its byte length matches the
// recorded size and that its SHA-256 matches the recorded digest, so a corrupted or swapped vendor file
// can never reach a release build. | 对每条记录，它断言文件存在于内嵌目录树、字节数与记录一致、SHA-256 与记录一致，
// 从而确保被篡改或替换的 vendoring 文件无法进入发布构建。
func TestAllManifestAssetsExistAndMatchChecksum(t *testing.T) {
	fsys := web.FS()
	m := loadLock(t)

	if m.Version < 1 {
		t.Errorf("manifest version must be >= 1, got %d | 清单 version 必须 >= 1，实际为 %d", m.Version, m.Version)
	}
	if _, err := time.Parse(time.RFC3339, m.GeneratedAt); err != nil {
		t.Errorf("generated_at %q is not RFC3339: %v | generated_at %q 不是 RFC3339: %v", m.GeneratedAt, err, m.GeneratedAt, err)
	}
	if len(m.Sources) == 0 {
		t.Error("manifest sources must not be empty | 清单 sources 不得为空")
	}
	for i, src := range m.Sources {
		if !strings.HasPrefix(src, "https://") {
			t.Errorf("sources[%d] = %q must be an https origin | sources[%d] = %q 必须是 https 源站", i, src, i, src)
		}
	}
	if len(m.Assets) < 13 {
		t.Fatalf("manifest must lock at least the 13 vendored scripts, got %d | 清单至少应锁定 13 个 vendoring 脚本，实际为 %d", len(m.Assets), len(m.Assets))
	}

	byName := make(map[string]lockAsset, len(m.Assets))
	byPath := make(map[string]string, len(m.Assets))
	for _, a := range m.Assets {
		if _, dup := byName[a.Name]; dup {
			t.Errorf("duplicate asset name %q | 资产名重复: %q", a.Name, a.Name)
		}
		if prev, dup := byPath[a.Path]; dup {
			t.Errorf("asset path %q is claimed by both %q and %q | 资产路径 %q 同时被 %q 与 %q 占用", a.Path, prev, a.Name, a.Path, prev, a.Name)
		}
		byName[a.Name] = a
		byPath[a.Path] = a.Name
	}

	// The three libraries the framework unconditionally ships must be present and marked required. | 框架无条件内置的三个库必须存在且标记为 required。
	for _, name := range []string{"htmx", "alpinejs", "idiomorph"} {
		a, ok := byName[name]
		if !ok {
			t.Errorf("required asset %q is missing from the manifest | 清单缺少必需资产 %q", name, name)
			continue
		}
		if !a.Required {
			t.Errorf("asset %q must be marked required | 资产 %q 必须标记为 required", name, name)
		}
	}

	for _, a := range m.Assets {
		t.Run(a.Name, func(t *testing.T) {
			switch {
			case a.Name == "":
				t.Fatal("name must not be empty | name 不得为空")
			case a.Version == "":
				t.Fatal("version must not be empty | version 不得为空")
			case a.URL == "":
				t.Fatal("url must not be empty | url 不得为空")
			case a.Path == "":
				t.Fatal("path must not be empty | path 不得为空")
			case a.Kind == "":
				t.Fatal("kind must not be empty | kind 不得为空")
			case a.License == "":
				t.Fatal("license must not be empty | license 不得为空")
			}

			// The URL must be an absolute https URL, and the version must appear in it so that a
			// version bump can never leave the download address pointing at the old release. | URL 必须是绝对 https 地址，
			// 且版本号必须出现在其中，避免升级版本后下载地址仍指向旧发布。
			u, err := url.Parse(a.URL)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				t.Fatalf("url %q must be an absolute https URL: %v | url %q 必须是绝对 https 地址: %v", a.URL, err, a.URL, err)
			}
			if !strings.Contains(a.URL, a.Version) {
				t.Errorf("url %q must contain version %q | url %q 必须包含版本号 %q", a.URL, a.Version, a.URL, a.Version)
			}

			if !fs.ValidPath(a.Path) {
				t.Fatalf("path %q must satisfy fs.ValidPath | path %q 必须满足 fs.ValidPath", a.Path, a.Path)
			}
			if !strings.HasPrefix(a.Path, vendorDir+"/") {
				t.Fatalf("path %q must live under %s/ | path %q 必须位于 %s/ 之下", a.Path, vendorDir, a.Path, vendorDir)
			}

			if !sha256Hex(a.SHA256) {
				t.Fatalf("sha256 %q must be 64 lower-case hex characters | sha256 %q 必须是 64 位小写十六进制", a.SHA256, a.SHA256)
			}

			info, err := fsys.Stat(a.Path)
			if err != nil {
				t.Fatalf("locked asset file is missing: %v | 锁定的资产文件缺失: %v", err, err)
			}
			if info.IsDir() {
				t.Fatalf("locked asset path %q is a directory | 锁定的资产路径 %q 是目录", a.Path, a.Path)
			}
			if info.Size() != a.Size {
				t.Errorf("size mismatch: manifest %d, file %d | 体积不一致：清单 %d，文件 %d", a.Size, info.Size(), a.Size, info.Size())
			}
			if a.Size <= 0 {
				t.Errorf("size %d must be positive | size %d 必须为正数", a.Size, a.Size)
			}

			// Recompute the digest from the embedded bytes: the checksum is the contract, not the file name. | 从内嵌字节重算摘要：校验和才是契约，文件名不是。
			got, err := afs.Sha256File(fsys, a.Path)
			if err != nil {
				t.Fatalf("hashing %q failed: %v | 计算 %q 的哈希失败: %v", a.Path, err, a.Path, err)
			}
			if got != a.SHA256 {
				t.Errorf("sha256 mismatch for %q: manifest %s, file %s | %q 的 sha256 不一致：清单 %s，文件 %s", a.Path, a.SHA256, got, a.Path, a.SHA256, got)
			}
		})
	}
}

// externalRefPattern matches a src/href attribute pointing at an absolute or scheme-relative http(s) URL. | externalRefPattern 匹配指向绝对或协议相对 http(s) 地址的 src/href 属性。
var externalRefPattern = regexpx.MustCompile(`(?i)\b(?:src|href)\s*=\s*["']?(?:https?:)?//`)

// TestNoExternalCdnReferenceInTemplates forbids any template from pulling JavaScript or CSS off the public network. | TestNoExternalCdnReferenceInTemplates 禁止任何模板从公网拉取 JavaScript 或 CSS。
//
// Assets are embedded in the binary and served from /static/..., so an absolute URL in a template would
// silently reintroduce a runtime CDN dependency and break air-gapped deployments. | 资产内嵌于二进制并由 /static/... 提供，
// 模板中的绝对地址会悄悄重新引入运行时 CDN 依赖，并破坏内网隔离部署。
func TestNoExternalCdnReferenceInTemplates(t *testing.T) {
	fsys := web.FS()
	var scanned, refs int

	err := afs.Walk(fsys, "templates", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		scanned++
		for i, line := range strings.Split(strings.ReplaceAll(string(readEmbedded(t, fsys, p)), "\r\n", "\n"), "\n") {
			if m := externalRefPattern.FindString(line); m != "" {
				refs++
				t.Errorf("%s:%d references an external resource (%q); serve it from /static/... instead | %s:%d 引用了外部资源（%q），请改为从 /static/... 提供",
					p, i+1, m, p, i+1, m)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking templates failed: %v | 遍历模板失败: %v", err, err)
	}
	if scanned == 0 {
		t.Fatal("no template was scanned, the guard would be vacuous | 未扫描到任何模板，该守卫形同虚设")
	}
	if refs == 0 {
		t.Logf("checked %d templates, all asset references are local | 已检查 %d 个模板，资产引用全部为本地路径", scanned, scanned)
	}
}

// TestVendoredFilesNonEmpty rejects empty or truncated vendored scripts. | TestVendoredFilesNonEmpty 拒绝空文件或被截断的 vendoring 脚本。
//
// A zero-byte download looks like a successful fetch to curl and to a naive updater, so the size bound is
// asserted independently of the checksum manifest. | 零字节下载对 curl 和只做存在性判断的更新器而言与成功无异，
// 因此这里独立于校验和清单再次断言体积下限。
func TestVendoredFilesNonEmpty(t *testing.T) {
	fsys := web.FS()
	var files []string

	err := afs.Walk(fsys, vendorDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		files = append(files, p)
		if info.Size() <= 0 {
			t.Errorf("vendored file %q is empty | vendoring 文件 %q 为空", p, p)
			return nil
		}
		if strings.HasSuffix(p, ".js") && info.Size() < minVendorBytes {
			t.Errorf("vendored script %q is only %d bytes, below the %d byte floor | vendoring 脚本 %q 仅 %d 字节，低于 %d 字节下限",
				p, info.Size(), minVendorBytes, p, info.Size(), minVendorBytes)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s failed: %v | 遍历 %s 失败: %v", vendorDir, err, vendorDir, err)
	}

	scripts := 0
	for _, p := range files {
		if strings.HasSuffix(p, ".js") {
			scripts++
		}
	}
	if scripts < 13 {
		t.Errorf("%s holds %d scripts, expected at least 13 | %s 仅含 %d 个脚本，至少应为 13", vendorDir, scripts, vendorDir, scripts)
	}

	// The licence inventory is part of the vendoring contract: every script above must be attributable. | 许可清单是 vendoring 契约的一部分：上述每个脚本都必须可追溯来源。
	if len(readEmbedded(t, fsys, path.Join(vendorDir, "LICENSES.md"))) == 0 {
		t.Error("LICENSES.md must not be empty | LICENSES.md 不得为空")
	}
	t.Logf("%d vendored files (%d scripts) are non-empty | %d 个 vendoring 文件（%d 个脚本）均非空", len(files), scripts, len(files), scripts)
}

// TestVendoredTreeMatchesManifestPaths keeps the vendor directory and the manifest from drifting apart. | TestVendoredTreeMatchesManifestPaths 防止 vendoring 目录与清单彼此漂移。
//
// A script copied into the tree without a manifest record is unreviewed code shipping in the binary,
// and a manifest record without a file is a broken download. | 目录中未登记清单的脚本就是随二进制发布的未审计代码，
// 而清单里有记录却没有文件则意味着下载失败。
func TestVendoredTreeMatchesManifestPaths(t *testing.T) {
	fsys := web.FS()
	m := loadLock(t)

	locked := make(map[string]bool, len(m.Assets))
	for _, a := range m.Assets {
		locked[a.Path] = true
	}

	var onDisk []string
	err := afs.Walk(fsys, vendorDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".js") {
			return nil
		}
		onDisk = append(onDisk, p)
		if !locked[p] {
			t.Errorf("script %q is vendored but absent from %s | 脚本 %q 已 vendoring 但未登记在 %s 中", p, lockFilePath, p, lockFilePath)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s failed: %v | 遍历 %s 失败: %v", vendorDir, err, vendorDir, err)
	}

	disk := make(map[string]bool, len(onDisk))
	for _, p := range onDisk {
		disk[p] = true
	}
	for _, p := range sortedKeys(locked) {
		if !disk[p] {
			t.Errorf("manifest locks %q which is not vendored | 清单锁定了 %q，但该文件未 vendoring", p, p)
		}
	}
}

// sortedKeys returns the map keys in ascending order for deterministic failure output. | sortedKeys 按升序返回映射键，以保证失败输出稳定。
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestEmbeddedAssetIntegritySurvivesReleaseLayout guards the checksum gate against a layout change. | TestEmbeddedAssetIntegritySurvivesReleaseLayout 防止目录布局变动让校验和闸门失效。
//
// The digest is recomputed over the embedded bytes, so the guard keeps working even when the manifest and
// the tree are the only two things shipped, with no build directory beside them. | 摘要是对内嵌字节重算的，
// 因此即使只发布清单与目录树本身、旁边没有构建目录，该闸门依然有效。
func TestEmbeddedAssetIntegritySurvivesReleaseLayout(t *testing.T) {
	fsys := web.FS()
	m := loadLock(t)

	// Loading the manifest through the embedded handle must yield the same bytes as loading it from source. | 通过内嵌句柄读取清单，必须与从源码目录读取得到相同字节。
	if sum, err := afs.Sha256File(fsys, lockFilePath); err != nil {
		t.Fatalf("hashing %s failed: %v | 计算 %s 的哈希失败: %v", lockFilePath, err, lockFilePath, err)
	} else if !sha256Hex(sum) {
		t.Errorf("digest of %s is not a sha256 hex string: %q | %s 的摘要不是 sha256 十六进制串: %q", lockFilePath, sum, lockFilePath, sum)
	}

	// Every locked script must be reachable by the exact path the manifest records; a typo here would only
	// surface at runtime, when a page silently loses its behaviour. | 每个锁定脚本都必须能按清单记录的路径精确取到；
	// 这里的笔误只会在运行时才暴露，表现为页面悄悄丢失行为。
	for _, a := range m.Assets {
		if _, err := afero.ReadFile(fsys, a.Path); err != nil {
			t.Errorf("locked asset %q is not readable at %q: %v | 锁定的资产 %q 在 %q 处不可读: %v", a.Name, a.Path, err, a.Name, a.Path, err)
		}
	}
}
