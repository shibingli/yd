package build

import "strings"

// VersionPackage is the import path of the package holding the version variables
// that -ldflags -X injects into. | VersionPackage 是承载版本变量、由 -ldflags -X 注入的包的导入路径。
const VersionPackage = "github.com/shibingli/yd/version"

// GenerateLDFlags returns the argv fragment appended to `go build` for version
// injection, shaped as ["-ldflags", "<flags>"]. | GenerateLDFlags 返回追加到 `go build`
// 之后用于版本注入的参数片段，形如 ["-ldflags", "<flags>"]。
//
// The linker flags always start with -s -w (strip the symbol table and the DWARF
// data, which is what keeps the single binary small), then carry the caller
// supplied o.LDFlags and finally one -X per non-empty Version, Commit and Date.
// Values must not contain spaces, since the whole flag list travels to the
// toolchain as a single argument. | 链接参数始终以 -s -w 开头（剥离符号表与 DWARF
// 调试信息，这是单二进制体积可控的关键），随后是调用方传入的 o.LDFlags，最后为每个非空的
// Version、Commit、Date 各生成一个 -X。取值不得包含空格，因为整个参数列表以单个实参传给工具链。
func GenerateLDFlags(o Options) []string {
	flags := make([]string, 0, 2+len(o.LDFlags)+6)
	flags = append(flags, "-s", "-w")
	flags = append(flags, o.LDFlags...)
	for _, kv := range [...]struct{ name, value string }{
		{name: "Version", value: o.Version},
		{name: "Commit", value: o.Commit},
		{name: "Date", value: o.Date},
	} {
		if kv.value == "" {
			continue
		}
		flags = append(flags, "-X", VersionPackage+"."+kv.name+"="+kv.value)
	}
	return []string{"-ldflags", strings.Join(flags, " ")}
}
