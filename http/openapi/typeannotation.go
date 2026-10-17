package openapi

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/spf13/afero"
)

// Type annotation parsing errors, declared once at package level. | 类型注解解析错误，在包级声明一次。
var (
	// ErrTypeParse means a scanned file is not valid Go and therefore cannot yield type
	// annotations. | ErrTypeParse 表示被扫描的文件不是合法 Go 代码，无法产出类型注解。
	ErrTypeParse = errx.New("openapi: failed to parse type annotation source | openapi：类型注解源文件解析失败")
)

// typeOnlyDirectives are the directives that only the type dialect understands: a comment
// block carrying one of them is a type annotation rather than a handler annotation. | typeOnlyDirectives
// 是仅类型方言可识别的指令：含其中之一注释块属于类型注解而非处理器注解。
var typeOnlyDirectives = map[string]bool{
	"name": true, "format": true, "enum": true, "type": true, "schema": true,
	"minimum": true, "maximum": true, "pattern": true,
	"nullable": true, "deprecated": true, "readonly": true,
}

// isTypeDirective reports whether a directive name belongs to the type dialect, which
// additionally shares @Description and @Example with the handler dialect. | isTypeDirective
// 报告指令名是否属于类型方言；类型方言与处理器方言还共用 @Description 与 @Example。
func isTypeDirective(name string) bool {
	return typeOnlyDirectives[name] || name == "description" || name == "example"
}

// FieldAnnotation is the parsed form of the annotations attached to one struct field. | FieldAnnotation
// 是单个结构体字段注解的解析结果。
//
// The understood dialect, in the field doc comment or in its trailing line comment: | 可识别的方言，
// 写在字段 doc 注释或行尾注释中：
//
//	// ID is the primary key. | ID 是主键。
//	// @Example u_1
//	// @Format uuid
//	ID string `json:"id"`
//
// @Description and @Example also accept the surrounding doc prose as a fallback, so a field
// documented in plain English carries that text into the schema. | @Description 与 @Example
// 之外，字段 doc 的正文也会作为说明兜底，因此纯文字说明的字段同样能把文本带进 Schema。
type FieldAnnotation struct {
	// Minimum is the @Minimum bound, nil when the directive is absent. | Minimum 为 @Minimum 下界，缺指令时为 nil。
	Minimum *float64
	// Maximum is the @Maximum bound, nil when the directive is absent. | Maximum 为 @Maximum 上界，缺指令时为 nil。
	Maximum *float64
	// Name is the JSON name the annotation applies to: the json tag name, or the Go field
	// name when untagged. It is also the key of SchemaAnnotation.Fields. | Name 为注解作用的
	// JSON 名：json 标签名，无标签时取 Go 字段名；同时是 SchemaAnnotation.Fields 的键。
	Name string
	// Description is the @Description text, falling back to the doc prose. | Description 为
	// @Description 文本，缺省时回退到 doc 正文。
	Description string
	// Example is the @Example text, kept verbatim. | Example 为 @Example 文本，原样保留。
	Example string
	// Format is the @Format value, for example uuid. | Format 为 @Format 取值，例如 uuid。
	Format string
	// Pattern is the @Pattern regular expression. | Pattern 为 @Pattern 正则表达式。
	Pattern string
	// Pos is the "file:line" of the field annotation, used for diagnostics. | Pos 为字段注解的
	// "文件:行号"，用于诊断。
	Pos string
	// Enum lists the @Enum values, which are space or comma separated. | Enum 列出 @Enum 取值，以空格或逗号分隔。
	Enum []string
	// Nullable reports whether @Nullable is present. | Nullable 报告是否出现 @Nullable。
	Nullable bool
	// Deprecated reports whether @Deprecated is present. | Deprecated 报告是否出现 @Deprecated。
	Deprecated bool
	// ReadOnly reports whether @ReadOnly is present. | ReadOnly 报告是否出现 @ReadOnly。
	ReadOnly bool
}

