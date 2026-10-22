package openapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/afero"

	"github.com/shibingli/ydx/log"
	"github.com/shibingli/ydx/router"
)

// FromRouter derives a document from the routes registered on r. | FromRouter 依据 r 上已注册的路由推导文档。
//
// Patterns are converted into OpenAPI path templates and their path parameters are
// merged into every operation, so /report/:id-xx.png becomes /report/{id}-xx.png and
// /static/*filepath becomes /static/{filepath}. | 模式会被转换为 OpenAPI 路径模板，其路径参数
// 会合并进每个操作，因此 /report/:id-xx.png 变为 /report/{id}-xx.png，/static/*filepath 变为
// /static/{filepath}。
//
// o only provides the fallback document title (Info.Title wins when set), the type annotation
// sources (TypeAnnotations or SourceDirs) and the serving endpoints configured by Handler. | o
// 仅用于提供文档标题兜底（Info.Title 非空时优先）、类型注解来源（TypeAnnotations 或 SourceDirs）
// 以及由 Handler 配置的服务端点。
func FromRouter(r *router.Router, info Info, o Options) *Document {
	return FromRouterWithAnnotations(r, info, o, nil, nil)
}

// FromRouterWithAnnotations derives a document from the routes registered on r and
// merges the source annotations collected by ParseAnnotations. | FromRouterWithAnnotations
// 依据 r 上已注册的路由推导文档，并合并由 ParseAnnotations 收集到的源码注解。
//
// Annotations take precedence over auto derivation: an annotated route gets the
// operation described by its annotation, every other route gets a minimal operation
// with a default 200 response. | 注解优先于自动推导：有注解的路由使用注解描述的操作，
// 其余路由使用带默认 200 响应的最小操作。
//
// Type annotations are merged automatically: o.TypeAnnotations is used when set, otherwise
// they are parsed from o.SourceDirs over the operating system file system, and a parse
// failure is logged instead of failing the document. Example values, descriptions, enums and
// variable samples therefore reach the component schemas without extra wiring. | 类型注解会自动
// 合并：o.TypeAnnotations 非空时直接使用，否则从 o.SourceDirs 经操作系统文件系统解析；解析失败
// 只记录日志，不会让文档构建失败。因此示例、描述、枚举与变量样例无需额外装配即可进入组件 Schema。
//
// The report is filled with the router aware coverage view (Matched, Missing, Orphan,
// Incomplete) and, when type annotations were merged, with AnnotatedTypes,
// UnannotatedTypes and EnumCount. | report 会被写入结合路由的覆盖视图（Matched、Missing、Orphan、
// Incomplete）；合并过类型注解时还会写入 AnnotatedTypes、UnannotatedTypes 与 EnumCount。
//
// Both annots and report may be nil. | annots 与 report 均允许为 nil。
func FromRouterWithAnnotations(r *router.Router, info Info, o Options, annots map[string]Annotation, report *Report) *Document {
	b := NewBuilder(info)
	if strings.TrimSpace(b.doc.Info.Title) == "" {
		b.doc.Info.Title = strings.TrimSpace(o.Title)
	}

	var routes []router.RouteInfo
	if r != nil {
		routes = r.Routes()
	}
	matched := make([]string, 0, len(routes))
	missing := make([]string, 0, len(routes))
	seen := make(map[string]bool, len(routes))
	for _, route := range routes {
		path, params := pathTemplate(route.Pattern)
		key := routeKey(route.Method, path)
		if seen[key] {
			continue
		}
		seen[key] = true
		op := autoOperation()
		if a, ok := annots[key]; ok {
			op = a.operation(path, params)
			matched = append(matched, key)
		} else {
			op = mergePathParams(op, params)
			// Static mounts and other non-API prefixes stay in the document but do not count as
			// missing annotations. | 静态挂载等非 API 前缀保留在文档中，但不计入缺失注解。
			if !skipsCoverage(path, o.SkipPrefixes) {
				missing = append(missing, key)
			}
		}
		b.add(key, path, route.Method, op, params)
	}

	orphan := make([]string, 0, len(annots))
	for key := range annots {
		if !seen[key] {
			orphan = append(orphan, key)
		}
	}

	types := resolveTypeAnnotations(o)
	if types != nil {
		b.AddTypeAnnotations(types)
	}
	if report != nil {
		report.Matched = mergeUniqueSorted(nil, matched)
		report.Missing = mergeUniqueSorted(nil, missing)
		report.Orphan = mergeUniqueSorted(nil, orphan)
		report.Incomplete = mergeUniqueSorted(report.Incomplete, incompleteKeys(annots))
		if types != nil {
			coverage := b.TypeCoverage()
			report.AnnotatedTypes = mergeUniqueSorted(report.AnnotatedTypes, coverage.Annotated)
			report.UnannotatedTypes = mergeUniqueSorted(report.UnannotatedTypes, coverage.Unannotated)
			report.EnumCount = coverage.Enums
		}
	}
	return b.Build()
}

