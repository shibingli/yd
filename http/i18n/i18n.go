// Package i18n loads multi-language message catalogs from any afero filesystem and negotiates the request language for the router. | Package i18n 从任意 afero 文件系统加载多语言消息目录，并为路由协商请求语言。
// One file per language, named after its tag: `zh-CN.yaml`, `en.toml`, `ja.json`. | 每种语言一份文件，文件名即语言标签：`zh-CN.yaml`、`en.toml`、`ja.json`。
// The file base name is the language tag and `_` is normalized to `-`, so `zh_CN.yaml` and `zh-CN.yaml` are the same language. | 文件名（去扩展名）即语言标签，`_` 归一化为 `-`，故 `zh_CN.yaml` 与 `zh-CN.yaml` 是同一种语言。
// Catalogs are parsed by viper, sharing the parser of the framework configuration center; keys are therefore normalized to lower case and lookups are case-insensitive while message values keep their original spelling. | 目录文件由 viper 解析，与框架配置中心复用同一套解析能力；因此键名统一归一化为小写、查找对大小写不敏感，而消息正文保留原始拼写。
// Wire it once at startup with `b, err := i18n.New(afs.NewOS("configs/langs"), ".", "zh-CN")`, then `r.Use(i18n.Middleware(b, i18n.Options{}))` and `r.SetRenderer(render.New().Funcs(render.FuncMap{"t": i18n.TemplateFunc}))`. | 启动时装配一次即可：`b, err := i18n.New(afs.NewOS("configs/langs"), ".", "zh-CN")`，随后 `r.Use(i18n.Middleware(b, i18n.Options{}))` 与 `r.SetRenderer(render.New().Funcs(render.FuncMap{"t": i18n.TemplateFunc}))`。
//
// Hot path: catalogs are flattened and every message is precompiled once at load/Reload; reads take
// an immutable snapshot through atomic.Pointer and never lock. A message without arguments or
// placeholders is returned verbatim, with zero allocations. | 热路径：目录在加载/热重载时被扁平化，
// 每条消息预编译一次；读取通过 atomic.Pointer 取不可变快照，全程不加锁。无参数、无占位符的消息
// 原样返回，零分配。
package i18n

import (
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/spf13/afero"
	"github.com/spf13/viper"

	"github.com/shibingli/yd/core/errx"
)

// catalogExtensions is the set of file extensions recognized as language catalogs. | catalogExtensions 是被识别为语言文件的文件扩展名集合。
var catalogExtensions = map[string]bool{
	"yaml": true,
	"yml":  true,
	"toml": true,
	"json": true,
}

// Sentinel errors of this package, all testable with errors.Is. | 本包的哨兵错误，均可通过 errors.Is 判定。
var (
	// ErrNilFS reports a nil filesystem passed to New. | ErrNilFS 表示传给 New 的文件系统为 nil。
	ErrNilFS = errx.New("i18n: nil filesystem | i18n：文件系统为 nil")
	// ErrEmptyDefaultLang reports an empty default language. | ErrEmptyDefaultLang 表示默认语言为空。
	ErrEmptyDefaultLang = errx.New("i18n: empty default language | i18n：默认语言不能为空")
	// ErrNoCatalogs reports a directory holding no readable catalog file. | ErrNoCatalogs 表示语言目录下没有任何可读取的语言文件。
	ErrNoCatalogs = errx.New("i18n: no catalog files under the language directory | i18n：语言目录下没有任何语言文件")
	// ErrDefaultLangMissing reports a default language without catalog file. | ErrDefaultLangMissing 表示默认语言缺少对应的语言文件。
	ErrDefaultLangMissing = errx.New("i18n: default language has no catalog file | i18n：默认语言缺少对应的语言文件")
	// ErrReadDir reports a catalog directory that could not be read. | ErrReadDir 表示语言目录无法读取。
	ErrReadDir = errx.New("i18n: failed to read the language directory | i18n：读取语言目录失败")
	// ErrParseCatalog reports a catalog file that could not be parsed. | ErrParseCatalog 表示语言文件无法解析。
	ErrParseCatalog = errx.New("i18n: failed to parse the catalog file | i18n：解析语言文件失败")
	// ErrCatalogNotMap reports a catalog file whose top level is not a key-value map. | ErrCatalogNotMap 表示语言文件的顶层不是键值映射。
	ErrCatalogNotMap = errx.New("i18n: catalog file top level must be a key-value map | i18n：语言文件的顶层必须是键值映射")
)

