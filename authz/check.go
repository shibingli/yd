package authz

import (
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/afs"
	"github.com/shibingli/yd/errx"
)

// This file is the executable half of "one single source of truth": the registry above says what
// exists, and Check says whether the project only ever refers to what exists, and only ever by
// name. Without it the constants would be an exhortation - a misspelled token still compiles, still
// starts, and still denies every request forever.
// 本文件是「单一事实来源」可执行的那一半：上面的注册表说明有什么，Check 则说明项目是否只引用存在的名字、
// 并且只以名字引用。没有它，常量只是一句倡议——写错的标识照样编译、照样启动，然后永远拒绝每一个请求。

// IssueKind classifies one finding. | IssueKind 对一处发现分类。
type IssueKind string

// The three findings of the checker. | 检查器的三种发现。
const (
	// IssueUnknown marks a reference the registry does not know: a typo, a renamed capability or a
	// permission that was never declared. It is an error. | IssueUnknown 标记注册表不认识的引用：
	// 拼写错误、被改名的能力，或从未声明的权限。它是错误。
	IssueUnknown IssueKind = "unknown"
	// IssueBare marks a capability token written as a string literal outside a declaration: the
	// constants exist precisely so that this never happens. It is an error. | IssueBare 标记在声明处之外
	// 以字符串字面量写出的能力标识：常量存在的全部意义就是让它永不发生。它是错误。
	IssueBare IssueKind = "bare"
	// IssueUnused marks a declared capability nothing refers to - a zombie permission. It is a hint,
	// never a failure, because a capability may be granted through data that no source mentions.
	// IssueUnused 标记无人引用的已声明能力——僵尸权限。它只是提示，绝不算失败，
	// 因为能力也可能经由源码中不出现的数据库授予。
	IssueUnused IssueKind = "unused"
)

// IsError reports whether the kind fails a check. | IsError 报告该类别是否让校验失败。
func (k IssueKind) IsError() bool { return k == IssueUnknown || k == IssueBare }

// Issue is one finding of the checker. | Issue 是检查器的一处发现。
type Issue struct {
	// Kind classifies the finding. | Kind 对发现分类。
	Kind IssueKind
	// File is the slash-separated path as walked from the scanned directory, so it starts with the
	// root that was passed in and is what a reader can paste into an editor. | File 是自扫描目录遍历得到的斜杠路径，
	// 因此它以传入的根开头，读者可以直接粘进编辑器。
	File string
	// Ref is the reference exactly as the source wrote it. | Ref 是源码原样写出的引用。
	Ref string
	// Note is the bilingual explanation of this finding. | Note 是本处发现的中英对照说明。
	Note string
	// Text is the source line, trimmed. | Text 是源码行，已去除首尾空白。
	Text string
	// Line is the 1-based line number. | Line 是从 1 开始的行号。
	Line int
}

// CheckOptions configures Check. | CheckOptions 配置 Check。
type CheckOptions struct {
	// FS is the filesystem the project is read from; nil means the operating system. | FS 是读取项目所用的文件系统；为空表示操作系统。
	FS afero.Fs
	// Dir is the scanned root, "." when empty. | Dir 是扫描根，空表示 "."。
	Dir string
	// SkipDirs are directory names skipped wherever they appear; nil means DefaultSkipDirs. | SkipDirs 是随处跳过的目录名；为空表示 DefaultSkipDirs。
	SkipDirs []string
	// Exts are the scanned file extensions; nil means DefaultScanExts. | Exts 是参与扫描的文件扩展名；为空表示 DefaultScanExts。
	Exts []string
}

// DefaultSkipDirs are the directories the checker never enters. | DefaultSkipDirs 是检查器永不进入的目录。
//
// testdata is skipped on purpose: it holds fixtures - including the deliberately broken ones a
// project writes to prove the checker catches them. | 刻意跳过 testdata：那里存放夹具，
// 包括项目为了证明检查器确实能抓住问题而故意写坏的夹具。
var DefaultSkipDirs = []string{".git", ".idea", ".vscode", ".yd", "vendor", "node_modules", "bin", "dist", "testdata"}

// DefaultScanExts are the file extensions the checker reads. | DefaultScanExts 是检查器读取的文件扩展名。
var DefaultScanExts = []string{".go", ".tmpl", ".html", ".gotmpl", ".js", ".mjs"}

// Report is the outcome of one check. | Report 是一次校验的结果。
type Report struct {
	// Dir is the scanned root. | Dir 是扫描根。
	Dir string
	// Issues lists every finding, sorted by file and line. | Issues 列出全部发现，按文件与行号排序。
	Issues []Issue
	// Files is the number of scanned files. | Files 是扫描的文件数。
	Files int
	// Refs is the number of permission and role references found. | Refs 是找到的权限与角色引用数。
	Refs int
	// Perms is the size of the registry that was checked against. | Perms 是作为比对基准的注册表大小。
	Perms int
	// Roles is the number of registered roles. | Roles 是已注册角色数。
	Roles int
}

// Errors counts the findings that fail the check. | Errors 统计让校验失败的发现数。
func (r Report) Errors() int {
	n := 0
	for _, is := range r.Issues {
		if is.Kind.IsError() {
			n++
		}
	}
	return n
}

// Hints counts the findings that are only advice. | Hints 统计仅作建议的发现数。
func (r Report) Hints() int { return len(r.Issues) - r.Errors() }

// OK reports whether the check passed. | OK 报告校验是否通过。
func (r Report) OK() bool { return r.Errors() == 0 }

// Fprint renders the report to w. | Fprint 把报告渲染到 w。
//
// Errors come first and carry their file, line and the source line itself; hints follow in a
// compact list, because a zombie capability is worth a glance and not a page. | 错误在前，并带有文件、
// 行号与源码行本身；提示随后以紧凑列表给出，因为僵尸权限值得瞄一眼而不值得占一页。
func (r Report) Fprint(w io.Writer) {
	fmt.Fprintf(w, "yd authz check | yd authz 校验\n")
	fmt.Fprintf(w, "  dir | 目录: %s\n", r.Dir)
	fmt.Fprintf(w, "  files | 文件: %d, refs | 引用: %d, permissions | 权限: %d, roles | 角色: %d\n",
		r.Files, r.Refs, r.Perms, r.Roles)

	for _, is := range r.Issues {
		if !is.Kind.IsError() {
			continue
		}
		fmt.Fprintf(w, "  error | 错误 %s[%s]: %s\n", is.Kind, is.Ref, is.Note)
		fmt.Fprintf(w, "    %s:%d  %s\n", is.File, is.Line, is.Text)
	}
	hints := make([]Issue, 0, 4)
	for _, is := range r.Issues {
		if is.Kind.IsError() {
			continue
		}
		hints = append(hints, is)
	}
	if len(hints) > 0 {
		fmt.Fprintf(w, "  hint | 提示 (%d):\n", len(hints))
		for _, is := range hints {
			fmt.Fprintf(w, "    %s  %s\n", is.Ref, is.Note)
		}
	}
	fmt.Fprintf(w, "  result | 结论: %d error(s) | 错误, %d hint(s) | 提示\n", r.Errors(), r.Hints())
}