// SchemaAnnotation is the parsed form of the annotations attached to one struct type. | SchemaAnnotation
// 是单个结构体类型注解的解析结果。
//
// The understood dialect: | 可识别的方言：
//
//	// User is the account entity. | User 是账号实体。
//	//
//	// @Name User
//	A system user account | // @Description 系统用户账号
//	// @Example {"id":"u_1","name":"admin","status":"active"}
//	type User struct { ... }
type SchemaAnnotation struct {
	// Name is the @Name value, falling back to the Go type name. | Name 为 @Name 取值，缺省时
	// 回退到 Go 类型名。
	Name string
	// Description is the @Description text, falling back to the doc prose. | Description 为
	// @Description 文本，缺省时回退到 doc 正文。
	Description string
	// Example is the @Example text, kept verbatim as JSON or as a plain string. | Example 为
	// @Example 文本，作为 JSON 或普通字符串原样保留。
	Example string
	// Fields maps a field JSON name onto its annotation; fields without any annotation or
	// doc prose are absent. | Fields 把字段 JSON 名映射到其注解；无注解也无 doc 正文的字段不出现。
	Fields map[string]FieldAnnotation
	// Pos is the "file:line" of the struct annotation, used for diagnostics. | Pos 为结构体注解的
	// "文件:行号"，用于诊断。
	Pos string
}

// EnumValue is one value of an @Enum declaration. | EnumValue 是一条 @Enum 声明中的单个取值。
type EnumValue struct {
	// Value is the literal value, for example active or 1. | Value 为字面量取值，例如 active 或 1。
	Value string
	// Description is the "值:说明" tail, empty when the item has no description. | Description 为
	// "值:说明" 的尾部，无说明时为空。
	Description string
}

// EnumAnnotation is the parsed form of the annotations attached to one const block. | EnumAnnotation
// 是单个常量块注解的解析结果。
//
// The understood dialect: | 可识别的方言：
//
//	// Status values. | 状态取值。
//	//
//	// @Name UserStatus
//	Account status | // @Description 账号状态
//	// @Type string
//	// @Enum active:启用 | enabled, disabled:禁用 | disabled
//	const (
//		StatusActive   Status = "active"
//		StatusDisabled Status = "disabled"
//	)
//
// Without @Enum the values are inferred from the const literals: string, integer, float and
// boolean literals keep their value, iota becomes the spec index and an omitted expression
// list inherits the one above it. | 未写 @Enum 时，取值由 const 字面量推断：字符串、整数、浮点
// 与布尔字面量保留其取值，iota 按 spec 序号推断，省略的表达式列表沿用上一条。
type EnumAnnotation struct {
	// Name is the @Name value, falling back to the declared type name and then to the first
	// const name. | Name 为 @Name 取值，缺省时回退到声明的类型名，再回退到首个常量名。
	Name string
	// Description is the @Description text, falling back to the doc prose. | Description 为
	// @Description 文本，缺省时回退到 doc 正文。
	Description string
	// Type is the @Type value or the inferred literal kind: string, integer, number or
	// boolean. | Type 为 @Type 取值或推断出的字面量种类：string、integer、number 或 boolean。
	Type string
	// Pos is the "file:line" of the const block annotation, used for diagnostics. | Pos 为常量块
	// 注解的 "文件:行号"，用于诊断。
	Pos string
	// Values lists the enum values in source order. | Values 按源码顺序列出枚举取值。
	Values []EnumValue
}

// VarAnnotation is the parsed form of the annotations attached to one variable. | VarAnnotation
// 是单个变量注解的解析结果。
//
// The understood dialect: | 可识别的方言：
//
//	// DefaultUser is the seed account. | DefaultUser 是种子账号。
//	//
//	// @Name DefaultUser
//	// @Schema User
//	// @Example {"id":"u_1","name":"admin"}
//	var DefaultUser = User{ ... }
//
// The example is written back into the schema named by @Schema, which makes a variable the
// canonical place for a document wide sample value; a sample declared by the struct itself
// wins, and variables are visited in name order. | 示例会写回 @Schema 指向的 Schema，因此变量是
// 文档级示例值的规范位置；结构体自身声明的示例优先，多个变量按名字顺序处理。
type VarAnnotation struct {
	// Name is the @Name value, falling back to the Go variable name. | Name 为 @Name 取值，
	// 缺省时回退到 Go 变量名。
	Name string
	// Schema is the @Schema value, falling back to the declared or composite literal type. | Schema
	// 为 @Schema 取值，缺省时回退到声明的类型或复合字面量类型。
	Schema string
	// Description is the @Description text, falling back to the doc prose. | Description 为
	// @Description 文本，缺省时回退到 doc 正文。
	Description string
	// Example is the @Example text, kept verbatim. | Example 为 @Example 文本，原样保留。
	Example string
	// Pos is the "file:line" of the variable annotation, used for diagnostics. | Pos 为变量注解的
	// "文件:行号"，用于诊断。
	Pos string
}

