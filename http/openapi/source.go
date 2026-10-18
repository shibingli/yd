package openapi

import (
	"bytes"
	"go/parser"
	"go/token"
	"sync"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/sysinfo"
)

// Source is the combined result of one source scan. | Source 是一次源码扫描的合并结果。
//
// ParseSource reads and parses every candidate file once, so callers that need both the
// handler annotations and the type annotations avoid the second directory walk, read and
// parse that ParseAnnotations plus ParseTypeAnnotations would perform. | ParseSource 只读取并
// 解析每个候选文件一次，因此同时需要处理器注解与类型注解的调用方，可以避免
// ParseAnnotations 加 ParseTypeAnnotations 带来的第二次目录遍历、读取与解析。
type Source struct {
	// Annotations maps "<METHOD> <template path>" to the handler annotation; blocks without
	// @Router are omitted. | Annotations 把 "<METHOD> <模板路径>" 映射到处理器注解；没有
	// @Router 的块会被忽略。
	Annotations map[string]Annotation
	// Types carries the struct, const block and variable annotations. | Types 携带结构体、
	// 常量块与变量注解。
	Types *TypeAnnotations
	// blocks holds every handler annotation block in deterministic file order, including the
	// ones without @Router; CollectFromSource uses it for its report. | blocks 按确定文件顺序
	// 保存全部处理器注解块（含没有 @Router 的块）；CollectFromSource 用它生成报告。
	blocks []Annotation
}

// ParseSource walks dirs once and parses both dialects from the same file bytes, using a
// bounded worker pool sized by GOMAXPROCS. | ParseSource 只遍历一次 dirs，并从同一份文件字节解析
// 两个方言，使用按 GOMAXPROCS 确定上界的协程池。
//
// The fsys implementation must allow concurrent reads; afero.OsFs, afero.MemMapFs and the
// framework's afs wrappers do. Results are merged in sorted file order, so the output is
// deterministic regardless of scheduling. | fsys 实现必须允许并发读取；afero.OsFs、
// afero.MemMapFs 与框架的 afs 包装都满足这一点。结果按排序后的文件顺序合并，因此输出不受调度影响。
func ParseSource(fsys afero.Fs, dirs []string) (*Source, error) {
	return scanSource(fsys, dirs, true, true)
}

// scanSource is the shared implementation of the public scan functions. | scanSource 是各公开
// 扫描函数的共享实现。
func scanSource(fsys afero.Fs, dirs []string, wantHandlers, wantTypes bool) (*Source, error) {
	files, err := annotationFiles(fsys, dirs)
	if err != nil {
		return nil, err
	}
	out := &Source{Annotations: map[string]Annotation{}, Types: newTypeAnnotations()}
	if len(files) == 0 {
		return out, nil
	}

	results := make([]fileScan, len(files))
	workers := sysinfo.GOMAXPROCS()
	if workers < 1 {
		workers = 1
	}
	if workers > len(files) {
		workers = len(files)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for index := range jobs {
				results[index] = scanSourceFile(fsys, files[index], wantHandlers, wantTypes)
			}
		}()
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	// Errors are reported by file order, not by completion order. | 错误按文件顺序而非完成顺序上报。
	for i := range results {
		if results[i].err != nil {
			return nil, results[i].err
		}
	}
	for i := range results {
		out.blocks = append(out.blocks, results[i].annotations...)
		out.Types.merge(results[i].types)
	}
	if wantHandlers {
		for _, annotation := range out.blocks {
			key := annotation.key()
			if key == "" {
				continue
			}
			if _, duplicate := out.Annotations[key]; duplicate {
				continue
			}
			out.Annotations[key] = annotation
		}
	}
	return out, nil
}

// fileScan is the parsed result of one source file. | fileScan 是单个源文件的解析结果。
type fileScan struct {
	err         error
	types       *TypeAnnotations
	annotations []Annotation
}

