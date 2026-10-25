package gen

import (
	"strings"

	"github.com/shibingli/ydx/errs"
)

// tmplView 是渲染一个模块全部生成物时共享的视图数据。
// tmplView is the shared view data for rendering every artifact of one module.
type tmplView struct {

	// Marker 是生成标记，写在每个生成物首行。
	// Marker is the generated-file marker written on the first line of every artifact.
	Marker string
	// Package 是目标工程的 Go 模块路径。
	// Package is the Go module path of the target project.
	Package string
	// Name 是模块的导出名，例如 Post。
	// Name is the exported module name, for example Post.
	Name string
	// Snake 是模块的小写下划线名，例如 post。
	// Snake is the lower snake case module name, for example post.
	Snake string
	// Var 是模块的未导出名，例如 post。
	// Var is the unexported module name, for example post.
	Var string
	// Table 是数据库表名。
	// Table is the database table name.
	Table string
	// Module 是业务模块名，用作 OpenAPI 的 @Tags。
	// Module is the business module name used as the OpenAPI @Tags value.
	Module string
	// Route 是模块的路由前缀，例如 /post。
	// Route is the route prefix of the module, for example /post.
	Route string
	// Event 是模块变更后派发的 htmx 事件名，例如 post:changed。
	// Event is the htmx event fired after a module change, for example post:changed.
	Event string

	// SearchArgs 是搜索 SQL 中与 SearchColumns 对应的参数占位文本。
	// SearchArgs is the placeholder text matching SearchColumns in the search SQL.
	SearchArgs string
	// SearchWhere 是搜索用的 SQL 条件文本。
	// SearchWhere is the SQL condition text used for searching.
	SearchWhere string

	// PkgModel 是模型包的导入路径。
	// PkgModel is the import path of the model package.
	PkgModel string
	// PkgRepo 是仓储包的导入路径。
	// PkgRepo is the import path of the repository package.
	PkgRepo string
	// PkgService 是服务包的导入路径。
	// PkgService is the import path of the service package.
	PkgService string
	// PkgController 是控制器包的导入路径。
	// PkgController is the import path of the controller package.
	PkgController string

	// Fields 是模型字段（不含自动补写的时间戳字段）。
	// Fields are the model fields (without the automatically added timestamp fields).
	Fields []Field
	// ModelFields 是落盘到模型文件的全部字段，含时间戳字段。
	// ModelFields are all fields written into the model file, timestamp fields included.
	ModelFields []Field
	// FormFields 是表单字段，排除主键与时间戳字段。
	// FormFields are the form fields, excluding the primary key and the timestamp fields.
	FormFields []Field
	// ListFields 是表格列，排除长文本字段。
	// ListFields are the table columns, excluding long text fields.
	ListFields []Field
	// TimeFields 是需要补写的时间戳字段。
	// TimeFields are the timestamp fields that must be added.
	TimeFields []Field
	// SearchColumns 是可搜索的字符串列名。
	// SearchColumns are the searchable string column names.
	SearchColumns []string
	// Primary 是主键字段。
	// Primary is the primary key field.
	Primary Field

	// ListColspan 是列表表头的列数，含末尾的操作列。
	// ListColspan is the number of list columns, including the trailing actions column.
	ListColspan int

	// HasFile 报告表单是否含文件字段，决定表单是否需要 multipart 编码。
	// HasFile reports whether the form carries a file field and therefore needs multipart encoding.
	HasFile bool

	// HasSearch 报告是否存在可搜索的字符串列。
	// HasSearch reports whether the model has searchable string columns.
	HasSearch bool
	// HasTenant reports whether the source model already declares a tenant_id column; when false
	// the generated model appends the standard TenantID field. | HasTenant 报告源模型是否已经声明
	// tenant_id 列；为 false 时生成模型会追加标准的 TenantID 字段。
	HasTenant bool
	// UsesTime 报告模型层是否需要导入 time 包。
	// UsesTime reports whether the model layer needs to import the time package.
	UsesTime bool
	// UsesGorm reports whether the model layer needs gorm.DeletedAt. |
	// UsesGorm 报告模型层是否需要导入 gorm.DeletedAt。
	UsesGorm bool
	// FormTime 报告表单是否需要时间类型，服务层与控制器层据此导入 time 包。
	// FormTime reports whether the form carries a time type; the service and controller layers import the time package accordingly.
	FormTime bool
	// RepoStrings 报告仓储层是否需要导入 strings 包。
	// RepoStrings reports whether the repository layer needs to import the strings package.
	RepoStrings bool
	// ServiceStrings 报告服务层是否需要导入 strings 包。
	// ServiceStrings reports whether the service layer needs to import the strings package.
	ServiceStrings bool
	// CtrlStrings 报告控制器层是否需要导入 strings 包（搜索或表单取值都要用到 TrimSpace）。
	// CtrlStrings reports whether the controller layer needs to import the strings package (both search and form binding call TrimSpace).
	CtrlStrings bool
}

