// This file implements the gen command: it turns a field specification, a database table or an existing
// struct into a full module, and owns nothing but flag parsing, path resolution and the human-readable
// report. | 本文件实现 gen 命令：它把字段描述串、数据库表或既有结构体变成完整模块，只负责标志解析、
// 路径解析与人类可读的报告。

package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"
	"github.com/spf13/pflag"
	"github.com/shibingli/yd/data/gormx"
	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/gen"
	"github.com/shibingli/yd/logx"
)

// genCommandName is the command word of the generator command. | genCommandName 是生成器命令的命令词。
const genCommandName = "gen"

// genUsageLine is the usage line of the gen command. | genUsageLine 是 gen 命令的用法行。
const genUsageLine = "yd gen <module|crud> <name> [flags]"

// The gen command's subcommand names and flag names. | gen 命令的子命令名与标志名。
const (
	// genKindModule 依据字段描述串生成模块。| genKindModule generates a module from a field specification.
	genKindModule = "module"
	// genKindCrud 依据表结构或既有结构体生成模块。| genKindCrud generates a module from a table schema or an existing struct.
	genKindCrud = "crud"

	// flagGenFields 是字段描述串。| flagGenFields is the field specification.
	flagGenFields = "fields"
	// flagGenModule 是业务模块名，用作 OpenAPI 的 @Tags。| flagGenModule is the business module name used as the OpenAPI @Tags value.
	flagGenModule = "module"
	// flagGenPackage 是目标工程的 Go 模块路径。| flagGenPackage is the Go module path of the target project.
	flagGenPackage = "package"
	// flagGenDir 是生成物的输出目录。| flagGenDir is the output directory of the artifacts.
	flagGenDir = "dir"
	// flagGenTable 是 crud 模式的表名。| flagGenTable is the table name in crud mode.
	flagGenTable = "table"
	// flagGenDSN 是 crud 模式的数据库连接串。| flagGenDSN is the database DSN in crud mode.
	flagGenDSN = "dsn"
	// flagGenDriver 是 crud 模式的驱动名。| flagGenDriver is the driver name in crud mode.
	flagGenDriver = "driver"
	// flagGenStruct 是 crud 模式的结构体引用，形如 pkg.Type。| flagGenStruct is the struct reference in crud mode, shaped like pkg.Type.
	flagGenStruct = "struct"
	// flagGenForce 允许覆盖没有生成标记的手写文件。| flagGenForce allows overwriting hand-written files without the generator marker.
	flagGenForce = "force"
	// flagGenDryRun 只报告将要写入的文件。| flagGenDryRun only reports the files that would be written.
	flagGenDryRun = "dry-run"
)

// genFlags keeps the gen command's flag set, installed by registerGenFlags. | genFlags 保存 gen 命令的标志集，由 registerGenFlags 安装。
var genFlags *pflag.FlagSet

func init() {
	Register(Command{
		Name:  genCommandName,
		Short: "generate module and CRUD code | 生成模块与 CRUD 代码",
		Long: "Generate a model, repository, service, HTML-fragment controller, routes and web templates from a\n" +
			"field specification, a database table or an existing struct. Generated files carry a marker and are\n" +
			"skipped when their content is unchanged, so re-running the command is always safe.\n" +
			"依据字段描述串、数据库表或既有结构体生成模型、仓储、服务、返回 HTML 片段的控制器、路由与页面模板。\n" +
			"生成物带生成标记，内容未变化时整批跳过，因此重复执行永远安全。",
		Usage: genUsageLine,
		Flags: registerGenFlags,
		Run:   runGen,
	})
}

