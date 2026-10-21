package gen

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/shibingli/yd/core/errx"
)

// 字段描述串支持的类型标记；左侧是用户输入，右侧是生成的 Go 类型与默认控件。
// Type tokens accepted by a field specification; the left side is user input, the right side is the generated Go type and default widget.
var specTypes = map[string]struct {
	goType string
	widget string
}{
	"string":   {"string", "text"},
	"text":     {"string", "textarea"},
	"file":     {"string", "file"},
	"int":      {"int64", "number"},
	"int64":    {"int64", "number"},
	"bool":     {"bool", "checkbox"},
	"float":    {"float64", "number"},
	"float64":  {"float64", "number"},
	"time":     {typeTime, "date"},
	"date":     {typeTime, "date"},
	"datetime": {typeTime, "date"},
}

// ParseFields 解析 "name:string:required,age:int,avatar:file" 形式的字段描述串。
// ParseFields parses a field specification shaped like "name:string:required,age:int,avatar:file".
//
// 以逗号分隔字段，每段形如 名称:类型[:修饰符...]；不含冒号的段视为上一字段的续写修饰符，
// 因此 "title:string:required,max=64" 会把 max=64 并入 title 的校验规则。
// Fields are separated by commas, each shaped name:type[:modifiers]; a segment
// without a colon continues the previous field, so "title:string:required,max=64"
// folds max=64 into the rules of title.
//
// 修饰符 primary/required/unique/index/nullable 映射到列标记或校验规则，
// key=value 形式的 max/min/size/label/widget/comment/column/json 有专门含义，
// 其余 key=value 原样并入校验规则。
// The modifiers primary/required/unique/index/nullable map onto column flags or
// rules; the key=value forms max/min/size/label/widget/comment/column/json have
// dedicated meanings; any other key=value falls through into the rule list.
func ParseFields(spec string) ([]Field, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, ErrBadFieldSpec
	}

	fields := make([]Field, 0, 8)
	for _, raw := range strings.Split(spec, ",") {
		seg := strings.TrimSpace(raw)
		if seg == "" {
			return nil, ErrBadFieldSpec
		}

		if !strings.Contains(seg, ":") {
			// A modifier continuing the previous field. | 续写上一字段的修饰符。
			if len(fields) == 0 {
				return nil, errx.Detail(ErrBadFieldSpec, "modifier %q has no field to attach to | 修饰符 %q 没有可归属的字段", seg, seg)
			}
			applySpecModifier(&fields[len(fields)-1], seg)
			continue
		}

		name, rest, _ := strings.Cut(seg, ":")
		name = strings.TrimSpace(name)
		typeName, modifiers, _ := strings.Cut(rest, ":")
		typeName = strings.TrimSpace(typeName)
		if name == "" {
			return nil, errx.Detail(ErrBadFieldSpec, "missing field name in %q | %q 缺少字段名", seg, seg)
		}
		if typeName == "" {
			return nil, errx.Detail(ErrBadFieldSpec, "missing field type in %q | %q 缺少字段类型", seg, seg)
		}

		mapped, ok := specTypes[strings.ToLower(typeName)]
		if !ok {
			return nil, errx.Detail(ErrUnknownType, "unknown type %q for field %s | 字段 %s 的类型 %q 未知",
				typeName, name, name, typeName)
		}

		f := Field{
			Name:     name,
			GoName:   exportedName(name),
			GoType:   mapped.goType,
			Column:   snakeCase(name),
			JSONName: snakeCase(name),
			Widget:   mapped.widget,
		}
		f.Label = f.GoName
		if f.Column == "" {
			f.Column = name
			f.JSONName = name
		}
		for _, mod := range strings.Split(modifiers, ":") {
			if mod = strings.TrimSpace(mod); mod != "" {
				applySpecModifier(&f, mod)
			}
		}
		fields = append(fields, f)
	}

	if len(fields) == 0 {
		return nil, ErrBadFieldSpec
	}
	return fields, nil
}