// skipsCoverage reports whether path belongs to one of the configured non-API prefixes. | skipsCoverage
// 报告 path 是否属于某个配置的非 API 前缀。
//
// Matching is literal and boundary-aware: a prefix without a trailing slash covers the exact path and
// its children, so "/static" does not swallow "/static-file". | 匹配是字面量且带边界判断的：不带结尾
// 斜杠的前缀覆盖该路径本身及其子路径，因此 "/static" 不会误吞 "/static-file"。
func skipsCoverage(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		if !strings.HasPrefix(prefix, "/") {
			prefix = "/" + prefix
		}
		if strings.HasSuffix(prefix, "/") {
			if strings.HasPrefix(path, prefix) {
				return true
			}
			continue
		}
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// resolveTypeAnnotations returns the type annotations to merge: the ones the caller provided
// explicitly, or the ones parsed from Options.SourceDirs over the operating system file
// system; a parse failure is logged and yields nil so that the document still builds. |
// resolveTypeAnnotations 返回待合并的类型注解：调用方显式提供的，或从 Options.SourceDirs 经
// 操作系统文件系统解析出的；解析失败时记录日志并返回 nil，保证文档仍能构建。
func resolveTypeAnnotations(o Options) *TypeAnnotations {
	if o.TypeAnnotations != nil {
		return o.TypeAnnotations
	}
	if len(o.SourceDirs) == 0 {
		return nil
	}
	types, err := ParseTypeAnnotations(afero.NewOsFs(), o.SourceDirs)
	if err != nil {
		log.Default().Warn("openapi: type annotations not merged | openapi：类型注解未合并",
			log.Strs("dirs", o.SourceDirs), log.Err(err))
		return nil
	}
	return types
}

// autoOperation is the minimal operation used for routes without annotation. | autoOperation
// 是无注解路由使用的最小操作。
func autoOperation() Operation {
	return Operation{
		Responses: map[string]Response{
			strconv.Itoa(http.StatusOK): {Description: http.StatusText(http.StatusOK)},
		},
	}
}

// pathTemplate converts a router pattern into an OpenAPI path template and returns
// the path parameters it declares. | pathTemplate 把路由模式转换为 OpenAPI 路径模板，并返回其声明的路径参数。
//
// "/report/:id-xx.png" becomes "/report/{id}-xx.png"; "/static/*filepath" becomes
// "/static/{filepath}"; a trailing slash segment is preserved without producing an
// empty parameter name. | "/report/:id-xx.png" 变为 "/report/{id}-xx.png"；
// "/static/*filepath" 变为 "/static/{filepath}"；尾斜杠段被保留且不产生空参数名。
func pathTemplate(pattern string) (string, []Parameter) {
	segments, err := router.ParsePattern(pattern)
	if err != nil {
		return scanPathTemplate(pattern)
	}
	var (
		b        strings.Builder
		params   []Parameter
		trailing bool
	)
	for _, segment := range segments {
		if segment.TrailingSlash {
			trailing = true
			continue
		}
		b.WriteByte('/')
		switch segment.Kind {
		case router.SegStatic:
			b.WriteString(segment.Literal)
		case router.SegParam, router.SegWildcard:
			b.WriteString("{" + segment.Name + "}")
			params = append(params, pathParameter(segment.Name))
		case router.SegMixed:
			for _, token := range segment.Tokens {
				if token.Kind == router.TokenLiteral {
					b.WriteString(token.Text)
					continue
				}
				b.WriteString("{" + token.Text + "}")
				params = append(params, pathParameter(token.Text))
			}
		default:
			b.WriteString(segment.Raw)
		}
	}
	if trailing {
		b.WriteByte('/')
	}
	out := b.String()
	if out == "" {
		out = "/"
	}
	return out, params
}

// scanPathTemplate is the textual fallback used when a pattern cannot be parsed. | scanPathTemplate
// 是模式无法解析时使用的文本兜底转换。
func scanPathTemplate(pattern string) (string, []Parameter) {
	var (
		b      strings.Builder
		params []Parameter
		seen   = map[string]bool{}
	)
	if !strings.HasPrefix(pattern, "/") {
		b.WriteByte('/')
	}
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c != ':' && c != '*' {
			b.WriteByte(c)
			continue
		}
		j := i + 1
		for j < len(pattern) && isPathNameChar(pattern[j]) {
			j++
		}
		if j == i+1 {
			b.WriteByte(c)
			continue
		}
		name := pattern[i+1 : j]
		b.WriteString("{" + name + "}")
		if !seen[name] {
			seen[name] = true
			params = append(params, pathParameter(name))
		}
		i = j - 1
	}
	out := b.String()
	if out == "" {
		out = "/"
	}
	return out, params
}