// moduleRef 记录一个已生成模块的注册信息，用于 routes_gen.go 的标记区块。
// moduleRef records one generated module for the marker block of routes_gen.go.
type moduleRef struct {
	// Name 是模块的导出名，即 Deps 结构体的字段名。
	// Name is the exported module name, i.e. the Deps struct field name.
	Name string
	// Snake 是模块的小写下划线名，用于注释文案。
	// Snake is the lower snake case module name used in comments.
	Snake string
}

// routesGenView 是渲染 routes_gen.go 及其标记区块的数据。
// routesGenView is the data for rendering routes_gen.go and its marker block.
type routesGenView struct {
	*tmplView
	// Block 是已渲染好的标记区块文本，仅在整文件渲染时使用。
	// Block is the already rendered marker block, used only when rendering the whole file.
	Block string
	// Modules 是按注册顺序排列的模块清单。
	// Modules is the module list in registration order.
	Modules []moduleRef
}

// 时间戳字段的固定形态，用于判断模型是否已有创建/更新时间列。
// The fixed shape of timestamp fields, used to detect whether the model already carries creation/update columns.
var (
	timestampCreated = Field{
		Name: "created_at", GoName: "CreatedAt", GoType: typeTime,
		Column: "created_at", JSONName: "created_at", Nullable: true, Widget: "date", Label: "CreatedAt",
		Comment: "Creation time maintained by GORM | 由 GORM 自动维护的创建时间",
	}
	timestampUpdated = Field{
		Name: "updated_at", GoName: "UpdatedAt", GoType: typeTime,
		Column: "updated_at", JSONName: "updated_at", Nullable: true, Widget: "date", Label: "UpdatedAt",
		Comment: "Update time maintained by GORM | 由 GORM 自动维护的更新时间",
	}
)

// isTimestampField 报告字段是否为创建/更新时间列。
// isTimestampField reports whether the field is a creation/update time column.
func isTimestampField(f Field) bool {
	switch {
	case f.Column == "created_at", f.Column == "updated_at",
		f.GoName == "CreatedAt", f.GoName == "UpdatedAt":
		return true
	default:
		return false
	}
}

// isDeletedAtField reports whether the field is GORM's soft-delete marker. |
// isDeletedAtField 报告字段是否为 GORM 的软删除标记。
func isDeletedAtField(f Field) bool {
	return f.Column == "deleted_at" || f.GoName == "DeletedAt" || f.GoType == "gorm.DeletedAt"
}