// Check validates a project against the registry. | Check 按注册表校验一个项目。
//
// It reads every source file under the root, and reports three things: a reference to a capability
// the registry does not know, a capability written as a string literal instead of through a
// constant, and a declared capability nothing refers to. | 它读取扫描根下的每个源文件，报告三件事：
// 引用了注册表不认识的能力、以字符串字面量而非常量写出能力、以及无人引用的已声明能力。
func Check(o CheckOptions) (Report, error) {
	fsys := o.FS
	if fsys == nil {
		fsys = afero.NewOsFs()
	}
	dir := strings.Trim(strings.ReplaceAll(o.Dir, "\\", "/"), "/")
	if dir == "" {
		dir = "."
	}
	skip := o.SkipDirs
	if skip == nil {
		skip = DefaultSkipDirs
	}
	exts := o.Exts
	if exts == nil {
		exts = DefaultScanExts
	}

	files, err := collectSources(fsys, dir, skip, exts)
	if err != nil {
		return Report{}, err
	}

	rep := Report{Dir: dir, Perms: len(Permissions()), Roles: len(Roles())}
	// The project vocabulary is read in a first pass: a reference may point at a capability the
	// project declares itself, and declaring it later in the walk must not matter.
	// 项目词汇表在第一遍读取：引用可能指向项目自行声明的能力，而该声明出现在遍历的稍后位置不应有影响。
	vocab := newVocab()
	for _, f := range files {
		vocab.absorb(f)
	}

	var refs []reference
	for _, f := range files {
		rep.Files++
		found := scanFile(f, vocab)
		refs = append(refs, found...)
		for _, ref := range found {
			if ref.kind.IsError() {
				rep.Issues = append(rep.Issues, Issue{
					Kind: ref.kind,
					File: f.rel,
					Line: ref.line,
					Ref:  ref.ref,
					Note: ref.note,
					Text: f.line(ref.line),
				})
			}
		}
	}
	rep.Refs = len(refs)
	rep.Issues = append(rep.Issues, unusedIssues(refs)...)
	sort.SliceStable(rep.Issues, func(i, j int) bool {
		if rep.Issues[i].File != rep.Issues[j].File {
			return rep.Issues[i].File < rep.Issues[j].File
		}
		return rep.Issues[i].Line < rep.Issues[j].Line
	})
	return rep, nil
}

// reference is one resolved or failed reference found in the sources. | reference 是源码中找到的一处已解析或解析失败的引用。
type reference struct {
	// ref is the reference as written. | ref 是原样写出的引用。
	ref string
	// note is the bilingual explanation. | note 是中英对照的说明。
	note string
	// file is the scanned file the reference lives in. | file 是引用所在的扫描文件。
	file string
	// kind is the finding this reference produces, empty when it is clean. | kind 是该引用产生的发现，无问题时为空。
	kind IssueKind
	// entry is the capability or role the reference points at, empty when it resolved to nothing.
	// entry 是引用指向的能力或角色，解析失败时为空。
	entry vEntry
	// line is the 1-based line number. | line 是从 1 开始的行号。
	line int
}

// vEntry is one entry of the project vocabulary. | vEntry 是项目词汇表的一条记录。
type vEntry struct {
	// token is the capability token or the role name. | token 是能力标识或角色名。
	token string
	// role marks a role name instead of a capability. | role 标记这是角色名而不是能力标识。
	role bool
}

// vocab is what the project declares on its own. | vocab 是项目自行声明的内容。
//
// A project is expected to declare its capabilities through this very package, but it may also
// keep its own typed constants (the scaffold generates exactly that), so the checker reads those
// declarations out of the sources and treats them as known. | 项目应当通过本包声明自己的能力，
// 但也可以保有自己的强类型常量（脚手架正是这样生成的），因此检查器从源码中读出这些声明并视为已知。
type vocab struct {
	// names maps a Go constant name to its entry. | names 把 Go 常量名映射到记录。
	names map[string]vEntry
	// keys maps a data key or a JavaScript key to its entry. | keys 把数据键或 JavaScript 键映射到记录。
	keys map[string]vEntry
	// tokens maps a token to its entry. | tokens 把标识映射到记录。
	tokens map[string]vEntry
	// groups is the set of resource groups the project mentions. | groups 是项目提到过的资源分组集合。
	groups map[string]bool
}

// newVocab returns an empty project vocabulary. | newVocab 返回空的项目词汇表。
func newVocab() *vocab {
	return &vocab{
		names:  map[string]vEntry{},
		keys:   map[string]vEntry{},
		tokens: map[string]vEntry{},
		groups: map[string]bool{},
	}
}

// add records one declared capability or role. | add 记录一条已声明的能力或角色。
//
// The declared name is recorded as well, so that a project which spells its constants differently
// from the convention is still understood; the conventional names are always recorded, because that
// is what a template reads. | 声明的名字也会被记录，因此不按约定拼写常量的项目照样能被理解；
// 约定名字则始终记录，因为模板读的就是它们。
func (v *vocab) add(name, token string, role bool) {
	if token == "" {
		return
	}
	e := vEntry{token: token, role: role}
	v.tokens[token] = e
	if name != "" {
		v.names[name] = e
	}
	if role {
		v.names[RoleConstantName(token)] = e
		v.keys[RoleDataKey(token)] = e
		v.keys[RoleJSKey(token)] = e
		return
	}
	v.names[ConstantName(token)] = e
	v.keys[DataKey(token)] = e
	v.keys[JSKey(token)] = e
	if g, _, ok := strings.Cut(token, ":"); ok {
		v.groups[g] = true
	}
}