// applySpecModifier 应用单个修饰符到字段上。
// applySpecModifier applies one modifier to a field.
func applySpecModifier(f *Field, mod string) {
	key, value, hasValue := strings.Cut(mod, "=")
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if !hasValue {
		switch key {
		case "primary":
			f.Primary = true
			f.Nullable = false
		case "required":
			f.Validate = joinRule(f.Validate, "required")
		case "unique":
			f.Unique = true
		case "index":
			f.Indexed = true
		case "nullable", "null":
			f.Nullable = true
		default:
			f.Validate = joinRule(f.Validate, mod)
		}
		return
	}

	switch key {
	case "required":
		f.Validate = joinRule("required", value)
	case "max":
		f.Validate = joinRule(f.Validate, "max="+value)
		f.Size = atoiOrZero(value)
	case "size":
		f.Size = atoiOrZero(value)
	case "min":
		f.Validate = joinRule(f.Validate, "min="+value)
	case "label":
		f.Label = value
	case "widget":
		f.Widget = value
	case "comment":
		f.Comment = value
	case "column":
		f.Column = value
	case "json":
		f.JSONName = value
	case "validate":
		for _, rule := range strings.Split(value, ",") {
			f.Validate = joinRule(f.Validate, rule)
		}
	default:
		f.Validate = joinRule(f.Validate, mod)
	}
}

// ModelFromFields 依据字段清单构造模型，与 ModelFromStruct / ModelFromTable 并列。
// ModelFromFields builds a model from an already parsed field list, sibling to
// ModelFromStruct and ModelFromTable.
//
// name 是用户书写的模块名（"post" 与 "Post" 皆可），类型名取导出形式、表名取复数蛇形；
// o 里的取值非空时覆盖推导结果。它正是 `yd gen module --fields` 走的那一步：
// ParseFields 之后把字段交到这里，即可得到一个可直接生成的模型。
// name is the module name as the user writes it ("post" and "Post" both work); the
// type name is its exported form and the table name its plural snake form, while any
// non-empty value in o overrides the derived one. This is the very step taken by
// `yd gen module --fields`: hand the parsed fields over and a generatable model comes back.
func ModelFromFields(name string, fields []Field, o ModelOptions) (*Model, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errx.Detail(ErrBadModel, "empty model name | 模型名为空")
	}
	if len(fields) == 0 {
		return nil, ErrNoFields
	}

	goName := exportedName(name)
	if o.Name != "" {
		goName = exportedName(o.Name)
	}
	table := strings.TrimSpace(o.Table)
	if table == "" {
		table = pluralize(snakeCase(goName))
	}

	m := &Model{Name: goName, GoName: goName, Table: table, Module: o.Module, Fields: fields}
	m.Timestamps = true
	if o.Timestamps != nil {
		m.Timestamps = *o.Timestamps
	}
	if err := m.prepare(); err != nil {
		return nil, err
	}
	return m, nil
}

// ModelOptions 覆盖从字段清单构造模型时的自动取值，字段含义与 StructOptions 一致。
// ModelOptions overrides the values derived when building a model from a field list;
// every field means the same as its StructOptions counterpart.
type ModelOptions struct {
	// Timestamps 显式开关 CreatedAt / UpdatedAt 字段，nil 表示开启。
	// Timestamps explicitly toggles the CreatedAt / UpdatedAt fields; nil means enabled.
	Timestamps *bool
	// Name 覆盖模型名（导出形式），为空时取 name 参数的导出形式。
	// Name overrides the model name (exported form); when empty the exported form of the name argument is used.
	Name string
	// Table 覆盖表名，为空时取模型名的复数蛇形。
	// Table overrides the table name; when empty the plural snake form of the model name is used.
	Table string
	// Module 是业务模块名，用作 OpenAPI 的 @Tags。
	// Module is the business module name used as the OpenAPI @Tags value.
	Module string
}

