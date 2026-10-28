//go:build tools

// Package scaffold's tools build keeps the ydx modules that only generated projects import in the
// framework module graph. Without these requirements `yd versions sync` cannot record their tags,
// and a generated go.mod pinned to a yd release omits them even though the generated router imports
// them. The file is excluded from normal builds, so no scaffold-only component reaches the yd
// binary. | 本文件的 tools 构建把「只有生成项目才导入」的 ydx 模块保留在框架模块图中。缺少这些依赖时，
// `yd versions sync` 无法记录它们的标签，而锁定到 yd 发行版的生成 go.mod 会漏掉它们——即使生成项目的
// router 会导入这些包。该文件不参与普通构建，因此这些组件不会进入 yd 二进制。
package scaffold

import (
	// Blank imports only keep the module requirements alive for tidy/sync. | 空导入只为 tidy/sync 保留模块依赖。
	_ "github.com/shibingli/ydx/asynq"
	_ "github.com/shibingli/ydx/openapi"
	_ "github.com/shibingli/ydx/openapi/ydrouter"
)
