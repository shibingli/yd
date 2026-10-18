package render

// Execution engine. | 执行引擎。
//
// The executor is a tiny stack machine over the compiled node graph. State is
// allocated per render, never shared, so one compiled program serves unlimited
// concurrent requests. Output is appended to one byte slice and written to the
// caller only after the whole template succeeded. | 执行器是运行在已编译节点图上的
// 小型栈机。状态按渲染分配、从不共享，因此一份已编译程序可服务任意并发请求。
// 输出追加到单个字节切片，整份模板成功后才交给调用方。

import (
	"reflect"
	"strconv"
	"sync"

	"github.com/shibingli/yd/core/errx"
)

// maxExecDepth mirrors text/template's recursion bound. | maxExecDepth 对齐 text/template 的递归上限。
const maxExecDepth = 100000

// flowKind carries break/continue without panicking. | flowKind 以非 panic 方式传递 break/continue。
type flowKind uint8

const (
	flowNone flowKind = iota
	flowBreak
	flowContinue
)

// output is the shared append target of one render. | output 是一次渲染共享的追加目标。
type output struct {
	b []byte
}

// outputPool reuses the shared output holder. | outputPool 复用共享输出持有者。
var outputPool = sync.Pool{New: func() any { return &output{} }}

// variable is one dynamic template variable. | variable 是一个动态模板变量。
type variable struct {
	value any
	name  string
}

// execState is the mutable state of one execution. | execState 是一次执行的可变状态。
type execState struct {
	dot          any
	tr           Translator
	az           Authz
	out          *output
	tmpl         *compiledTemplate
	vars         []variable
	scratch      []any
	scratchBytes []byte
	depth        int
	flow         flowKind
}

// statePool reuses execution states so a render does not allocate its variable
// and argument stacks. | statePool 复用执行状态，使一次渲染不再分配变量栈与参数栈。
var statePool = sync.Pool{New: func() any {
	return &execState{
		vars:         make([]variable, 0, 16),
		scratch:      make([]any, 0, 32),
		scratchBytes: make([]byte, 0, 256),
	}
}}

// acquireState returns a zeroed execution state. | acquireState 返回已清零的执行状态。
func acquireState() *execState {
	s := statePool.Get().(*execState)
	s.dot = nil
	s.tr = nil
	s.az = nil
	s.depth = 0
	s.flow = flowNone
	s.tmpl = nil
	s.vars = s.vars[:0]
	s.scratch = s.scratch[:0]
	s.scratchBytes = s.scratchBytes[:0]
	return s
}

// releaseState clears references and returns the state to the pool. | releaseState 清除引用并把状态归还池。
func releaseState(s *execState) {
	clear(s.vars)
	clear(s.scratch)
	s.vars = s.vars[:0]
	s.scratch = s.scratch[:0]
	if cap(s.scratchBytes) > maxBufferHint {
		s.scratchBytes = nil
	} else {
		s.scratchBytes = s.scratchBytes[:0]
	}
	s.out = nil
	s.dot = nil
	s.tr = nil
	s.az = nil
	s.tmpl = nil
	statePool.Put(s)
}

// execErr builds a sentinel-wrapped execution error. | execErr 构造带哨兵的执行错误。
func execErr(format string, args ...any) error {
	return errx.Detail(ErrExecuteTemplate, format, args...)
}

// push appends a variable. | push 追加变量。
func (s *execState) push(name string, v any) {
	s.vars = append(s.vars, variable{name: name, value: v})
}

// mark returns the current variable stack depth. | mark 返回当前变量栈深度。
func (s *execState) mark() int { return len(s.vars) }

// pop truncates the variable stack to mark. | pop 把变量栈截断到 mark。
func (s *execState) pop(mark int) { s.vars = s.vars[:mark] }

// setVar assigns an existing variable. | setVar 给已有变量赋值。
func (s *execState) setVar(name string, v any) error {
	for i := len(s.vars) - 1; i >= 0; i-- {
		if s.vars[i].name == name {
			s.vars[i].value = v
			return nil
		}
	}
	return execErr("undefined variable %[1]s | 未定义变量 %[1]s", name)
}

// setTopVar assigns the n-th variable from the top. | setTopVar 给从顶部数第 n 个变量赋值。
func (s *execState) setTopVar(n int, v any) {
	s.vars[len(s.vars)-n].value = v
}

