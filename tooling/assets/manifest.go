// Package assets governs the framework's front-end assets: a checked-in manifest with SHA-256 locks,
// an online updater that downloads through a chain of mirror sources, and a strictly offline verifier. | Package
// assets 负责框架的前端资产：签入仓库、带 SHA-256 锁定的清单，按镜像源链下载的在线更新器，以及严格离线的校验器。
//
// Responsibility boundary | 职责边界：
//
//   - it owns the manifest format (Manifest/Asset) and the lock semantics around it; | 它负责清单格式
//     （Manifest/Asset）及其锁定语义；
//   - it does NOT know about the repository layout: the write root and the manifest path are always
//     passed in, so the same code works for the repo (web/) and for a generated project; | 它不了解仓库
//     布局：写入根与清单路径始终由调用方传入，因此同一份代码既适用于本仓库（web/）也适用于生成的项目；
//   - it does NOT unzip, execute or inspect asset content: a downloaded body is treated as opaque bytes
//     and only ever measured by SHA-256. | 它不解包、不执行、不解析资产内容：下载到的字节体被视为
//     不透明数据，只用 SHA-256 度量。
//
// Hard rules enforced here (Review Focus #4) | 本包强制的硬规则（Review Focus #4）：
//
//  1. download into memory, verify, and only then write; a failed verification never touches the disk
//     and never rewrites the lock; | 先下载到内存并校验，通过后才落盘；校验失败绝不动磁盘、也绝不改写锁定；
//  2. Offline=true injects a RoundTripper that refuses to open a connection, and the offline code path
//     checks local files only, so no request is ever issued; | Offline=true 会注入一个拒绝建立连接的
//     RoundTripper，且离线分支只做本地检查，因此绝不会发出任何请求；
//  3. one failing asset never blocks the others: successful ones stay updated, failures are aggregated
//     with errors.Join. | 单个资产失败不影响其它资产：成功的保持更新，失败项用 errors.Join 聚合返回。
package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
	"github.com/spf13/afero"
)

// Package-level sentinel errors; every failure path references one of them so callers can branch with
// errors.Is instead of comparing strings. | 包级哨兵错误；所有失败路径都引用其中之一，使调用方能用
// errors.Is 分支而不是比较字符串。
var (
	// ErrManifestNotFound reports a manifest path that does not hold a file. | ErrManifestNotFound 表示清单路径上不存在文件。
	ErrManifestNotFound = errx.New("assets: manifest not found | assets：未找到资产清单")
	// ErrManifestInvalid reports a manifest that cannot be parsed or written. | ErrManifestInvalid 表示清单无法解析或写入。
	ErrManifestInvalid = errx.New("assets: invalid manifest | assets：资产清单非法")
	// ErrManifestVersion reports a manifest written by a newer version of yd. | ErrManifestVersion 表示清单由更新版本的 yd 写入。
	ErrManifestVersion = errx.New("assets: unsupported manifest version | assets：不支持的资产清单版本")
	// ErrInvalidAsset reports an asset entry with an empty name or an unusable path. | ErrInvalidAsset 表示资产条目缺少名字或路径不可用。
	ErrInvalidAsset = errx.New("assets: invalid asset entry | assets：非法的资产条目")
	// ErrChecksumMismatch reports downloaded or on-disk content that does not match the lock. | ErrChecksumMismatch 表示下载内容或本地内容与锁定值不匹配。
	ErrChecksumMismatch = errx.New("assets: checksum mismatch | assets：校验和不匹配")
	// ErrOffline reports a network access attempt while offline mode is on. | ErrOffline 表示在离线模式下尝试访问网络。
	ErrOffline = errx.New("assets: offline mode forbids network access | assets：离线模式禁止网络访问")
	// ErrOfflineMissing reports a missing local file in offline mode. | ErrOfflineMissing 表示离线模式下本地文件缺失。
	ErrOfflineMissing = errx.New("assets: file missing in offline mode | assets：离线模式下文件缺失")
	// ErrDownloadFailed reports that every candidate source failed. | ErrDownloadFailed 表示所有候选源均失败。
	ErrDownloadFailed = errx.New("assets: download failed | assets：下载失败")
	// ErrAssetTooLarge reports a body beyond the in-memory size limit. | ErrAssetTooLarge 表示内容超过内存体积上限。
	ErrAssetTooLarge = errx.New("assets: asset exceeds the size limit | assets：资产超过体积上限")
	// ErrNilManifest reports a nil manifest handed to Verify. | ErrNilManifest 表示传给 Verify 的清单为空。
	ErrNilManifest = errx.New("assets: nil manifest | assets：资产清单为空")
	// ErrVerifyFailed reports at least one missing or mismatching asset. | ErrVerifyFailed 表示至少有一项资产缺失或校验和不符。
	ErrVerifyFailed = errx.New("assets: verification failed | assets：资产校验未通过")
)

