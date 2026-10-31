//go:build tools

// Package wafdeps pins the optional WAF dependencies in a nested module so the engine's
// third-party requirements (libinjection-go, wazero) and the CRS rule bundle never enter the
// framework's root module graph. | Package wafdeps 在嵌套模块中锁定可选的 WAF 依赖，使引擎的
// 第三方依赖（libinjection-go、wazero）与 CRS 规则包永远不会进入框架根模块图。
package wafdeps

import (
	_ "github.com/corazawaf/coraza-coreruleset/crs/v4"
	_ "github.com/shibingli/ydx/waf"
)
