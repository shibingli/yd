// This file implements the online updater: the download-with-verification pipeline, the mirror source
// chain, the offline tripwire and the manifest locking that follows a successful write. | 本文件实现在线
// 更新器：下载并校验的流水线、镜像源链、离线拦截器，以及写入成功后对清单的锁定。

package assets

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/afero"
	"github.com/yun-dev/yd/afs"
	"github.com/yun-dev/yd/errx"
)

// defaultHTTPTimeout bounds a single asset download. | defaultHTTPTimeout 限制单次资产下载的耗时。
const defaultHTTPTimeout = 30 * time.Second

// UpdateOptions configures one Update or Add call. | UpdateOptions 配置一次 Update 或 Add 调用。
type UpdateOptions struct {
	// FS holds both the manifest and the assets; nil means the OS file system. | FS 承载清单与资产；为空表示操作系统文件系统。
	FS afero.Fs
	// ManifestPath is the lock file location; empty means DefaultManifestPath. | ManifestPath 是锁文件位置；为空表示 DefaultManifestPath。
	ManifestPath string
	// Root is the directory asset paths resolve against; empty means DefaultRoot. | Root 是资产路径解析所依据的目录；为空表示 DefaultRoot。
	Root string
	// Offline forbids every network access: only local files are inspected and nothing is written. | Offline 禁止一切网络访问：只检查本地文件，且不写入任何内容。
	Offline bool
	// CheckOnly reports what would change without writing a file or the manifest. | CheckOnly 只报告将要发生的变化，不写文件也不写清单。
	CheckOnly bool
	// Force re-locks an asset whose downloaded content no longer matches the recorded checksum. | Force 对下载内容与记录校验和不符的资产重新锁定。
	Force bool
	// Sources is the mirror chain for this call; empty falls back to Manifest.Sources, then DefaultSources. | Sources 是本次调用的镜像链；为空时回退到 Manifest.Sources，再回退到 DefaultSources。
	Sources []string
	// HTTPClient performs the downloads; a client is required online, and offline mode hands out a
	// tripwire when the caller supplies none. | HTTPClient 执行下载；在线模式需要客户端，离线模式下调用方
	// 未提供客户端时会装入一个拦截器。
	HTTPClient *http.Client
	// Concurrency is the download fan-out; 0 means DefaultConcurrency. | Concurrency 是下载并发度；0 表示 DefaultConcurrency。
	Concurrency int
}

// UpdateReport summarizes one Update run. | UpdateReport 概述一次 Update 的结果。
//
// Updated/Unchanged/Failed hold asset names, each list in manifest order, so the same lock file always
// yields the same report. | Updated/Unchanged/Failed 保存资产名，各列表均按清单顺序排列，因此同一份
// 锁文件总是给出同一份报告。
type UpdateReport struct {
	// Updated lists assets that were (or, with CheckOnly, would be) written. | Updated 列出已写入（CheckOnly 下为将要写入）的资产。
	Updated []string
	// Unchanged lists assets whose local file already matches the lock. | Unchanged 列出本地文件已与锁定一致的资产。
	Unchanged []string
	// Failed lists assets that could not be updated or verified. | Failed 列出无法更新或校验失败的资产。
	Failed []string
	// Checked is the number of assets inspected. | Checked 是检查过的资产数。
	Checked int
	// Offline echoes UpdateOptions.Offline. | Offline 回显 UpdateOptions.Offline。
	Offline bool
}

// decision is the per-asset outcome before it is folded into an UpdateReport. | decision 是折叠进 UpdateReport 之前的单个资产结果。
type decision int

const (
	// stateUnchanged means the local file already matches the lock. | stateUnchanged 表示本地文件已与锁定一致。
	stateUnchanged decision = iota
	// stateUpdated means the asset was written and re-locked. | stateUpdated 表示资产已写入并重新锁定。
	stateUpdated
	// stateFailed means the asset was skipped and an error recorded. | stateFailed 表示该资产被跳过并记录了错误。
	stateFailed
)

// assetResult is one worker's outcome, kept in a per-index slot so no goroutine ever shares a value. | assetResult 是单个 worker 的结果，按索引独占存放，任何 goroutine 都不共享同一份数据。
type assetResult struct {
	// name is the asset name reported back to the caller. | name 是回报给调用方的资产名。
	name string
	// state is the outcome. | state 是结果状态。
	state decision
	// err is set only for stateFailed. | err 仅在 stateFailed 时非空。
	err error
	// sha256 and size are the values to write back into the lock. | sha256 与 size 是要回写进锁定的取值。
	sha256 string
	// size is the locked byte length. | size 是锁定的字节长度。
	size int64
}