// ProblemKind classifies a translation problem reported to a Bundle's handler. | ProblemKind 对报告给 Bundle 处理器的翻译问题分类。
type ProblemKind int

const (
	// ProblemMiss reports an unknown key; the key itself is returned. | ProblemMiss 表示未知键；返回键本身。
	ProblemMiss ProblemKind = iota
	// ProblemFormat reports a printf argument mismatch; the raw message is returned. | ProblemFormat 表示 printf 参数不匹配；返回原始消息。
	ProblemFormat
)

// String returns the stable English name of the kind. | String 返回该类型的稳定英文名。
func (k ProblemKind) String() string {
	switch k {
	case ProblemMiss:
		return "miss"
	case ProblemFormat:
		return "format"
	default:
		return "unknown"
	}
}

// Problem is one translation problem handed to the optional handler. | Problem 是交给可选处理器的一个翻译问题。
//
// The handler runs on the translating goroutine, so it must be cheap and concurrency-safe; it is
// meant for sampling or logging, not for retrying. | 处理器在翻译协程上运行，因此必须轻量且并发安全；
// 它用于采样或记日志，不用于重试。
type Problem struct {
	// Lang is the resolved language tag. | Lang 是已解析的语言标签。
	Lang string
	// Key is the requested message key. | Key 是请求的消息键。
	Key string
	// Kind distinguishes a missing key from a formatting failure. | Kind 区分缺失键与格式化失败。
	Kind ProblemKind
}

// problemHandler is the stored form of the optional handler. | problemHandler 是可选处理器的存储形态。
type problemHandler func(Problem)

// bundleSeq hands out the process-local identity used by Translator.CacheKey. | bundleSeq 分配 Translator.CacheKey 使用的进程内身份。
var bundleSeq atomic.Uint64

// snapshot is one immutable catalog generation. | snapshot 是一代不可变的目录快照。
//
// Reload builds a whole new snapshot and publishes it with one atomic store, so a reader sees either
// the old generation or the new one, never a half-updated catalog. | Reload 构建一整代新快照并用
// 一次原子写发布，因此读者看到的要么是旧代、要么是新代，绝不会是半更新的目录。
type snapshot struct {
	catalogs map[string]map[string]any      // Nested catalog per tag, for Catalog(). | 每种标签的嵌套目录，供 Catalog() 使用。
	messages map[string]map[string]*message // Flattened and precompiled messages per tag. | 每种标签的扁平化预编译消息。
	keys     map[string]string              // Stable translator cache key per tag. | 每种标签的稳定译者缓存键。
	langs    []string                       // Ascending tag list. | 升序标签列表。
}

// Bundle holds every loaded catalog and hands out translators for them. | Bundle 持有全部已加载的语言目录，并按语言派发译者。
// Reads are lock-free: Reload swaps an immutable snapshot atomically. | 读取无锁：Reload 原子替换不可变快照。
type Bundle struct {
	fsys        afero.Fs
	snap        atomic.Pointer[snapshot]
	problem     atomic.Pointer[problemHandler]
	dir         string
	defaultLang string
	id          uint64
	misses      atomic.Uint64
	formatErrs  atomic.Uint64
}