// ModelFromTable 读取数据库表的列元数据并推导模型。
// ModelFromTable reads the column metadata of a database table and derives a model.
//
// 列名、类型、可空、长度、主键、唯一与注释来自 gorm 的 Migrator().ColumnTypes，
// 因此在 sqlite / mysql / postgres 上的行为一致；主键列一律视为不可空。
// Column names, types, nullability, length, primary key, uniqueness and comments
// come from gorm's Migrator().ColumnTypes, so the behaviour is identical on
// sqlite, mysql and postgres; primary key columns are always treated as not null.
func ModelFromTable(ctx context.Context, db *gorm.DB, table string, o TableOptions) (*Model, error) {
	if db == nil {
		return nil, ErrNilDB
	}
	table = strings.TrimSpace(table)
	if table == "" {
		return nil, errx.Detail(ErrTableNotFound, "empty table name | 表名为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	columns, err := db.WithContext(ctx).Migrator().ColumnTypes(table)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrTableNotFound, "table=%[1]s | 表=%[1]s", table)
	}
	if len(columns) == 0 {
		return nil, errx.Detail(ErrTableNotFound, "table=%[1]s | 表=%[1]s", table)
	}

	ignored := make(map[string]bool, len(o.Ignore))
	for _, name := range o.Ignore {
		ignored[strings.ToLower(strings.TrimSpace(name))] = true
	}

	fields := make([]Field, 0, len(columns))
	for _, col := range columns {
		name := strings.TrimSpace(col.Name())
		if name == "" || ignored[strings.ToLower(name)] {
			continue
		}

		dbType := strings.ToUpper(strings.TrimSpace(col.DatabaseTypeName()))
		f := Field{
			Name:     name,
			GoName:   exportedName(name),
			GoType:   goTypeForDB(dbType),
			Column:   name,
			JSONName: name,
			Size:     sizeFromDBType(dbType),
		}
		// GORM 的 ColumnType 允许驱动不表态，第二个返回值报告标记是否可用。
		// GORM's ColumnType lets a driver stay silent; the second value reports whether the flag is meaningful.
		if primary, ok := col.PrimaryKey(); ok {
			f.Primary = primary
		}
		if unique, ok := col.Unique(); ok {
			f.Unique = unique
		}
		if n, ok := col.Nullable(); ok {
			f.Nullable = n
		} else {
			f.Nullable = true
		}
		if f.Primary {
			// sqlite 的整型主键在 DDL 中不带 NOT NULL，这里统一按不可空处理。
			// An integer primary key in sqlite carries no NOT NULL in its DDL, so primary keys are always treated as not null.
			f.Nullable = false
		}
		if f.Size == 0 {
			if n, ok := col.Length(); ok && n > 0 {
				f.Size = int(n)
			}
		}
		if c, ok := col.Comment(); ok {
			f.Comment = sanitizeText(c)
		}
		f.Widget = defaultWidget(f.GoType)
		f.Label = f.GoName
		if f.Nullable || f.Primary {
			// 可空列默认非必填，主键由数据库自行分配。
			// Nullable columns default to optional, and primary keys are assigned by the database.
			f.Validate = ""
		} else {
			// Not-null columns automatically carry the required rule. | 非空列自动带上 required 规则。
			f.Validate = joinRule("", "required")
		}
		fields = append(fields, f)
	}
	if len(fields) == 0 {
		return nil, errx.Detail(ErrTableNotFound, "table=%s has no readable column | 表 %s 没有可读的列", table, table)
	}

	name := strings.TrimSpace(o.Name)
	if name == "" {
		name = exportedName(singularize(snakeCase(table)))
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

// goTypeForDB 把数据库类型名映射到 Go 类型名。
// goTypeForDB maps a database type name onto a Go type name.
func goTypeForDB(dbType string) string {
	base := dbType
	if i := strings.IndexByte(base, '('); i >= 0 {
		base = strings.TrimSpace(base[:i])
	}
	if i := strings.IndexByte(base, ' '); i >= 0 {
		base = strings.TrimSpace(base[:i])
	}

	base = strings.ToUpper(base)

	switch base {
	case "":
		return "string"
	case "INT", "INTEGER", "BIGINT", "SMALLINT", "TINYINT", "MEDIUMINT",
		"SERIAL", "BIGSERIAL", "SMALLSERIAL", "INT2", "INT4", "INT8":
		return "int64"
	case "BOOL", "BOOLEAN":
		return "bool"
	case "FLOAT", "FLOAT4", "FLOAT8", "REAL", "DOUBLE", "NUMERIC", "DECIMAL", "MONEY":
		return "float64"
	case "DATE", "DATETIME", "TIMESTAMP", "TIMESTAMPTZ", "TIME", "TIMETZ":
		return typeTime
	case "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BINARY", "VARBINARY", "BYTEA":
		return "[]byte"
	default:
		return "string"
	}
}

// sizeFromDBType 从 "VARCHAR(120)" 这类类型名中提取长度；无长度时返回 0。
// sizeFromDBType extracts the length from a type name such as "VARCHAR(120)"; it returns 0 when the type carries none.
func sizeFromDBType(dbType string) int {
	open := strings.IndexByte(dbType, '(')
	if open < 0 {
		return 0
	}
	closeIdx := strings.IndexByte(dbType[open:], ')')
	if closeIdx < 0 {
		return 0
	}
	inner := dbType[open+1 : open+closeIdx]
	// 数值类型与十进制精度不接受长度上限，只有字符类型才有意义。
	// Numeric types and decimal precision do not carry a length limit, only character types do.
	head := strings.TrimSpace(dbType[:open])
	if strings.Contains(head, "INT") || strings.Contains(head, "DEC") ||
		strings.Contains(head, "NUMERIC") || strings.Contains(head, "FLOAT") {
		return 0
	}
	if comma := strings.IndexByte(inner, ','); comma >= 0 {
		inner = inner[:comma]
	}
	return atoiOrZero(strings.TrimSpace(inner))
}