// normalized returns o with every default filled in. | normalized 返回填好全部默认值的 o。
//
// Offline mode never keeps a caller-supplied client: the tripwire replaces it, so the offline branch is
// the only thing that decides whether a request is built, and the tripwire is the only thing that could
// ever answer one. | 离线模式绝不保留调用方传入的客户端：拦截器会取而代之，因此是否构造请求只由离线
// 分支决定，而唯一可能应答请求的只有拦截器。
func (o UpdateOptions) normalized() UpdateOptions {
	if o.FS == nil {
		o.FS = afero.NewOsFs()
	}
	if o.ManifestPath == "" {
		o.ManifestPath = DefaultManifestPath
	}
	if o.Root == "" {
		o.Root = DefaultRoot
	}
	if o.Concurrency <= 0 {
		o.Concurrency = DefaultConcurrency
	}
	if o.Offline {
		// Offline mode always installs the tripwire, even over a caller-supplied client: a bug in the
		// offline branch may then waste a counter tick, but it can never reach the network. | 离线模式
		// 始终装入拦截器，即使调用方传入了客户端：这样离线分支的缺陷最多浪费一次计数，绝不可能触网。
		o.HTTPClient = newOfflineClient()
	} else if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return o
}

// templates returns the mirror chain to use: the per-call sources, else the manifest's, else the
// built-in defaults. | templates 返回要使用的镜像链：本次调用的源、否则清单里的源、最后是内置默认值。
func (o UpdateOptions) templates(m *Manifest) []string {
	if len(o.Sources) > 0 {
		return o.Sources
	}
	if m != nil && len(m.Sources) > 0 {
		return m.Sources
	}
	return DefaultSources
}

// Update brings every manifest asset in line with its lock, downloading through the mirror chain. | Update 使清单中的每项资产与其锁定保持一致，下载走镜像链。
//
// Semantics per asset | 单项资产的语义：
//
//   - local file missing or stale, but locked: the locked content is restored and the checksum is
//     re-checked before anything is written; | 本地文件缺失或过期但已锁定：先恢复被锁定的内容，写入前
//     重新核对校验和；
//   - locked and the download does not match: ErrChecksumMismatch, no write, no lock change, unless
//     Force is set; | 已锁定但下载内容不匹配：返回 ErrChecksumMismatch，不写盘、不改锁，除非设置 Force；
//   - unlocked: this is the first lock, so the checksum and size are backfilled. | 未锁定：这是首次锁定，
//     回填校验和与体积。
//
// A failing asset never stops the others: the successful ones stay updated and every failure is
// returned in one aggregated error (errors.Join), so the caller can report them all at once. | 单个资产
// 失败不会中断其它资产：成功的保持更新，全部失败项用一个聚合错误（errors.Join）返回，便于调用方
// 一次性报告。
func Update(ctx context.Context, o UpdateOptions) (*UpdateReport, error) {
	o = o.normalized()
	if err := ctx.Err(); err != nil {
		return nil, errx.Wrap(err, ErrDownloadFailed)
	}

	m, err := LoadManifest(o.FS, o.ManifestPath)
	if err != nil {
		return nil, err
	}

	sources := o.templates(m)
	rep := &UpdateReport{Offline: o.Offline, Checked: len(m.Assets)}

	// Record the mirror chain in the lock file so the next run does not have to guess it. | 把镜像链
	// 记录进锁文件，使后续运行无需再猜。
	dirty := false
	if !o.CheckOnly && len(m.Sources) == 0 && len(sources) > 0 {
		m.Sources = sources
		dirty = true
	}

	results := make([]assetResult, len(m.Assets))
	writeMu := &sync.Mutex{}
	sem := make(chan struct{}, o.Concurrency)
	var wg sync.WaitGroup
	for i := range m.Assets {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = o.apply(ctx, m.Assets[i], sources, writeMu)
		}(i)
	}
	wg.Wait()

	var errs []error
	for i := range results {
		switch results[i].state {
		case stateUpdated:
			rep.Updated = append(rep.Updated, results[i].name)
			m.Assets[i].SHA256 = results[i].sha256
			m.Assets[i].Size = results[i].size
		case stateFailed:
			rep.Failed = append(rep.Failed, results[i].name)
			errs = append(errs, results[i].err)
		default:
			rep.Unchanged = append(rep.Unchanged, results[i].name)
		}
	}

	if !o.CheckOnly && (dirty || len(rep.Updated) > 0) {
		if err := m.Save(o.FS, o.ManifestPath); err != nil {
			errs = append(errs, err)
		}
	}
	return rep, errors.Join(errs...)
}

