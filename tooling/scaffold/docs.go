// 本文件把框架文档随生成项目一起发布：生成项目得到的是与框架仓库逐字节相同的指南，而不是外链或第二份副本。
// This file ships the framework guides with every generated project: a project receives the same bytes as the
// framework repository instead of external links or a second copy that could drift.

package scaffold

import (
	"fmt"
	"strings"

	frameworkdocs "github.com/shibingli/yd/docs"
	"github.com/shibingli/ydx/errs"
)

// frameworkDocFiles is the ordered list of guides copied into a generated project. The framework
// release manual and the read-only requirement baseline stay out: they describe the framework
// repository itself, not the application built from it. | frameworkDocFiles 是复制进生成项目的指南顺序。
// 框架发布手册与只读需求基线不在此列：它们描述框架仓库本身，而不是由它构建的应用。
var frameworkDocFiles = []string{
	"attachments.md",
	"dictionary.md",
	"workflow.md",
	"message-center.md",
	"cache.md",
	"redis-asynq.md",
	"realtime-transports.md",
	"node-identity.md",
	"captcha.md",
	"gorm-dialects.md",
	"icon-usage.md",
	"frontend-ui.md",
	"frontend-components.md",
	"render-usage.md",
	"render-performance.md",
	"middleware-metrics.md",
	"runtime-gc.md",
	"verification.md",
	"yd-usage.md",
}

// frameworkDocs loads the selected guides and builds the local documentation index. |
// frameworkDocs 加载选中的指南并生成本地文档索引。
func frameworkDocs(name string) ([]plannedFile, error) {
	files := make([]plannedFile, 0, len(frameworkDocFiles)+1)
	var index strings.Builder
	index.WriteString("# 项目文档 | Project documentation\n\n")
	index.WriteString("本目录由 `yd new` 随项目生成；除 `developer_guidelines.md` 由项目模板生成、")
	index.WriteString("本文件为索引外，其余指南与框架仓库的 `docs/*.md` 逐字节一致，")
	index.WriteString("修改框架文档后重新生成项目即可同步。\n\n")
	index.WriteString("Development constraints live in [developer_guidelines.md](developer_guidelines.md); ")
	index.WriteString("the guides below describe the framework features wired into this project.\n\n")
	for _, nameInDocs := range frameworkDocFiles {
		data, err := frameworkdocs.FS.ReadFile(nameInDocs)
		if err != nil {
			return nil, errs.WrapDetail(err, ErrTemplateRead, "docs/%[1]s | 文档=%[1]s", nameInDocs)
		}
		fmt.Fprintf(&index, "- [%s](%s)\n", docTitle(nameInDocs, data), nameInDocs)
		files = append(files, plannedFile{
			path:    projectPrefix(name) + "docs/" + nameInDocs,
			content: data,
		})
	}
	files = append(files, plannedFile{
		path:    projectPrefix(name) + "docs/README.md",
		content: []byte(index.String()),
	})
	return files, nil
}

// docTitle returns the first level-one heading, or the file name when a guide has none. |
// docTitle 返回首个一级标题；指南没有标题时回退到文件名。
func docTitle(file string, data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			if title := strings.TrimSpace(strings.TrimPrefix(line, "# ")); title != "" {
				return title
			}
		}
	}
	return file
}