// knowsGroup reports whether token's resource half is a group the project (or the framework) uses.
// knowsGroup 报告 token 的资源段是否是项目（或框架）使用过的分组。
//
// It is the discriminator that keeps MIME types out of the report: "user:creat" shares a group with
// a declared capability and is therefore read as a typo, while "text/html" shares none and is read
// as what it is. | 它是把 MIME 类型挡在报告之外的判别器：`"user:creat"` 与已声明的能力同组，
// 因此被读作拼写错误；而 `"text/html"` 不与任何分组相同，因此被读作它本来的样子。
func (v *vocab) knowsGroup(token string) bool {
	g, _, ok := strings.Cut(token, ":")
	if !ok {
		return false
	}
	if v.groups[g] {
		return true
	}
	for _, p := range Permissions() {
		if p.Group == g {
			return true
		}
	}
	return false
}

// resolveToken resolves a capability token or a role name. | resolveToken 解析能力标识或角色名。
func (v *vocab) resolveToken(ref string) (vEntry, bool) {
	if Registered(ref) {
		return vEntry{token: ref}, true
	}
	if RoleRegistered(ref) {
		return vEntry{token: ref, role: true}, true
	}
	if e, ok := v.tokens[ref]; ok {
		return e, true
	}
	return vEntry{}, false
}

// resolveName resolves a Go constant name. | resolveName 解析 Go 常量名。
func (v *vocab) resolveName(ref string) (vEntry, bool) {
	for _, p := range Permissions() {
		if ConstantName(p.Name) == ref {
			return vEntry{token: p.Name}, true
		}
	}
	for _, r := range Roles() {
		if RoleConstantName(r.Name) == ref {
			return vEntry{token: r.Name, role: true}, true
		}
	}
	return v.names[ref], v.names[ref].token != ""
}

// resolveKey resolves a data key or a JavaScript key. | resolveKey 解析数据键或 JavaScript 键。
func (v *vocab) resolveKey(ref string) (vEntry, bool) {
	for _, p := range Permissions() {
		if DataKey(p.Name) == ref || JSKey(p.Name) == ref {
			return vEntry{token: p.Name}, true
		}
	}
	for _, r := range Roles() {
		if RoleDataKey(r.Name) == ref || RoleJSKey(r.Name) == ref {
			return vEntry{token: r.Name, role: true}, true
		}
	}
	return v.keys[ref], v.keys[ref].token != ""
}

// sourceFile is one scanned file with its comment-free text. | sourceFile 是一个已扫描文件及其去注释文本。
type sourceFile struct {
	// rel is the slash-separated path relative to the scanned root. | rel 是相对扫描根的斜杠路径。
	rel string
	// text is the source with comments blanked out, line structure preserved. | text 是注释被抹空后的源码，行结构保持不变。
	text string
	// starts holds the offset of every line start, for line number lookups. | starts 保存每行起始偏移，用于换算行号。
	starts []int
	// kind selects the scanners that run on this file. | kind 选择作用于该文件的扫描器。
	kind fileKind
}

// fileKind is the language family of a scanned file. | fileKind 是扫描文件的语言族。
type fileKind int

const (
	// kindGo is a Go source file. | kindGo 是 Go 源文件。
	kindGo fileKind = iota
	// kindJS is a JavaScript module. | kindJS 是 JavaScript 模块。
	kindJS
	// kindTemplate is a Go template, optionally wrapped in HTML. | kindTemplate 是 Go 模板，可能嵌在 HTML 中。
	kindTemplate
)

// line returns the trimmed source line n, or "" when it is out of range. | line 返回第 n 行源码（已去首尾空白），越界时返回空串。
func (f sourceFile) line(n int) string {
	if n <= 0 || n > len(f.starts) {
		return ""
	}
	start := f.starts[n-1]
	end := len(f.text)
	if n < len(f.starts) {
		end = f.starts[n]
	}
	raw := f.text[start:end]
	return strings.TrimSpace(strings.TrimRight(raw, "\r\n"))
}

// lineOf returns the 1-based line number of an offset. | lineOf 返回某个偏移量所在的行号（从 1 开始）。
func (f sourceFile) lineOf(off int) int {
	i := sort.Search(len(f.starts), func(i int) bool { return f.starts[i] > off })
	if i == 0 {
		return 1
	}
	return i
}