// apply decides what one asset needs and, unless CheckOnly is set, performs the write. | apply 决定单个资产的处理方式，并在未设置 CheckOnly 时执行写入。
//
// writeMu serializes the file system writes: the downloads run concurrently, the disk writes do not,
// which keeps afero backends (including the in-memory one) free of concurrent directory creation. | writeMu
// 串行化文件系统写入：下载并发执行，写盘不并发，从而使 afero 后端（含内存后端）不会并发建目录。
func (o UpdateOptions) apply(ctx context.Context, a Asset, sources []string, writeMu *sync.Mutex) assetResult {
	res := assetResult{name: a.Name}
	local := a.LocalPath(o.Root)
	existing, exists := readLocal(o.FS, local)

	// Offline: local inspection only, no request is ever built. | 离线：只做本地检查，绝不构造请求。
	if o.Offline {
		switch {
		case !exists:
			res.state, res.err = stateFailed, errx.Detail(ErrOfflineMissing, "%s", local)
		case a.SHA256 == "":
			res.state, res.err = stateFailed, errx.Detail(ErrChecksumMismatch, "%s: 未锁定，离线模式无法核对", a.Name)
		case sha256Hex(existing) != a.SHA256:
			res.state, res.err = stateFailed, errx.Detail(ErrChecksumMismatch, "%s", local)
		default:
			res.state = stateUnchanged
		}
		return res
	}

	candidates := candidatesFor(a, sources)
	if len(candidates) == 0 {
		res.state, res.err = stateFailed, errx.Detail(ErrDownloadFailed, "%s: 没有可用的下载源", a.Name)
		return res
	}

	// Every online run re-downloads on purpose: comparing the upstream bytes against the lock is what
	// detects a moved tag, a tampered mirror or a stale local copy, and `--offline` is the mode that
	// skips the network. | 每次在线运行都刻意重新下载：把上游字节与锁定值比对，正是发现标签被移动、
	// 镜像被篡改或本地副本过期的唯一手段；跳过网络是 `--offline` 模式的职责。
	data, err := fetch(ctx, o.HTTPClient, candidates)
	if err != nil {
		res.state, res.err = stateFailed, err
		return res
	}
	sum := sha256Hex(data)

	// The lock is authoritative: a mismatch means the upstream moved, which is a failure unless the
	// caller explicitly asked for a re-lock. | 锁定是权威：不匹配说明上游已变动，除非调用方明确要求
	// 重新锁定，否则一律视为失败。
	if a.SHA256 != "" && sum != a.SHA256 && !o.Force {
		res.state, res.err = stateFailed, errx.Detail(ErrChecksumMismatch, "%s: 锁定 %s，下载 %s", a.Name, a.SHA256, sum)
		return res
	}

	// The lock itself is a change worth recording, so an unlocked asset counts as updated even when the
	// bytes on disk happen to be identical already. | 锁定本身就是要记录的变更，因此即使磁盘上的字节
	// 恰好一致，未锁定的资产也算作已更新。
	needsWrite := a.SHA256 == "" || !exists || sha256Hex(existing) != sum
	if !needsWrite {
		res.state = stateUnchanged
		return res
	}

	res.state, res.sha256, res.size = stateUpdated, sum, int64(len(data))
	if o.CheckOnly {
		return res
	}

	writeMu.Lock()
	_, err = afs.WriteFileIfChanged(o.FS, local, data)
	writeMu.Unlock()
	if err != nil {
		res.state, res.err, res.sha256, res.size = stateFailed, err, "", 0
		return res
	}
	return res
}

