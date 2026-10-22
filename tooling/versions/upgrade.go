package versions

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/shibingli/yd/tooling/assets"
	yderrs "github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/json"
)

// npmRegistryBase is the metadata endpoint used to resolve the latest release of one package. | npmRegistryBase 是解析单个包最新版本所用的元数据端点。
const npmRegistryBase = "https://registry.npmjs.org/"

var (
	// ErrRegistry is returned when the npm registry cannot be queried or answers without a version. | ErrRegistry 表示 npm 仓库无法查询或应答中没有版本。
	ErrRegistry = yderrs.New("query npm registry failed | 查询 npm 仓库失败")
)

// Doer performs one HTTP request; *http.Client satisfies it. | Doer 执行一次 HTTP 请求；*http.Client 满足它。
type Doer interface {
	// Do sends the request and returns the response. | Do 发送请求并返回响应。
	Do(*http.Request) (*http.Response, error)
}

// AssetUpgrade is the upgrade decision for one locked asset. | AssetUpgrade 是单个已锁定资产的升级决定。
type AssetUpgrade struct {
	// Name is the asset name in the manifest. | Name 是清单中的资产名。
	Name string
	// Package is the npm package the asset comes from, empty when the source is not npm. | Package 是资产来自的 npm 包；来源不是 npm 时为空。
	Package string
	// From is the currently locked version. | From 是当前锁定的版本。
	From string
	// To is the latest upstream version; empty when it could not be resolved. | To 是最新的上游版本；无法解析时为空。
	To string
	// NewURL is the rewritten URL; empty when the asset does not change. | NewURL 是改写后的地址；资产不变时为空。
	NewURL string
	// Reason explains a skipped asset. | Reason 说明资产被跳过的原因。
	Reason string
	// Changed reports whether the version and URL move to a newer release. | Changed 表示版本与地址将升级到更新的发布。
	Changed bool
}

// ParseNPMURL extracts the package name and version from a jsDelivr or unpkg URL. | ParseNPMURL 从 jsDelivr 或 unpkg 地址中提取包名与版本。
//
// Both mirrors spell versions in the path: | 两个镜像都在路径中写版本：
//
//	https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js
//	https://unpkg.com/alpinejs@3.17.4/dist/cdn.min.js
//
// Scoped packages keep their leading @ and carry the version after the second path segment. | 带作用域的包保留开头的 @，版本写在第二个路径段之后。
func ParseNPMURL(raw string) (pkg, version string, ok bool) {
	if !strings.HasPrefix(raw, "https://cdn.jsdelivr.net/npm/") && !strings.HasPrefix(raw, "https://unpkg.com/") {
		return "", "", false
	}
	pkg, version, _ = assets.PackageParts(raw)
	if pkg == "" || version == "" {
		return "", "", false
	}
	return pkg, version, true
}