// TypeAnnotations collects every struct, const block and variable annotation found in
// source. | TypeAnnotations 汇总源码中发现的全部结构体、常量块与变量注解。
//
// Schemas, Enums and Vars are keyed by the annotated name (@Name, or the Go name as a
// fallback), which is what AddTypeAnnotations matches against the component schemas. | Schemas、
// Enums 与 Vars 以注解名（@Name，缺省时取 Go 名）为键，AddTypeAnnotations 据此与组件
// Schema 匹配。
type TypeAnnotations struct {
	// Schemas maps a type name onto its struct annotation. | Schemas 把类型名映射到结构体注解。
	Schemas map[string]SchemaAnnotation
	// Enums maps a const block name onto its enum annotation. | Enums 把常量块名映射到枚举报注解。
	Enums map[string]EnumAnnotation
	// Vars maps a variable name onto its annotation. | Vars 把变量名映射到其注解。
	Vars map[string]VarAnnotation
}

// ParseTypeAnnotations walks dirs inside fsys, parses the struct, const block and variable
// annotations of non-test Go files with go/parser and go/ast, and returns them keyed by
// annotated name. | ParseTypeAnnotations 遍历 fsys 中 dirs 目录下的非测试 Go 文件，借助
// go/parser 与 go/ast 解析结构体、常量块与变量注解，并以注解名为键返回。
//
// Skipped: files ending in _test.go, and directories named vendor, node_modules or testdata,
// or whose name starts with "." or "_". Only declarations carrying at least one type level
// directive are collected, so plain prose docs never leak into the document. | 跳过：以
// _test.go 结尾的文件；名为 vendor、node_modules、testdata 或以 "."、"_" 开头的目录。
// 只有带至少一条类型级指令的声明才会被收集，纯文字的 doc 注释不会进入文档。
//
// The same name declared twice keeps the first occurrence, which keeps the result
// deterministic across files. | 同名声明以先出现者为准，保证结果在不同文件间是确定的。
func ParseTypeAnnotations(fsys afero.Fs, dirs []string) (*TypeAnnotations, error) {
	files, err := annotationFiles(fsys, dirs)
	if err != nil {
		return nil, err
	}
	types := &TypeAnnotations{
		Schemas: map[string]SchemaAnnotation{},
		Enums:   map[string]EnumAnnotation{},
		Vars:    map[string]VarAnnotation{},
	}
	fset := token.NewFileSet()
	for _, path := range files {
		src, err := afero.ReadFile(fsys, path)
		if err != nil {
			return nil, errx.WrapDetail(err, ErrRead, "%q", path)
		}
		file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			return nil, errx.WrapDetail(err, ErrTypeParse, "%q", path)
		}
		types.collectFile(fset, file)
	}
	return types, nil
}

// TypeNames returns the sorted, de-duplicated names of every annotated type: struct
// annotations and const block enums. | TypeNames 返回全部被注解类型的排序去重名：结构体注解与
// 常量块枚举。
func (t *TypeAnnotations) TypeNames() []string {
	if t == nil {
		return nil
	}
	names := make([]string, 0, len(t.Schemas)+len(t.Enums))
	for name := range t.Schemas {
		names = append(names, name)
	}
	for name := range t.Enums {
		names = append(names, name)
	}
	return mergeUniqueSorted(nil, names)
}