// Add downloads a, locks its checksum and appends it to the manifest, creating the manifest when it
// does not exist yet. | Add 下载 a、锁定其校验和并追加到清单，清单不存在时自动创建。
//
// Add always needs the network, so offline mode rejects it instead of silently writing an unlocked
// entry. | Add 始终需要网络，因此离线模式会直接拒绝，而不是静默写入一条未锁定的条目。
func Add(ctx context.Context, o UpdateOptions, a Asset) (Asset, error) {
	o = o.normalized()
	if strings.TrimSpace(a.Name) == "" {
		return Asset{}, errx.Detail(ErrInvalidAsset, "缺少 name")
	}
	path, err := NormalizePath(a.Path)
	if err != nil {
		return Asset{}, err
	}
	a.Path = path
	if a.Kind == "" {
		a.Kind = KindFromPath(a.Path)
	}
	if o.Offline {
		return Asset{}, errx.Detail(ErrOffline, "yd assets add %s", a.Name)
	}
	if err := ctx.Err(); err != nil {
		return Asset{}, errx.Wrap(err, ErrDownloadFailed)
	}

	m, err := LoadManifest(o.FS, o.ManifestPath)
	if err != nil {
		if !errors.Is(err, ErrManifestNotFound) {
			return Asset{}, err
		}
		m = &Manifest{Version: CurrentManifestVersion}
	}

	sources := o.templates(m)
	if len(m.Sources) == 0 {
		m.Sources = sources
	}

	candidates := candidatesFor(a, sources)
	if len(candidates) == 0 {
		return Asset{}, errx.Detail(ErrDownloadFailed, "%s: 没有可用的下载源", a.Name)
	}
	data, err := fetch(ctx, o.HTTPClient, candidates)
	if err != nil {
		return Asset{}, err
	}

	a.SHA256 = sha256Hex(data)
	a.Size = int64(len(data))
	if _, err := afs.WriteFileIfChanged(o.FS, a.LocalPath(o.Root), data); err != nil {
		return Asset{}, err
	}
	if err := m.Add(a); err != nil {
		return Asset{}, err
	}
	if err := m.Save(o.FS, o.ManifestPath); err != nil {
		return Asset{}, err
	}
	return a, nil
}

// candidatesFor renders the mirror chain for a and appends the asset's own URL as the last resort. | candidatesFor 为 a 渲染镜像链，并把资产自身 URL 作为最后兜底追加在末尾。
//
// Duplicates are dropped while the order is preserved, so a source that repeats the canonical URL does
// not cause a second request. | 去重的同时保持顺序，因此与权威 URL 重复的源不会引发第二次请求。
func candidatesFor(a Asset, sources []string) []string {
	seen := make(map[string]struct{}, len(sources)+1)
	out := make([]string, 0, len(sources)+1)
	add := func(u string) {
		if u == "" {
			return
		}
		if _, dup := seen[u]; dup {
			return
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	for _, tmpl := range sources {
		if u, ok := renderSource(tmpl, a); ok {
			add(u)
		}
	}
	add(a.URL)
	return out
}

// renderSource expands one source template for a, reporting false when the template needs a value that
// cannot be derived. | renderSource 为 a 展开单个源模板，模板需要无法推导的取值时返回 false。
//
// Placeholders | 占位符：{pkg} {ver} {file} {name} {url} {path}.
//   - {pkg}/{ver}/{file} come from the canonical URL, so a mirror can be derived from it; | {pkg}/{ver}/{file}
//     取自权威 URL，因此可以从它推导出镜像地址；
//   - {ver} falls back to Asset.Version when the URL carries no version; | URL 不含版本时 {ver} 回退到
//     Asset.Version；
//   - a template without placeholders is used verbatim, which is how a one-off host is pinned. | 不含占位符
//     的模板原样使用，这就是固定某个一次性主机的方式。
func renderSource(tmpl string, a Asset) (string, bool) {
	if strings.TrimSpace(tmpl) == "" {
		return "", false
	}
	pkg, ver, file := packageParts(a.URL)
	if ver == "" {
		ver = a.Version
	}
	values := map[string]string{
		"{name}": a.Name,
		"{url}":  a.URL,
		"{path}": a.Path,
		"{pkg}":  pkg,
		"{ver}":  ver,
		"{file}": file,
	}
	out := tmpl
	for ph, v := range values {
		if !strings.Contains(out, ph) {
			continue
		}
		if v == "" {
			return "", false
		}
		out = strings.ReplaceAll(out, ph, v)
	}
	if out == "" {
		return "", false
	}
	return out, true
}

// packageParts splits a package URL into its package name, version and file path. | packageParts 把包地址拆分为包名、版本与文件路径。
//
// The layout of both jsDelivr and unpkg is `/{npm}/{pkg}@{ver}/{file}`, where {pkg} may be scoped
// (`@scope/name`); the leading "npm" segment is dropped and the version is only recognized on a
// segment that carries an "@" past its first character, which is what keeps a scope's "@" from being
// mistaken for a version. | jsDelivr 与 unpkg 的布局都是 `/{npm}/{pkg}@{ver}/{file}`，其中 {pkg}
// 允许带作用域（`@scope/name`）；前导的 "npm" 段会被丢弃，且只有在首字符之后仍含 "@" 的段上才识别
// 版本，从而避免把作用域的 "@" 误判为版本。
func packageParts(rawURL string) (pkg, ver, file string) {
	if rawURL == "" {
		return "", "", ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", ""
	}
	segs := splitPathSegments(u.Path)
	if len(segs) > 0 && segs[0] == "npm" {
		segs = segs[1:]
	}
	if len(segs) == 0 {
		return "", "", ""
	}
	for i, s := range segs {
		at := strings.LastIndex(s, "@")
		if at <= 0 {
			continue
		}
		switch {
		case i == 0:
			return s[:at], s[at+1:], strings.Join(segs[1:], "/")
		case i == 1 && strings.HasPrefix(segs[0], "@"):
			return segs[0] + "/" + s[:at], s[at+1:], strings.Join(segs[2:], "/")
		default:
			// The "@" belongs to a plain path segment, not to a package version. | 该 "@" 属于普通路径段，
			// 不是包版本。
			return "", "", strings.Join(segs, "/")
		}
	}
	return "", "", strings.Join(segs, "/")
}

// splitPathSegments splits a URL path on "/" and drops empty segments. | splitPathSegments 按 "/" 切分 URL 路径并丢弃空段。
func splitPathSegments(p string) []string {
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// fetch downloads the first candidate that answers with a success status, in order. | fetch 按顺序下载第一个返回成功状态的候选地址。
func fetch(ctx context.Context, client *http.Client, candidates []string) ([]byte, error) {
	errs := make([]error, 0, len(candidates))
	for _, u := range candidates {
		data, err := fetchOne(ctx, client, u)
		if err == nil {
			return data, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// fetchOne performs one download and returns the body, which is held in memory only. | fetchOne 执行一次下载并返回内容，内容只存在于内存中。
func fetchOne(ctx context.Context, client *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrDownloadFailed, "%s", u)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrDownloadFailed, "%s", u)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, errx.Detail(ErrDownloadFailed, "%s: HTTP %d", u, resp.StatusCode)
	}
	data, err := readLimited(resp.Body, maxAssetSize)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrDownloadFailed, "%s", u)
	}
	return data, nil
}

// readLimited reads at most limit bytes and rejects anything larger. | readLimited 最多读取 limit 字节，超出即拒绝。
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if n > limit {
		return nil, errx.Detail(ErrAssetTooLarge, "%d > %d", n, limit)
	}
	return buf.Bytes(), nil
}