// newView 把模型与选项归一为渲染视图，并补写缺省值。
// newView normalises the model and the options into the render view, filling in defaults.
func newView(m *Model, o Options) (*tmplView, error) {
	if m == nil || len(m.Fields) == 0 {
		return nil, ErrNoFields
	}
	if err := m.prepare(); err != nil {
		return nil, err
	}

	pkg := strings.TrimSpace(o.Package)
	if pkg == "" {
		pkg = FrameworkModule
	}
	name := m.GoName
	snake := snakeCase(name)
	// The snake name is used as a file name and as a template directory; a Windows device name
	// there resolves to a device instead of a path, so it is refused on every platform. | 蛇形名会
	// 用作文件名与模板目录；Windows 上的设备名会解析为设备而不是路径，因此在所有平台都拒绝。
	if windowsDeviceName(snake) {
		return nil, errs.Detail(ErrBadModel, "model %[1]q generates the Windows device name %[2]q; choose another name | 模型 %[1]q 会生成 Windows 设备名 %[2]q，请换一个名称", name, snake)
	}
	module := strings.TrimSpace(o.Module)
	if module == "" {
		module = strings.TrimSpace(m.Module)
	}
	if module == "" {
		module = name
	}

	v := &tmplView{
		Marker:        GeneratedMarker,
		Package:       pkg,
		Name:          name,
		Snake:         snake,
		Var:           unexportedName(name),
		Table:         m.Table,
		Module:        module,
		Route:         "/" + snake,
		Event:         snake + ":changed",
		PkgModel:      pkg + "/internal/model",
		PkgRepo:       pkg + "/internal/repository",
		PkgService:    pkg + "/internal/service",
		PkgController: pkg + "/internal/controller",
	}
	if pk := m.PrimaryKey(); pk != nil {
		v.Primary = *pk
	}

	v.ModelFields = append([]Field(nil), m.Fields...)
	hasCreated, hasUpdated := false, false
	for _, f := range v.ModelFields {
		switch {
		case isTimestampField(f) && (f.Column == "created_at" || f.GoName == "CreatedAt"):
			hasCreated = true
		case isTimestampField(f) && (f.Column == "updated_at" || f.GoName == "UpdatedAt"):
			hasUpdated = true
		}
	}
	if m.Timestamps {
		if !hasCreated {
			v.TimeFields = append(v.TimeFields, timestampCreated)
		}
		if !hasUpdated {
			v.TimeFields = append(v.TimeFields, timestampUpdated)
		}
		v.ModelFields = append(v.ModelFields, v.TimeFields...)
	}

	// Tenant scoping is part of every generated module: the model owns a tenant_id column, the
	// repository filters on it and the controller takes it from the authenticated request. An
	// existing tenant_id field is reused, but it must be a string because auth tenant ids are
	// strings (tn_...). | 租户隔离是每个生成模块的一部分：模型带 tenant_id 列，仓储按它过滤，
	// 控制器从已认证请求中取得它。已有的 tenant_id 字段会被复用，但必须是字符串，因为 auth 的
	// 租户 ID 是字符串（tn_...）。
	for i := range v.ModelFields {
		f := &v.ModelFields[i]
		if !strings.EqualFold(f.Column, "tenant_id") {
			continue
		}
		if f.GoType != "string" {
			return nil, errs.Detail(ErrBadModel, "column tenant_id of %s must be a string | %s 的 tenant_id 列必须是字符串", m.GoName, m.GoName)
		}
		f.GoName = "TenantID"
		f.JSONName = "tenant_id"
		f.Label = "Tenant"
		v.HasTenant = true
	}

	for _, f := range v.ModelFields {
		if f.GoType == typeTime {
			v.UsesTime = true
		}
		if f.GoType == "gorm.DeletedAt" {
			v.UsesGorm = true
		}
		if isTimestampField(f) || isDeletedAtField(f) || f.GoType == "[]byte" {
			continue
		}
		// The tenant column is an internal boundary, never a user-editable form or list column.
		// | 租户列是内部边界，绝不是用户可编辑的表单列或列表列。
		if strings.EqualFold(f.Column, "tenant_id") {
			continue
		}
		// 列表不展示长文本，其余列按模型顺序排列。
		// The list hides long text columns and keeps every other column in model order.
		if !f.IsTextarea() {
			v.ListFields = append(v.ListFields, f)
		}
		if f.Primary {
			continue
		}
		v.FormFields = append(v.FormFields, f)
		if f.GoType == typeTime {
			v.FormTime = true
		}
		if f.IsFile() {
			// 文件字段要求表单以 multipart 提交，控制器也要改成读取上传接口。
			// A file field makes the form submit as multipart and the controller read the upload endpoint.
			v.HasFile = true
		}
		if f.IsString() && f.Required() {
			v.ServiceStrings = true
		}
		if f.IsTime() || f.IsNumber() || (f.IsString() && !f.IsTextarea()) {
			v.CtrlStrings = true
		}
		if f.IsString() && !f.Unique {
			v.SearchColumns = append(v.SearchColumns, f.Column)
		}
	}

	if len(v.SearchColumns) > 0 {
		v.HasSearch = true
		v.RepoStrings = true
		v.CtrlStrings = true
		conds := make([]string, 0, len(v.SearchColumns))
		args := make([]string, 0, len(v.SearchColumns))
		for _, col := range v.SearchColumns {
			conds = append(conds, col+" LIKE ? ESCAPE '!'")
			args = append(args, "like")
		}
		v.SearchWhere = strings.Join(conds, " OR ")
		v.SearchArgs = strings.Join(args, ", ")
	}

	if v.Primary.GoType == typeTime {
		v.UsesTime = true
	}
	if v.Primary.GoType == "" {
		v.Primary.GoType = "int64"
	}
	// 操作列始终存在，空表的 colspan 因此比列表字段多一列。 |
	// The actions column is always present, so an empty table spans one column more than the list fields.
	v.ListColspan = len(v.ListFields) + 1
	// 游标参数需要 TrimSpace，控制器因此总是导入 strings。 |
	// Cursor parameters need TrimSpace, so the controller always imports strings.
	v.CtrlStrings = true
	return v, nil
}
