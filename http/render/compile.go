package render

// Template compiler. | 模板编译器。
//
// html/template remains the front end: it parses the text/template syntax and
// rewrites every dynamic action with context-aware escapers. This compiler then
// turns the already-escaped parse trees into immutable node graphs that the
// executor walks without reflection on the common path. The split keeps syntax
// and security semantics exactly equal to the standard library while removing
// its per-execution reflection cost. | html/template 仍是前端：它解析
// text/template 语法，并为每个动态动作按上下文重写转义器。本编译器随后把已转义的
// 解析树转换成不可变节点图，执行器在常见路径上无需反射即可遍历。该分工使语法与安全
// 语义与标准库完全一致，同时去掉其逐次执行的反射开销。

import (
	"errors"
	"html/template"
	"reflect"
	"strings"
	"sync/atomic"
	"text/template/parse"

	"github.com/shibingli/yd/core/errx"
)

// errDerivedContext reports that html/template had to clone a callee for a
// non-text context. Those clones live only in the internal text/template name
// space and are not reachable through the exported API, so the entry falls back
// to standard html/template execution. | errDerivedContext 表示 html/template 为
// 非文本上下文克隆了被调模板。这些克隆只存在于内部的 text/template 命名空间，
// 无法经导出 API 访问，因此该入口回退到标准 html/template 执行。
var errDerivedContext = errors.New("derived template context | 派生模板上下文")

// program is one immutable, fully compiled template set rooted at one entry.
// | program 是以某个入口为根的不可变、完全编译的模板集合。
type program struct {
	err       error
	fallback  *fallbackProgram
	templates map[string]*compiledTemplate
	root      *compiledTemplate
	sizeHint  int
}

// compiledTemplate is one executable template. | compiledTemplate 是一个可执行模板。
type compiledTemplate struct {
	root *listNode
	name string
}

// node is one executable instruction. | node 是一条可执行指令。
type node interface {
	exec(*execState) error
}

// expr evaluates to a value. | expr 求值为一个值。
type expr interface {
	eval(*execState) (any, error)
}

// listNode is a sequence of instructions. | listNode 是一组顺序指令。
type listNode struct {
	nodes  []node
	static int
}

// textNode writes a static chunk. | textNode 输出一段静态文本。
type textNode struct{ text string }

// actionNode evaluates a pipeline and optionally escapes and prints it. | actionNode 求值流水线，必要时转义并输出。
type actionNode struct {
	pipe       *pipeNode
	quick      *quickAction
	field      *fieldAction
	declField  *fieldAction
	declName   string
	escs       []escKind
	print      bool
	declAssign bool
}

// fieldAction prints a single field chain through a reflect.Value path, which
// avoids boxing every string and integer of a struct row into an interface.
// | fieldAction 通过 reflect.Value 路径输出单条字段链，避免把结构体行的每个字符串与
// 整数装箱进接口。
type fieldAction struct {
	path    atomic.Pointer[structPath]
	chain   []string
	keys    []reflect.Value
	varName string
	escs    []escKind
}

// newFieldAction builds a field action and precomputes the reflect map keys so
// map lookups on the hot path do not allocate a string header per render.
// | newFieldAction 构建字段动作并预计算反射映射键，使热路径上的映射查找不再
// 每次渲染分配字符串头。
func newFieldAction(chain []string, varName string, escs []escKind) *fieldAction {
	f := &fieldAction{chain: chain, varName: varName, escs: escs}
	if len(chain) > 0 {
		f.keys = make([]reflect.Value, len(chain))
		for i, name := range chain {
			f.keys[i] = reflect.ValueOf(name)
		}
	}
	return f
}

// structPath is a type-level resolved chain of struct field indexes. | structPath 是类型层解析出的结构体字段索引链。
type structPath struct {
	typ     reflect.Type
	indexes [][]int
	ok      bool
}

// quickAction is a single-binding action printed without boxing its result into
// an interface value, which is the common {{ tr "key" }} shape. | quickAction 是
// 单绑定动作的无接口装箱输出路径，对应最常见的 {{ tr "key" }} 形状。
type quickAction struct {
	args []expr
	escs []escKind
	kind bindingKind
}

// quickCond is a single-binding if/with condition, the common
// {{ if can "perm" }} shape. | quickCond 是单绑定 if/with 条件，对应常见的
// {{ if can "perm" }} 形状。
type quickCond struct {
	args []expr
	kind bindingKind
}

