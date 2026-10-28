package dictionary

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// BuiltinItem is one item of a predefined dictionary. | BuiltinItem 是一个预置数据字典的字典项。
type BuiltinItem struct {
	// Value is the machine value. | Value 是机器值。
	Value string
	// Label is the Chinese label. | Label 是中文标签。
	Label string
	// LabelEN is the English label. | LabelEN 是英文标签。
	LabelEN string
	// Default marks the preselected item. | Default 标记预选项。
	Default bool
	// Sort is the display order. | Sort 是展示顺序。
	Sort int
}

// BuiltinType is one predefined dictionary type and its items. | BuiltinType 是一个预置数据字典类型及其字典项。
type BuiltinType struct {
	// Code is the immutable code. | Code 是不可变编码。
	Code string
	// Name is the Chinese display name. | Name 是中文展示名。
	Name string
	// NameEN is the English display name. | NameEN 是英文展示名。
	NameEN string
	// Description explains the dictionary. | Description 说明字典用途。
	Description string
	// Items are the dictionary items in display order. | Items 是按展示顺序排列的字典项。
	Items []BuiltinItem
	// Sort is the display order in administration lists. | Sort 是管理列表中的展示顺序。
	Sort int
}

// builtinCatalog is the framework's predefined dictionary catalogue. | builtinCatalog 是框架预置的字典目录。
//
// The catalogue is generic on purpose: every generated project can use these four dictionaries for
// genders, booleans, priorities and task states without inventing its own vocabulary. A tenant may
// rename labels or disable a type; the seeder only adds missing types and never overwrites a row. |
// 这份目录刻意保持通用：每个生成项目都能直接使用性别、是否、优先级与任务状态这四组字典，
// 不必自造词汇。租户可以改标签或停用类型；种子只补缺失类型，从不覆盖已有行。
var builtinCatalog = []BuiltinType{
	{
		Code:        "gender",
		Name:        "性别",
		NameEN:      "Gender",
		Description: "通用性别选项 | common gender options",
		Sort:        10,
		Items: []BuiltinItem{
			{Value: "male", Label: "男", LabelEN: "Male", Sort: 10},
			{Value: "female", Label: "女", LabelEN: "Female", Sort: 20},
			{Value: "unknown", Label: "未知", LabelEN: "Unknown", Default: true, Sort: 30},
		},
	},
	{
		Code:        "yes_no",
		Name:        "是否",
		NameEN:      "Yes or no",
		Description: "通用是/否选项 | common yes/no options",
		Sort:        20,
		Items: []BuiltinItem{
			{Value: "yes", Label: "是", LabelEN: "Yes", Sort: 10},
			{Value: "no", Label: "否", LabelEN: "No", Default: true, Sort: 20},
		},
	},
	{
		Code:        "priority",
		Name:        "优先级",
		NameEN:      "Priority",
		Description: "通用优先级选项 | common priority options",
		Sort:        30,
		Items: []BuiltinItem{
			{Value: "low", Label: "低", LabelEN: "Low", Sort: 10},
			{Value: "normal", Label: "普通", LabelEN: "Normal", Default: true, Sort: 20},
			{Value: "high", Label: "高", LabelEN: "High", Sort: 30},
			{Value: "urgent", Label: "紧急", LabelEN: "Urgent", Sort: 40},
		},
	},
	{
		Code:        "task_status",
		Name:        "任务状态",
		NameEN:      "Task status",
		Description: "通用任务状态选项 | common task status options",
		Sort:        40,
		Items: []BuiltinItem{
			{Value: "pending", Label: "待处理", LabelEN: "Pending", Default: true, Sort: 10},
			{Value: "in_progress", Label: "进行中", LabelEN: "In progress", Sort: 20},
			{Value: "done", Label: "已完成", LabelEN: "Done", Sort: 30},
			{Value: "cancelled", Label: "已取消", LabelEN: "Cancelled", Sort: 40},
		},
	},
}

// BuiltinCatalog returns a deep copy of the predefined dictionary catalogue. | BuiltinCatalog 返回预置字典目录的深拷贝。
func BuiltinCatalog() []BuiltinType {
	out := make([]BuiltinType, len(builtinCatalog))
	for i, definition := range builtinCatalog {
		out[i] = definition
		out[i].Items = append([]BuiltinItem(nil), definition.Items...)
	}
	return out
}

// BuiltinRows materializes the predefined catalogue for one owner (a tenant or the system layer).
// Identifiers are derived from the owner, type code and item value, so two replicas starting at the
// same moment generate identical rows and the unique indexes turn the race into a no-op instead of
// an orphaned item. | BuiltinRows 为一个归属方（租户或系统层）生成预置目录行。标识由归属方、
// 类型编码与字典项值派生，因此同一瞬间启动的两个副本会生成完全相同的行，唯一索引把竞争变成
// 无操作，而不是留下孤立字典项。
func BuiltinRows(tenantID string, now time.Time) ([]Type, []Item) {
	types := make([]Type, 0, len(builtinCatalog))
	items := make([]Item, 0, len(builtinCatalog)*4)
	for _, definition := range builtinCatalog {
		typeID := builtinID("dict_", tenantID, definition.Code, "")
		types = append(types, Type{
			CreatedAt:   now,
			UpdatedAt:   now,
			ID:          typeID,
			TenantID:    tenantID,
			Code:        definition.Code,
			Name:        definition.Name,
			NameEN:      definition.NameEN,
			Description: definition.Description,
			Status:      StatusEnabled,
			Builtin:     true,
			Sort:        definition.Sort,
		})
		for _, definitionItem := range definition.Items {
			items = append(items, Item{
				CreatedAt: now,
				UpdatedAt: now,
				ID:        builtinID("dit_", tenantID, definition.Code, definitionItem.Value),
				TenantID:  tenantID,
				TypeID:    typeID,
				Value:     definitionItem.Value,
				Label:     definitionItem.Label,
				LabelEN:   definitionItem.LabelEN,
				Status:    StatusEnabled,
				IsDefault: definitionItem.Default,
				Sort:      definitionItem.Sort,
			})
		}
	}
	return types, items
}

// builtinID derives a stable identifier from the tenant, the type code and the item value. |
// builtinID 从租户、类型编码与字典项值派生稳定标识。
func builtinID(prefix, tenantID, code, value string) string {
	digest := sha256.Sum256([]byte(prefix + "|" + tenantID + "|" + code + "|" + value))
	return prefix + hex.EncodeToString(digest[:16])
}