// collectSources reads every scanned file under root. | collectSources 读取 root 下的每个待扫描文件。
func collectSources(fsys afero.Fs, root string, skip, exts []string) ([]sourceFile, error) {
	var files []sourceFile
	err := afs.Walk(fsys, root, func(p string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel := strings.Trim(strings.ReplaceAll(p, "\\", "/"), "/")
		if info.IsDir() {
			if rel == "" || rel == "." || p == root {
				return nil
			}
			if skipDir(path.Base(rel), skip) {
				return fs.SkipDir
			}
			return nil
		}
		kind, ok := kindOf(path.Base(rel), exts)
		if !ok || isTestFile(path.Base(rel)) {
			return nil
		}
		b, err := afero.ReadFile(fsys, p)
		if err != nil {
			return errx.WrapDetail(err, ErrReadSource, "path=%s", rel)
		}
		files = append(files, newSourceFile(rel, string(b), kind))
		return nil
	})
	if err != nil {
		return nil, errx.WrapDetail(err, ErrScanDir, "dir=%s", root)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files, nil
}

// newSourceFile prepares one file for scanning. | newSourceFile 为一个文件做好扫描准备。
func newSourceFile(rel, raw string, kind fileKind) sourceFile {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	text := stripComments(raw, kind)
	starts := make([]int, 0, strings.Count(text, "\n")+1)
	starts = append(starts, 0)
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return sourceFile{rel: rel, text: text, kind: kind, starts: starts}
}

// skipDir reports whether a directory name is skipped. | skipDir 报告某个目录名是否跳过。
func skipDir(name string, skip []string) bool {
	for _, s := range skip {
		if name == s {
			return true
		}
	}
	return false
}

// kindOf maps a file name to its language family. | kindOf 把文件名映射到语言族。
//
// The template wrapper is peeled first, so that internal/model/permission.go.tmpl is read as Go and
// web/templates/pages/users.html.tmpl as a template; documentation is never read at all, because a
// governance document quotes capabilities on purpose and flagging its examples would only teach
// people to stop writing them down. | 先剥掉模板外壳：internal/model/permission.go.tmpl 按 Go 读取，
// web/templates/pages/users.html.tmpl 按模板读取；文档则完全不读，
// 因为治理文档本就要引用能力，把它的示例报错只会教人别再写下来。
func kindOf(name string, exts []string) (fileKind, bool) {
	stem := name
	if strings.HasSuffix(stem, ".tmpl") {
		stem = strings.TrimSuffix(stem, ".tmpl")
	}
	ext := path.Ext(stem)
	if ext == "" {
		ext = path.Ext(name)
	}
	matched := false
	for _, e := range exts {
		if ext == e || path.Ext(name) == e {
			matched = true
			break
		}
	}
	if !matched {
		return 0, false
	}
	switch ext {
	case ".go":
		return kindGo, true
	case ".js", ".mjs":
		return kindJS, true
	case ".html", ".gotmpl", ".tmpl":
		return kindTemplate, true
	default:
		return 0, false
	}
}

// isTestFile reports whether a file is a fixture rather than shipped code. | isTestFile 报告该文件是夹具而不是交付代码。
//
// Test files are skipped wholesale: they are where a project writes the deliberately broken
// fixtures that prove the checker works, and a checker that reports its own self-test would be
// useless in CI. | 测试文件整体跳过：项目在那里写下故意写坏的夹具以证明检查器有效，
// 而一个会报告自身自检的检查器在 CI 里毫无用处。
func isTestFile(name string) bool {
	return strings.HasSuffix(name, "_test.go") ||
		strings.Contains(name, ".test.") ||
		strings.HasSuffix(name, "_test.mjs") ||
		strings.HasSuffix(name, "_test.js")
}

// The scanners. | 扫描器。

// permissionConstRe matches the Go constant name of a capability or a role. | permissionConstRe 匹配能力或角色的 Go 常量名。
var permissionConstRe = regexp.MustCompile(`\b(Perm[A-Z][A-Za-z0-9_]*|Role[A-Z][A-Za-z0-9_]*)\b`)

// permSelectorRe matches a template read of the injected name maps. | permSelectorRe 匹配对注入名字表的模板读取。
//
// The prefixes come from the constants of the front-end contract, quoted for the regular expression:
// the checker and the browser must never disagree about what `.Perm.UserCreate` means.
// 前缀来自前端契约里的常量，并为正则表达式做了转义：检查器与浏览器对 `.Perm.UserCreate` 的含义
// 绝不能有分歧。
var (
	// permSelectorRe matches .Perm.UserCreate. | permSelectorRe 匹配 .Perm.UserCreate。
	permSelectorRe = regexp.MustCompile(regexp.QuoteMeta(PermSelector) + `([A-Za-z][A-Za-z0-9_]*)`)
	// roleSelectorRe matches .Role.Admin. | roleSelectorRe 匹配 .Role.Admin。
	roleSelectorRe = regexp.MustCompile(regexp.QuoteMeta(RoleSelector) + `([A-Za-z][A-Za-z0-9_]*)`)
	// permScriptRe matches yd.PERM.USER_CREATE. | permScriptRe 匹配 yd.PERM.USER_CREATE。
	permScriptRe = regexp.MustCompile(regexp.QuoteMeta(PermScriptSelector) + `([A-Z][A-Z0-9_]*)`)
	// roleScriptRe matches yd.ROLE.ADMIN. | roleScriptRe 匹配 yd.ROLE.ADMIN。
	roleScriptRe = regexp.MustCompile(regexp.QuoteMeta(RoleScriptSelector) + `([A-Z][A-Z0-9_]*)`)
)

// attrRe matches the declarative attributes of the front end. | attrRe 匹配前端的声明式属性。
var attrRe = regexp.MustCompile(`data-yd-(perm|role)="([^"]*)"`)

// helperCallRe matches a template helper that takes a capability or a role. | helperCallRe 匹配接收能力或角色的模板辅助函数。
//
// The context in front of the name is required - an action, an if/with, or a parenthesis - because
// prose does say "you can "download" it", and a checker that reads English as a permission
// reference would be switched off within a week. | 名字前面必须出现上下文——动作、if/with 或左括号——
// 因为散文里确实会写「you can "download" it」，而一个把英文当成权限引用的检查器活不过一周。
var helperCallRe = regexp.MustCompile(`(?m)(?:\{\{-?\s*|(?:^|[\s;{])(?:if|with|else\s+if)\s+|\()\s*(can|hasRole|perm|role)\s+("(?:[^"\\]|\\.)*"|[^\s"|(){}]+)`)

// literalRe matches a Go or JavaScript string literal. | literalRe 匹配 Go 或 JavaScript 的字符串字面量。
var literalRe = regexp.MustCompile("\"(?:[^\"\\\\]|\\\\.)*\"|`[^`]*`|'(?:[^'\\\\]|\\\\.)*'")

// assignLineRe reports a line whose right-hand side is a single literal or name. | assignLineRe 报告右侧只有一个字面量或名字的行。
var assignLineRe = regexp.MustCompile(`^\s*(?:(?:export\s+)?(?:const|var|let|type)\s+)?[\w.]+\s+(?:[\w.\[\]*]+|\[\][\w.]+)\s*=\s*(?:"(?:[^"\\]|\\.)*"|[A-Za-z_][\w.]*)\s*;?\s*$`)

// shortAssignLineRe reports a short assignment whose right-hand side is a single literal or name.
// shortAssignLineRe 报告右侧只有一个字面量或名字的短声明行。
var shortAssignLineRe = regexp.MustCompile(`^\s*[\w.]+\s*(?::=|=)\s*(?:"(?:[^"\\]|\\.)*"|` + "`[^`]*`" + `|[A-Za-z_][\w.]*)\s*;?\s*$`)

// scanFile runs every scanner that applies to f. | scanFile 对 f 运行所有适用的扫描器。
func scanFile(f sourceFile, v *vocab) []reference {
	var refs []reference
	refs = append(refs, scanLiterals(f, v)...)
	refs = append(refs, scanConstants(f, v)...)
	switch f.kind {
	case kindTemplate:
		refs = append(refs, scanTemplates(f, v)...)
	case kindJS:
		refs = append(refs, scanScripts(f, v)...)
	}
	return refs
}

// scanScripts resolves the keys a script reads through the two script selectors. | scanScripts 解析脚本经两个脚本选择器读取的键。
//
// A script writes yd.PERM.USER_CREATE, and that key is derived from the token exactly like the
// template's .Perm.UserCreate - so a typo here is the same failure as a typo there, and it has to be
// caught by the same pass. | 脚本写 yd.PERM.USER_CREATE，而该键与模板的 .Perm.UserCreate 一样派生自标识——
// 因此这里写错与那里写错是同一种故障，必须由同一趟扫描抓住。
func scanScripts(f sourceFile, v *vocab) []reference {
	refs := scanSelector(f, v, permScriptRe, false)
	return append(refs, scanSelector(f, v, roleScriptRe, true)...)
}

// refCallRe matches the callee of a call whose argument is a capability or a role. | refCallRe 匹配实参为能力或角色的调用被调名。
//
// The set is deliberately short and curated: the point of position detection is to make "a
// capability written as a literal" an exact finding rather than a guess, and every extra name here
// is a new way to be wrong. | 这个集合刻意短小而经过挑选：位置判定的意义在于让「被写成字面量的能力」
// 成为精确的发现而不是猜测，而这里每多一个名字，就多一种出错的方式。
var refCallRe = regexp.MustCompile(`(?:^|\.)(?:RequirePermission|RequireRole|Require|Allow|Can|HasPermission|HasRole|Grant|can|hasRole)$`)

// isIdentByte reports whether b may appear in an identifier. | isIdentByte 报告 b 是否可作为标识符的一部分。
func isIdentByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// templateHelperNames are the template helpers that take a capability or a role. | templateHelperNames 是接收能力或角色的模板辅助函数。
var templateHelperNames = map[string]bool{"can": true, "hasRole": true, "perm": true, "role": true}

// claimedByTemplate reports whether the template scanners already own the literal at off. | claimedByTemplate 报告模板扫描器是否已认领 off 处的字面量。
func claimedByTemplate(src string, off int) bool {
	if name := attributeName(src, off); name == "data-yd-perm" || name == "data-yd-role" {
		return true
	}
	return templateHelperNames[precedingWord(src, off)]
}

// attributeName returns the HTML attribute the literal at off is the value of, if any. | attributeName 返回 off 处的字面量所属的 HTML 属性，没有则返回空串。
func attributeName(src string, off int) string {
	start := strings.LastIndexByte(src[:off], '\n') + 1
	prefix := strings.TrimRight(src[start:off], " \t")
	if !strings.HasSuffix(prefix, "=") {
		return ""
	}
	return tailIdent(strings.TrimRight(prefix[:len(prefix)-1], " \t"), '-')
}

// precedingWord returns the word in front of the literal at off. | precedingWord 返回 off 处字面量前面的那个词。
func precedingWord(src string, off int) string {
	start := strings.LastIndexByte(src[:off], '\n') + 1
	return tailIdent(strings.TrimRight(src[start:off], " \t"), 0)
}

// tailIdent returns the trailing identifier of s, optionally allowing extra bytes. | tailIdent 返回 s 末尾的标识符，可额外允许部分字节。
func tailIdent(s string, extra byte) string {
	end := len(s)
	i := end
	for i > 0 {
		b := s[i-1]
		if isIdentByte(b) || (extra != 0 && b == extra) {
			i--
			continue
		}
		break
	}
	return s[i:end]
}

// enclosingCall returns the callee of the call whose argument list contains off. | enclosingCall 返回参数表中包含 off 的那个调用的被调名。
//
// The scan walks backwards to the innermost unmatched "(", then backwards over the callee name, and
// stops at a line break; a multi-line argument list is followed as long as its parentheses stay
// open. Requiring the call to open on the literal's own line is what keeps the scan from wandering
// into an unrelated earlier statement - every capability call in this repository is written on one
// line, so nothing is lost. | 扫描向后走到最内层未配对的 "("，再向后走过被调名，并在换行处停止；
// 只要括号没闭合，跨行的参数表仍会被跟随。要求调用在字面量自己那一行开始，
// 是为了让扫描不会漂到前面无关的语句里去——本仓的每个能力调用都写在一行内，因此不会漏掉任何东西。
func enclosingCall(src string, off int) (string, bool) {
	depth := 0
	for i := off - 1; i >= 0; i-- {
		switch src[i] {
		case ')':
			depth++
		case '(':
			if depth > 0 {
				depth--
				continue
			}
			j := i - 1
			for j >= 0 && (src[j] == ' ' || src[j] == '\t') {
				j--
			}
			end := j + 1
			for j >= 0 && (isIdentByte(src[j]) || src[j] == '.') {
				j--
			}
			if end == j+1 {
				return "", false
			}
			return src[j+1 : end], true
		case '\n':
			if depth == 0 {
				return "", false
			}
		}
	}
	return "", false
}

// scanLiterals finds every string literal that names a capability or a role. | scanLiterals 找出每个指明能力或角色的字符串字面量。
func scanLiterals(f sourceFile, v *vocab) []reference {
	var refs []reference
	for _, loc := range literalRe.FindAllStringIndex(f.text, -1) {
		raw := f.text[loc[0]:loc[1]]
		value := unquote(raw)
		if value == "" {
			continue
		}
		line := f.lineOf(loc[0])
		if isDefinitionLine(f.line(line)) {
			continue
		}
		if f.kind == kindTemplate && claimedByTemplate(f.text, loc[0]) {
			// A template attribute or helper argument is reported by scanTemplates, which knows the
			// difference between a data key and a token; reporting it here as well would print the
			// same mistake twice and teach people to skim the report.
			// 模板属性与辅助函数实参由 scanTemplates 报告，它分得清数据键与标识；
			// 这里再报一次会把同一个错误印两遍，教人学会略读报告。
			continue
		}
		call, inCall := enclosingCall(f.text, loc[0])
		position := inCall && refCallRe.MatchString(call)
		if !position && !IsPermissionName(value) {
			// Outside a capability call only a token-shaped literal can be a capability; inside one,
			// anything the call was handed was meant as one. | 在能力调用之外，只有形如标识的字面量才可能是能力；
			// 而在能力调用之内，交给它的任何值都被当成了能力。
			continue
		}
		ref := reference{ref: value, file: f.rel, line: line}
		entry, known := v.resolveToken(value)
		switch {
		case known:
			ref.entry = entry
			ref.kind = IssueBare
			ref.note = "capability written as a string literal; reference a constant instead | 能力被写成字符串字面量；请改为引用常量"
		case position || v.knowsGroup(value):
			// Either a call that takes a capability was given something the registry does not know -
			// a typo, most often - or the token shares a resource group with a declared capability,
			// which no media type does. | 要么是接收能力的调用拿到了注册表不认识的值（多数是拼写错误），
			// 要么是标识与已声明的能力同组——没有哪个媒体类型会同组。
			ref.kind = IssueUnknown
			ref.note = "unregistered capability token | 未注册的能力标识"
		default:
			// Neither registered nor recognisably grouped: it is a media type, a scheme or some
			// other "a:b" string, and not our business.
			// 既未注册也不属于已知分组：它是媒体类型、协议 scheme 或别的 "a:b" 字符串，不归我们管。
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}

// scanConstants records every reference to a capability or a role by constant name. | scanConstants 记录每一处按常量名对能力或角色的引用。
//
// Only names that resolve count as references. An unresolved one is deliberately not reported here,
// for two reasons: in compiled code the compiler already refuses an unknown constant, and it does so
// far better than a scanner; and "Perm"/"Role" is an ordinary domain prefix that real code spends on
// RoleID, RoleOption and PermMap, so treating every Perm*/Role* identifier as a reference would
// drown the report in names that were never capabilities. In a template the name is a string, the
// compiler is not watching, and scanTemplates does report the unknown ones. | 只有能解析的名字才算引用。
// 解析不了的刻意不在这里报告，理由有二：在需要编译的代码里，编译器本就会拒绝未知常量，且比扫描器做得更好；
// 而 "Perm"/"Role" 只是普通的领域前缀，真实代码会把它用在 RoleID、RoleOption 与 PermMap 上，
// 因此把每个 Perm*/Role* 标识都当成引用，会让报告淹没在从来不是能力的名字里。
// 在模板里名字是字符串、编译器看不见，那里未注册的名字由 scanTemplates 负责报告。
func scanConstants(f sourceFile, v *vocab) []reference {
	var refs []reference
	for _, loc := range permissionConstRe.FindAllStringSubmatchIndex(f.text, -1) {
		name := f.text[loc[2]:loc[3]]
		entry, known := v.resolveName(name)
		if !known {
			continue
		}
		line := f.lineOf(loc[2])
		if isDefinitionLine(f.line(line)) || isDeclaredName(f.text, loc[2], name) {
			continue
		}
		refs = append(refs, reference{ref: name, file: f.rel, line: line, entry: entry})
	}
	return refs
}

// scanTemplates finds the capability references of a template file. | scanTemplates 找出模板文件中的能力引用。
func scanTemplates(f sourceFile, v *vocab) []reference {
	var refs []reference
	for _, m := range helperCallRe.FindAllStringSubmatchIndex(f.text, -1) {
		arg := f.text[m[4]:m[5]]
		value := unquote(arg)
		if value == "" {
			// A template field or expression: .Perm.UserCreate is picked up below, and anything
			// else is a computed value the checker cannot follow.
			// 模板字段或表达式：.Perm.UserCreate 会在下面被抓到，其余是检查器跟不上的计算值。
			continue
		}
		refs = append(refs, templateRef(f, v, value, f.lineOf(m[4])))
	}
	refs = append(refs, scanSelector(f, v, permSelectorRe, false)...)
	refs = append(refs, scanSelector(f, v, roleSelectorRe, true)...)
	refs = append(refs, scanSelector(f, v, permScriptRe, false)...)
	refs = append(refs, scanSelector(f, v, roleScriptRe, true)...)
	for _, m := range attrRe.FindAllStringSubmatchIndex(f.text, -1) {
		value := f.text[m[4]:m[5]]
		if strings.Contains(value, "{{") {
			continue // the action inside is scanned by the loops above | 其中的动作由上面的循环扫描
		}
		ref := templateRef(f, v, value, f.lineOf(m[4]))
		if ref.kind == "" && ref.entry.role != (f.text[m[2]:m[3]] == "role") {
			// The attribute names one kind and the reference resolved to the other: a role name
			// under data-yd-perm hides an element that no permission can ever show again.
			// 属性指明了一类，引用却解析成另一类：把角色名写在 data-yd-perm 下，
			// 会让该元素被隐藏，且再没有任何权限能让它显示出来。
			ref.entry = vEntry{}
			ref.kind = IssueUnknown
			ref.note = "attribute and reference disagree on the kind | 属性与引用的类别不一致"
		}
		refs = append(refs, ref)
	}
	return refs
}

// scanSelector resolves the keys read through one selector prefix. | scanSelector 解析经某个选择器前缀读取的键。
//
// wantRole is what makes the check worth having: `.Perm.Admin` is not a permission however
// registered "admin" is, and only this comparison knows that. | wantRole 正是这项检查值得存在的原因：
// 无论 "admin" 注册得多好，`.Perm.Admin` 都不是权限，而只有这项比对知道这一点。
func scanSelector(f sourceFile, v *vocab, re *regexp.Regexp, wantRole bool) []reference {
	var refs []reference
	for _, m := range re.FindAllStringSubmatchIndex(f.text, -1) {
		key := f.text[m[2]:m[3]]
		entry, known := v.resolveKey(key)
		ref := reference{ref: key, file: f.rel, line: f.lineOf(m[2]), entry: entry}
		switch {
		case !known:
			ref.kind = IssueUnknown
			ref.note = "unregistered permission or role key | 未注册的权限或角色键"
		case entry.role != wantRole:
			ref.entry = vEntry{}
			ref.kind = IssueUnknown
			ref.note = "selector reads the other kind | 选择器读取的是另一类"
		}
		refs = append(refs, ref)
	}
	return refs
}

// templateRef classifies one reference written in a template. | templateRef 对模板中写出的单处引用分类。
//
// A template has no constants, so a *constant name* in a string is the sanctioned form and is
// resolved silently, while a *token* in a string is exactly the bare literal this whole package
// exists to eliminate. | 模板里没有常量，因此字符串中的*常量名*是被认可的形式、静默解析，
// 而字符串中的*标识*正是本包存在的全部意义所在——要消灭的裸字面量。
func templateRef(f sourceFile, v *vocab, value string, line int) reference {
	ref := reference{ref: value, file: f.rel, line: line}
	if IsPermissionName(value) {
		entry, known := v.resolveToken(value)
		ref.entry = entry
		switch {
		case known:
			ref.kind = IssueBare
			ref.note = "capability written as a string literal; use a Perm/Role data key | 能力被写成字符串字面量；请改用 Perm/Role 数据键"
		case v.knowsGroup(value):
			ref.kind = IssueUnknown
			ref.note = "unregistered capability token | 未注册的能力标识"
		default:
			ref.kind = IssueBare
			ref.note = "capability written as a string literal; use a Perm/Role data key | 能力被写成字符串字面量；请改用 Perm/Role 数据键"
		}
		return ref
	}
	entry, known := v.resolveName(value)
	ref.entry = entry
	if !known {
		if e, ok := v.resolveKey(value); ok {
			ref.entry = e
			return ref
		}
		ref.kind = IssueUnknown
		ref.note = "unregistered permission or role reference | 未注册的权限或角色引用"
	}
	return ref
}

// unusedIssues reports every declared capability the sources never refer to. | unusedIssues 报告源码从未引用过的每个已声明能力。
func unusedIssues(refs []reference) []Issue {
	used := make(map[vEntry]bool, len(refs))
	for _, r := range refs {
		if r.entry.token != "" {
			used[r.entry] = true
		}
	}
	var out []Issue
	for _, p := range Permissions() {
		if used[vEntry{token: p.Name}] {
			continue
		}
		out = append(out, Issue{
			Kind: IssueUnused,
			Ref:  p.Name,
			Note: "declared but never referenced | 已声明但从未被引用",
		})
	}
	for _, r := range Roles() {
		if used[vEntry{token: r.Name, role: true}] {
			continue
		}
		out = append(out, Issue{
			Kind: IssueUnused,
			Ref:  r.Name,
			Note: "role declared but never referenced | 已声明但从未被引用的角色",
		})
	}
	return out
}

// absorb reads the declarations of one file into the project vocabulary. | absorb 把一个文件的声明读入项目词汇表。
//
// Only real declarations are absorbed. Reading every token-shaped literal - the tempting shortcut -
// would take `hx-sse:close="yd:close"` and `style="display:none"` for capability declarations and
// then quietly bless them everywhere else in the tree. | 只有真正的声明会被吸收。
// 读取每个形似标识的字面量——那条诱人的捷径——会把 `hx-sse:close="yd:close"` 与 `style="display:none"`
// 当成能力声明，然后在整棵树的其它地方悄悄为它们背书。
func (v *vocab) absorb(f sourceFile) {
	if f.kind == kindTemplate {
		// A template declares nothing: every capability written there is a reference.
		// 模板不声明任何东西：写在其中的每个能力都是引用。
		return
	}
	for _, line := range strings.Split(f.text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !isDefinitionLine(trimmed) {
			continue
		}
		for _, loc := range literalRe.FindAllStringIndex(trimmed, -1) {
			value := unquote(trimmed[loc[0]:loc[1]])
			if value == "" {
				continue
			}
			switch name := declaredName(trimmed[:loc[0]]); {
			case name == "Name" || name == "name":
				// A registration record: authz.Register(Permission{Name: "invoice:approve"}).
				// 注册记录：authz.Register(Permission{Name: "invoice:approve"})。
				switch {
				case IsPermissionName(value):
					v.add("", value, false)
				case IsRoleName(value):
					v.add("", value, true)
				}
			case strings.HasPrefix(name, "Perm") && IsPermissionName(value):
				v.add(name, value, false)
			case strings.HasPrefix(name, "Role") && IsRoleName(value):
				v.add(name, value, true)
			}
		}
	}
}

// declaredName returns the identifier a declaration binds a literal to. | declaredName 返回声明把字面量绑定到的标识符。
//
// Three shapes are understood: `PermUserRead = "user:read"`, `PermUserRead Permission = "user:read"`,
// and `Name: "invoice:approve"` inside a registration record. The field name of that record is not a
// constant name, and the caller treats it separately. | 识别三种形态：
// `PermUserRead = "user:read"`、`PermUserRead Permission = "user:read"`，
// 以及注册记录里的 `Name: "invoice:approve"`。该记录的字段名不是常量名，由调用方单独处理。
func declaredName(prefix string) string {
	p := strings.TrimSpace(prefix)
	p = strings.TrimRight(p, "=:")
	p = strings.Trim(p, "{[,( \t")
	fields := strings.Fields(p)
	if len(fields) == 0 {
		return ""
	}
	first := strings.Trim(fields[0], "[]{}(),")
	switch first {
	case "const", "var", "let", "export", "type", "public", "private", "static":
		if len(fields) < 2 {
			return ""
		}
		return strings.Trim(fields[1], "[]{}(),")
	default:
		return first
	}
}

// isDefinitionLine reports whether a line declares a value rather than using one. | isDefinitionLine 报告该行是在声明值还是在引用值。
//
// The assignment forms only count when the name being bound is itself a capability name. That is
// the difference between `PermUserCreate = "user:create"` - the one place the token is allowed to be
// a literal, which is the point of the whole package - and `var _ = "user:create"`, which is a
// capability written as a literal and therefore exactly what has to be reported.
// 赋值形态只在被绑定的名字本身就是能力名时才算声明。这正是
// `PermUserCreate = "user:create"`——标识唯一被允许写成字面量的地方，也是整包存在的意义——
// 与 `var _ = "user:create"` 的区别，后者是「被写成字面量的能力」，恰恰应当被报告。
func isDefinitionLine(line string) bool {
	if line == "" {
		return false
	}
	if keyValueLine(line) {
		return true
	}
	if !assignLineRe.MatchString(line) && !shortAssignLineRe.MatchString(line) {
		return false
	}
	lhs, _, ok := strings.Cut(line, "=")
	if !ok {
		return false
	}
	return isCapabilityDeclName(declaredName(lhs))
}

// isCapabilityDeclName reports whether name is a name a project binds a capability or a role to.
// isCapabilityDeclName 报告 name 是否是项目把能力或角色绑定到的名字。
//
// Four spellings are accepted because four consumers each have their own: the Go constant
// (PermUserCreate, RoleAdmin) and the script constant (PERM_USER_CREATE, ROLE_ADMIN). A binding to
// any other name is a reference that happens to be assigned, not a declaration.
// 接受四种写法，因为四种消费方各有各的：Go 常量（PermUserCreate、RoleAdmin）与脚本常量
// （PERM_USER_CREATE、ROLE_ADMIN）。绑定到其它任何名字都只是一次恰好带赋值的引用，而不是声明。
func isCapabilityDeclName(name string) bool {
	for _, prefix := range []string{"Perm", "Role", "PERM_", "ROLE_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// keyValueLine reports whether a line is a composite-literal or object field. | keyValueLine 报告该行是否为复合字面量或对象的字段。
func keyValueLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	for _, open := range []string{"{", ","} {
		if strings.HasPrefix(trimmed, open) {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, open))
			break
		}
	}
	key, rest, ok := strings.Cut(trimmed, ":")
	if !ok || rest == "" {
		return false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	for _, r := range key {
		if !isIdentRune(r) {
			return false
		}
	}
	// A Go label, a "case x:" or a JS ternary would also cut at a colon; requiring the key to be a
	// plain identifier and the value to start like a literal or a name keeps them out.
	// Go 标签、"case x:" 或 JS 三元表达式同样会在这里切出冒号；要求键是纯标识符、值以字面量或名字开头，
	// 就能把它们排除掉。
	rest = strings.TrimSpace(rest)
	return rest != "" && (rest[0] == '"' || rest[0] == '`' || rest[0] == '\'' || isIdentRune(rune(rest[0])))
}

// isDeclaredName reports whether the identifier at off is the name being declared. | isDeclaredName 报告 off 处的标识符是否正是被声明的那个名字。
func isDeclaredName(text string, off int, name string) bool {
	rest := text[off+len(name):]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return false
	}
	// `PermUserRead Permission = "user:read"` and `PermUserRead = "user:read"` both declare.
	// 两种写法都是声明。
	if eq := strings.IndexByte(rest, '='); eq >= 0 {
		if eq+1 < len(rest) && rest[eq+1] == '=' {
			return false // a comparison, not an assignment | 是比较而不是赋值
		}
		return true
	}
	return strings.HasPrefix(rest, "Permission") || strings.HasPrefix(rest, "Role ") || strings.HasPrefix(rest, "string")
}

// isIdentRune reports whether r may appear in an identifier. | isIdentRune 报告 r 是否可作为标识符的一部分。
func isIdentRune(r rune) bool {
	return r == '_' || r == '.' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// unquote strips the quotes of a literal and rejects interpolated template literals. | unquote 去掉字面量的引号，并拒绝含插值的模板字符串。
func unquote(raw string) string {
	if len(raw) < 2 {
		return ""
	}
	body := raw[1 : len(raw)-1]
	switch raw[0] {
	case '"':
		if strings.ContainsRune(body, '\\') {
			// Escapes in a permission token never happen; a literal with one is not a reference.
			// 权限标识里不会出现转义；带转义的字符串不是引用。
			return ""
		}
		return body
	case '`':
		if strings.ContainsRune(body, '`') || strings.ContainsRune(body, '\n') {
			return ""
		}
		return body
	case '\'':
		return ""
	default:
		return ""
	}
}

// stripComments blanks out comments while keeping every byte offset and line break. | stripComments 抹掉注释，同时保持每个字节偏移与换行不变。
//
// Blanking rather than deleting keeps line numbers and columns exact, so a finding can point at the
// source line it came from. | 抹空而不是删除，使行号与列号保持精确，从而让发现能指向来源行。
func stripComments(src string, kind fileKind) string {
	buf := []byte(src)
	out := make([]byte, len(buf))
	copy(out, buf)

	i := 0
	for i < len(buf) {
		switch {
		case kind == kindTemplate && strings.HasPrefix(src[i:], "{{/*"):
			i = blankUntil(out, src, i, "*/}}", dirBlock, kind)
		case kind == kindTemplate && strings.HasPrefix(src[i:], "<!--"):
			i = blankUntil(out, src, i, "-->", dirBlock, kind)
		case strings.HasPrefix(src[i:], "//"):
			i = blankUntil(out, src, i, "\n", dirLine, kind)
		case strings.HasPrefix(src[i:], "/*"):
			i = blankUntil(out, src, i, "*/", dirBlock, kind)
		case src[i] == '"':
			i = skipString(src, i, '"')
		case src[i] == '`' && kind != kindTemplate:
			i = skipString(src, i, '`')
		case src[i] == '\'' && kind == kindGo:
			i = skipString(src, i, '\'')
		default:
			i++
		}
	}
	return string(out)
}

// commentDir distinguishes a line comment from a block comment. | commentDir 区分行注释与块注释。
type commentDir int

const (
	// dirLine is a comment that ends at the end of its line. | dirLine 是止于行尾的注释。
	dirLine commentDir = iota
	// dirBlock is a comment that ends at a closing marker. | dirBlock 是止于结束标记的注释。
	dirBlock
)

// blankUntil blanks the comment starting at i and returns the offset after it. | blankUntil 抹掉从 i 开始的注释，并返回其后的偏移量。
func blankUntil(out []byte, src string, i int, end string, dir commentDir, kind fileKind) int {
	j := i + 2
	if dir == dirBlock {
		if idx := strings.Index(src[j:], end); idx >= 0 {
			j += idx + len(end)
		} else {
			j = len(src)
		}
	} else if idx := strings.IndexByte(src[i:], '\n'); idx >= 0 {
		j = i + idx
	} else {
		j = len(src)
	}
	for k := i; k < j; k++ {
		if out[k] != '\n' {
			out[k] = ' '
		}
	}
	return j
}

// skipString returns the offset just past the string literal starting at i. | skipString 返回 i 处字符串字面量之后的偏移量。
//
// A backtick string spans lines and has no escapes; the other two end at their own quote, after an
// odd number of backslashes at most. | 反引号字符串可跨行且无转义；另外两种在自身引号处结束，
// 最多前面带奇数个反斜杠。
func skipString(src string, i int, quote byte) int {
	j := i + 1
	for j < len(src) {
		if quote != '`' && src[j] == '\\' {
			j += 2
			continue
		}
		if src[j] == quote {
			return j + 1
		}
		if quote != '`' && src[j] == '\n' {
			// An unterminated string: treat the line break as the end so a stray quote cannot
			// swallow the rest of the file. | 未闭合的字符串：把换行当作结束，使一个落单的引号不会吞掉文件其余部分。
			return j
		}
		j++
	}
	return len(src)
}
