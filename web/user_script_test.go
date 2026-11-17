// user_script_test.go — the framework's no-op user script fallback.
// user_script_test.go — 框架提供的空用户脚本兜底。
//
// A generated project loads `/static/js/yd-app.user.js` after the main shell script. Projects created
// before the layered assets existed have no project copy, so the merged static tree must still resolve
// the path to a harmless script instead of a 404 (the production bundler reads the same path).
// 生成项目在主外壳脚本之后加载 `/static/js/yd-app.user.js`。分层资源出现之前创建的项目没有项目副本，
// 因此合并静态树必须让该路径解析到一个无害脚本而不是 404（生产 bundle 读取的是同一路径）。
package web

import (
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// TestFrameworkShipsFallbackUserScript asserts the fallback exists and is recognisably a user script.
// | TestFrameworkShipsFallbackUserScript 断言兜底脚本存在，并且能辨认出是用户脚本。
func TestFrameworkShipsFallbackUserScript(t *testing.T) {
	data, err := afero.ReadFile(FS(), "static/js/yd-app.user.js")
	if err != nil {
		t.Fatalf("framework lacks static/js/yd-app.user.js: %v | 框架缺少 static/js/yd-app.user.js: %v", err, err)
	}
	if !strings.Contains(string(data), "yd-app.user.js") {
		t.Errorf("the fallback does not identify itself as yd-app.user.js | 兜底脚本没有标明自己是 yd-app.user.js")
	}
}