// collectFile records the annotated declarations of one parsed file. | collectFile 记录单个已解析
// 文件中带注解的声明。
func (t *TypeAnnotations) collectFile(fset *token.FileSet, file *ast.File) {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		switch gd.Tok {
		case token.TYPE:
			t.collectTypes(fset, gd)
		case token.CONST:
			t.collectConsts(fset, gd)
		case token.VAR:
			t.collectVars(fset, gd)
		}
	}
}

// collectTypes records the annotated struct types of one type declaration. | collectTypes
// 记录一条类型声明中带注解的结构体类型。
func (t *TypeAnnotations) collectTypes(fset *token.FileSet, gd *ast.GenDecl) {
	for _, spec := range gd.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok || ts.Name == nil {
			continue
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok || st.Fields == nil {
			continue
		}
		doc := newDocBlock(fset, declDoc(ts.Doc, gd))
		if !doc.annotated() {
			continue
		}
		sa := SchemaAnnotation{
			Name:        doc.first("name"),
			Description: doc.description(),
			Example:     doc.first("example"),
			Fields:      map[string]FieldAnnotation{},
			Pos:         doc.pos,
		}
		if sa.Name == "" {
			sa.Name = ts.Name.Name
		}
		if sa.Pos == "" {
			sa.Pos = posOf(fset, ts.Pos())
		}
		for _, field := range st.Fields.List {
			collectField(fset, sa.Fields, field)
		}
		if len(sa.Fields) == 0 {
			sa.Fields = nil
		}
		if _, dup := t.Schemas[sa.Name]; dup {
			continue
		}
		t.Schemas[sa.Name] = sa
	}
}

// collectConsts records the annotated const blocks of one const declaration. | collectConsts
// 记录一条常量声明中带注解的常量块。
func (t *TypeAnnotations) collectConsts(fset *token.FileSet, gd *ast.GenDecl) {
	doc := newDocBlock(fset, gd.Doc)
	if !doc.annotated() {
		return
	}
	values, inferred := inferredEnumValues(fset, gd)
	if items := doc.first("enum"); items != "" {
		values = parseEnumItems(items)
	}
	ea := EnumAnnotation{
		Name:        doc.first("name"),
		Description: doc.description(),
		Type:        doc.first("type"),
		Values:      values,
		Pos:         doc.pos,
	}
	if ea.Type == "" {
		ea.Type = inferred
	}
	if ea.Name == "" {
		ea.Name = constBlockName(gd)
	}
	if ea.Name == "" {
		return
	}
	if ea.Pos == "" {
		ea.Pos = posOf(fset, gd.Pos())
	}
	if _, dup := t.Enums[ea.Name]; dup {
		return
	}
	t.Enums[ea.Name] = ea
}

// collectVars records the annotated variables of one var declaration. | collectVars 记录一条
// 变量声明中带注解的变量。
func (t *TypeAnnotations) collectVars(fset *token.FileSet, gd *ast.GenDecl) {
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || len(vs.Names) == 0 {
			continue
		}
		doc := newDocBlock(fset, declDoc(vs.Doc, gd))
		if !doc.annotated() {
			continue
		}
		va := VarAnnotation{
			Name:        doc.first("name"),
			Schema:      doc.first("schema"),
			Description: doc.description(),
			Example:     doc.first("example"),
			Pos:         doc.pos,
		}
		if va.Name == "" {
			va.Name = vs.Names[0].Name
		}
		if va.Schema == "" {
			va.Schema = varSchemaName(vs)
		}
		if va.Pos == "" {
			va.Pos = posOf(fset, vs.Pos())
		}
		if _, dup := t.Vars[va.Name]; dup {
			continue
		}
		t.Vars[va.Name] = va
	}
}