// New loads every catalog file under dir as one language. | New 加载 dir 下的所有语言文件，每个文件即一种语言。
// The file base name is the language tag and `_` is normalized to `-`; files without a supported extension and hidden files are skipped, and when several files share one tag the one sorting last by file name wins. | 文件名（去扩展名）即语言标签，`_` 归一化为 `-`；扩展名不受支持的文件与隐藏文件被跳过，多个文件映射到同一标签时按文件名字典序靠后者胜出。
// Misconfiguration is reported as an error wrapping ErrNilFS, ErrEmptyDefaultLang, ErrNoCatalogs or ErrDefaultLangMissing. | 配置错误会返回包装了 ErrNilFS、ErrEmptyDefaultLang、ErrNoCatalogs 或 ErrDefaultLangMissing 的错误。
// A failed read or parse is annotated with ErrReadDir, ErrParseCatalog or ErrCatalogNotMap. | 读取或解析失败会以 ErrReadDir、ErrParseCatalog 或 ErrCatalogNotMap 注解。
func New(fsys afero.Fs, dir string, defaultLang string) (*Bundle, error) {
	if fsys == nil {
		return nil, ErrNilFS
	}
	def := normalizeLang(defaultLang)
	if def == "" {
		return nil, ErrEmptyDefaultLang
	}
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}

	// The catalog directory is a virtual path on the injected filesystem. Slash-separated paths
	// work on every backend (OsFs accepts them on Windows too, MemMapFs normalizes, io/fs requires
	// them), while a backslash produced by filepath.Join is rejected by an embedded backend. |
	// 目录路径是注入文件系统上的虚拟路径。斜杠形式在所有后端都成立（Windows 上的 OsFs 同样接受，
	// MemMapFs 会规范化，io/fs 则要求斜杠），而 filepath.Join 产生的反斜杠会被内嵌后端拒绝。
	dir = filepath.ToSlash(dir)
	b := &Bundle{fsys: fsys, id: bundleSeq.Add(1), dir: dir, defaultLang: def}
	if err := b.Reload(); err != nil {
		return nil, err
	}
	return b, nil
}

// Reload re-reads every catalog file and swaps the in-memory set atomically. | Reload 重新读取全部语言文件，并原子替换内存中的目录集合。
// On failure the previously loaded catalogs stay untouched, so a broken file never takes a running service down. | 失败时此前已加载的目录保持原样，因此损坏的文件不会拖垮正在运行的服务。
func (b *Bundle) Reload() error {
	catalogs, langs, err := b.load()
	if err != nil {
		return err
	}
	if _, ok := catalogs[b.defaultLang]; !ok {
		return errx.Detail(ErrDefaultLangMissing, "language %s | 语言 %s", b.defaultLang, b.defaultLang)
	}
	b.snap.Store(buildSnapshot(catalogs, langs, b.id))
	return nil
}

// load reads the catalog directory into a fresh tag -> catalog map. | load 读取语言目录，构建一份全新的「标签 -> 目录」映射。
func (b *Bundle) load() (map[string]map[string]any, []string, error) {
	entries, err := afero.ReadDir(b.fsys, b.dir)
	if err != nil {
		return nil, nil, errx.WrapDetail(err, ErrReadDir, "dir %s | 目录 %s", b.dir, b.dir)
	}

	catalogs := make(map[string]map[string]any, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(entry.Name()), "."))
		if !catalogExtensions[ext] {
			continue
		}
		tag := normalizeLang(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		if tag == "" {
			continue
		}

		file := path.Join(b.dir, entry.Name())
		catalog, err := parseCatalog(b.fsys, file)
		if err != nil {
			return nil, nil, errx.WrapDetail(err, ErrParseCatalog, "file %s | 文件 %s", file, file)
		}
		catalogs[tag] = catalog
	}

	if len(catalogs) == 0 {
		return nil, nil, errx.Detail(ErrNoCatalogs, "dir %s | 目录 %s", b.dir, b.dir)
	}

	langs := make([]string, 0, len(catalogs))
	for tag := range catalogs {
		langs = append(langs, tag)
	}
	sort.Strings(langs)
	return catalogs, langs, nil
}

// buildSnapshot flattens and precompiles every catalog once. | buildSnapshot 把每个目录扁平化并预编译一次。
func buildSnapshot(catalogs map[string]map[string]any, langs []string, id uint64) *snapshot {
	prefix := "b" + strconv.FormatUint(id, 10) + ":"
	s := &snapshot{
		catalogs: catalogs,
		messages: make(map[string]map[string]*message, len(catalogs)),
		keys:     make(map[string]string, len(catalogs)),
		langs:    langs,
	}
	for tag, catalog := range catalogs {
		flat := make(map[string]*message, len(catalog))
		flattenCatalog(catalog, "", flat)
		s.messages[tag] = flat
		s.keys[tag] = prefix + tag
	}
	return s
}

