package gen

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/shibingli/ydx/errs"
)

// typeTime 是时间字段在生成代码中的 Go 类型名。
// typeTime is the Go type name of a time field in generated code.
const typeTime = "time.Time"

// Field 描述模型中的一列：列名、Go 字段名与类型、约束以及表单呈现方式。
// Field describes one column of a model: column name, Go field name and type,
// constraints, and how it is rendered in a form.
//
// 约束（Primary、Nullable、Indexed、Unique、Size）来自 gorm 标签或数据库列元数据；
// 表单元数据（Widget、Label、Validate）来自 yd 标签或字段描述串。
// Constraints (Primary, Nullable, Indexed, Unique, Size) come from gorm tags or
// database column metadata; form metadata (Widget, Label, Validate) comes from yd
// tags or a field specification string.
type Field struct {
	// Name 是字段的原始名（小写下划线形式）。
	// Name is the raw field name (lower snake case).
	Name string
	// GoName 是生成的 Go 字段名（导出形式）。
	// GoName is the generated Go field name (exported form).
	GoName string
	// GoType 是生成的 Go 类型名，例如 string、int64、time.Time。
	// GoType is the generated Go type name, for example string, int64 or time.Time.
	GoType string
	// Column 是数据库列名。
	// Column is the database column name.
	Column string
	// JSONName 是 JSON 与表单字段名。
	// JSONName is the JSON and form field name.
	JSONName string
	// Comment 是列注释，写入生成注释。
	// Comment is the column comment, emitted as a generated comment.
	Comment string
	// Widget 是表单控件类型：text、number、checkbox、date、file、textarea。
	// Widget is the form control type: text, number, checkbox, date, file or textarea.
	Widget string
	// Label 是表单显示名，默认取 GoName。
	// Label is the form display name; it defaults to GoName.
	Label string
	// Validate 是逗号分隔的校验规则，例如 "required,max=120"。
	// Validate is a comma separated rule list, for example "required,max=120".
	Validate string
	// Size 是列的字符长度上限，0 表示不限。
	// Size is the column length limit; 0 means unlimited.
	Size int
	// Primary 报告该列是否为主键。
	// Primary reports whether the column is the primary key.
	Primary bool
	// Nullable 报告该列是否允许 NULL。
	// Nullable reports whether the column allows NULL.
	Nullable bool
	// Indexed 报告该列是否建了普通索引。
	// Indexed reports whether the column carries a plain index.
	Indexed bool
	// Unique 报告该列是否有唯一约束。
	// Unique reports whether the column carries a unique constraint.
	Unique bool
	// JSONExcluded 报告源字段显式声明了 json:"-"，生成物必须保留该排除而不是回退成列名。
	// JSONExcluded reports that the source field declared json:"-" explicitly; the generated
	// artifact must preserve the exclusion instead of falling back to the column name.
	JSONExcluded bool
}

// reservedModuleNames lists the type names the generated scaffold already defines in the
// packages yd gen writes to. A generated module with one of these names would redeclare them.
// | reservedModuleNames 列出生成脚手架在 yd gen 写入的包中已经定义的类型名。用这些名字生成模块
// 会重复声明它们。
// validDBIdent reports whether s is a lowercase plain SQL identifier that is safe to interpolate
// into generated SQL. Uppercase and reserved words are rejected because the generated statements do
// not quote identifiers. | validDBIdent 报告 s 是否为可安全写入生成 SQL 的小写普通标识符。
// 生成语句不对标识符加引号，因此大写与保留字一律拒绝。
func validDBIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || 'a' <= r && r <= 'z' || i > 0 && '0' <= r && r <= '9' {
			continue
		}
		return false
	}
	return !sqlReservedWords[s]
}

// sqlReservedWords lists identifiers that cannot be interpolated unquoted into portable SQL. |
// sqlReservedWords 列出无法不加引号写入可移植 SQL 的标识符。
var sqlReservedWords = map[string]bool{
	"all": true, "alter": true, "and": true, "as": true, "authorization": true, "between": true,
	"by": true, "case": true, "check": true, "column": true, "constraint": true, "create": true,
	"cross": true, "current_date": true, "current_time": true, "current_timestamp": true,
	"default": true, "delete": true, "distinct": true, "drop": true, "else": true, "end": true,
	"exists": true, "false": true, "fetch": true, "for": true, "foreign": true, "from": true,
	"full": true, "group": true, "having": true, "in": true, "index": true, "inner": true,
	"insert": true, "interval": true, "into": true, "is": true, "join": true, "key": true,
	"lateral": true, "left": true, "like": true, "limit": true, "locked": true, "natural": true,
	"not": true, "nowait": true, "null": true, "offset": true, "on": true, "only": true, "or": true,
	"order": true, "outer": true, "over": true, "partition": true, "primary": true, "references": true,
	"returning": true, "right": true, "select": true, "set": true, "share": true, "skip": true,
	"table": true, "then": true, "true": true, "union": true, "unique": true, "update": true,
	"user": true, "using": true, "values": true, "when": true, "where": true, "window": true,
	"with": true,
}