// registerGenFlags declares the flags shared by both gen subcommands. | registerGenFlags 声明两个 gen 子命令共享的标志。
func registerGenFlags(fs *pflag.FlagSet) {
	fs.String(flagGenFields, "", "field spec: name:type[:modifier,...] (module/crud) | 字段描述串：名称:类型[:修饰符,...]（module/crud）")
	fs.String(flagGenTable, "", "table name: the source table in crud, an override elsewhere | 表名：crud 下作为数据来源，其它模式下覆盖推导出的表名")
	fs.String(flagGenDSN, "", "database DSN of the table (crud) | 表所在数据库的连接串（crud）")
	fs.String(flagGenDriver, "sqlite", "database driver: sqlite/mysql/postgres (crud) | 数据库驱动（crud）")
	fs.String(flagGenStruct, "", "existing struct: pkg.Type (crud) | 既有结构体：pkg.Type（crud）")
	fs.String(flagGenModule, "", "business module name used as @Tags | 业务模块名，用作 @Tags")
	fs.String(flagGenPackage, "", "Go module path of the target project | 目标工程的 Go 模块路径")
	fs.String(flagGenDir, "", "output directory | 输出目录")
	fs.Bool(flagGenForce, false, "overwrite hand-written files without the marker | 覆盖没有生成标记的手写文件")
	fs.Bool(flagGenDryRun, false, "report the files that would be written | 只报告将要写入的文件")
	genFlags = fs
}

// runGen dispatches the gen subcommands. | runGen 派发 gen 的子命令。
func runGen(ctx context.Context, env *Env) error {
	fs := genFlags
	args := fs.Args()
	if len(args) == 0 {
		printGenUsage(env.Out)
		return ErrNoCommand
	}

	kind, rest := args[0], args[1:]
	switch kind {
	case genKindModule:
		return runGenModule(ctx, env, fs, rest)
	case genKindCrud:
		return runGenCrud(ctx, env, fs, rest)
	default:
		fmt.Fprintf(env.Err, "yd gen: unknown subcommand %q | yd gen：未知子命令 %q\n", kind, kind)
		printGenUsage(env.Err)
		return errx.Detail(ErrUnknownCommand, "yd gen %s", kind)
	}
}

// runGenModule builds a model from the field specification and generates it. | runGenModule 依据字段描述串构造模型并生成。
func runGenModule(ctx context.Context, env *Env, fs *pflag.FlagSet, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(env.Err, "yd gen module: missing module name | yd gen module：缺少模块名\n")
		printGenUsage(env.Err)
		return ErrNoCommand
	}

	spec := flagString(fs, flagGenFields)
	if strings.TrimSpace(spec) == "" {
		fmt.Fprintf(env.Err, "yd gen module: --fields is required | yd gen module：--fields 为必填\n")
		printGenUsage(env.Err)
		return errx.Detail(ErrFlagParse, "yd gen module %s", args[0])
	}

	fields, err := gen.ParseFields(spec)
	if err != nil {
		return err
	}
	model, err := gen.ModelFromFields(args[0], fields, gen.ModelOptions{
		Table:  flagString(fs, flagGenTable),
		Module: flagString(fs, flagGenModule),
	})
	if err != nil {
		return err
	}
	return generateAndReport(ctx, env, fs, model)
}

// runGenCrud builds a model from a database table or an existing struct and generates it. | runGenCrud 依据数据库表或既有结构体构造模型并生成。
func runGenCrud(ctx context.Context, env *Env, fs *pflag.FlagSet, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(env.Err, "yd gen crud: missing module name | yd gen crud：缺少模块名\n")
		printGenUsage(env.Err)
		return ErrNoCommand
	}

	table, source, spec := flagString(fs, flagGenTable), flagString(fs, flagGenStruct), flagString(fs, flagGenFields)
	given := 0
	for _, v := range []string{table, source, spec} {
		if strings.TrimSpace(v) != "" {
			given++
		}
	}
	switch {
	case given == 0:
		fmt.Fprintf(env.Err, "yd gen crud: one of --fields, --table or --struct is required | yd gen crud：--fields、--table 与 --struct 必须给出其一\n")
		printGenUsage(env.Err)
		return errx.Detail(ErrFlagParse, "yd gen crud %s", args[0])
	case given > 1:
		fmt.Fprintf(env.Err, "yd gen crud: --fields, --table and --struct are mutually exclusive | yd gen crud：--fields、--table 与 --struct 互斥\n")
		printGenUsage(env.Err)
		return errx.Detail(ErrFlagParse, "yd gen crud %s", args[0])
	}

	model, err := crudModel(ctx, env, fs, args[0], spec, table, source)
	if err != nil {
		return err
	}
	return generateAndReport(ctx, env, fs, model)
}