// flattenCatalog walks a nested catalog and records every scalar leaf under its dotted key. | flattenCatalog 遍历嵌套目录，把每个标量叶子记录到点分键下。
//
// Maps and slices are not messages (matching the old drill), empty keys are skipped so a lookup can
// never match an empty segment. | 映射与切片不是消息（与原下钻一致），空键被跳过，使查找永远不会命中空段。
func flattenCatalog(node map[string]any, prefix string, out map[string]*message) {
	for key, value := range node {
		if key == "" {
			continue
		}
		full := key
		if prefix != "" {
			full = prefix + "." + key
		}
		switch val := value.(type) {
		case map[string]any:
			flattenCatalog(val, full, out)
		case []any:
			// Arrays are not messages. | 数组不是消息。
		default:
			if text, ok := scalarString(val); ok {
				out[strings.ToLower(full)] = compileMessage(text)
			}
		}
	}
}

// parseCatalog decodes one catalog file with viper and lower-cases every key. | parseCatalog 用 viper 解码单个语言文件，并把所有键名转为小写。
func parseCatalog(fsys afero.Fs, file string) (map[string]any, error) {
	v := viper.New()
	v.SetFs(fsys)
	v.SetConfigFile(file)
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}
	catalog, ok := normalizeValue(v.AllSettings()).(map[string]any)
	if !ok {
		return nil, errx.Detail(ErrCatalogNotMap, "file %s | 文件 %s", file, file)
	}
	return catalog, nil
}

// normalizeValue lower-cases map keys recursively so lookups stay predictable. | normalizeValue 递归地把映射键名转为小写，使查找结果可预期。
func normalizeValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			out[strings.ToLower(k)] = normalizeValue(item)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = normalizeValue(item)
		}
		return out
	default:
		return v
	}
}

// Langs returns every loaded language tag in ascending order. | Langs 返回全部已加载的语言标签（升序）。
func (b *Bundle) Langs() []string {
	snap := b.snap.Load()
	if snap == nil {
		return nil
	}
	out := make([]string, len(snap.langs))
	copy(out, snap.langs)
	return out
}

// DefaultLang returns the default fallback language tag. | DefaultLang 返回默认兜底语言标签。
func (b *Bundle) DefaultLang() string { return b.defaultLang }

// Misses returns the number of translations that missed every catalog. | Misses 返回所有目录都未命中的翻译次数。
func (b *Bundle) Misses() uint64 { return b.misses.Load() }

// FormatErrors returns the number of translations whose printf arguments did not match. | FormatErrors 返回 printf 参数不匹配的翻译次数。
func (b *Bundle) FormatErrors() uint64 { return b.formatErrs.Load() }

// SetProblemHandler installs (or clears, with nil) the optional problem handler. | SetProblemHandler 安装（传 nil 则清除）可选的问题处理器。
// Set it once during wiring; the handler is read atomically on the translating goroutine. | 在装配期设置一次；处理器在翻译协程上被原子读取。
func (b *Bundle) SetProblemHandler(fn func(Problem)) {
	if fn == nil {
		b.problem.Store(nil)
		return
	}
	h := problemHandler(fn)
	b.problem.Store(&h)
}

// report hands one problem to the optional handler. | report 把一个问题上交给可选处理器。
func (b *Bundle) report(p Problem) {
	if h := b.problem.Load(); h != nil {
		(*h)(p)
	}
}

// Translator returns a translator bound to lang. | Translator 返回绑定到 lang 的译者。
// The tag goes through the same matching as MatchLanguage, so `zh` resolves to a loaded `zh-CN` and an unknown or empty tag resolves to the default language. | 标签沿用与 MatchLanguage 相同的匹配规则：`zh` 会解析到已加载的 `zh-CN`，未知或空标签则解析为默认语言。
func (b *Bundle) Translator(lang string) *Translator {
	return &Translator{bundle: b, lang: b.resolve(lang)}
}

// Catalog returns a deep copy of the catalog for lang, ready to be injected into the frontend as JSON. | Catalog 返回 lang 的目录深拷贝，可直接序列化为 JSON 注入前端。
// Unknown languages fall back to the default catalog, and a missing default catalog yields nil; mutating the copy never affects the bundle. | 未知语言回退默认目录，默认目录缺失时返回 nil；修改副本不会影响 Bundle。
func (b *Bundle) Catalog(lang string) map[string]any {
	snap := b.snap.Load()
	if snap == nil {
		return nil
	}
	catalog := snap.catalogs[resolveIn(snap, lang, b.defaultLang)]
	if catalog == nil {
		return nil
	}
	out, _ := deepCopy(catalog).(map[string]any)
	return out
}

