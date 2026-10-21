// 本文件实现「从已存在的 Go 源码推导模型」：`yd gen crud --struct` 走的就是这条路径。
// This file derives a model from existing Go source, which is the path taken by `yd gen crud --struct`.

package gen

import (
	"go/ast"
	"go/parser"
	"go/token"
	pathpkg "path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/core/errx"
)

// gormModelName 是 gorm.Model 的限定名；内嵌它等价于内嵌主键与两个时间戳列。
// gormModelName is the qualified name of gorm.Model; embedding it is equivalent to
// embedding the primary key and the two timestamp columns.
const gormModelName = "gorm.Model"

// ModelFromSource 从 Go 源码推导模型：读取结构体字段上的 gorm / json / yd 三种标签。
// ModelFromSource derives a model from Go source by reading the gorm, json and yd tags of a struct's fields.
//
// path 可以是目录，也可以是单个 .go 文件；typeName 是要读取的结构体名。
// 目录下的每个非测试 .go 文件都会被解析，因此同一个包里的具名类型与匿名的内嵌结构体都能被展开
// （内嵌 gorm.Model 按框架约定展开为主键与时间戳列）。未导出字段、gorm:"-" 标记的字段以及
// 切片/映射/接口/自定义结构体等非列类型会被跳过，与 ModelFromStruct 的规则一致。
// path is either a directory or a single .go file, and typeName is the struct to read.
// Every non-test .go file of that directory is parsed, so named types of the same package
// and anonymous embedded structs can be flattened (an embedded gorm.Model expands into the
// primary key and the timestamp columns, following the framework convention). Unexported
// fields, fields tagged gorm:"-" and non-column types such as slices, maps, interfaces and
// custom structs are skipped, exactly as in ModelFromStruct.
func ModelFromSource(fs afero.Fs, path, typeName string, o StructOptions) (*Model, error) {
	if fs == nil {
		return nil, ErrNilFS
	}
	typeName = strings.TrimSpace(typeName)
	if typeName == "" {
		return nil, errx.Detail(ErrBadModel, "empty struct name | 结构体名为空")
	}

	pkg, err := loadSourcePackage(fs, path)
	if err != nil {
		return nil, err
	}
	if pkg.types[typeName] == nil {
		return nil, errx.Detail(ErrTypeNotFound, "type=%[1]s path=%[2]s | 类型=%[1]s 路径=%[2]s", typeName, path)
	}
	if _, ok := pkg.types[typeName].(*ast.StructType); !ok {
		return nil, errx.Detail(ErrTypeNotFound, "type=%s is not a struct | %s 不是结构体类型", typeName, typeName)
	}

	fields, err := pkg.collectFields(typeName, map[string]bool{typeName: true}, nil)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, ErrNoFields
	}

	name := strings.TrimSpace(o.Name)
	if name == "" {
		name = exportedName(typeName)
	}
	table := strings.TrimSpace(o.Table)
	if table == "" {
		table = pkg.tableName(typeName)
	}
	if table == "" {
		table = pluralize(snakeCase(name))
	}

	m := &Model{Name: name, GoName: name, Table: table, Module: o.Module, Fields: fields}
	m.Timestamps = hasTimestampFields(fields)
	if o.Timestamps != nil {
		m.Timestamps = *o.Timestamps
	}
	if err := m.prepare(); err != nil {
		return nil, err
	}
	return m, nil
}

// sourcePackage 是一次源码解析的结果：已解析的文件与同包内具名类型的声明。
// sourcePackage is the result of one source parse: the parsed files and the named type declarations of the package.
type sourcePackage struct {
	// types maps every named type onto its type expression. | types 把具名类型映射到它的类型表达式。
	types map[string]ast.Expr
	// files are the parsed files, sorted by file name. | files 是按文件名排序的已解析文件。
	files []*ast.File
}