// collectField records the annotation of one struct field; embedded and unexported fields
// are skipped, exactly as the reflection based schema generator skips them. | collectField
// 记录单个结构体字段的注解；匿名内嵌与未导出字段会被跳过，与基于反射的 Schema 生成器一致。
func collectField(fset *token.FileSet, out map[string]FieldAnnotation, field *ast.Field) {
	if field == nil || len(field.Names) == 0 {
		return
	}
	doc := newDocBlock(fset, field.Doc, field.Comment)
	if !doc.annotated() && len(doc.prose) == 0 {
		return
	}
	single := len(field.Names) == 1
	for _, ident := range field.Names {
		if ident == nil || !ast.IsExported(ident.Name) {
			continue
		}
		name := ident.Name
		if single {
			if name = wireFieldName(field, ident.Name); name == "" {
				continue
			}
		}
		if _, dup := out[name]; dup {
			continue
		}
		fa := FieldAnnotation{
			Name:        name,
			Description: doc.description(),
			Example:     doc.first("example"),
			Format:      doc.first("format"),
			Enum:        enumTokens(doc),
			Pattern:     doc.first("pattern"),
			Nullable:    doc.flag("nullable"),
			Deprecated:  doc.flag("deprecated"),
			ReadOnly:    doc.flag("readonly"),
			Pos:         doc.pos,
		}
		if fa.Pos == "" {
			fa.Pos = posOf(fset, ident.Pos())
		}
		if v, ok := directiveFloat(doc.first("minimum")); ok {
			fa.Minimum = &v
		}
		if v, ok := directiveFloat(doc.first("maximum")); ok {
			fa.Maximum = &v
		}
		out[name] = fa
	}
}

// inferredEnumValues infers the enum values of a const block from its literals and reports
// the inferred JSON type. | inferredEnumValues 从 const 块的字面量推断枚举取值，并返回推断出的
// JSON 类型。
func inferredEnumValues(fset *token.FileSet, gd *ast.GenDecl) ([]EnumValue, string) {
	var (
		out   []EnumValue
		known = map[string]constLiteral{}
		last  []ast.Expr
		iota  int
		kinds = map[string]bool{}
	)
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		exprs := vs.Values
		if len(exprs) == 0 {
			// 省略表达式列表等价于重复上一条列表，但 iota 仍按当前序号取值。
			// An omitted expression list repeats the one above, while iota still takes the current index.
			exprs = last
		} else {
			last = vs.Values
		}
		prose := strings.Join(newDocBlock(fset, vs.Doc).prose, "\n")
		for i, ident := range vs.Names {
			if i >= len(exprs) || ident.Name == "_" {
				continue
			}
			lit, ok := literalOf(exprs[i], iota, known)
			if !ok {
				continue
			}
			known[ident.Name] = lit
			kinds[lit.kind] = true
			out = append(out, EnumValue{Value: lit.value, Description: prose})
		}
		iota++
	}
	return out, singleKind(kinds)
}

// constLiteral is one literal value extracted from a const spec. | constLiteral 是从 const spec
// 中提取的单个字面量取值。
type constLiteral struct {
	// value is the literal text without quotes. | value 为去掉引号的字面量文本。
	value string
	// kind is string, integer, number or boolean. | kind 取 string、integer、number 或 boolean。
	kind string
}

// literalOf resolves one const expression into a literal, following iota and the const
// names already seen in the same block. | literalOf 把单个常量表达式解析为字面量，支持 iota 与
// 同一块内已出现的常量名。
func literalOf(expr ast.Expr, iota int, known map[string]constLiteral) (constLiteral, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		switch e.Kind {
		case token.STRING:
			if v, err := strconv.Unquote(e.Value); err == nil {
				return constLiteral{value: v, kind: "string"}, true
			}
		case token.INT:
			return constLiteral{value: e.Value, kind: "integer"}, true
		case token.FLOAT:
			return constLiteral{value: e.Value, kind: "number"}, true
		case token.CHAR:
			if v, err := strconv.Unquote(e.Value); err == nil {
				return constLiteral{value: v, kind: "string"}, true
			}
		}
	case *ast.Ident:
		switch {
		case e.Name == "iota":
			return constLiteral{value: strconv.Itoa(iota), kind: "integer"}, true
		case e.Name == "true" || e.Name == "false":
			return constLiteral{value: e.Name, kind: "boolean"}, true
		}
		if lit, ok := known[e.Name]; ok {
			return lit, true
		}
	case *ast.UnaryExpr:
		if e.Op == token.SUB {
			if lit, ok := literalOf(e.X, iota, known); ok {
				return constLiteral{value: "-" + lit.value, kind: lit.kind}, true
			}
		}
	}
	return constLiteral{}, false
}