// ifNode implements both if and with. | ifNode 同时实现 if 与 with。
type ifNode struct {
	pipe     *pipeNode
	quick    *quickCond
	cond     *fieldAction
	body     *listNode
	elseBody *listNode
	with     bool
}

// rangeNode implements range. | rangeNode 实现 range。
type rangeNode struct {
	pipe     *pipeNode
	field    *fieldAction
	body     *listNode
	elseBody *listNode
}

// templateNode invokes another compiled template. | templateNode 调用另一个已编译模板。
type templateNode struct {
	pipe   *pipeNode
	target *compiledTemplate
	name   string
}

// breakNode and continueNode signal loop control without panics. | breakNode 与 continueNode 以非 panic 方式传递循环控制。
type breakNode struct{}

type continueNode struct{}

// cmdKind selects one command evaluation strategy. | cmdKind 选择一种命令求值策略。
type cmdKind uint8

const (
	cmdValue cmdKind = iota
	cmdField
	cmdVar
	cmdChain
	cmdFunc
)

// commandNode is one pipeline command. | commandNode 是一条流水线命令。
type commandNode struct {
	value   expr
	base    expr
	fn      *funcRef
	varName string
	args    []expr
	chain   []string
	kind    cmdKind
}

// pipeNode is a compiled pipeline. | pipeNode 是已编译的流水线。
type pipeNode struct {
	cmds   []*commandNode
	decl   []string
	assign bool
}

// literalExpr yields a constant. | literalExpr 产生常量。
type literalExpr struct{ v any }

// dotExpr yields the current dot. | dotExpr 产生当前 dot。
type dotExpr struct{}

// nilExpr yields nil. | nilExpr 产生 nil。
type nilExpr struct{}

// fieldExpr walks fields from dot. | fieldExpr 从 dot 开始遍历字段。
type fieldExpr struct{ chain []string }

// varExpr reads a variable and then walks fields. | varExpr 读取变量后遍历字段。
type varExpr struct {
	name  string
	chain []string
}

// chainExpr evaluates a base expression and then walks fields. | chainExpr 先求值基表达式，再遍历字段。
type chainExpr struct {
	base  expr
	chain []string
}

// pipeExpr evaluates a parenthesized pipeline. | pipeExpr 求值括号内的流水线。
type pipeExpr struct{ pipe *pipeNode }

// callExpr invokes a function or builtin. | callExpr 调用函数或内建。
type callExpr struct {
	ref  *funcRef
	args []expr
}

// compiler carries the state of one load. | compiler 承载一次加载的状态。
type compiler struct {
	root  *template.Template
	funcs map[string]any
	owned map[string]bool
	prog  *program
	seen  map[string]bool
}

// compileProgram compiles every template reachable from root. | compileProgram 编译从 root 可达的全部模板。
func compileProgram(root *template.Template, name string, funcs map[string]any, owned map[string]bool) (*program, error) {
	c := &compiler{
		root:  root,
		funcs: funcs,
		owned: owned,
		prog:  &program{templates: make(map[string]*compiledTemplate, 16)},
		seen:  make(map[string]bool, 16),
	}
	t, err := c.template(root, name)
	if err != nil {
		return nil, err
	}
	c.prog.root = t
	return c.prog, nil
}

// template compiles one named template and every template it calls. | template 编译指定模板及其调用的全部模板。
func (c *compiler) template(root *template.Template, name string) (*compiledTemplate, error) {
	if t, ok := c.prog.templates[name]; ok {
		return t, nil
	}
	ht := root.Lookup(name)
	if ht == nil {
		if strings.Contains(name, "$htmltemplate_") {
			return nil, errDerivedContext
		}
		return nil, errx.Detail(ErrParseTemplate, "name=%[1]s | 名称=%[1]s", name)
	}
	if ht.Tree == nil || ht.Tree.Root == nil {
		return nil, errx.Detail(ErrParseTemplate, "name=%[1]s | 名称=%[1]s", name)
	}
	out := &compiledTemplate{name: name}
	c.prog.templates[name] = out
	list, err := c.list(ht.Tree.Root)
	if err != nil {
		return nil, err
	}
	out.root = list
	c.prog.sizeHint += list.static
	return out, nil
}