// loadSourcePackage 解析 path 下的 Go 源码；path 是文件时只解析该文件。
// loadSourcePackage parses the Go source under path; when path is a file only that file is parsed.
func loadSourcePackage(fs afero.Fs, path string) (*sourcePackage, error) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return nil, errx.Detail(ErrBadModel, "empty source path | 源码路径为空")
	}

	names, err := sourceFiles(fs, path)
	if err != nil {
		return nil, err
	}

	pkg := &sourcePackage{types: make(map[string]ast.Expr, 8)}
	fset := token.NewFileSet()
	for _, name := range names {
		src, err := afero.ReadFile(fs, name)
		if err != nil {
			return nil, errx.WrapDetail(err, ErrReadFile, "path=%[1]s | 路径=%[1]s", name)
		}
		// 解析失败说明源码本身不合法，属于调用方的输入问题而非生成器故障。
		// A parse failure means the source itself is invalid, which is the caller's input problem rather than a generator fault.
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			return nil, errx.WrapDetail(err, ErrBadModel, "path=%[1]s | 路径=%[1]s", name)
		}
		pkg.files = append(pkg.files, f)
		pkg.declare(f)
	}
	if len(pkg.types) == 0 {
		return nil, errx.Detail(ErrTypeNotFound, "no Go type found in %s | %s 下没有找到任何 Go 类型", path, path)
	}
	return pkg, nil
}

// sourceFiles 展开 path：目录取其中全部非测试 .go 文件，文件则原样返回。
// sourceFiles expands path: a directory yields every non-test .go file in it, a file is returned as is.
func sourceFiles(fs afero.Fs, path string) ([]string, error) {
	info, err := fs.Stat(path)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrReadFile, "path=%[1]s | 路径=%[1]s", path)
	}
	if !info.IsDir() {
		return []string{path}, nil
	}

	entries, err := afero.ReadDir(fs, path)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrReadFile, "path=%[1]s | 路径=%[1]s", path)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		names = append(names, pathpkg.Join(path, name))
	}
	if len(names) == 0 {
		return nil, errx.Detail(ErrTypeNotFound, "no Go source file in %s | %s 下没有 Go 源码文件", path, path)
	}
	// 目录内容的顺序由文件系统决定，排序后解析结果与文件系统无关。
	// Directory order is up to the file system, so sorting keeps the parse result file-system independent.
	sort.Strings(names)
	return names, nil
}

// declare 记录一个文件里声明的具名类型。
// declare records the named types declared in one file.
func (p *sourcePackage) declare(f *ast.File) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil {
				continue
			}
			if _, dup := p.types[ts.Name.Name]; dup {
				continue
			}
			p.types[ts.Name.Name] = ts.Type
		}
	}
}

// collectFields 收集结构体的列字段，展开匿名的内嵌结构体。
// collectFields collects the column fields of a struct, flattening anonymous embedded structs.
func (p *sourcePackage) collectFields(typeName string, seen map[string]bool, out []Field) ([]Field, error) {
	st, ok := p.types[typeName].(*ast.StructType)
	if !ok {
		return nil, errx.Detail(ErrBadModel, "type=%s is not a struct | %s 不是结构体类型", typeName, typeName)
	}

	for _, f := range st.Fields.List {
		tag := structTagOf(f)
		gormTag := tag.Get("gorm")
		if gormIgnored(gormTag) {
			continue
		}

		if len(f.Names) == 0 {
			// 匿名内嵌字段：同包的结构体展开，gorm.Model 按框架约定展开。
			// An anonymous embedded field: structs of the same package are flattened and gorm.Model expands per framework convention.
			switch embedded := embeddedTypeName(f.Type); {
			case embedded == gormModelName:
				out = append(out, gormModelFields()...)
			case p.isStruct(embedded) && !seen[embedded]:
				seen[embedded] = true
				next, err := p.collectFields(embedded, seen, out)
				if err != nil {
					return nil, err
				}
				out = next
			}
			continue
		}

		goType, isColumn := p.columnType(f.Type)
		if !isColumn {
			continue
		}
		for _, name := range f.Names {
			if !ast.IsExported(name.Name) {
				continue
			}
			out = append(out, fieldFromTags(name.Name, goType, tag))
		}
	}
	return out, nil
}

// isStruct 报告同包内是否存在该名字的结构体类型。
// isStruct reports whether the package declares a struct type with that name.
func (p *sourcePackage) isStruct(name string) bool {
	if name == "" {
		return false
	}
	_, ok := p.types[name].(*ast.StructType)
	return ok
}