// MatchLanguage picks the best loaded language for an Accept-Language style value. | MatchLanguage 为 Accept-Language 风格取值挑选最合适的已加载语言。
// Entries are sorted by q value (descending, client order wins ties), then matched exactly, then by dropping trailing subtags (`zh-Hans-CN` -> `zh-Hans` -> `zh`), then by primary subtag (`zh` hits `zh-CN`). | 先按 q 值降序排序（同值保持客户端顺序），随后依次尝试精确匹配、逐级丢弃尾部子标签（`zh-Hans-CN` -> `zh-Hans` -> `zh`），最后按主语言子标签匹配（`zh` 命中 `zh-CN`）。
// `*`, an exhausted candidate list and an empty input all resolve to the default language. | `*`、候选耗尽与空输入都解析为默认语言。
func (b *Bundle) MatchLanguage(accept string) string {
	snap := b.snap.Load()
	if snap == nil {
		return b.defaultLang
	}
	accept = strings.TrimSpace(accept)
	if accept == "" {
		return b.defaultLang
	}
	// Fast path: a single plain tag (the overwhelmingly common header) needs no parsing at all.
	// | 快路径：单个普通标签（最常见的头部形态）完全不需要解析。
	if !strings.ContainsAny(accept, ",;*") {
		if tag, ok := matchTag(accept, snap.langs); ok {
			return tag
		}
		return b.defaultLang
	}
	for _, pref := range parseAcceptLanguage(accept) {
		if pref.tag == "*" {
			return b.defaultLang
		}
		if tag, ok := matchTag(pref.tag, snap.langs); ok {
			return tag
		}
	}
	return b.defaultLang
}

// cacheKey returns the precomputed translator identity of a resolved tag. | cacheKey 返回已解析标签的预计算译者身份。
func (b *Bundle) cacheKey(lang string) string {
	snap := b.snap.Load()
	if snap == nil {
		return ""
	}
	if key := snap.keys[lang]; key != "" {
		return key
	}
	return snap.keys[b.defaultLang]
}

// resolve maps a requested tag to a loaded tag against the current snapshot. | resolve 在当前快照上把请求标签映射为已加载的标签。
func (b *Bundle) resolve(lang string) string {
	snap := b.snap.Load()
	if snap == nil {
		return b.defaultLang
	}
	return resolveIn(snap, lang, b.defaultLang)
}

// resolveIn is the snapshot-local core of resolve. | resolveIn 是 resolve 的快照内内核。
func resolveIn(snap *snapshot, lang, def string) string {
	if tag, ok := matchTag(lang, snap.langs); ok {
		return tag
	}
	return def
}

// lookupMessage resolves key and returns the precompiled message plus the language that supplied it.
// | lookupMessage 解析 key，返回预编译消息以及提供它的语言。
func (b *Bundle) lookupMessage(lang, key string) (*message, string, bool) {
	snap := b.snap.Load()
	if snap == nil {
		return nil, "", false
	}
	key = normalizeKey(key)
	if key == "" {
		return nil, "", false
	}
	resolved := resolveIn(snap, lang, b.defaultLang)
	if msg := snap.messages[resolved][key]; msg != nil {
		return msg, resolved, true
	}
	if resolved != b.defaultLang {
		if msg := snap.messages[b.defaultLang][key]; msg != nil {
			return msg, b.defaultLang, true
		}
	}
	return nil, "", false
}

// normalizeKey lower-cases and trims every dotted segment, matching the old drill semantics. | normalizeKey 对每个点分段做去空白与小写，与旧的下钻语义一致。
//
// The fast path returns the input untouched when it is already lower-case and contains no spaces,
// which is true for every key written in source templates. | 当输入已是小写且不含空格（源码模板中的每个键都如此）
// 时，快路径原样返回。
func normalizeKey(key string) string {
	simple := true
	for i := 0; i < len(key); i++ {
		c := key[i]
		if (c >= 'A' && c <= 'Z') || c == ' ' || c == '\t' {
			simple = false
			break
		}
	}
	if simple {
		return key
	}
	parts := strings.Split(key, ".")
	for i, part := range parts {
		parts[i] = strings.ToLower(strings.TrimSpace(part))
	}
	return strings.Join(parts, ".")
}