// list compiles a node list, merging adjacent text nodes. | list 编译节点列表并合并相邻文本节点。
func (c *compiler) list(in *parse.ListNode) (*listNode, error) {
	out := &listNode{nodes: make([]node, 0, len(in.Nodes))}
	for _, n := range in.Nodes {
		switch x := n.(type) {
		case *parse.TextNode:
			if string(x.Text) == "" {
				continue
			}
			if len(out.nodes) > 0 {
				if prev, ok := out.nodes[len(out.nodes)-1].(*textNode); ok {
					prev.text += string(x.Text)
					out.static += len(x.Text)
					continue
				}
			}
			out.nodes = append(out.nodes, &textNode{text: string(x.Text)})
			out.static += len(x.Text)
		case *parse.CommentNode:
			continue
		default:
			n, err := c.node(x)
			if err != nil {
				return nil, err
			}
			if n != nil {
				out.nodes = append(out.nodes, n)
			}
		}
	}
	return out, nil
}

// node compiles one parse node. | node 编译单个解析节点。
func (c *compiler) node(in parse.Node) (node, error) {
	switch n := in.(type) {
	case *parse.ActionNode:
		pipe, escs, err := c.pipeline(n.Pipe)
		if err != nil {
			return nil, err
		}
		act := &actionNode{pipe: pipe, escs: escs, print: len(pipe.decl) == 0}
		if act.print && len(pipe.cmds) == 1 && len(escs) > 0 {
			cmd := pipe.cmds[0]
			switch {
			case quickActionFor(cmd) != nil:
				q := quickActionFor(cmd)
				q.escs = escs
				act.quick = q
			case cmd.kind == cmdField && len(cmd.chain) > 0 && len(cmd.args) == 0:
				act.field = newFieldAction(cmd.chain, "", escs)
			case cmd.kind == cmdVar && len(cmd.chain) == 0 && len(cmd.args) == 0:
				act.field = newFieldAction(nil, cmd.varName, escs)
			case cmd.kind == cmdValue && len(cmd.args) == 0:
				if _, isDot := cmd.value.(dotExpr); isDot {
					act.field = newFieldAction(nil, "", escs)
				}
			}
		}
		if !act.print && len(pipe.decl) == 1 && len(pipe.cmds) == 1 {
			cmd := pipe.cmds[0]
			if cmd.kind == cmdField && len(cmd.chain) > 0 && len(cmd.args) == 0 {
				act.declField = newFieldAction(cmd.chain, "", nil)
				act.declName = pipe.decl[0]
				act.declAssign = pipe.assign
			}
		}
		return act, nil
	case *parse.IfNode:
		return c.branch(n.Pipe, n.List, n.ElseList, false)
	case *parse.WithNode:
		return c.branch(n.Pipe, n.List, n.ElseList, true)
	case *parse.RangeNode:
		pipe, _, err := c.pipeline(n.Pipe)
		if err != nil {
			return nil, err
		}
		body, err := c.list(n.List)
		if err != nil {
			return nil, err
		}
		var elseBody *listNode
		if n.ElseList != nil {
			elseBody, err = c.list(n.ElseList)
			if err != nil {
				return nil, err
			}
		}
		out := &rangeNode{pipe: pipe, body: body, elseBody: elseBody}
		if len(pipe.decl) == 0 && len(pipe.cmds) == 1 {
			cmd := pipe.cmds[0]
			if cmd.kind == cmdField && len(cmd.chain) > 0 && len(cmd.args) == 0 {
				out.field = newFieldAction(cmd.chain, "", nil)
			} else if cmd.kind == cmdVar && len(cmd.chain) == 0 && len(cmd.args) == 0 {
				out.field = newFieldAction(nil, cmd.varName, nil)
			}
		}
		return out, nil
	case *parse.TemplateNode:
		pipe, _, err := c.pipeline(n.Pipe)
		if err != nil {
			return nil, err
		}
		target, err := c.template(c.root, n.Name)
		if err != nil {
			return nil, err
		}
		return &templateNode{pipe: pipe, name: n.Name, target: target}, nil
	case *parse.BreakNode:
		return &breakNode{}, nil
	case *parse.ContinueNode:
		return &continueNode{}, nil
	case *parse.ListNode:
		return c.list(n)
	default:
		return nil, errx.Detail(ErrParseTemplate, "node=%[1]s | 节点=%[1]s", in.String())
	}
}