// ResolveLatest returns the newest stable release of pkg on the same major track as current. | ResolveLatest 返回 pkg 在与 current 同一大版本轨道上的最新稳定版本。
//
// npm's `latest` tag is not always the version a project tracks: htmx publishes the 4.x line under
// `next` while `latest` still points at 2.x, so taking `latest` blindly would *downgrade* the
// project. The resolver reads the abbreviated registry metadata, keeps stable releases whose major
// equals current's, and returns the highest one; it never returns a version lower than current.
// Major upgrades remain a deliberate edit of the lock file. | npm 的 `latest` 标签并不总是项目跟踪的
// 版本：htmx 把 4.x 线发布在 `next` 下，而 `latest` 仍指向 2.x，因此盲取 `latest` 会把项目**降级**。
// 解析器读取精简的仓库元数据，保留大版本与 current 相同的稳定发布并返回其中最高的一个；绝不返回低于
// current 的版本。大版本升级仍然是对锁文件的一次显式修改。
func ResolveLatest(ctx context.Context, client Doer, pkg, current string) (string, error) {
	if client == nil {
		return "", yderrs.Detail(ErrRegistry, "nil http client | HTTP 客户端为 nil")
	}
	cur, ok := parseSemver(current)
	if !ok {
		return "", yderrs.Detail(ErrRegistry, "package %[1]q: invalid current version %[2]q | 包 %[1]q：非法的当前版本 %[2]q", pkg, current)
	}
	endpoint := npmRegistryBase + strings.ReplaceAll(pkg, "/", "%2F")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", yderrs.WrapDetail(err, ErrRegistry, "package %[1]q | 包 %[1]q", pkg)
	}
	// The abbreviated document carries dist-tags and the version list without tarball metadata. | 精简文档包含 dist-tags 与版本列表，但不含 tarball 元数据。
	req.Header.Set("Accept", "application/vnd.npm.install-v1+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", yderrs.WrapDetail(err, ErrRegistry, "package %[1]q | 包 %[1]q", pkg)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", yderrs.Detail(ErrRegistry, "package %[1]q: HTTP %[2]d | 包 %[1]q：HTTP 状态 %[2]d", pkg, resp.StatusCode)
	}
	var body struct {
		DistTags map[string]string   `json:"dist-tags"`
		Versions map[string]struct{} `json:"versions"`
	}
	// Cap the registry response: a broken or hostile registry must not be able to make the tool
	// buffer an arbitrarily large metadata document. | 限制 registry 响应体积：损坏或恶意的 registry
	// 不得让工具缓冲任意大的元数据文档。
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return "", yderrs.WrapDetail(err, ErrRegistry, "package %[1]q | 包 %[1]q", pkg)
	}
	best := current
	bestSV := cur
	for raw := range body.Versions {
		sv, ok := parseSemver(raw)
		if !ok || sv.major != cur.major || sv.pre != "" {
			continue // only stable releases on the current major track | 只看当前大版本上的稳定发布
		}
		if cmp := compareSemver(sv, bestSV); cmp > 0 || (cmp == 0 && raw < best) {
			// The lexical tie-break makes an equal-semver pair ("4.1.0" vs "v4.1.0") resolve the
			// same way on every run instead of depending on map iteration order. | 同版本的不同
			// 拼写（"4.1.0" 与 "v4.1.0"）用字典序决胜，使每次运行结果一致，而不是取决于 map 遍历顺序。
			best, bestSV = raw, sv
		}
	}
	// A package whose current major has no stable entry yet keeps its pinned version; a `latest`
	// from another major is never a candidate. | 当前大版本尚无稳定发布的包保持原锁定；其他大版本的
	// `latest` 永远不是候选。
	if len(body.Versions) == 0 && body.DistTags["latest"] != "" {
		if sv, ok := parseSemver(body.DistTags["latest"]); ok && sv.major == cur.major && compareSemver(sv, bestSV) > 0 {
			best, bestSV = body.DistTags["latest"], sv
		}
	}
	return best, nil
}

// semver is the subset of a version needed for ordering. | semver 是排序所需的版本子集。
type semver struct {
	// pre is the prerelease suffix, empty for a stable release. | pre 是预发布后缀，稳定版为空。
	pre string
	// major, minor and patch are the numeric core. | major、minor、patch 是数字主体。
	major, minor, patch int
}

// parseSemver parses "1.2.3" or "1.2.3-beta.1", optionally prefixed with "v". | parseSemver 解析 "1.2.3" 或 "1.2.3-beta.1"，允许带 "v" 前缀。
func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, false
		}
		nums[i] = n
	}
	return semver{major: nums[0], minor: nums[1], patch: nums[2], pre: pre}, true
}

