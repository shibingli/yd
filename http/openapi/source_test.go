package openapi

import (
	"testing"
	"testing/fstest"

	"github.com/spf13/afero"

	"github.com/shibingli/ydx/afs"
)

// writeSource writes one fixture file into the memory filesystem. | writeSource 把单个测试文件写入
// 内存文件系统。
func writeSource(t *testing.T, fsys afero.Fs, path, src string) {
	t.Helper()
	if err := afero.WriteFile(fsys, path, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestParseSourceCombinesDialects proves that one scan returns both dialects and matches the
// separate API calls. | TestParseSourceCombinesDialects 证明一次扫描同时返回两个方言，且与分开的
// API 调用结果一致。
func TestParseSourceCombinesDialects(t *testing.T) {
	fsys := afero.NewMemMapFs()
	writeSource(t, fsys, "/src/handlers.go", `package demo

// Get returns one item. | Get 返回单个条目。
//
// @Summary Get item | 获取条目
// @Tags items
// @Param id path string true "id | 标识"
// @Success 200 {object} string "ok | 成功"
// @Router /items/{id} [get]
func Get() {}

// Item is a payload. | Item 是载荷。
//
// @Name Item
// @Description payload | 载荷
type Item struct {
	ID int64 `+"`json:\"id\"`"+`
}
`)
	writeSource(t, fsys, "/src/plain.go", `package demo

// Ping has no type annotations. | Ping 没有类型注解。
//
// @Summary Ping | 探针
// @Tags system
// @Success 200 {string} string "ok | 成功"
// @Router /ping [get]
func Ping() {}
`)

	source, err := ParseSource(fsys, []string{"/src"})
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	if len(source.Annotations) != 2 {
		t.Fatalf("annotations = %v | 处理器注解数量不正确", source.Annotations)
	}
	if len(source.Types.Schemas) != 1 {
		t.Fatalf("schemas = %v | 类型注解数量不正确", source.Types.Schemas)
	}
	if _, ok := source.Types.Schemas["Item"]; !ok {
		t.Fatalf("missing Item schema | 缺少 Item 类型注解")
	}

	annotations, err := ParseAnnotations(fsys, []string{"/src"})
	if err != nil {
		t.Fatalf("ParseAnnotations: %v", err)
	}
	types, err := ParseTypeAnnotations(fsys, []string{"/src"})
	if err != nil {
		t.Fatalf("ParseTypeAnnotations: %v", err)
	}
	if len(annotations) != len(source.Annotations) || len(types.Schemas) != len(source.Types.Schemas) {
		t.Fatalf("one-pass and split scans disagree | 单次扫描与分开扫描结果不一致")
	}
	for key := range annotations {
		if _, ok := source.Annotations[key]; !ok {
			t.Errorf("one-pass scan lost %q | 单次扫描丢失 %q", key, key)
		}
	}
}

// TestParseSourceDeterministic verifies that repeated parallel scans agree. | TestParseSourceDeterministic
// 校验重复的并行扫描结果一致。
func TestParseSourceDeterministic(t *testing.T) {
	fsys := afero.NewMemMapFs()
	for i := 0; i < 24; i++ {
		writeSource(t, fsys, "/src/"+string(rune('a'+i))+".go", `package demo

// Handler handles one route. | Handler 处理一条路由。
//
// @Summary Handler | 处理器
// @Tags bench
// @Success 200 {string} string "ok | 成功"
// @Router /r`+string(rune('a'+i))+` [get]
func Handler() {}

// @Name Type`+string(rune('A'+i))+`
// @Description payload | 载荷
type Payload struct {
	ID int64 `+"`json:\"id\"`"+`
}
`)
	}
	first, err := ParseSource(fsys, []string{"/src"})
	if err != nil {
		t.Fatalf("ParseSource first: %v", err)
	}
	for run := 0; run < 5; run++ {
		next, err := ParseSource(fsys, []string{"/src"})
		if err != nil {
			t.Fatalf("ParseSource run %d: %v", run, err)
		}
		if len(next.Annotations) != len(first.Annotations) || len(next.Types.Schemas) != len(first.Types.Schemas) {
			t.Fatalf("run %d differs in size | 第 %d 次结果数量不同", run, run)
		}
		for key := range first.Annotations {
			if _, ok := next.Annotations[key]; !ok {
				t.Fatalf("run %d lost %q | 第 %d 次丢失 %q", run, key, run, key)
			}
		}
		for key := range first.Types.Schemas {
			if _, ok := next.Types.Schemas[key]; !ok {
				t.Fatalf("run %d lost type %q | 第 %d 次丢失类型 %q", run, key, run, key)
			}
		}
	}
}

// TestParseSourceReadsEmbeddedTree proves annotation scanning works on an io/fs backend: walking
// it with afero.Walk joins nested entries with a backslash on Windows, which fs.ValidPath rejects. |
// TestParseSourceReadsEmbeddedTree 证明注解扫描在 io/fs 后端上可用：用 afero.Walk 遍历时，Windows
// 会用反斜杠拼接嵌套条目，而 fs.ValidPath 会拒绝它。
func TestParseSourceReadsEmbeddedTree(t *testing.T) {
	fsys := afs.NewEmbed(fstest.MapFS{
		"src/api/handlers.go": &fstest.MapFile{Data: []byte(`package api

// Get returns one item. | Get 返回单个条目。
//
// @Summary Get item | 获取条目
// @Tags items
// @Router /items/{id} [get]
func Get() {}
`)},
	})
	source, err := ParseSource(fsys, []string{"src"})
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	if _, ok := source.Annotations["GET /items/{id}"]; !ok {
		t.Fatalf("缺少处理器注解 | missing handler annotation: %v", source.Annotations)
	}
}