// crudModel 依据 --fields、--table 或 --struct 构造模型。| crudModel builds the model from --fields, --table or --struct.
func crudModel(ctx context.Context, env *Env, fs *pflag.FlagSet, name, spec, table, source string) (*gen.Model, error) {
	module := flagString(fs, flagGenModule)

	if strings.TrimSpace(spec) != "" {
		fields, err := gen.ParseFields(spec)
		if err != nil {
			return nil, err
		}
		return gen.ModelFromFields(name, fields, gen.ModelOptions{Module: module})
	}

	if source != "" {
		// --struct 取值形如 pkg.Type；最后一个点之前是目录或 .go 文件，之后是类型名。
		// A --struct value is shaped like pkg.Type: everything before the last dot is a directory
		// or a .go file, everything after it is the type name.
		dir, typeName, ok := splitSourceRef(source)
		if !ok {
			fmt.Fprintf(env.Err, "yd gen crud: --struct must be shaped like pkg.Type | yd gen crud：--struct 的取值必须形如 pkg.Type\n")
			printGenUsage(env.Err)
			return nil, errx.Detail(ErrFlagParse, "yd gen crud %s --struct %s", name, source)
		}
		return gen.ModelFromSource(env.FS, joinWorkDir(env.WorkDir, dir), typeName, gen.StructOptions{
			Name:   name,
			Table:  table,
			Module: module,
		})
	}

	dsn := flagString(fs, flagGenDSN)
	if strings.TrimSpace(dsn) == "" {
		fmt.Fprintf(env.Err, "yd gen crud: --dsn is required with --table | yd gen crud：使用 --table 时必须提供 --dsn\n")
		printGenUsage(env.Err)
		return nil, errx.Detail(ErrFlagParse, "yd gen crud %s --table %s", name, table)
	}

	db, err := gormx.Open(gormx.Options{Driver: flagString(fs, flagGenDriver), DSN: dsn}, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := gormx.Close(db); cerr != nil {
			logx.Default().Warn("yd gen crud: closing the database failed | yd gen crud：关闭数据库失败", logx.Err(cerr))
		}
	}()

	return gen.ModelFromTable(ctx, db, table, gen.TableOptions{Name: name, Module: module})
}

// generateAndReport 执行生成并把结果报告给用户。| generateAndReport runs the generator and reports the result to the user.
func generateAndReport(ctx context.Context, env *Env, fs *pflag.FlagSet, model *gen.Model) error {
	relDir := flagString(fs, flagGenDir)
	res, err := gen.Generate(ctx, model, gen.Options{
		Module:  flagString(fs, flagGenModule),
		Package: packagePath(env, fs),
		Dir:     joinWorkDir(env.WorkDir, relDir),
		Force:   flagBool(fs, flagGenForce),
		DryRun:  flagBool(fs, flagGenDryRun),
	}, env.FS)

	if res == nil {
		return err
	}
	if flagBool(fs, flagGenDryRun) {
		fmt.Fprintf(env.Out, "dry run | 试运行: %d file(s) would be written | 将写入 %d 个文件\n",
			len(res.Written), len(res.Written))
		printArtifacts(env.Out, relDir, res.Written)
		return err
	}
	fmt.Fprintf(env.Out, "written | 已写入: %d\n", len(res.Written))
	printArtifacts(env.Out, relDir, res.Written)
	if len(res.Skipped) > 0 {
		fmt.Fprintf(env.Out, "skipped | 已跳过: %d\n", len(res.Skipped))
		printArtifacts(env.Out, relDir, res.Skipped)
	}
	return err
}

// printArtifacts 逐个写出生成物的路径。| printArtifacts writes the path of every artifact.
func printArtifacts(w io.Writer, dir string, rels []string) {
	for _, rel := range rels {
		fmt.Fprintf(w, "  %s\n", displayPath(dir, rel))
	}
}

// displayPath 把生成物的相对路径拼成用户可辨认的路径。| displayPath joins an artifact's relative path into a path the user can recognise.
func displayPath(dir, rel string) string {
	if dir == "" || dir == "." {
		return rel
	}
	return filepath.ToSlash(filepath.Join(dir, filepath.FromSlash(rel)))
}

