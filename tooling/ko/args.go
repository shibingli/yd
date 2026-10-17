// This file assembles the argv of one `ko build` invocation and normalizes the list options. | 本文件组装一次 `ko build` 调用的 argv 并归一化列表型选项。

package ko

import (
	"strconv"
	"strings"

	"github.com/shibingli/yd/core/errx"
)

// DefaultPackage is the import path built when Options.Packages is empty. | DefaultPackage 是 Options.Packages 为空时构建的导入路径。
//
// It matches the layout `yd new` scaffolds, so `yd ko` works in a fresh project without arguments. | 它与 `yd new`
// 脚手架生成的布局一致，因此 `yd ko` 在新项目里无需参数即可工作。
const DefaultPackage = "./cmd/app"

// The ko subcommand and flags used by Args. | Args 使用的 ko 子命令与标志。
const (
	// buildVerb is the ko subcommand that builds and publishes images. | buildVerb 是构建并发布镜像的 ko 子命令。
	buildVerb = "build"
	// flagTags is ko's repeatable tag flag, spelled with the plural. | flagTags 是 ko 可重复的标签标志，采用复数拼写。
	flagTags = "--tags"
	// flagPlatform is ko's repeatable platform flag, spelled with the singular. | flagPlatform 是 ko 可重复的平台标志，
	// 采用单数拼写。
	flagPlatform = "--platform"
	// flagPush is ko's push switch; it is always passed explicitly. | flagPush 是 ko 的推送开关，始终显式传入。
	flagPush = "--push"
	// flagLocal publishes into the local docker daemon instead of a registry. | flagLocal 把结果发布到本地
	// docker 守护进程而不推送到仓库。
	flagLocal = "--local"
	// flagBaseImportPaths names each image after the base name of its package, i.e.
	// <repository>/app instead of <repository>/app-<md5>. | flagBaseImportPaths 用包的基础名命名镜像，即
	// <仓库>/app 而不是 <仓库>/app-<md5>。
	flagBaseImportPaths = "--base-import-paths"
)

// platformAll is ko's keyword for "every platform of the base image". | platformAll 是 ko 表示「基础镜像的全部平台」的关键字。
const platformAll = "all"

// Args returns the argv handed to the ko CLI. | Args 返回交给 ko CLI 的 argv。
//
// The assembly is deterministic and fully explicit: | 组装规则确定且完全显式：
//
//   - the repository and the base image are not flags at all: ko reads them from KO_DOCKER_REPO and
//     KO_DEFAULTBASEIMAGE, so they travel in the environment (see Options.environ); |   - 仓库与基础镜像完全不是标志：
//     ko 从 KO_DOCKER_REPO 与 KO_DEFAULTBASEIMAGE 读取它们，因此它们经环境传递（见 Options.environ）；
//   - --base-import-paths pins the image name to <repository>/<package base> instead of ko's default
//     <repository>/<package base>-<md5 hash>, because the hash makes the reference unpredictable for
//     whoever consumes it; the price is that two packages sharing a base name would land on the same
//     repository name, which is acceptable for the single ./cmd/app layout `yd new` scaffolds;
//     --bare is not used, as ko documents --base-import-paths as taking precedence over it; |   - --base-import-paths
//     把镜像名固定为 <仓库>/<包基础名>，而不是 ko 默认的 <仓库>/<包基础名>-<md5 哈希>，因为哈希会让引用对消费方
//     不可预测；代价是基础名相同的两个包会落在同一个仓库名上，这对 `yd new` 脚手架生成的单个 ./cmd/app 布局是可以接受的；
//     不使用 --bare，因为 ko 文档说明 --base-import-paths 的优先级高于它；
//   - --push is always present, as --push=true or --push=false, because ko's own default is true and
//     a silent push would be a surprise; |   - --push 始终存在，形如 --push=true 或 --push=false，
//     因为 ko 自身的默认值是 true，静默推送会让人意外；
//   - --local is passed only when Local is set; |   - 仅在 Local 为真时传入 --local；
//   - --tags and --platform are repeated once per value, in the order given (and never comma-joined,
//     which ko documents as a deprecated form); |   - --tags 与 --platform 按给出的顺序逐个重复传入（绝不逗号拼接，
//     ko 文档把逗号形式标记为已废弃）；
//   - the package import paths close the line. |   - 包导入路径位于末尾。
func (o Options) Args() []string {
	args := make([]string, 0, 2+len(o.Tags)+len(o.Platforms)*2+len(o.Packages)+1)
	args = append(args, buildVerb, flagBaseImportPaths, flagPush+"="+strconv.FormatBool(o.Push))
	if o.Local {
		args = append(args, flagLocal)
	}
	for _, tag := range o.tags() {
		args = append(args, flagTags, tag)
	}
	for _, platform := range o.platforms() {
		args = append(args, flagPlatform, platform)
	}
	return append(args, o.packages()...)
}

// tags returns the normalized tags of the run. | tags 返回本次运行归一化后的标签。
func (o Options) tags() []string { return normalizeList(o.Tags) }

// platforms returns the normalized platforms of the run. | platforms 返回本次运行归一化后的平台。
func (o Options) platforms() []string { return normalizeList(o.Platforms) }

// packages returns the import paths of the run, defaulting to DefaultPackage. | packages 返回本次运行的导入路径，缺省为 DefaultPackage。
func (o Options) packages() []string {
	if p := normalizeList(o.Packages); len(p) > 0 {
		return p
	}
	return []string{DefaultPackage}
}

// validate rejects option combinations that cannot produce an image. | validate 拒绝无法产出镜像的选项组合。
//
// Two cases are caught here rather than left to ko: a missing repository would otherwise fail deep
// inside the publisher with ko's own wording, and "all" mixed with specific platforms is exactly the
// combination ko refuses with "all or specific platforms should be used". | 两种情况在这里拦下而不是留给 ko：
// 仓库缺失否则会在发布器深处以 ko 自己的措辞失败；把 "all" 与具体平台混用正是 ko 以
// "all or specific platforms should be used" 拒绝的组合。
func (o Options) validate() error {
	if strings.TrimSpace(o.Repository) == "" && !o.Local {
		return errx.Detail(ErrNoRepository,
			"set --repo (or ko.repository / KO_DOCKER_REPO), or pass --local | 请用 --repo 指定仓库（或配置 ko.repository / KO_DOCKER_REPO），或改用 --local")
	}
	if platforms := o.platforms(); len(platforms) > 1 {
		for _, platform := range platforms {
			if platform == platformAll {
				return errx.Detail(ErrInvalidOptions,
					"%q cannot be combined with specific platforms | %q 不能与具体平台混用", platformAll, platformAll)
			}
		}
	}
	return nil
}

// normalizeList trims every entry, splits the comma-separated ones, drops the blanks and removes
// duplicates while keeping the first occurrence order. | normalizeList 去除各项首尾空白、拆分逗号分隔项、
// 丢弃空项，并按首次出现顺序去重。
//
// One entry may itself hold a comma-separated list because both shapes reach this package: a YAML
// list from ko.platforms, and a --flags a,b typed on the command line. | 单项内部可以再用逗号分隔，因为两种
// 形态都会到达本包：ko.platforms 的 YAML 列表，以及命令行上输入的 --flags a,b。
func normalizeList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, entry := range in {
		for _, value := range strings.Split(entry, ",") {
			if value = strings.TrimSpace(value); value == "" {
				continue
			}
			if _, dup := seen[value]; dup {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
