// Package gen_test 以黑盒方式验证代码生成器的公开契约。
// Package gen_test verifies the public contract of the code generator from the outside.
package gen_test

import (
	"errors"
	"testing"
	"time"

	"github.com/shibingli/yd/gen"
)

// TestParseFieldsSpec 覆盖 `name:string:required` 形式的字段描述解析与类型映射。
// TestParseFieldsSpec covers the `name:string:required` field-spec form and its type mapping.
func TestParseFieldsSpec(t *testing.T) {
	spec := "name:string:required,age:int,active:bool,score:float,published:time,avatar:file,body:text"
	fields, err := gen.ParseFields(spec)
	if err != nil {
		t.Fatalf("ParseFields(%q) 返回错误: %v", spec, err)
	}
	if len(fields) != 7 {
		t.Fatalf("字段数 = %d，期望 7", len(fields))
	}

	want := []struct {
		name   string
		goName string
		goType string
		widget string
	}{
		{"name", "Name", "string", "text"},
		{"age", "Age", "int64", "number"},
		{"active", "Active", "bool", "checkbox"},
		{"score", "Score", "float64", "number"},
		{"published", "Published", "time.Time", "date"},
		{"avatar", "Avatar", "string", "file"},
		{"body", "Body", "string", "textarea"},
	}
	for i, w := range want {
		got := fields[i]
		if got.Name != w.name {
			t.Errorf("fields[%d].Name = %q，期望 %q", i, got.Name, w.name)
		}
		if got.GoName != w.goName {
			t.Errorf("fields[%d].GoName = %q，期望 %q", i, got.GoName, w.goName)
		}
		if got.GoType != w.goType {
			t.Errorf("fields[%d].GoType = %q，期望 %q", i, got.GoType, w.goType)
		}
		if got.Widget != w.widget {
			t.Errorf("fields[%d].Widget = %q，期望 %q", i, got.Widget, w.widget)
		}
		if got.Column != w.name {
			t.Errorf("fields[%d].Column = %q，期望 %q", i, got.Column, w.name)
		}
		if got.JSONName != w.name {
			t.Errorf("fields[%d].JSONName = %q，期望 %q", i, got.JSONName, w.name)
		}
		if got.Label != w.goName {
			t.Errorf("fields[%d].Label = %q，期望 %q", i, got.Label, w.goName)
		}
	}

	if got := fields[0].Validate; got != "required" {
		t.Errorf("name 字段的 Validate = %q，期望 %q", got, "required")
	}
	if fields[0].Primary || fields[0].Unique || fields[0].Indexed || fields[0].Nullable {
		t.Errorf("name 字段不应带 primary/unique/index/nullable 标记: %+v", fields[0])
	}

	t.Run("修饰符", func(t *testing.T) {
		// The modifiers map onto the column flags and the validation rules. | 修饰符映射到列标记与校验规则。
		mods, err := gen.ParseFields("id:int:primary,title:string:required,max=64,email:string:unique,slug:string:index,note:text:nullable")
		if err != nil {
			t.Fatalf("ParseFields 返回错误: %v", err)
		}
		if !mods[0].Primary {
			t.Error("id 字段应为 primary")
		}
		if !mods[1].Required() {
			t.Error("title 字段应为 required")
		}
		if mods[1].Validate != "required,max=64" || mods[1].Size != 64 {
			t.Errorf("title 字段 Validate/Size = %q/%d，期望 %q/64", mods[1].Validate, mods[1].Size, "required,max=64")
		}
		if !mods[2].Unique {
			t.Error("email 字段应为 unique")
		}
		if !mods[3].Indexed {
			t.Error("slug 字段应为 index")
		}
		if !mods[4].Nullable {
			t.Error("note 字段应为 nullable")
		}
	})

	t.Run("非法输入", func(t *testing.T) {
		cases := []struct {
			spec string
			want error
		}{
			{"", gen.ErrBadFieldSpec},
			{"   ", gen.ErrBadFieldSpec},
			{":string", gen.ErrBadFieldSpec},
			{"name:", gen.ErrBadFieldSpec},
			{"name:money", gen.ErrUnknownType},
			{"name:string,", gen.ErrBadFieldSpec},
		}
		for _, c := range cases {
			_, err := gen.ParseFields(c.spec)
			if !errors.Is(err, c.want) {
				t.Errorf("ParseFields(%q) 错误 = %v，期望 errors.Is(_, %v)", c.spec, err, c.want)
			}
		}
	})
}