// packagePath 返回生成代码的导入前缀：优先取 --package，其次取 go.mod 里的模块路径，都没有时留空由 gen 兜底。
// packagePath returns the import prefix of the generated code: --package first, then the module path
// read from go.mod, and an empty value (the gen default) when neither is available.
func packagePath(env *Env, fs *pflag.FlagSet) string {
	if pkg := strings.TrimSpace(flagString(fs, flagGenPackage)); pkg != "" {
		return pkg
	}

	gomod := joinWorkDir(env.WorkDir, "go.mod")
	data, err := afero.ReadFile(env.FS, gomod)
	if err != nil {
		logx.Default().Warn("yd gen: go.mod unreadable, falling back to the framework import prefix | yd gen：go.mod 不可读，回退到框架导入前缀",
			logx.Str("path", gomod), logx.Err(err))
		return ""
	}
	pkg := modulePathOf(string(data))
	if pkg == "" {
		logx.Default().Warn("yd gen: go.mod carries no module path, falling back to the framework import prefix | yd gen：go.mod 没有模块路径，回退到框架导入前缀",
			logx.Str("path", gomod))
	}
	return pkg
}

// modulePathOf 从 go.mod 内容里取模块路径。| modulePathOf extracts the module path from the content of a go.mod.
func modulePathOf(gomod string) string {
	for _, line := range strings.Split(gomod, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "module ")), `"`)
	}
	return ""
}

// splitSourceRef 把 "pkg.Type" 拆成目录（或 .go 文件）与类型名。| splitSourceRef splits "pkg.Type" into a directory (or a .go file) and a type name.
func splitSourceRef(ref string) (dir, typeName string, ok bool) {
	ref = strings.TrimSpace(ref)
	i := strings.LastIndex(ref, ".")
	if i <= 0 || i == len(ref)-1 {
		return "", "", false
	}
	return ref[:i], ref[i+1:], true
}

// printGenUsage writes the gen command's subcommand list. | printGenUsage 写出 gen 命令的子命令列表。
func printGenUsage(w io.Writer) {
	fmt.Fprintf(w, "yd gen - module and CRUD generator | yd gen - 模块与 CRUD 生成器\n\n")
	fmt.Fprintf(w, "Usage | 用法:\n")
	fmt.Fprintf(w, "  yd gen module <name> --fields \"...\" [flags] | 依据字段描述串生成模块\n")
	fmt.Fprintf(w, "  yd gen crud <name> --table <t> --dsn <dsn> [flags] | 依据数据库表生成模块\n")
	fmt.Fprintf(w, "  yd gen crud <name> --struct <pkg.Type> [flags] | 依据既有结构体生成模块\n")
	fmt.Fprintf(w, "  yd gen crud <name> --fields \"...\" [flags] | 依据字段描述串生成模块\n\n")
	fmt.Fprintf(w, "Fields | 字段描述串:\n")
	fmt.Fprintf(w, "  name:string:required,age:int,avatar:file | 逗号分隔字段，修饰符跟在类型之后\n")
	fmt.Fprintf(w, "  types       string int int64 bool float float64 time date datetime file text | 可用类型\n")
	fmt.Fprintf(w, "  modifiers   primary required unique index nullable max= min= size= label= widget=\n")
	fmt.Fprintf(w, "              comment= column= json= validate= | 可用修饰符\n\n")
	fmt.Fprintf(w, "Examples | 示例:\n")
	fmt.Fprintf(w, "  yd gen module Post --fields \"title:string:required,max=120,body:text\" --module cms\n")
	fmt.Fprintf(w, "  yd gen crud Post --table posts --dsn \"file:./dev.db\" --dry-run\n")
	fmt.Fprintf(w, "  yd gen crud Post --struct internal/model.Post --force\n\n")
	fmt.Fprintf(w, "Global flags | 全局标志:\n%s", globalFlagUsages())
	fmt.Fprintf(w, "  -h, --help                 print help | 打印帮助\n")
}
