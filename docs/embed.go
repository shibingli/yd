// Package docs embeds the framework guides so the scaffold can ship the exact same bytes inside
// every generated project. | Package docs 内嵌框架文档，使脚手架能把逐字节相同的说明随每个生成项目一起发布。
//
// The embed directive deliberately covers every Markdown file in this directory: the scaffold picks
// the guides a project should receive, while this package stays a mechanical container with no
// second copy to drift. | embed 指令刻意覆盖本目录的全部 Markdown 文件：由脚手架挑选项目应获得的
// 指南，本包只是机械容器，不保存会漂移的第二份副本。
package docs

import "embed"

// FS contains every Markdown guide under docs/. | FS 包含 docs/ 下的全部 Markdown 指南。
//
//go:embed *.md
var FS embed.FS