// scalarString renders a scalar leaf as a string. | scalarString 把标量叶子渲染为字符串。
func scalarString(v any) (string, bool) {
	switch val := v.(type) {
	case string:
		return val, true
	case bool:
		return strconv.FormatBool(val), true
	case int:
		return strconv.Itoa(val), true
	case int64:
		return strconv.FormatInt(val, 10), true
	case uint64:
		return strconv.FormatUint(val, 10), true
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64), true
	case float32:
		return strconv.FormatFloat(float64(val), 'f', -1, 32), true
	default:
		return "", false
	}
}

// deepCopy clones nested maps and slices so callers can mutate their own copy. | deepCopy 递归复制嵌套映射与切片，使调用方可以安全修改自己的副本。
func deepCopy(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			out[k] = deepCopy(item)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = deepCopy(item)
		}
		return out
	default:
		return v
	}
}

// normalizeLang canonicalizes a tag: trimmed, `_` turned into `-`. | normalizeLang 归一化语言标签：去空白，并把 `_` 换成 `-`。
func normalizeLang(tag string) string {
	return strings.ReplaceAll(strings.TrimSpace(tag), "_", "-")
}

// matchTag matches one candidate tag against the loaded tags. | matchTag 把单个候选标签与已加载的标签做匹配。
func matchTag(tag string, langs []string) (string, bool) {
	norm := normalizeLang(tag)
	if norm == "" || len(langs) == 0 {
		return "", false
	}

	if hit, ok := findTag(norm, langs); ok {
		return hit, true
	}
	// Drop trailing subtags one by one: zh-Hans-CN -> zh-Hans -> zh. | 逐级丢弃尾部子标签：zh-Hans-CN -> zh-Hans -> zh。
	for {
		cut := strings.LastIndexByte(norm, '-')
		if cut <= 0 {
			break
		}
		norm = norm[:cut]
		if hit, ok := findTag(norm, langs); ok {
			return hit, true
		}
	}
	// Finally let a bare primary subtag hit any region of the same language. | 最后让裸主语言子标签命中同语言的任意地区变体。
	primary := primarySubtag(tag)
	for _, lang := range langs {
		if primary != "" && primarySubtag(lang) == primary {
			return lang, true
		}
	}
	return "", false
}

// findTag looks up one tag case-insensitively. | findTag 以大小写不敏感的方式查找单个标签。
func findTag(tag string, langs []string) (string, bool) {
	for _, lang := range langs {
		if strings.EqualFold(lang, tag) {
			return lang, true
		}
	}
	return "", false
}

// primarySubtag returns the lower-cased part before the first dash. | primarySubtag 返回首个连字符之前的主语言子标签（小写）。
func primarySubtag(tag string) string {
	norm := strings.ToLower(normalizeLang(tag))
	if cut := strings.IndexByte(norm, '-'); cut >= 0 {
		return norm[:cut]
	}
	return norm
}

// langPref is one parsed Accept-Language entry. | langPref 是一条解析后的 Accept-Language 条目。
type langPref struct {
	tag string  // Language tag or `*`. | 语言标签或 `*`。
	q   float64 // Quality value in (0, 1]. | 取值于 (0, 1] 的 q 值。
}

// parseAcceptLanguage parses an Accept-Language value into q-ordered preferences. | parseAcceptLanguage 把 Accept-Language 取值解析为按 q 值排序的偏好列表。
// Entries with q=0 are not acceptable and are dropped; a malformed q falls back to 1.0; ties keep the client order thanks to the stable sort. | q=0 表示不可接受、直接丢弃；q 解析失败时回退 1.0；稳定排序保证同 q 值沿用客户端顺序。
func parseAcceptLanguage(accept string) []langPref {
	parts := strings.Split(accept, ",")
	prefs := make([]langPref, 0, len(parts))

	for _, raw := range parts {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		tag := raw
		q := 1.0
		if cut := strings.IndexByte(raw, ';'); cut >= 0 {
			tag = strings.TrimSpace(raw[:cut])
			for _, param := range strings.Split(raw[cut+1:], ";") {
				value, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(param)), "q=")
				if !ok {
					continue
				}
				if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
					q = parsed
				}
			}
		}
		if tag == "" || q <= 0 {
			continue
		}
		prefs = append(prefs, langPref{tag: tag, q: q})
	}

	sort.SliceStable(prefs, func(i, j int) bool { return prefs[i].q > prefs[j].q })
	return prefs
}