// columnType 把一个字段的类型表达式映射到 Go 类型名；返回 false 表示该字段不是列。
// columnType maps a field's type expression onto a Go type name; false means the field is not a column.
func (p *sourcePackage) columnType(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.Ident:
		switch t.Name {
		case "string", "int", "int64", "int32", "int16", "int8",
			"uint", "uint64", "uint32", "uint16", "uint8", "byte",
			"bool", "float64", "float32", "rune":
			return normaliseBuiltin(t.Name), true
		}
		if e, ok := p.types[t.Name]; ok {
			// 同包具名类型：结构体与接口不是列，其余按别名继续推导。
			// A named type of the same package: structs and interfaces are not columns, everything else is followed as an alias.
			switch e.(type) {
			case *ast.StructType, *ast.InterfaceType:
				return "", false
			}
			return p.columnType(e)
		}
		// 外部具名类型无法在源码层面判定，按字符串列处理。
		// An external named type cannot be judged from the source, so it is treated as a string column.
		return "string", true
	case *ast.StarExpr:
		return p.columnType(t.X)
	case *ast.ArrayType:
		if elt, ok := t.Elt.(*ast.Ident); ok && (elt.Name == "byte" || elt.Name == "uint8") && t.Len == nil {
			return "[]byte", true
		}
		return "", false
	case *ast.SelectorExpr:
		if embeddedTypeName(t) == typeTime {
			return typeTime, true
		}
		return "", false
	default:
		return "", false
	}
}

// tableName 读取结构体的 TableName 方法返回的字面量表名，没有该方法时返回空串。
// tableName reads the literal table name returned by the struct's TableName method, or "" when the method is absent.
func (p *sourcePackage) tableName(typeName string) string {
	for _, f := range p.files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Name.Name != "TableName" || fd.Body == nil || fd.Recv == nil || len(fd.Recv.List) == 0 {
				continue
			}
			if embeddedTypeName(fd.Recv.List[0].Type) != typeName {
				continue
			}
			if lit := firstStringLiteral(fd.Body); lit != "" {
				return lit
			}
		}
	}
	return ""
}

// firstStringLiteral 返回函数体里第一个字符串字面量的取值。
// firstStringLiteral returns the value of the first string literal in a function body.
func firstStringLiteral(body *ast.BlockStmt) string {
	var found string
	ast.Inspect(body, func(n ast.Node) bool {
		if found != "" {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if v, err := strconv.Unquote(lit.Value); err == nil {
			found = strings.TrimSpace(v)
		}
		return false
	})
	return found
}

// embeddedTypeName 返回内嵌或接收者类型表达式的名字："Post" 或 "gorm.Model"。
// embeddedTypeName returns the name of an embedded or receiver type expression: "Post" or "gorm.Model".
func embeddedTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return embeddedTypeName(t.X)
	case *ast.SelectorExpr:
		pkg := embeddedTypeName(t.X)
		if pkg == "" || t.Sel == nil {
			return ""
		}
		return pkg + "." + t.Sel.Name
	default:
		return ""
	}
}

// structTagOf 读取字段上的结构体标签字面量。
// structTagOf reads the struct tag literal of a field.
func structTagOf(f *ast.Field) reflect.StructTag {
	if f == nil || f.Tag == nil {
		return ""
	}
	raw, err := strconv.Unquote(f.Tag.Value)
	if err != nil {
		raw = strings.Trim(f.Tag.Value, "`\"")
	}
	return reflect.StructTag(raw)
}

// normaliseBuiltin 归并基础类型的别名：byte 等价 uint8、rune 等价 int32。
// normaliseBuiltin folds builtin aliases: byte equals uint8 and rune equals int32.
func normaliseBuiltin(name string) string {
	switch name {
	case "byte":
		return "uint8"
	case "rune":
		return "int32"
	default:
		return name
	}
}

// gormModelFields 返回内嵌 gorm.Model 展开出的字段，列名与框架生成的模型保持一致。
// gormModelFields returns the fields that an embedded gorm.Model expands into, with the same columns the framework generates.
func gormModelFields() []Field {
	return []Field{
		fieldFromTags("ID", "int64", `gorm:"column:id;primaryKey" json:"id"`),
		fieldFromTags("CreatedAt", typeTime, `gorm:"column:created_at" json:"created_at"`),
		fieldFromTags("UpdatedAt", typeTime, `gorm:"column:updated_at" json:"updated_at"`),
		fieldFromTags("DeletedAt", "gorm.DeletedAt", `gorm:"column:deleted_at;index" json:"deleted_at"`),
	}
}