// compareSemver returns -1, 0 or 1; a prerelease is lower than the same stable core. | compareSemver 返回 -1、0 或 1；同一数字主体的预发布低于稳定版。
func compareSemver(a, b semver) int {
	if a.major != b.major {
		return cmpInt(a.major, b.major)
	}
	if a.minor != b.minor {
		return cmpInt(a.minor, b.minor)
	}
	if a.patch != b.patch {
		return cmpInt(a.patch, b.patch)
	}
	switch {
	case a.pre == b.pre:
		return 0
	case a.pre == "":
		return 1
	case b.pre == "":
		return -1
	default:
		return strings.Compare(a.pre, b.pre)
	}
}

// cmpInt returns -1, 0 or 1 for a against b. | cmpInt 返回 a 相对 b 的 -1、0 或 1。
func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// PlanAssetUpgrades compares every npm-backed asset with its latest upstream release. | PlanAssetUpgrades 把每个 npm 资产与其最新上游发布比较。
//
// Assets that do not come from npm are reported with a reason instead of being guessed at, and each
// npm package is queried once even when several assets share it (the htmx extensions do). | 非 npm
// 资产会带原因报告而不是被猜测；同一个 npm 包即使被多个资产共用也只查询一次（htmx 扩展正是如此）。
func PlanAssetUpgrades(ctx context.Context, client Doer, inv *Inventory) ([]AssetUpgrade, error) {
	if inv == nil {
		return nil, yderrs.Detail(ErrRegistry, "nil inventory | 版本清单为 nil")
	}
	latest := make(map[string]string)
	var errs []error
	plan := make([]AssetUpgrade, 0, len(inv.Assets))
	for _, a := range inv.Assets {
		pkg, ver, ok := ParseNPMURL(a.URL)
		if !ok {
			plan = append(plan, AssetUpgrade{Name: a.Name, From: a.Version, Reason: "not an npm asset | 不是 npm 资产"})
			continue
		}
		up := AssetUpgrade{Name: a.Name, Package: pkg, From: ver}
		key := pkg + "\x00" + ver
		to, seen := latest[key]
		if !seen {
			var err error
			to, err = ResolveLatest(ctx, client, pkg, ver)
			if err != nil {
				errs = append(errs, err)
				up.Reason = "registry query failed | 仓库查询失败"
				plan = append(plan, up)
				latest[key] = ""
				continue
			}
			latest[key] = to
		}
		up.To = to
		if to == "" {
			up.Reason = "registry query failed | 仓库查询失败"
			plan = append(plan, up)
			continue
		}
		if to == ver {
			up.Reason = "up to date | 已是最新"
			plan = append(plan, up)
			continue
		}
		up.Changed = true
		up.NewURL = strings.Replace(a.URL, "@"+ver, "@"+to, 1)
		plan = append(plan, up)
	}
	return plan, errors.Join(errs...)
}

// ApplyAssetUpgrades writes the planned versions and URLs into the manifest. | ApplyAssetUpgrades 把计划中的版本与地址写入清单。
//
// The checksum of a rewritten asset is cleared, because the old digest belongs to the old version and
// assets.Update treats an empty checksum as "not locked yet" and records the new one. Unchanged
// assets keep their checksums, so Update still rejects a moved tag or a tampered mirror for them. | 被
// 改写资产的校验和被清空：旧摘要属于旧版本，而 assets.Update 把空校验和视为「尚未锁定」并记录新的
// 摘要。未变化的资产保留校验和，因此 Update 对它们仍会拒绝被移动的标签或被篡改的镜像。
func ApplyAssetUpgrades(m *assets.Manifest, plan []AssetUpgrade) int {
	if m == nil {
		return 0
	}
	byName := make(map[string]AssetUpgrade, len(plan))
	for _, up := range plan {
		if up.Changed {
			byName[up.Name] = up
		}
	}
	applied := 0
	for i := range m.Assets {
		up, ok := byName[m.Assets[i].Name]
		if !ok {
			continue
		}
		m.Assets[i].Version = up.To
		m.Assets[i].URL = up.NewURL
		m.Assets[i].SHA256 = ""
		m.Assets[i].Size = 0
		applied++
	}
	return applied
}