// validDBTable accepts an optionally schema-qualified table name whose every segment is a plain
// identifier. | validDBTable 接受可带模式限定的表名，其中每一段都必须是普通标识符。
func validDBTable(s string) bool {
	for _, part := range strings.Split(s, ".") {
		if !validDBIdent(part) {
			return false
		}
	}
	return s != ""
}

// validJSONKey reports whether s can be embedded verbatim in a struct tag. | validJSONKey 报告 s
// 能否原样嵌入结构体标签。
func validJSONKey(s string) bool {
	if s == "" {
		return false
	}
	return !strings.ContainsAny(s, "\"`\\\r\n\t")
}

var reservedModuleNames = map[string]bool{
	"user":       true,
	"role":       true,
	"tenant":     true,
	"permission": true,
	"auth":       true,
	"session":    true,
	"controller": true,
}

// Rules 返回逗号分隔的校验规则列表，空白项被忽略。
// Rules returns the comma separated validation rules, dropping blank entries.
func (f Field) Rules() []string {
	if strings.TrimSpace(f.Validate) == "" {
		return nil
	}
	parts := strings.Split(f.Validate, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// HasRule 报告是否声明了名为 name 的规则，既接受 "required" 也接受 "max=120" 形式。
// HasRule reports whether the rule named name is declared, matching both the bare
// "required" form and the "max=120" form.
func (f Field) HasRule(name string) bool {
	for _, r := range f.Rules() {
		if r == name || strings.HasPrefix(r, name+"=") {
			return true
		}
	}
	return false
}

// Required 报告该字段是否为必填项。
// Required reports whether the field is mandatory.
func (f Field) Required() bool { return f.HasRule("required") }

// RuleValue 返回 "name=value" 形式规则的取值，未声明时返回空串。
// RuleValue returns the value of a "name=value" rule, or an empty string when it is absent.
func (f Field) RuleValue(name string) string {
	for _, r := range f.Rules() {
		k, v, ok := strings.Cut(r, "=")
		if ok && strings.TrimSpace(k) == name {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// MaxLength 返回字段的长度上限：先看 max= 规则，再回落到列长度 Size，都没有时为 0。
// MaxLength returns the field length limit: the max= rule first, then the column
// length in Size, and 0 when neither is present.
func (f Field) MaxLength() int {
	if v := f.RuleValue("max"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return f.Size
}

// MinValue 返回 min= 规则的原始文本，未声明时返回空串。
// MinValue returns the raw text of the min= rule, or an empty string when it is absent.
func (f Field) MinValue() string { return f.RuleValue("min") }

// LabelText 返回表单显示名，Label 为空时退回 GoName。
// LabelText returns the form display name, falling back to GoName when Label is empty.
func (f Field) LabelText() string {
	if f.Label != "" {
		return f.Label
	}
	if f.GoName != "" {
		return f.GoName
	}
	return f.Column
}

// SafeLabel 返回可直接嵌入 Go 字符串字面量的显示名：压成单行并去掉引号与反斜杠。
// SafeLabel returns a display name that can be embedded directly into a Go string
// literal: flattened into one line with quotes and backslashes removed.
func (f Field) SafeLabel() string {
	label := strings.NewReplacer(`"`, "", `\`, "", "\n", " ").Replace(sanitizeText(f.LabelText()))
	if label == "" {
		return f.Column
	}
	return label
}

// SafeName 返回可直接嵌入 Go 字符串字面量的列名。
// SafeName returns a column name that can be embedded directly into a Go string literal.
func (f Field) SafeName() string {
	name := strings.NewReplacer(`"`, "", `\`, "", "\n", " ").Replace(sanitizeText(f.Column))
	if name == "" {
		return f.GoName
	}
	return name
}

// IsTime 报告字段是否为时间类型。
// IsTime reports whether the field is a time type.
func (f Field) IsTime() bool { return f.GoType == typeTime }

// IsBool 报告字段是否为布尔类型。
// IsBool reports whether the field is a boolean.
func (f Field) IsBool() bool { return f.GoType == "bool" }

// IsNumber 报告字段是否为数值类型。
// IsNumber reports whether the field is numeric.
func (f Field) IsNumber() bool {
	switch f.GoType {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return true
	default:
		return false
	}
}

// IsInteger reports whether the field is an integer kind. | IsInteger 报告字段是否为整数类型。
func (f Field) IsInteger() bool {
	switch f.GoType {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return true
	default:
		return false
	}
}

// IsPrimaryKeyType reports whether a path parameter can carry the type. Strings and integers can be
// parsed back from the route; floats and []byte cannot. | IsPrimaryKeyType 报告路径参数能否承载该类型。
// 字符串与整数可以从路由还原；浮点与 []byte 不能。
func (f Field) IsPrimaryKeyType() bool { return f.GoType == "string" || f.IsInteger() }

// IsUnsigned 报告数值字段是否为无符号类型。
// IsUnsigned reports whether the numeric field is unsigned.
func (f Field) IsUnsigned() bool {
	switch f.GoType {
	case "uint", "uint8", "uint16", "uint32", "uint64":
		return true
	default:
		return false
	}
}

// IsString 报告字段是否为字符串类型。
// IsString reports whether the field is a string.
func (f Field) IsString() bool { return f.GoType == "string" || f.GoType == "[]byte" }

// IsTextarea 报告字段是否用多行文本框呈现。
// IsTextarea reports whether the field renders as a multi-line textarea.
func (f Field) IsTextarea() bool { return f.Widget == "textarea" }

// IsCheckbox 报告字段是否用复选框呈现。
// IsCheckbox reports whether the field renders as a checkbox.
func (f Field) IsCheckbox() bool { return f.IsBool() || f.Widget == "checkbox" }

// IsFile 报告字段是否用文件上传控件呈现。
// IsFile reports whether the field renders as a file upload control.
func (f Field) IsFile() bool { return f.Widget == "file" }

// InputType 返回 HTML 输入框的 type 取值（单行 input 使用）。
// InputType returns the HTML input type used by a single-line input.
func (f Field) InputType() string {
	switch {
	case f.IsCheckbox():
		return "checkbox"
	case f.IsFile():
		return "file"
	case f.Widget == "datetime":
		return "datetime-local"
	case f.IsTime() || f.Widget == "date":
		return "date"
	case f.Widget == "number" || f.IsNumber():
		return "number"
	case f.Widget == "email":
		return "email"
	case f.Widget == "password":
		return "password"
	default:
		return "text"
	}
}

// TimeLayout 返回时间字段在表单与列表里使用的格式，与 InputType 的取值保持配套。
// TimeLayout returns the layout used by a time field in forms and lists, matching the InputType value.
func (f Field) TimeLayout() string {
	switch {
	case f.Widget == "datetime":
		return "2006-01-02T15:04"
	case f.Widget == "time":
		return "15:04"
	default:
		return "2006-01-02"
	}
}

// ScalarSpec 描述把表单或路径里的字符串解析为标量所需的代码片段。
// ScalarSpec describes the code fragments needed to parse a form or path string into a scalar.
//
// 生成器用它拼出类型安全的绑定代码：pipeline 统一为「取值 → 解析 → 转换」三步。
// The generator uses it to assemble type-safe binding code: the pipeline is always
// "read → parse → convert".
type ScalarSpec struct {
	// Func 是 strconv 中的解析函数名，例如 ParseInt；字符串类型为空。
	// Func is the strconv parsing function name, for example ParseInt; it is empty for strings.
	Func string
	// Args 是解析函数除入参外的额外参数，例如 ", 10, 64"；无需额外参数时为空。
	// Args holds the extra arguments of the parsing function, for example ", 10, 64"; it is empty when there are none.
	Args string
	// Cast 是转换函数的类型名，例如 uint32；与目标类型一致时为空。
	// Cast is the type name of the conversion function, for example uint32; it is empty when the parsed type already matches.
	//
	// 用 CastExpr 拼出调用，避免生成 "uint32(n)n" 这样的重复后缀。
	// CastExpr builds the call so that a stray suffix such as "uint32(n)n" can never be generated.
	Cast string
	// Zero 是字段类型的零值字面量。
	// Zero is the zero literal of the field type.
	Zero string
	// Guard 是主键有效性判断表达式，n 为解析结果；字符串类型为空。
	// Guard is the validity check of a primary key where n is the parsed value; it is empty for strings.
	Guard string
}

// Scalar 返回字段的标量解析规格，供生成器构造绑定与取参代码。
// Scalar returns the scalar parsing spec of the field, used by the generator to build binding and path-parameter code.
func (f Field) Scalar() ScalarSpec {
	switch f.GoType {
	case "string":
		return ScalarSpec{Zero: `""`}
	case "int":
		return ScalarSpec{Func: "ParseInt", Args: ", 10, 64", Cast: "int", Zero: "0", Guard: "n <= 0"}
	case "int8":
		return ScalarSpec{Func: "ParseInt", Args: ", 10, 64", Cast: "int8", Zero: "0", Guard: "n <= 0"}
	case "int16":
		return ScalarSpec{Func: "ParseInt", Args: ", 10, 64", Cast: "int16", Zero: "0", Guard: "n <= 0"}
	case "int32":
		return ScalarSpec{Func: "ParseInt", Args: ", 10, 64", Cast: "int32", Zero: "0", Guard: "n <= 0"}
	case "int64":
		return ScalarSpec{Func: "ParseInt", Args: ", 10, 64", Zero: "0", Guard: "n <= 0"}
	case "uint":
		return ScalarSpec{Func: "ParseUint", Args: ", 10, 64", Cast: "uint", Zero: "0", Guard: "n == 0"}
	case "uint8":
		return ScalarSpec{Func: "ParseUint", Args: ", 10, 64", Cast: "uint8", Zero: "0", Guard: "n == 0"}
	case "uint16":
		return ScalarSpec{Func: "ParseUint", Args: ", 10, 64", Cast: "uint16", Zero: "0", Guard: "n == 0"}
	case "uint32":
		return ScalarSpec{Func: "ParseUint", Args: ", 10, 64", Cast: "uint32", Zero: "0", Guard: "n == 0"}
	case "uint64":
		return ScalarSpec{Func: "ParseUint", Args: ", 10, 64", Zero: "0", Guard: "n == 0"}
	case "float32":
		return ScalarSpec{Func: "ParseFloat", Args: ", 64", Cast: "float32", Zero: "0"}
	case "float64":
		return ScalarSpec{Func: "ParseFloat", Args: ", 64", Zero: "0"}
	case "bool":
		return ScalarSpec{Zero: "false"}
	case typeTime:
		return ScalarSpec{Zero: "time.Time{}"}
	default:
		return ScalarSpec{}
	}
}

// CastExpr 返回把表达式 v 转换为字段类型的表达式；无需转换时原样返回 v。
// CastExpr returns the expression that converts v into the field type, or v itself when no conversion is needed.
func (s ScalarSpec) CastExpr(v string) string {
	if s.Cast == "" {
		return v
	}
	return s.Cast + "(" + v + ")"
}

// FormatExpr 返回把表达式 v 转成字符串的代码片段，用于拼接 DOM id、URL 与日志字段。
// FormatExpr returns the code fragment that turns expression v into a string, used to build DOM ids, URLs and log fields.
func (f Field) FormatExpr(v string) string {
	switch f.GoType {
	case "string":
		return v
	case "int":
		return "strconv.Itoa(" + v + ")"
	case "int8", "int16", "int32", "int64":
		return "strconv.FormatInt(int64(" + v + "), 10)"
	case "uint", "uint8", "uint16", "uint32", "uint64":
		return "strconv.FormatUint(uint64(" + v + "), 10)"
	default:
		return v
	}
}

// GormTag 返回写入结构体标签的 gorm 取值（不含反引号），顺序固定以便生成结果稳定。
// GormTag returns the gorm tag value written into the struct tag (without
// backticks); the order is fixed so the generated result stays stable.
func (f Field) GormTag() string {
	parts := []string{"column:" + f.Column}
	if f.Size > 0 && f.IsString() {
		parts = append(parts, "size:"+strconv.Itoa(f.Size))
	}
	if f.Primary {
		parts = append(parts, "primaryKey")
		if f.IsNumber() {
			parts = append(parts, "autoIncrement")
		}
	}
	if !f.Nullable && !f.Primary {
		parts = append(parts, "not null")
	}
	if f.Unique {
		parts = append(parts, "unique")
	}
	if f.Indexed && !f.Unique {
		parts = append(parts, "index")
	}
	return sanitizeTagValue(strings.Join(parts, ";"))
}

// JSONTag 返回写入结构体标签的 json 取值（不含反引号）。
// JSONTag returns the json tag value written into the struct tag (without backticks).
func (f Field) JSONTag() string {
	if f.JSONExcluded {
		return "-"
	}
	name := f.JSONName
	if name == "" {
		name = f.Column
	}
	if f.Nullable {
		return name + ",omitempty"
	}
	return name
}

// CommentText 返回用于生成注释的文本：优先用列注释，其次用显示名。
// CommentText returns the text used by generated comments: the column comment
// first, then the display name.
func (f Field) CommentText() string {
	if strings.TrimSpace(f.Comment) != "" {
		return sanitizeText(f.Comment)
	}
	return f.LabelText()
}

// Model 描述一个可生成的业务模块：模型名、表名与字段集合。
// Model describes one generatable business module: its name, table and fields.
type Model struct {
	// Name 是模块名（导出形式），用于生成类型名与注册函数名。
	// Name is the module name (exported form) used for generated type and registration function names.
	Name string
	// GoName 是生成的模型类型名，通常与 Name 相同。
	// GoName is the generated model type name, usually the same as Name.
	GoName string
	// Table 是数据库表名。
	// Table is the database table name.
	Table string
	// Module 是业务模块名，用作 OpenAPI 的 @Tags。
	// Module is the business module name used as the OpenAPI @Tags value.
	Module string
	// Fields 是模型的字段列表，顺序与生成顺序一致。
	// Fields is the field list; its order is the generated order.
	Fields []Field
	// Timestamps 报告是否生成 CreatedAt / UpdatedAt 字段。
	// Timestamps reports whether CreatedAt / UpdatedAt fields are generated.
	Timestamps bool
}

// Field 按列名、原始名或导出名查找字段（先精确匹配再忽略大小写），未命中返回 nil。
// Field looks a field up by column, raw or exported name (exact match first, then
// case-insensitive); it returns nil when nothing matches.
func (m *Model) Field(name string) *Field {
	if m == nil || name == "" {
		return nil
	}
	for i := range m.Fields {
		f := &m.Fields[i]
		if f.Column == name || f.Name == name || f.GoName == name {
			return f
		}
	}
	for i := range m.Fields {
		f := &m.Fields[i]
		if strings.EqualFold(f.Column, name) || strings.EqualFold(f.Name, name) ||
			strings.EqualFold(f.GoName, name) || strings.EqualFold(snakeCase(f.GoName), name) {
			return f
		}
	}
	return nil
}

// PrimaryKey 返回第一个主键字段，没有主键时返回 nil。
// PrimaryKey returns the first primary key field, or nil when the model has none.
func (m *Model) PrimaryKey() *Field {
	if m == nil {
		return nil
	}
	for i := range m.Fields {
		if m.Fields[i].Primary {
			return &m.Fields[i]
		}
	}
	return nil
}

// Columns 返回全部列名。
// Columns returns every column name.
func (m *Model) Columns() []string {
	out := make([]string, 0, len(m.Fields))
	for _, f := range m.Fields {
		out = append(out, f.Column)
	}
	return out
}

// prepare 补齐模型与字段的缺省值，并校验模型是否可以生成。
// prepare fills in the model and field defaults and checks that the model can be generated.
func (m *Model) prepare() error {
	if m == nil || len(m.Fields) == 0 {
		return ErrNoFields
	}
	if m.GoName == "" {
		m.GoName = exportedName(m.Name)
	}
	if m.GoName == "" {
		m.GoName = exportedName(m.Table)
	}
	if !validGoIdent(m.GoName) {
		return errs.Detail(ErrBadModel, "model name %q is not a legal Go identifier | 模型名 %q 不是合法的 Go 标识符", m.GoName, m.GoName)
	}
	if m.Name == "" || exportedName(m.Name) != m.Name {
		// 模板把 Name 直接嵌进生成的标识符（Err<Name>BadRequest 之类），
		// 因此任何不是导出形式的名称都统一回退到已经校验过的 GoName。
		// Templates embed Name straight into generated identifiers (Err<Name>BadRequest
		// and the like), so any name that is not already in exported form falls back to
		// the GoName that has just been validated.
		m.Name = m.GoName
	}
	if m.Table == "" {
		m.Table = pluralize(snakeCase(m.Name))
	}
	if !validDBTable(m.Table) {
		return errs.Detail(ErrBadModel, "table name %[1]q contains characters that cannot be embedded in generated SQL/Go | 表名 %[1]q 含无法嵌入生成 SQL/Go 的字符", m.Table)
	}

	for i := range m.Fields {
		f := &m.Fields[i]
		if f.Column == "" {
			f.Column = snakeCase(f.GoName)
		}
		if f.Column == "" {
			f.Column = snakeCase(f.Name)
		}
		if f.Name == "" {
			f.Name = f.Column
		}
		if f.GoName == "" {
			f.GoName = exportedName(f.Column)
		}
		if !validGoIdent(f.GoName) {
			return errs.Detail(ErrBadModel, "field %q of %s is not a legal Go identifier | %s 的字段名 %q 不是合法的 Go 标识符",
				f.GoName, m.GoName, m.GoName, f.GoName)
		}
		if f.GoType == "" {
			f.GoType = "string"
		}
		if f.JSONName == "" && !f.JSONExcluded {
			f.JSONName = f.Column
		}
		if f.JSONExcluded {
			f.JSONName = "-"
		}
		// Column and JSONName are interpolated raw into generated Go strings, SQL and struct tags.
		// A quote, backtick or control character would break the file (or inject code), so only
		// identifier-shaped values are accepted. | Column 与 JSONName 会原样写入生成的 Go 字符串、
		// SQL 与结构体标签。引号、反引号或控制字符会破坏文件（或注入代码），因此只接受标识符形态。
		if !validDBIdent(f.Column) {
			return errs.Detail(ErrBadModel, "column %q of %s is not a legal SQL identifier | %s 的列名 %q 不是合法的 SQL 标识符", f.Column, m.GoName, m.GoName, f.Column)
		}
		if !f.JSONExcluded && !validJSONKey(f.JSONName) {
			return errs.Detail(ErrBadModel, "json name %q of %s is not embeddable in a struct tag | %s 的 JSON 名 %q 无法嵌入结构体标签", f.JSONName, m.GoName, m.GoName, f.JSONName)
		}
		if f.Label == "" {
			f.Label = f.GoName
		}
		if f.Widget == "" {
			f.Widget = defaultWidget(f.GoType)
		}
		if f.Primary {
			f.Nullable = false
		}
	}

	if m.PrimaryKey() == nil {
		// 没有主键的模型无法生成 CRUD 路由，这里补一个自增整型主键。
		// A model without a primary key cannot back CRUD routes, so an auto-increment integer key is added.
		m.Fields = append([]Field{{
			Name: "id", GoName: "ID", GoType: "int64", Column: "id", JSONName: "id",
			Primary: true, Widget: "number", Label: "ID",
			Comment: "Primary key | 主键",
		}}, m.Fields...)
	}
	if pk := m.PrimaryKey(); pk != nil && !pk.IsPrimaryKeyType() {
		// Routes can only carry string or integer keys; floats and []byte cannot be rebuilt from a path parameter. |
		// 路由只能携带字符串或整数主键；浮点与 []byte 无法从路径参数还原。
		return errs.Detail(ErrBadModel, "primary key %s must be a string or integer type | 主键 %s 必须是字符串或整数类型", pk.Column, pk.Column)
	}
	if m.Module == "" {
		m.Module = m.Name
	}
	return nil
}

// defaultWidget 按 Go 类型给出默认表单控件。
// defaultWidget returns the default form control for a Go type.
func defaultWidget(goType string) string {
	switch goType {
	case typeTime:
		return "date"
	case "bool":
		return "checkbox"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return "number"
	default:
		return "text"
	}
}

// StructOptions 覆盖从结构体推导模型时的自动取值。
// StructOptions overrides the values derived from a struct.
type StructOptions struct {
	// Timestamps 显式开关 CreatedAt / UpdatedAt 字段的生成，nil 表示按结构体自动判定。
	// Timestamps explicitly toggles the CreatedAt / UpdatedAt fields; nil means detecting them from the struct.
	Timestamps *bool
	// Name 覆盖模型名（导出形式）。
	// Name overrides the model name (exported form).
	Name string
	// Table 覆盖表名，优先于模型自报的 TableName。
	// Table overrides the table name and wins over the model's own TableName.
	Table string
	// Module 是业务模块名，用作 OpenAPI 的 @Tags。
	// Module is the business module name used as the OpenAPI @Tags value.
	Module string
}

// TableOptions 覆盖从数据库表推导模型时的自动取值。
// TableOptions overrides the values derived from a database table.
type TableOptions struct {
	// Name 覆盖模型名（导出形式），为空时由表名单数化推导。
	// Name overrides the model name (exported form); when empty it is derived by singularizing the table name.
	Name string
	// Module 是业务模块名，用作 OpenAPI 的 @Tags。
	// Module is the business module name used as the OpenAPI @Tags value.
	Module string
	// Timestamps 显式开关 CreatedAt / UpdatedAt 字段的生成，nil 表示按列名自动判定。
	// Timestamps explicitly toggles the CreatedAt / UpdatedAt fields; nil means detecting them from the column names.
	Timestamps *bool
	// Ignore 列出需要跳过的列名。
	// Ignore lists the column names to skip.
	Ignore []string
}

// tableNamer 由自报表名的模型实现。
// tableNamer is implemented by models that report their own table name.
type tableNamer interface{ TableName() string }

// ModelFromStruct 从 Go 结构体推导模型：读取 gorm / json / yd 三种标签。
// ModelFromStruct derives a model from a Go struct by reading the gorm, json and yd tags.
//
// 匿名的内嵌结构体会被展开，未导出字段、gorm:"-" 标记的字段以及切片/映射等关系字段会被跳过。
// Anonymous embedded structs are flattened; unexported fields, fields tagged
// gorm:"-" and relation-like slice/map fields are skipped.
func ModelFromStruct(v any, o StructOptions) (*Model, error) {
	if v == nil {
		return nil, ErrUnsupportedValue
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, ErrUnsupportedValue
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, ErrUnsupportedValue
	}

	rt := rv.Type()
	name := o.Name
	if name == "" {
		name = exportedName(rt.Name())
	}
	table := o.Table
	if table == "" {
		table = tableNameOf(v)
	}
	if table == "" {
		table = pluralize(snakeCase(name))
	}

	fields := collectFields(rt, nil)
	if len(fields) == 0 {
		return nil, ErrNoFields
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

// tableNameOf 优先采用模型实现的 TableName 方法。
// tableNameOf prefers the table name reported by the model's TableName method.
func tableNameOf(v any) string {
	if tn, ok := v.(tableNamer); ok {
		return strings.TrimSpace(tn.TableName())
	}
	return ""
}

// hasTimestampFields 报告字段集合中是否含创建/更新时间列。
// hasTimestampFields reports whether the field set carries creation/update time columns.
func hasTimestampFields(fields []Field) bool {
	for _, f := range fields {
		switch {
		case f.GoName == "CreatedAt" || f.GoName == "UpdatedAt":
			return true
		case f.Column == "created_at" || f.Column == "updated_at":
			return true
		}
	}
	return false
}

// collectFields 递归收集结构体字段，展开匿名的内嵌结构体。
// collectFields collects struct fields recursively, flattening anonymous embedded structs.
func collectFields(rt reflect.Type, out []Field) []Field {
	for i := 0; i < rt.NumField(); i++ {
		sf := rt.Field(i)
		gormTag := sf.Tag.Get("gorm")

		if sf.Anonymous {
			et := sf.Type
			for et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct && !gormIgnored(gormTag) {
				out = collectFields(et, out)
			}
			continue
		}
		if !sf.IsExported() || gormIgnored(gormTag) {
			continue
		}

		ft := sf.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		// 切片、映射、函数与接口不可能是列；结构体除时间类型外按关联字段处理。
		// Slices, maps, functions and interfaces cannot be columns; structs other than time types are treated as relations.
		switch ft.Kind() {
		case reflect.Slice, reflect.Map, reflect.Chan, reflect.Func, reflect.Interface:
			continue
		case reflect.Struct:
			if ft.PkgPath() != "time" || ft.Name() != "Time" {
				continue
			}
		}
		out = append(out, fieldFromStruct(sf, ft))
	}
	return out
}

// fieldFromStruct 把一个结构体字段转换为 Field。
// fieldFromStruct converts one struct field into a Field.
func fieldFromStruct(sf reflect.StructField, ft reflect.Type) Field {
	return fieldFromTags(sf.Name, goTypeName(ft), sf.Tag)
}

// fieldFromTags 依据字段名、Go 类型名与结构体标签构造 Field。
// fieldFromTags builds a Field from the field name, the Go type name and the struct tags.
//
// 反射与源码解析两条路径共用它，从而 gorm / json / yd 三种标签的解读规则只有一份。
// Both the reflection path and the source-parsing path share it, so the reading rules of
// the gorm, json and yd tags exist exactly once.
func fieldFromTags(goName, goType string, tag reflect.StructTag) Field {
	gormTag := tag.Get("gorm")
	settings := parseSettings(gormTag)

	f := Field{
		GoName:   goName,
		GoType:   goType,
		Column:   settings["column"],
		JSONName: jsonName(tag.Get("json")),
		Size:     atoiOrZero(settings["size"]),
		Primary:  hasSetting(gormTag, "primarykey"),
		Unique:   hasSetting(gormTag, "unique") || hasSetting(gormTag, "uniqueindex"),
		Indexed:  hasSettingPrefix(gormTag, "index"),
		Nullable: !hasSetting(gormTag, "notnull") && !hasSetting(gormTag, "primarykey"),
	}
	if f.Column == "" {
		f.Column = snakeCase(f.GoName)
	}
	f.Name = snakeCase(f.Column)
	if strings.TrimSpace(tag.Get("json")) == "-" {
		f.JSONExcluded = true
		f.JSONName = "-"
	}
	if f.JSONName == "" && !f.JSONExcluded {
		f.JSONName = f.Column
	}
	applyYDTag(&f, tag.Get("yd"))
	return f
}

// parseSettings 解析 gorm 标签中 "key:value" 形式的设置项为小写键的映射。
// parseSettings parses the "key:value" entries of a gorm tag into a map keyed by lower-case names.
func parseSettings(tag string) map[string]string {
	out := make(map[string]string, 4)
	for _, tok := range strings.Split(tag, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(tok), ":")
		if !ok {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return out
}

// hasSetting 报告 gorm 标签中是否含指定开关项（忽略大小写与空白差异，故 "not null" 等写法等价）。
// hasSetting reports whether a gorm tag carries the given flag, ignoring case and
// whitespace so that spellings such as "not null" compare equal.
func hasSetting(tag, flag string) bool {
	flag = strings.ReplaceAll(strings.ToLower(sanitizeText(flag)), " ", "")
	for _, tok := range strings.Split(tag, ";") {
		if strings.ReplaceAll(strings.ToLower(sanitizeText(tok)), " ", "") == flag {
			return true
		}
	}
	return false
}

// hasSettingPrefix 报告 gorm 标签中是否含以 prefix 开头的设置项。
// hasSettingPrefix reports whether a gorm tag carries a setting starting with prefix.
func hasSettingPrefix(tag, prefix string) bool {
	for _, tok := range strings.Split(tag, ";") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(tok)), prefix) {
			return true
		}
	}
	return false
}

// gormIgnored 报告该 gorm 标签是否显式忽略字段。
// gormIgnored reports whether the gorm tag explicitly ignores the field.
func gormIgnored(tag string) bool {
	return strings.TrimSpace(tag) == "-" || hasSetting(tag, "-")
}

// jsonName 取 json 标签中的字段名，忽略 omitempty 等选项。
// jsonName extracts the field name from a json tag, ignoring options such as omitempty.
func jsonName(tag string) string {
	if tag == "" || tag == "-" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	return strings.TrimSpace(name)
}

// applyYDTag 解析 yd 标签：label / widget / validate / comment / column / json 与裸 required。
// applyYDTag parses a yd tag: label / widget / validate / comment / column / json plus the bare required flag.
func applyYDTag(f *Field, tag string) {
	for _, tok := range strings.Split(tag, ";") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		key, value, hasValue := strings.Cut(tok, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if !hasValue {
			f.Validate = joinRule(f.Validate, tok)
			continue
		}
		switch key {
		case "label":
			f.Label = value
		case "widget":
			f.Widget = value
		case "validate", "required":
			if key == "required" {
				value = joinRule("required", value)
			}
			f.Validate = joinRule(f.Validate, value)
		case "comment":
			f.Comment = value
		case "column":
			f.Column = value
		case "json":
			f.JSONName = value
		default:
			f.Validate = joinRule(f.Validate, tok)
		}
	}
}

// joinRule 以逗号追加一条校验规则，去重且忽略空白。
// joinRule appends one validation rule, de-duplicating and trimming blanks.
func joinRule(existing, rule string) string {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return existing
	}
	for _, r := range strings.Split(existing, ",") {
		if strings.TrimSpace(r) == rule {
			return existing
		}
	}
	if strings.TrimSpace(existing) == "" {
		return rule
	}
	return existing + "," + rule
}

// goTypeName 把反射类型映射为生成的 Go 类型名。
// goTypeName maps a reflected type onto the generated Go type name.
func goTypeName(ft reflect.Type) string {
	if ft.PkgPath() == "time" && ft.Name() == "Time" {
		return typeTime
	}
	switch ft.Kind() {
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int64"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int64"
	case reflect.Float32, reflect.Float64:
		return "float64"
	case reflect.Slice:
		if ft.Elem().Kind() == reflect.Uint8 {
			return "[]byte"
		}
		return "string"
	default:
		return "string"
	}
}

// atoiOrZero 解析十进制整数，失败时返回 0。
// atoiOrZero parses a decimal integer and returns 0 on failure.
func atoiOrZero(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