// singleKind returns the only kind seen, or "" when the kinds are mixed. | singleKind
// 返回唯一出现的种类，种类混合时返回 ""。
func singleKind(kinds map[string]bool) string {
	if len(kinds) != 1 {
		return ""
	}
	for kind := range kinds {
		return kind
	}
	return ""
}

// constBlockName derives the name of a const block: the declared type name of the first
// spec, or the first const name when the block is untyped. | constBlockName 推导常量块名：
// 首个 spec 声明的类型名，无类型时取首个常量名。
func constBlockName(gd *ast.GenDecl) string {
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || len(vs.Names) == 0 {
			continue
		}
		if vs.Type != nil {
			if name := typeExprName(vs.Type); name != "" {
				return name
			}
		}
		return vs.Names[0].Name
	}
	return ""
}

// varSchemaName derives the schema a variable example belongs to: the declared type, or
// the composite literal type of its value. | varSchemaName 推导变量示例所属的 Schema：声明的
// 类型，或取值的复合字面量类型。
func varSchemaName(vs *ast.ValueSpec) string {
	if vs.Type != nil {
		return typeExprName(vs.Type)
	}
	for _, value := range vs.Values {
		if lit, ok := value.(*ast.CompositeLit); ok {
			return typeExprName(lit.Type)
		}
	}
	return ""
}

// typeExprName renders a type expression as a name, keeping a package qualifier. | typeExprName
// 把类型表达式渲染为名字，保留包限定名。
func typeExprName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return typeExprName(t.X)
	case *ast.SelectorExpr:
		pkg := typeExprName(t.X)
		if pkg == "" || t.Sel == nil {
			return ""
		}
		return pkg + "." + t.Sel.Name
	case *ast.IndexExpr:
		return typeExprName(t.X)
	case *ast.IndexListExpr:
		return typeExprName(t.X)
	case *ast.ParenExpr:
		return typeExprName(t.X)
	default:
		return ""
	}
}

// parseEnumItems parses the comma separated items of an "@Enum" directive; every item is a
// value, optionally followed by ":description". | parseEnumItems 解析 "@Enum" 指令中逗号分隔的
// 各项；每项是一个取值，可选地以 ":说明" 结尾。
func parseEnumItems(items string) []EnumValue {
	var out []EnumValue
	for _, item := range strings.Split(items, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		value, description, _ := strings.Cut(item, ":")
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, EnumValue{Value: value, Description: strings.TrimSpace(description)})
	}
	return out
}

// wireFieldName returns the JSON name of a struct field: the json tag name, the Go name
// when untagged, and "" for a json:"-" field. | wireFieldName 返回结构体字段的 JSON 名：
// json 标签名；无标签时取 Go 名；json:"-" 字段返回 ""。
func wireFieldName(field *ast.Field, goName string) string {
	tag := astFieldTag(field)
	if tag == "" {
		return goName
	}
	raw, ok := reflect.StructTag(tag).Lookup("json")
	if !ok {
		return goName
	}
	name, _, _ := strings.Cut(raw, ",")
	switch name {
	case "-":
		return ""
	case "":
		return goName
	}
	return name
}

// astFieldTag returns the struct tag literal of a field without its quotes. | astFieldTag
// 返回字段的结构体标签字面量并去掉引号。
func astFieldTag(field *ast.Field) string {
	if field == nil || field.Tag == nil {
		return ""
	}
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return strings.Trim(field.Tag.Value, "`\"")
	}
	return raw
}

// declDoc returns the doc comment of a spec, falling back to the declaration comment when
// the declaration holds a single spec; that fallback mirrors how go/ast attaches the comment
// of "type X struct{...}" style declarations. | declDoc 返回 spec 的 doc 注释；声明只含单个
// spec 时回退到声明的注释。该回退与 go/ast 对 "type X struct{...}" 形式注释的挂载方式一致。
func declDoc(spec *ast.CommentGroup, gd *ast.GenDecl) *ast.CommentGroup {
	if spec != nil {
		return spec
	}
	if gd != nil && len(gd.Specs) == 1 {
		return gd.Doc
	}
	return nil
}