// scanSourceFile reads and parses one file. | scanSourceFile 读取并解析单个文件。
func scanSourceFile(fsys afero.Fs, path string, wantHandlers, wantTypes bool) fileScan {
	src, err := afero.ReadFile(fsys, path)
	if err != nil {
		return fileScan{err: errx.WrapDetail(err, ErrRead, "%q", path)}
	}
	var out fileScan
	if wantHandlers && bytes.Contains(src, []byte("@")) {
		out.annotations = parseFileAnnotations(path, string(src))
	}
	if wantTypes && mayHaveTypeAnnotations(src) {
		// A FileSet is not safe for concurrent use, so every worker owns one per file. The
		// type parser never resolves identifiers, which is the expensive part of go/parser. | FileSet
		// 不支持并发使用，因此每个文件的解析都持有独立的 FileSet。类型解析器不需要标识符解析，
		// 而那正是 go/parser 最昂贵的部分。
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return fileScan{err: errx.WrapDetail(err, ErrTypeParse, "%q", path)}
		}
		types := newTypeAnnotations()
		types.collectFile(fset, file)
		out.types = types
	}
	return out
}

// newTypeAnnotations returns an initialized annotation set. | newTypeAnnotations 返回已初始化的
// 注解集合。
func newTypeAnnotations() *TypeAnnotations {
	return &TypeAnnotations{
		Schemas: map[string]SchemaAnnotation{},
		Enums:   map[string]EnumAnnotation{},
		Vars:    map[string]VarAnnotation{},
	}
}

// typeDirectiveWords lists the directives that make a declaration interesting to the type
// parser; it mirrors isTypeDirective. | typeDirectiveWords 列出会让声明进入类型解析器的指令，
// 与 isTypeDirective 保持一致。
var typeDirectiveWords = []string{
	"name", "schema", "type", "enum", "format",
	"minimum", "maximum", "pattern", "nullable", "readonly",
	"deprecated", "description", "example",
}

// mayHaveTypeAnnotations is a cheap prefilter: a file without a type/const/var declaration or
// without a type dialect directive cannot contribute type annotations, so go/parser can be
// skipped entirely. | mayHaveTypeAnnotations 是廉价的预过滤：既没有 type/const/var 声明、也没有
// 类型方言指令的文件不可能贡献类型注解，因此可以完全跳过 go/parser。
func mayHaveTypeAnnotations(src []byte) bool {
	if !bytes.Contains(src, []byte("type ")) &&
		!bytes.Contains(src, []byte("const ")) &&
		!bytes.Contains(src, []byte("var ")) {
		return false
	}
	for i := 0; i < len(src); i++ {
		if src[i] != '@' {
			continue
		}
		j := i + 1
		for j < len(src) && isDirectiveLetter(src[j]) {
			j++
		}
		if isTypeDirectiveWord(src[i+1 : j]) {
			return true
		}
	}
	return false
}

// isDirectiveLetter reports whether c may appear in a directive name. | isDirectiveLetter 报告 c
// 是否可以出现在指令名中。
func isDirectiveLetter(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isTypeDirectiveWord reports whether word is one of typeDirectiveWords, case insensitively. | isTypeDirectiveWord
// 报告 word 是否为 typeDirectiveWords 之一，忽略大小写。
func isTypeDirectiveWord(word []byte) bool {
	for _, want := range typeDirectiveWords {
		if asciiEqualFold(word, want) {
			return true
		}
	}
	return false
}

// asciiEqualFold compares a byte word with an ASCII string ignoring case. | asciiEqualFold 忽略
// 大小写比较字节单词与 ASCII 字符串。
func asciiEqualFold(word []byte, want string) bool {
	if len(word) != len(want) {
		return false
	}
	for i := 0; i < len(word); i++ {
		a, b := word[i], want[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}

// merge copies the declarations of other that are not present yet, preserving the first
// occurrence across files. | merge 复制 other 中尚不存在的声明，保持跨文件的先出现者优先。
func (t *TypeAnnotations) merge(other *TypeAnnotations) {
	if t == nil || other == nil {
		return
	}
	for name, annotation := range other.Schemas {
		if _, exists := t.Schemas[name]; !exists {
			t.Schemas[name] = annotation
		}
	}
	for name, annotation := range other.Enums {
		if _, exists := t.Enums[name]; !exists {
			t.Enums[name] = annotation
		}
	}
	for name, annotation := range other.Vars {
		if _, exists := t.Vars[name]; !exists {
			t.Vars[name] = annotation
		}
	}
}
