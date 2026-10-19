// Package envx normalizes runtime environment names such as app.env. | Package envx 归一化 app.env 等运行环境名。
//
// Full names and the short aliases dev/prod are accepted interchangeably; matching is
// case-insensitive and surrounding blanks are ignored. | 完整名与 dev/prod 短别名可互换使用；匹配忽略大小写与首尾空白。
package envx

import "strings"

const (
	// Development is the canonical development environment name. | Development 是开发环境的规范名。
	Development = "development"
	// Production is the canonical production environment name. | Production 是生产环境的规范名。
	Production = "production"

	// shortDevelopment and shortProduction are the accepted aliases. | shortDevelopment 与 shortProduction 是接受的短别名。
	shortDevelopment = "dev"
	shortProduction  = "prod"
)

// Normalize canonicalizes v: surrounding blanks are removed, matching is
// case-insensitive, and the short aliases dev/prod map onto the full names.
// Unknown values are returned trimmed and lower-cased. | Normalize 归一化 v：去除首尾空白、忽略大小写，
// 并把 dev/prod 映射为完整名称；未知取值原样返回小写形式。
func Normalize(v string) string {
	switch s := strings.ToLower(strings.TrimSpace(v)); s {
	case shortDevelopment, Development:
		return Development
	case shortProduction, Production:
		return Production
	default:
		return s
	}
}

// IsProduction reports whether v names production, including the prod alias. | IsProduction 报告 v 是否表示生产环境（含 prod 别名）。
func IsProduction(v string) bool { return Normalize(v) == Production }

// IsDevelopment reports whether v names development, including the dev alias.
// Values such as staging or an empty string are deliberately not development. | IsDevelopment 报告 v 是否表示开发环境（含 dev 别名）。
// staging 或空串等取值刻意不算开发环境。
func IsDevelopment(v string) bool { return Normalize(v) == Development }