// docBlock splits the comment lines of a declaration into directives and prose. | docBlock
// 把声明上的注释行拆分为指令与正文。
type docBlock struct {
	// directives maps a lower cased directive name to its verbatim arguments. | directives
	// 把小写指令名映射到原样参数。
	directives map[string][]string
	// pos is the "file:line" of the comment block. | pos 为注释块的 "文件:行号"。
	pos string
	// prose lists the non-directive lines, used as the description fallback. | prose 列出非指令行，
	// 作为描述的兜底。
	prose []string
}

// newDocBlock splits the given comment groups into a docBlock. | newDocBlock 把给定的注释组拆分为 docBlock。
func newDocBlock(fset *token.FileSet, groups ...*ast.CommentGroup) docBlock {
	db := docBlock{directives: map[string][]string{}}
	for _, group := range groups {
		if group == nil {
			continue
		}
		if db.pos == "" {
			db.pos = posOf(fset, group.Pos())
		}
		for _, line := range commentLines(group) {
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "@") {
				db.prose = append(db.prose, line)
				continue
			}
			name, args := splitDirective(line[1:])
			db.directives[name] = append(db.directives[name], args)
		}
	}
	return db
}

// annotated reports whether the block carries at least one type level directive. | annotated
// 报告注释块是否至少含一条类型级指令。
func (d docBlock) annotated() bool {
	for name := range d.directives {
		if isTypeDirective(name) {
			return true
		}
	}
	return false
}

// first returns the first non-empty argument of a directive. | first 返回某指令的首个非空参数。
func (d docBlock) first(name string) string {
	for _, value := range d.directives[name] {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// joined returns the non-empty arguments of a directive joined with newlines, so multiple
// @Description lines produce a multi line description. | joined 用换行连接某指令的非空参数，
// 因此多行 @Description 会得到多行描述。
func (d docBlock) joined(name string) string {
	var parts []string
	for _, value := range d.directives[name] {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n")
}

// description returns the @Description lines, falling back to the prose of the block. | description
// 返回 @Description 各行，缺省时回退到注释块正文。
func (d docBlock) description() string {
	if value := d.joined("description"); value != "" {
		return value
	}
	return strings.Join(d.prose, "\n")
}

// flag reports whether a valueless directive such as @Nullable is present. | flag 报告
// @Nullable 之类无参指令是否出现。
func (d docBlock) flag(name string) bool {
	_, ok := d.directives[name]
	return ok
}

// enumTokens returns the @Enum tokens of a field, nil when the directive is absent, so a
// field without @Enum stays distinguishable from one annotated with an empty list. | enumTokens
// 返回字段的 @Enum 标记；指令缺失时为 nil，从而与"注解了空列表"的字段区分开。
func enumTokens(d docBlock) []string {
	if _, ok := d.directives["enum"]; !ok {
		return nil
	}
	return splitTokens(d.first("enum"))
}

// commentLines returns the trimmed text of every comment line of a group. | commentLines
// 返回注释组中每一行的去空白文本。
func commentLines(group *ast.CommentGroup) []string {
	if group == nil {
		return nil
	}
	var out []string
	for _, comment := range group.List {
		text := comment.Text
		switch {
		case strings.HasPrefix(text, "//"):
			text = text[2:]
		case strings.HasPrefix(text, "/*"):
			text = strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
		}
		for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
			out = append(out, line)
		}
	}
	return out
}

// directiveFloat parses the numeric argument of @Minimum or @Maximum; ok is false when the
// argument is missing or not a number. | directiveFloat 解析 @Minimum 或 @Maximum 的数值参数；
// 参数缺失或不是数字时 ok 为 false。
func directiveFloat(args string) (float64, bool) {
	text := strings.TrimSpace(args)
	if text == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// posOf renders a token position as "file:line". | posOf 把 token 位置渲染为 "文件:行号"。
func posOf(fset *token.FileSet, pos token.Pos) string {
	if fset == nil || !pos.IsValid() {
		return ""
	}
	position := fset.Position(pos)
	if position.Filename == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", position.Filename, position.Line)
}