// Manifest and update defaults, used when the caller leaves the corresponding field empty. | 清单与更新默认值，在调用方相应字段留空时使用。
const (
	// CurrentManifestVersion is the manifest schema version written by this package. | CurrentManifestVersion 是本包写入的清单结构版本。
	CurrentManifestVersion = 1
	// DefaultManifestPath is the manifest location inside a yd project. | DefaultManifestPath 是 yd 项目内清单的默认位置。
	DefaultManifestPath = "web/assets.lock.json"
	// DefaultRoot is the asset write root inside a yd project. | DefaultRoot 是 yd 项目内资产的默认写入根目录。
	DefaultRoot = "web"
	// DefaultConcurrency is the download fan-out used when UpdateOptions.Concurrency is zero. | DefaultConcurrency 是 UpdateOptions.Concurrency 为零时的下载并发度。
	DefaultConcurrency = 4
	// maxAssetSize caps a single asset body, keeping the in-memory download bounded. | maxAssetSize 限制单个资产的体积，使内存下载有界。
	maxAssetSize = 64 << 20
)

// DefaultSources is the mirror chain used when neither UpdateOptions.Sources nor Manifest.Sources is
// set. Placeholders: {pkg} {ver} {file} {name} {url}; a template whose placeholders cannot be derived
// is skipped, so an asset whose URL is not a package URL simply falls back to its own URL. | DefaultSources 是
// UpdateOptions.Sources 与 Manifest.Sources 都未设置时使用的镜像链。占位符：{pkg} {ver} {file}
// {name} {url}；无法推导占位符的模板会被跳过，因此 URL 不形如包地址的资产直接回退到自身 URL。
var DefaultSources = []string{
	"https://cdn.jsdelivr.net/npm/{pkg}@{ver}/{file}",
	"https://unpkg.com/{pkg}@{ver}/{file}",
}

// Asset kinds recorded in Manifest.Kind. | 记录在 Manifest.Kind 中的资产类型。
const (
	// KindJS marks a JavaScript file. | KindJS 标记 JavaScript 文件。
	KindJS = "js"
	// KindCSS marks a stylesheet. | KindCSS 标记样式表。
	KindCSS = "css"
	// KindWASM marks a WebAssembly module. | KindWASM 标记 WebAssembly 模块。
	KindWASM = "wasm"
	// KindHTML marks an HTML fragment or page. | KindHTML 标记 HTML 片段或页面。
	KindHTML = "html"
	// KindBinary marks anything else. | KindBinary 标记其它二进制资产。
	KindBinary = "binary"
)

// Asset is one front-end dependency pinned to an exact version and checksum. | Asset 是一个被固定到确定版本与校验和的前端依赖。
type Asset struct {
	// Name is the logical asset name, unique inside a manifest (e.g. "htmx"). | Name 是清单内唯一的逻辑资产名（例如 "htmx"）。
	Name string `json:"name"`
	// Version is the upstream version the lock was taken from (e.g. "4.0.0"). | Version 是锁定所依据的上游版本（例如 "4.0.0"）。
	Version string `json:"version,omitempty"`
	// URL is the canonical download URL; mirrors are derived from it. | URL 是权威下载地址，镜像地址由它推导。
	URL string `json:"url,omitempty"`
	// Path is the destination relative to the write root, always slash-separated. | Path 是相对于写入根的目标路径，始终以 "/" 分隔。
	Path string `json:"path"`
	// SHA256 is the lowercase hex digest of the file content; empty means "not locked yet". | SHA256 是文件内容的小写十六进制摘要；为空表示尚未锁定。
	SHA256 string `json:"sha256,omitempty"`
	// License is the SPDX identifier of the upstream license (e.g. "MIT","0BSD"). | License 是上游许可的 SPDX 标识（例如 "MIT"、"0BSD"）。
	License string `json:"license,omitempty"`
	// Kind is one of the Kind* constants. | Kind 取 Kind* 常量之一。
	Kind string `json:"kind,omitempty"`
	// Size is the byte length of the locked content, 0 when unknown. | Size 是锁定内容的字节长度，未知时为 0。
	Size int64 `json:"size,omitempty"`
	// Required marks assets the application cannot run without; Required assets get a full verify. | Required 标记应用缺少即无法运行的资产；Required 资产会做完整校验。
	Required bool `json:"required,omitempty"`
}

