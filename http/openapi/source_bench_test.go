package openapi

import (
	"fmt"
	"testing"

	"github.com/spf13/afero"
)

// benchSourceFS builds a memory filesystem with files annotated in both dialects. | benchSourceFS
// 构造一个包含双方言注解文件的内存文件系统。
func benchSourceFS(b *testing.B, files int) (afero.Fs, []string) {
	b.Helper()
	fsys := afero.NewMemMapFs()
	dir := "/src"
	for i := 0; i < files; i++ {
		src := fmt.Sprintf(`package demo

// Handler reports one value. | Handler 返回单个值。
//
// @Summary Handler | 处理器
// @Tags bench
// @Param id path string true "id | 标识"
// @Success 200 {object} string "ok | 成功"
// @Router /items/%d [get]
func Handler%d() {}

// Item is a benchmark payload. | Item 是基准载荷。
//
// @Name Item%d
// @Description item payload | 条目载荷
type Item%d struct {
	ID   int64  `+"`json:\"id\"`"+`
	Name string `+"`json:\"name,omitempty\"`"+`
}
`, i, i, i, i)
		if err := afero.WriteFile(fsys, fmt.Sprintf("%s/f%d.go", dir, i), []byte(src), 0o644); err != nil {
			b.Fatalf("write fixture: %v", err)
		}
	}
	return fsys, []string{dir}
}

// benchScan is the one-pass scan used by the benchmark. | benchScan 是基准使用的一次性扫描。
func benchScan(b *testing.B, fsys afero.Fs, dirs []string, files int) {
	b.Helper()
	source, err := ParseSource(fsys, dirs)
	if err != nil {
		b.Fatalf("ParseSource: %v", err)
	}
	if len(source.Annotations) != files || len(source.Types.Schemas) != files {
		b.Fatalf("unexpected result: %d annotations, %d schemas",
			len(source.Annotations), len(source.Types.Schemas))
	}
}

// BenchmarkParseSource compares the one-pass scan with the two-pass API pair and with a
// sequential scan over the same files. | BenchmarkParseSource 对比一次性扫描、两次 API 调用以及
// 同一批文件的顺序扫描。
func BenchmarkParseSource(b *testing.B) {
	for _, files := range []int{64, 256} {
		fsys, dirs := benchSourceFS(b, files)
		allFiles, err := annotationFiles(fsys, dirs)
		if err != nil {
			b.Fatalf("list files: %v", err)
		}
		b.Run(fmt.Sprintf("files=%d", files), func(b *testing.B) {
			b.Run("one-pass", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					benchScan(b, fsys, dirs, files)
				}
			})
			b.Run("two-pass", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					annotations, err := ParseAnnotations(fsys, dirs)
					if err != nil {
						b.Fatalf("ParseAnnotations: %v", err)
					}
					types, err := ParseTypeAnnotations(fsys, dirs)
					if err != nil {
						b.Fatalf("ParseTypeAnnotations: %v", err)
					}
					if len(annotations) != files || len(types.Schemas) != files {
						b.Fatalf("unexpected two-pass result")
					}
				}
			})
			b.Run("serial", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					source := &Source{Annotations: map[string]Annotation{}, Types: newTypeAnnotations()}
					for _, path := range allFiles {
						result := scanSourceFile(fsys, path, true, true)
						if result.err != nil {
							b.Fatalf("scanSourceFile: %v", result.err)
						}
						source.blocks = append(source.blocks, result.annotations...)
						source.Types.merge(result.types)
					}
					for _, annotation := range source.blocks {
						if key := annotation.key(); key != "" {
							if _, duplicate := source.Annotations[key]; !duplicate {
								source.Annotations[key] = annotation
							}
						}
					}
					if len(source.Annotations) != files || len(source.Types.Schemas) != files {
						b.Fatalf("unexpected sequential result")
					}
				}
			})
		})
	}
}