// varValue looks a variable up from the top of the stack. | varValue 从栈顶向下查找变量。
func (s *execState) varValue(name string) (any, error) {
	for i := len(s.vars) - 1; i >= 0; i-- {
		if s.vars[i].name == name {
			return s.vars[i].value, nil
		}
	}
	return nil, execErr("undefined variable %[1]s | 未定义变量 %[1]s", name)
}

// exec renders program p into dst. | exec 把程序 p 渲染到 dst。
func (p *program) exec(dst []byte, data any, tr Translator, az Authz) ([]byte, error) {
	if p.root == nil || p.root.root == nil {
		return dst, execErr("incomplete template | 模板不完整")
	}
	out := outputPool.Get().(*output)
	out.b = dst
	s := acquireState()
	s.out = out
	s.dot = data
	s.tr = tr
	s.az = az
	s.tmpl = p.root
	s.vars = append(s.vars, variable{name: "$", value: data})
	err := p.root.root.exec(s)
	result := out.b
	out.b = nil
	outputPool.Put(out)
	releaseState(s)
	if err != nil {
		return dst, err
	}
	return result, nil
}

// exec executes a list. | exec 执行节点列表。
func (l *listNode) exec(s *execState) error {
	for _, n := range l.nodes {
		if err := n.exec(s); err != nil {
			return err
		}
		if s.flow != flowNone {
			return nil
		}
	}
	return nil
}

// exec writes a static chunk. | exec 输出静态文本。
func (n *textNode) exec(s *execState) error {
	s.out.b = append(s.out.b, n.text...)
	return nil
}

// exec evaluates and prints one action. | exec 求值并输出一个动作。
func (n *actionNode) exec(s *execState) error {
	if n.quick != nil {
		return n.execQuick(s)
	}
	if n.field != nil {
		handled, err := n.execFieldQuick(s)
		if handled || err != nil {
			return err
		}
	}
	v, err := n.pipe.eval(s)
	if err != nil {
		return err
	}
	if !n.print {
		return nil
	}
	if len(n.escs) == 0 {
		return printValue(s, v)
	}
	start := len(s.out.b)
	s.out.b = appendEscaped(s.out.b, v, n.escs[0])
	for _, esc := range n.escs[1:] {
		// Copy the segment out before truncating: escaping inserts bytes, so an
		// in-place rewrite would overwrite unread input. | 截断前先把片段复制出来：
		// 转义会插入字节，就地改写会覆盖尚未读取的输入。
		s.scratchBytes = append(s.scratchBytes[:0], s.out.b[start:]...)
		s.out.b = s.out.b[:start]
		s.out.b = appendEscapedBytes(s.out.b, s.scratchBytes, esc)
	}
	return nil
}

// execQuick prints a single-binding action without boxing its result. | execQuick 无装箱输出单绑定动作。
func (n *actionNode) execQuick(s *execState) error {
	q := n.quick
	mark := len(s.scratch)
	args, err := s.evalArgs(q.args)
	if err != nil {
		s.scratch = s.scratch[:mark]
		return err
	}
	switch q.kind {
	case bindingTr:
		key, _ := stringArg(args, 0)
		out := key
		if s.tr != nil {
			out = s.tr.T(key, args[1:]...)
		}
		s.out.b = appendEscapedString(s.out.b, out, q.escs[0])
	case bindingPerm:
		ref := refString(args[0])
		out := ref
		if s.az != nil {
			out = s.az.Perm(ref)
		}
		s.out.b = appendEscapedString(s.out.b, out, q.escs[0])
	case bindingRole:
		ref := refString(args[0])
		out := ref
		if s.az != nil {
			out = s.az.Role(ref)
		}
		s.out.b = appendEscapedString(s.out.b, out, q.escs[0])
	case bindingCan:
		allow := false
		if s.az != nil {
			allow = s.az.Can(refString(args[0]))
		}
		s.out.b = appendEscapedString(s.out.b, strconv.FormatBool(allow), q.escs[0])
	case bindingHasRole:
		allow := false
		if s.az != nil {
			allow = s.az.HasRole(refString(args[0]))
		}
		s.out.b = appendEscapedString(s.out.b, strconv.FormatBool(allow), q.escs[0])
	}
	s.scratch = s.scratch[:mark]
	return nil
}

