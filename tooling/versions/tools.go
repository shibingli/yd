//go:build tools

// Package versions keeps the scaffold-only ydx modules in the framework's go.mod. The generated
// project template pins every ydx module it may import, but the framework itself does not compile
// some of them; a tools-tag blank import gives those modules a single versioned source in go.mod
// without linking them into the yd binary. Add a new scaffold-only module here instead of editing
// a second version table. | Package versions 把仅脚手架使用的 ydx 模块保留在框架 go.mod 中。生成项目
// 模板会锁定它可能导入的每个 ydx 模块，但框架本身并不编译其中一部分；tools 标签的 blank import
// 让这些模块在 go.mod 中有唯一的版本来源，又不会链接进 yd 二进制。新增仅脚手架模块时加到这里，
// 不要再维护第二份版本表。
package versions

import (
	_ "github.com/shibingli/ydx/cron"
	_ "github.com/shibingli/ydx/workflow"
)
