// layers.go — the generated project's main/user asset boundary.
// layers.go — 生成项目的主资源与用户资源边界。
//
// Most scaffold files are main resources: `yd sync` re-renders them and replaces local edits (after a
// backup). The two files below are user resources: `yd sync` creates them when missing and otherwise
// leaves them alone, so a main-resource upgrade never forces a manual merge. The boundary lives here,
// next to the template tree, so the sync and the guards share one definition.
// 大多数脚手架文件是主资源：`yd sync` 重新渲染它们，并在备份后替换本地修改。下面两个文件是用户资源：
// `yd sync` 只在缺失时创建，其余情况原样保留，因此主资源升级永远不会强迫用户手工合并。这条边界与
// 模板树放在一起，使同步逻辑与守卫共用同一个定义。
package scaffold

import (
	"path"
	"path/filepath"
	"strings"
)

const (
	// UserScriptPath is the project-owned script layer loaded after yd-app.js. | UserScriptPath 是
	// 在 yd-app.js 之后加载的项目自有脚本层。
	UserScriptPath = "web/static/js/yd-app.user.js"
	// UserStylePath is the project-owned stylesheet layer imported after yd-main.css. | UserStylePath
	// 是在 yd-main.css 之后导入的项目自有样式层。
	UserStylePath = "web/static/css/user.css"
	// UserScriptURL is the same-origin URL of UserScriptPath used by the production bundle. It must
	// stay in step with that path; TestUserOwnedAssetPaths asserts the relationship. | UserScriptURL
	// 是生产 bundle 使用的 UserScriptPath 同源地址；两者必须保持一致，TestUserOwnedAssetPaths
	// 会断言这层关系。
	UserScriptURL = "/static/js/yd-app.user.js"
)

// mainAssetPaths lists the scaffold-owned front-end assets whose local edits belong in the user
// layers. `yd sync` may replace them; the migration warning uses the same list. |
// mainAssetPaths 列出脚手架拥有的前端主资源；对它们的本地修改应放进用户资源层。`yd sync` 可以替换
// 它们，迁移提示也使用同一份清单。
var mainAssetPaths = []string{
	"web/static/js/yd-app.js",
	"web/static/css/app.css",
	"web/static/css/yd-main.css",
	"web/static/css/components.css",
	"web/static/css/ui.css",
}

// UserOwnedPaths returns the project-relative user-owned asset paths in load order. | UserOwnedPaths
// 按加载顺序返回项目相对的用户资源路径。
//
// The returned slice is a fresh copy so callers cannot mutate the boundary. | 返回的是新副本，调用方
// 不能修改这条边界。
func UserOwnedPaths() []string {
	return []string{UserScriptPath, UserStylePath}
}

// MainAssetPaths returns the project-relative main asset paths as a fresh copy. | MainAssetPaths
// 以新副本返回项目相对的主资源路径。
func MainAssetPaths() []string {
	return append([]string(nil), mainAssetPaths...)
}

// IsMainAsset reports whether a project-relative path is a scaffold-owned main asset. The check is
// exact, matching IsUserOwned. | IsMainAsset 报告一个项目相对路径是否是属于脚手架的主资源；与
// IsUserOwned 一样按精确路径比较。
func IsMainAsset(rel string) bool {
	cleaned := path.Clean(filepath.ToSlash(strings.TrimSpace(rel)))
	for _, main := range mainAssetPaths {
		if cleaned == main {
			return true
		}
	}
	return false
}

// IsUserOwned reports whether a project-relative path is a user resource that `yd sync` must never
// overwrite. | IsUserOwned 报告一个项目相对路径是否是 `yd sync` 绝不能覆盖的用户资源。
//
// Both slash forms and a leading "./" are accepted; the comparison is exact, so a main asset that
// merely shares a suffix is never mistaken for a user file. | 同时接受斜杠形式与前导 "./"；比较是精确的，
// 仅共享后缀的主资源不会被误判为用户文件。
func IsUserOwned(rel string) bool {
	cleaned := path.Clean(filepath.ToSlash(strings.TrimSpace(rel)))
	switch cleaned {
	case UserScriptPath, UserStylePath:
		return true
	default:
		return false
	}
}