// execFieldQuick prints a single field chain without interface boxing. | execFieldQuick 无接口装箱地输出单条字段链。
func (n *actionNode) execFieldQuick(s *execState) (bool, error) {
	rv, ok := n.field.value(s.dot)
	if !ok {
		return false, nil
	}
	start := len(s.out.b)
	s.out.b = appendEscapedReflect(s.out.b, rv, n.field.escs[0])
	for _, esc := range n.field.escs[1:] {
		s.scratchBytes = append(s.scratchBytes[:0], s.out.b[start:]...)
		s.out.b = s.out.b[:start]
		s.out.b = appendEscapedBytes(s.out.b, s.scratchBytes, esc)
	}
	return true, nil
}

// value resolves the field chain, reusing a per-type struct index path so the
// hot loop does not hash reflect types on every row. | value 解析字段链，并复用按类型
// 缓存的字段索引路径，使热循环不必每行哈希反射类型。
func (f *fieldAction) value(dot any) (reflect.Value, bool) {
	typ := reflect.TypeOf(dot)
	if typ != nil {
		path := f.path.Load()
		if path == nil || path.typ != typ {
			path = resolveStructPath(typ, f.chain)
			f.path.Store(path)
		}
		if path.ok {
			v := reflect.ValueOf(dot)
			for _, idx := range path.indexes {
				for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
					if v.IsNil() {
						// A nil receiver in the middle of a field chain is an explicit
						// nil; let the generic evaluator report it. | 字段链中途的 nil
						// 接收者是显式 nil，交给通用求值器报告。
						return reflect.Value{}, false
					}
					v = v.Elem()
				}
				next, err := v.FieldByIndexErr(idx)
				if err != nil {
					return reflect.Value{}, false
				}
				v = next
			}
			return v, true
		}
	}
	return resolveFieldRef(dot, f.chain)
}

// resolveStructPath resolves a chain assuming every step is a struct field. | resolveStructPath 假定每一步都是结构体字段来解析链。
func resolveStructPath(t reflect.Type, chain []string) *structPath {
	path := &structPath{typ: t, ok: true}
	cur := t
	for _, name := range chain {
		for cur.Kind() == reflect.Pointer {
			cur = cur.Elem()
		}
		if cur.Kind() != reflect.Struct {
			path.ok = false
			return path
		}
		idx, ok := structField(cur, name)
		if !ok {
			path.ok = false
			return path
		}
		path.indexes = append(path.indexes, idx)
		cur = cur.FieldByIndex(idx).Type
	}
	return path
}

// exec implements if and with. | exec 实现 if 与 with。
func (n *ifNode) exec(s *execState) error {
	mark := s.mark()
	defer s.pop(mark)
	var truth bool
	dotValue := s.dot
	if n.quick != nil {
		scratchMark := len(s.scratch)
		args, err := s.evalArgs(n.quick.args)
		if err != nil {
			s.scratch = s.scratch[:scratchMark]
			return err
		}
		if len(args) == 1 && s.az != nil {
			if n.quick.kind == bindingCan {
				truth = s.az.Can(refString(args[0]))
			} else {
				truth = s.az.HasRole(refString(args[0]))
			}
		}
		s.scratch = s.scratch[:scratchMark]
	} else {
		v, err := n.pipe.eval(s)
		if err != nil {
			return err
		}
		truth = truthy(v)
		dotValue = v
	}
	if truth {
		if n.with {
			old := s.dot
			s.dot = dotValue
			err := n.body.exec(s)
			s.dot = old
			return err
		}
		return n.body.exec(s)
	}
	if n.elseBody != nil {
		return n.elseBody.exec(s)
	}
	return nil
}

// exec invokes another template. | exec 调用另一个模板。
func (n *templateNode) exec(s *execState) error {
	arg, err := n.pipe.eval(s)
	if err != nil {
		return err
	}
	if n.target == nil || n.target.root == nil {
		return execErr("template %[1]q not defined | 模板 %[1]q 未定义", n.name)
	}
	if s.depth >= maxExecDepth {
		return execErr("exceeded maximum template depth | 超过模板最大嵌套深度")
	}
	child := acquireState()
	child.out = s.out
	child.dot = arg
	child.tr = s.tr
	child.az = s.az
	child.depth = s.depth + 1
	child.tmpl = n.target
	child.vars = append(child.vars, variable{name: "$", value: arg})
	err = n.target.root.exec(child)
	releaseState(child)
	return err
}

// exec signals break. | exec 发出 break 信号。
func (n *breakNode) exec(s *execState) error {
	s.flow = flowBreak
	return nil
}

// exec signals continue. | exec 发出 continue 信号。
func (n *continueNode) exec(s *execState) error {
	s.flow = flowContinue
	return nil
}