// branch compiles an if or with node. | branch 编译 if 或 with 节点。
func (c *compiler) branch(p *parse.PipeNode, body, elseBody *parse.ListNode, with bool) (node, error) {
	pipe, _, err := c.pipeline(p)
	if err != nil {
		return nil, err
	}
	b, err := c.list(body)
	if err != nil {
		return nil, err
	}
	var e *listNode
	if elseBody != nil {
		e, err = c.list(elseBody)
		if err != nil {
			return nil, err
		}
	}
	out := &ifNode{pipe: pipe, body: b, elseBody: e, with: with}
	if !with && len(pipe.cmds) == 1 && len(pipe.decl) == 0 {
		cmd := pipe.cmds[0]
		if q := quickCondFor(cmd); q != nil {
			out.quick = q
		} else if cmd.kind == cmdField && len(cmd.chain) > 0 && len(cmd.args) == 0 {
			out.cond = newFieldAction(cmd.chain, "", nil)
		} else if cmd.kind == cmdVar && len(cmd.chain) == 0 && len(cmd.args) == 0 {
			out.cond = newFieldAction(nil, cmd.varName, nil)
		}
	}
	return out, nil
}

// quickActionFor recognises the single-binding actions that can print without
// boxing. tr requires a literal key so a type mismatch can never be hidden. | quickActionFor
// 识别可无装箱输出的单绑定动作。tr 要求字面量键，从而不会掩盖类型不匹配。
func quickActionFor(cmd *commandNode) *quickAction {
	if cmd.kind != cmdFunc || cmd.fn.kind != funcBinding || len(cmd.args) == 0 {
		return nil
	}
	switch cmd.fn.binding {
	case bindingTr:
		lit, ok := cmd.args[0].(literalExpr)
		if !ok {
			return nil
		}
		if _, ok := lit.v.(string); !ok {
			return nil
		}
	case bindingCan, bindingHasRole, bindingPerm, bindingRole:
	default:
		return nil
	}
	return &quickAction{kind: cmd.fn.binding, args: cmd.args}
}

// quickCondFor recognises the single-binding conditions that can decide
// without boxing. | quickCondFor 识别可无装箱判定的单绑定条件。
func quickCondFor(cmd *commandNode) *quickCond {
	if cmd.kind != cmdFunc || cmd.fn.kind != funcBinding || len(cmd.args) != 1 {
		return nil
	}
	switch cmd.fn.binding {
	case bindingCan, bindingHasRole:
		return &quickCond{kind: cmd.fn.binding, args: cmd.args}
	}
	return nil
}

// pipeline compiles a pipeline, stripping its trailing contextual escapers. | pipeline 编译流水线并剥离末尾的上下文转义器。
func (c *compiler) pipeline(in *parse.PipeNode) (*pipeNode, []escKind, error) {
	if in == nil {
		return &pipeNode{}, nil, nil
	}
	cmds := in.Cmds
	var escs []escKind
	for len(cmds) > 1 {
		last := cmds[len(cmds)-1]
		if len(last.Args) != 1 {
			break
		}
		id, ok := last.Args[0].(*parse.IdentifierNode)
		if !ok {
			break
		}
		kind, ok := escapers[id.Ident]
		if !ok {
			break
		}
		escs = append([]escKind{kind}, escs...)
		cmds = cmds[:len(cmds)-1]
	}
	out := &pipeNode{
		cmds:   make([]*commandNode, 0, len(cmds)),
		decl:   make([]string, 0, len(in.Decl)),
		assign: in.IsAssign,
	}
	for _, d := range in.Decl {
		out.decl = append(out.decl, d.Ident[0])
	}
	for _, cmd := range cmds {
		cn, err := c.command(cmd)
		if err != nil {
			return nil, nil, err
		}
		out.cmds = append(out.cmds, cn)
	}
	return out, escs, nil
}