// LocalPath resolves the asset path against the write root for the local file system. | LocalPath 把资产路径按本地文件系统解析到写入根之下。
func (a Asset) LocalPath(root string) string {
	rel := filepath.FromSlash(strings.TrimPrefix(a.Path, "/"))
	if root == "" {
		return rel
	}
	return filepath.Join(root, rel)
}

// Manifest is the checked-in lock file describing every vendored front-end asset. | Manifest 是签入仓库的锁文件，描述全部 vendoring 的前端资产。
type Manifest struct {
	// GeneratedAt is the UTC timestamp of the last write. | GeneratedAt 是最近一次写入的 UTC 时间。
	GeneratedAt time.Time `json:"generated_at"`
	// Sources is the mirror chain used by Update when no per-call sources are given. | Sources 是 Update 在未传入本次源列表时使用的镜像链。
	Sources []string `json:"sources,omitempty"`
	// Assets is the locked asset list, kept sorted by name. | Assets 是锁定资产列表，按名字排序保存。
	Assets []Asset `json:"assets"`
	// Version is the schema version, written as CurrentManifestVersion. | Version 是结构版本，写入 CurrentManifestVersion。
	Version int `json:"version"`
}

// LoadManifest reads and validates a manifest from fs. | LoadManifest 从 fs 读取并校验清单。
//
// A missing file returns an error wrapping ErrManifestNotFound that also satisfies
// errors.Is(err, fs.ErrNotExist), so callers can bootstrap a fresh manifest when they want to. | 文件缺失时
// 返回包装了 ErrManifestNotFound 的错误，同时满足 errors.Is(err, fs.ErrNotExist)，便于调用方按需
// 初始化一份空清单。
func LoadManifest(fsys afero.Fs, path string) (*Manifest, error) {
	if fsys == nil {
		return nil, errx.Detail(ErrManifestInvalid, "nil file system | 文件系统为 nil")
	}
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errx.WrapDetail(err, ErrManifestNotFound, "%s", path)
		}
		return nil, errx.WrapDetail(err, ErrManifestInvalid, "%s", path)
	}

	var m Manifest
	if err := jsonx.Unmarshal(data, &m); err != nil {
		return nil, errx.WrapDetail(err, ErrManifestInvalid, "%s", path)
	}
	if m.Version == 0 {
		m.Version = CurrentManifestVersion
	}
	if m.Version > CurrentManifestVersion {
		return nil, errx.Detail(ErrManifestVersion, "%s: %d > %d", path, m.Version, CurrentManifestVersion)
	}
	return &m, nil
}

// Save writes the manifest back to fs, refreshing GeneratedAt and sorting the asset list. | Save 把清单写回 fs，刷新 GeneratedAt 并对资产列表排序。
//
// The write goes through afs.WriteFileIfChanged, so an unchanged manifest keeps its modification time. | 写入经
// afs.WriteFileIfChanged，内容未变化时不会触碰文件的修改时间。
func (m *Manifest) Save(fsys afero.Fs, path string) error {
	if m == nil {
		return ErrNilManifest
	}
	if err := Validate(m.Assets); err != nil {
		return err
	}
	if m.Version == 0 {
		m.Version = CurrentManifestVersion
	}
	m.GeneratedAt = time.Now().UTC()
	sortAssets(m.Assets)

	data, err := jsonx.MarshalIndent(m, "", "  ")
	if err != nil {
		return errx.WrapDetail(err, ErrManifestInvalid, "%s", path)
	}
	data = append(data, '\n')

	if _, err := afs.WriteFileIfChanged(fsys, path, data); err != nil {
		return errx.WrapDetail(err, ErrManifestInvalid, "%s", path)
	}
	return nil
}