// genTaggedUser 是 ModelFromStruct 的夹具：涵盖 gorm / json / yd 三种标签。
// genTaggedUser is the ModelFromStruct fixture: it covers gorm / json / yd tags.
type genTaggedUser struct {
	ID        int64     `gorm:"column:id;primaryKey" json:"id"`
	Name      string    `gorm:"column:name;size:64;not null;uniqueIndex" json:"name" yd:"label=姓名;widget=text;validate=required,max=64"`
	Email     string    `gorm:"column:email;index" json:"email" yd:"required"`
	Avatar    string    `gorm:"column:avatar" json:"avatar" yd:"widget=file"`
	Age       int64     `gorm:"column:age" json:"age"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName 让 ModelFromStruct 优先采用模型自报的表名。
// TableName makes ModelFromStruct prefer the table name reported by the model.
func (genTaggedUser) TableName() string { return "sys_users" }

// genBase 是被内嵌的公共字段集合，用于验证匿名字段会被展开。
// genBase is an embedded common column set, used to verify that anonymous fields are flattened.
type genBase struct {
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
}

// genEmbedded 内嵌 genBase，用于验证字段展开。
// genEmbedded embeds genBase to verify field flattening.
type genEmbedded struct {
	genBase
	Title string `gorm:"column:title;size:128;not null" json:"title"`
}

// TestModelFromStructReadsTags 验证 struct → Model 的标签解析。
// TestModelFromStructReadsTags verifies struct → Model tag parsing.
func TestModelFromStructReadsTags(t *testing.T) {
	m, err := gen.ModelFromStruct(&genTaggedUser{}, gen.StructOptions{Module: "cms"})
	if err != nil {
		t.Fatalf("ModelFromStruct 返回错误: %v", err)
	}
	if m.GoName != "GenTaggedUser" {
		t.Errorf("GoName = %q，期望 %q", m.GoName, "GenTaggedUser")
	}
	if m.Table != "sys_users" {
		t.Errorf("Table = %q，期望 %q（应采信 TableName 方法）", m.Table, "sys_users")
	}
	if m.Module != "cms" {
		t.Errorf("Module = %q，期望 %q", m.Module, "cms")
	}
	if !m.Timestamps {
		t.Error("Timestamps 应为 true（存在 CreatedAt/UpdatedAt）")
	}
	if len(m.Fields) != 7 {
		t.Fatalf("字段数 = %d，期望 7", len(m.Fields))
	}

	id := m.Field("id")
	if id == nil || !id.Primary {
		t.Fatalf("id 字段缺失或未标记主键: %+v", id)
	}

	name := m.Field("name")
	if name == nil {
		t.Fatal("name 字段缺失")
	}
	if name.Column != "name" || name.JSONName != "name" || name.GoName != "Name" || name.GoType != "string" {
		t.Errorf("name 字段基础属性错误: %+v", name)
	}
	if name.Size != 64 {
		t.Errorf("name.Size = %d，期望 64", name.Size)
	}
	if name.Nullable {
		t.Error("name 字段带 not null，Nullable 应为 false")
	}
	if !name.Unique {
		t.Error("name 字段带 uniqueIndex，Unique 应为 true")
	}
	if name.Label != "姓名" || name.Widget != "text" || name.Validate != "required,max=64" {
		t.Errorf("name 字段 yd 标签解析错误: label=%q widget=%q validate=%q", name.Label, name.Widget, name.Validate)
	}

	if email := m.Field("email"); email == nil || !email.Indexed || !email.Required() {
		t.Errorf("email 字段解析错误: %+v", email)
	}
	if avatar := m.Field("avatar"); avatar == nil || avatar.Widget != "file" {
		t.Errorf("avatar 字段解析错误: %+v", avatar)
	}
	if age := m.Field("age"); age == nil || age.GoType != "int64" || !age.Nullable {
		t.Errorf("age 字段解析错误: %+v", age)
	}

	t.Run("选项覆盖", func(t *testing.T) {
		no := false
		got, err := gen.ModelFromStruct(genTaggedUser{}, gen.StructOptions{
			Name:       "Account",
			Table:      "accounts",
			Module:     "iam",
			Timestamps: &no,
		})
		if err != nil {
			t.Fatalf("ModelFromStruct 返回错误: %v", err)
		}
		if got.GoName != "Account" || got.Table != "accounts" || got.Module != "iam" {
			t.Errorf("选项未被应用: GoName=%q Table=%q Module=%q", got.GoName, got.Table, got.Module)
		}
		if got.Timestamps {
			t.Error("Timestamps 被显式关闭后应为 false")
		}
	})

	t.Run("内嵌字段展开", func(t *testing.T) {
		got, err := gen.ModelFromStruct(genEmbedded{}, gen.StructOptions{})
		if err != nil {
			t.Fatalf("ModelFromStruct 返回错误: %v", err)
		}
		if got.Field("created_at") == nil {
			t.Errorf("内嵌字段未被展开: %+v", got.Fields)
		}
		if got.Field("title") == nil {
			t.Errorf("普通字段缺失: %+v", got.Fields)
		}
		if !got.Timestamps {
			t.Error("内嵌 CreatedAt 后 Timestamps 应为 true")
		}
		if got.Table != "gen_embeddeds" {
			t.Errorf("Table = %q，期望 %q", got.Table, "gen_embeddeds")
		}
	})

	t.Run("非法输入", func(t *testing.T) {
		if _, err := gen.ModelFromStruct(42, gen.StructOptions{}); !errors.Is(err, gen.ErrUnsupportedValue) {
			t.Errorf("标量输入错误 = %v，期望 ErrUnsupportedValue", err)
		}
		if _, err := gen.ModelFromStruct(struct{}{}, gen.StructOptions{}); !errors.Is(err, gen.ErrNoFields) {
			t.Errorf("空结构体错误 = %v，期望 ErrNoFields", err)
		}
	})
}