// command compiles one command. | command 编译单条命令。
func (c *compiler) command(in *parse.CommandNode) (*commandNode, error) {
	if len(in.Args) == 0 {
		return nil, errx.Detail(ErrParseTemplate, "empty command | 空命令")
	}
	first := in.Args[0]
	switch n := first.(type) {
	case *parse.IdentifierNode:
		ref, err := c.resolveFunc(n.Ident)
		if err != nil {
			return nil, err
		}
		args, err := c.args(in.Args[1:])
		if err != nil {
			return nil, err
		}
		return &commandNode{kind: cmdFunc, fn: ref, args: args}, nil
	case *parse.FieldNode:
		args, err := c.args(in.Args[1:])
		if err != nil {
			return nil, err
		}
		return &commandNode{kind: cmdField, chain: append([]string(nil), n.Ident...), args: args}, nil
	case *parse.VariableNode:
		args, err := c.args(in.Args[1:])
		if err != nil {
			return nil, err
		}
		return &commandNode{
			kind:    cmdVar,
			varName: n.Ident[0],
			chain:   append([]string(nil), n.Ident[1:]...),
			args:    args,
		}, nil
	case *parse.ChainNode:
		base, err := c.arg(n.Node)
		if err != nil {
			return nil, err
		}
		args, err := c.args(in.Args[1:])
		if err != nil {
			return nil, err
		}
		return &commandNode{
			kind:  cmdChain,
			base:  base,
			chain: append([]string(nil), n.Field...),
			args:  args,
		}, nil
	case *parse.PipeNode:
		if len(in.Args) != 1 {
			return nil, errx.Detail(ErrParseTemplate, "parenthesized pipeline with arguments | 括号流水线带参数")
		}
		p, _, err := c.pipeline(n)
		if err != nil {
			return nil, err
		}
		return &commandNode{kind: cmdValue, value: &pipeExpr{pipe: p}}, nil
	default:
		if len(in.Args) != 1 {
			return nil, errx.Detail(ErrParseTemplate, "arguments to non-function | 给非函数传参")
		}
		v, err := c.arg(first)
		if err != nil {
			return nil, err
		}
		return &commandNode{kind: cmdValue, value: v}, nil
	}
}

// args compiles command arguments. | args 编译命令参数。
func (c *compiler) args(in []parse.Node) ([]expr, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]expr, 0, len(in))
	for _, n := range in {
		a, err := c.arg(n)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// arg compiles one argument expression. | arg 编译单个参数表达式。
func (c *compiler) arg(in parse.Node) (expr, error) {
	switch n := in.(type) {
	case *parse.DotNode:
		return dotExpr{}, nil
	case *parse.NilNode:
		return nilExpr{}, nil
	case *parse.BoolNode:
		return literalExpr{v: n.True}, nil
	case *parse.StringNode:
		return literalExpr{v: n.Text}, nil
	case *parse.NumberNode:
		return literalExpr{v: idealConstant(n)}, nil
	case *parse.FieldNode:
		return fieldExpr{chain: append([]string(nil), n.Ident...)}, nil
	case *parse.VariableNode:
		return varExpr{name: n.Ident[0], chain: append([]string(nil), n.Ident[1:]...)}, nil
	case *parse.ChainNode:
		base, err := c.arg(n.Node)
		if err != nil {
			return nil, err
		}
		return chainExpr{base: base, chain: append([]string(nil), n.Field...)}, nil
	case *parse.PipeNode:
		p, _, err := c.pipeline(n)
		if err != nil {
			return nil, err
		}
		return pipeExpr{pipe: p}, nil
	case *parse.IdentifierNode:
		ref, err := c.resolveFunc(n.Ident)
		if err != nil {
			return nil, err
		}
		return callExpr{ref: ref}, nil
	default:
		return nil, errx.Detail(ErrParseTemplate, "argument=%[1]s | 参数=%[1]s", in.String())
	}
}

// idealConstant converts a parsed number into the Go value text/template
// would use when no target type is known. | idealConstant 把解析出的数字转换为
// text/template 在无目标类型时使用的 Go 值。
func idealConstant(n *parse.NumberNode) any {
	switch {
	case n.IsComplex:
		return n.Complex128
	case n.IsFloat && !isHexInt(n.Text) && !isRuneInt(n.Text) && strings.ContainsAny(n.Text, ".eEpP"):
		return n.Float64
	case n.IsInt:
		return int(n.Int64)
	case n.IsUint:
		return n.Uint64
	}
	return nil
}

// isRuneInt reports a rune literal. | isRuneInt 报告字符字面量。
func isRuneInt(s string) bool { return len(s) > 0 && s[0] == '\'' }

// isHexInt reports a hexadecimal integer literal. | isHexInt 报告十六进制整数字面量。
func isHexInt(s string) bool {
	return len(s) > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') && !strings.ContainsAny(s, "pP")
}
