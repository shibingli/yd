//go:build tools

// Package wafdeps pins the optional WAF dependencies in a nested module so Coraza never enters
// the framework's root module graph. | Package wafdeps 在嵌套模块中锁定可选的 WAF 依赖，使 Coraza
// 永远不会进入框架根模块的依赖图。
package wafdeps

import (
	_ "github.com/corazawaf/coraza-coreruleset"
	_ "github.com/shibingli/ydx/middleware/waf"
)