// readLocal reads a local file, reporting whether it exists at all. | readLocal 读取本地文件，并报告其是否存在。
func readLocal(fsys afero.Fs, path string) ([]byte, bool) {
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// OfflineTransport is the tripwire installed by offline mode: it fails every request with ErrOffline
// and never delegates to another transport, so a bug in the offline branch can waste a counter tick
// but can never open a connection. | OfflineTransport 是离线模式装入的拦截器：它对每个请求都以
// ErrOffline 失败，且绝不转发给其它传输层，因此即便离线分支出现缺陷，最多浪费一次计数，也绝不可能
// 建立连接。
type OfflineTransport struct {
	// calls counts the requests that reached the tripwire, which must stay 0 in a correct offline run. | calls 统计到达拦截器的请求数，正确的离线运行中必须保持为 0。
	calls atomic.Int64
}

// RoundTrip implements http.RoundTripper by refusing the request. | RoundTrip 通过拒绝请求实现 http.RoundTripper。
func (t *OfflineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	target := ""
	if req != nil && req.URL != nil {
		target = req.URL.Redacted()
	}
	return nil, errx.Detail(ErrOffline, "%s", target)
}

// Calls reports how many requests reached the tripwire; it must stay 0 offline. | Calls 报告到达拦截器的请求数；离线时必须保持为 0。
func (t *OfflineTransport) Calls() int64 { return t.calls.Load() }

// newOfflineClient builds the client used by offline mode. It is a variable so the test suite can
// capture the very transport that was injected and assert it was never called, which is the strongest
// available statement of "offline never touches the network". | newOfflineClient 构造离线模式使用的
// 客户端。它被声明为变量，便于测试捕获被注入的那个传输层并断言其从未被调用——这是「离线绝不触网」
// 能给出的最强陈述。
var newOfflineClient = NewOfflineClient

// NewOfflineClient returns a client whose transport is the offline tripwire. | NewOfflineClient 返回传输层为离线拦截器的客户端。
func NewOfflineClient() *http.Client {
	return &http.Client{Transport: &OfflineTransport{}, Timeout: defaultHTTPTimeout}
}