// Find returns the asset with the given name. | Find 返回指定名字的资产。
//
// The pointer points into the manifest's own slice, so mutating the returned asset (for example
// filling in a freshly computed checksum) updates the manifest. | 返回的指针指向清单自身的切片，因此
// 修改该资产（例如回填刚算出的校验和）会同步反映到清单上。
func (m *Manifest) Find(name string) (*Asset, bool) {
	if m == nil {
		return nil, false
	}
	for i := range m.Assets {
		if m.Assets[i].Name == name {
			return &m.Assets[i], true
		}
	}
	return nil, false
}

// Add appends a (an existing entry with the same name is replaced) and validates it. | Add 追加资产（同名条目会被替换）并校验其合法性。
func (m *Manifest) Add(a Asset) error {
	if m == nil {
		return ErrNilManifest
	}
	if err := Validate([]Asset{a}); err != nil {
		return err
	}
	if existing, ok := m.Find(a.Name); ok {
		*existing = a
		return nil
	}
	m.Assets = append(m.Assets, a)
	return nil
}

// Validate checks that every asset carries a name and a usable relative path. | Validate 校验每个资产都带名字且路径是合法的相对路径。
func Validate(assets []Asset) error {
	for i := range assets {
		a := assets[i]
		if strings.TrimSpace(a.Name) == "" {
			return errx.Detail(ErrInvalidAsset, "asset #%[1]d is missing name | asset #%[1]d 缺少 name", i)
		}
		if _, err := NormalizePath(a.Path); err != nil {
			return errx.WrapDetail(err, ErrInvalidAsset, "%s", a.Name)
		}
		if a.SHA256 != "" && !isHexDigest(a.SHA256) {
			return errx.Detail(ErrInvalidAsset, "the sha256 of %[1]s is not 64 hexadecimal digits | %[1]s 的 sha256 不是 64 位十六进制", a.Name)
		}
	}
	return nil
}

// NormalizePath validates a slash-separated relative asset path and returns its cleaned form. | NormalizePath 校验以 "/" 分隔的相对资产路径并返回其规范化形式。
//
// Absolute paths, drive letters and ".." segments are rejected: an asset path comes from a lock file
// that may be edited by hand, and it must never be able to escape the write root. | 绝对路径、盘符与 ".."
// 段都会被拒绝：资产路径来自可能被手工编辑的锁文件，绝不能逃逸出写入根。
func NormalizePath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errx.Detail(ErrInvalidAsset, "path is empty | path 为空")
	}
	slashed := filepath.ToSlash(p)
	if strings.HasPrefix(slashed, "/") || filepath.IsAbs(p) || filepath.VolumeName(p) != "" {
		return "", errx.Detail(ErrInvalidAsset, "path %[1]q is not a relative path | path %[1]q 不是相对路径", p)
	}
	cleaned := path.Clean(slashed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errx.Detail(ErrInvalidAsset, "path %[1]q escapes the write root | path %[1]q 逃逸出写入根", p)
	}
	return cleaned, nil
}

// KindFromPath infers the asset kind from its file extension. | KindFromPath 依据文件扩展名推断资产类型。
func KindFromPath(p string) string {
	switch strings.ToLower(path.Ext(filepath.ToSlash(p))) {
	case ".js", ".mjs", ".cjs":
		return KindJS
	case ".css":
		return KindCSS
	case ".wasm":
		return KindWASM
	case ".html", ".htm":
		return KindHTML
	default:
		return KindBinary
	}
}

// sortAssets sorts assets by name in place, keeping the lock file diff-stable. | sortAssets 按名字就地排序，保证锁文件差异稳定。
func sortAssets(assets []Asset) {
	for i := 1; i < len(assets); i++ {
		for j := i; j > 0 && assets[j].Name < assets[j-1].Name; j-- {
			assets[j], assets[j-1] = assets[j-1], assets[j]
		}
	}
}

// isHexDigest reports whether s is a 64-character lowercase hex SHA-256 digest. | isHexDigest 报告 s 是否为 64 位小写十六进制 SHA-256 摘要。
func isHexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	if s != strings.ToLower(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// sha256Hex returns the lowercase hex SHA-256 digest of data. | sha256Hex 返回 data 的小写十六进制 SHA-256 摘要。
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
