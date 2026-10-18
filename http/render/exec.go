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

// variable is one dynamic template variable. A value taken from a reflect path
// is kept as a reflect.Value until some generic operation needs an interface,
// which avoids boxing loop elements and field assignments. | variable 是一个
// 动态模板变量。来自反射路径的值在通用操作需要接口之前保持为 reflect.Value，
// 从而避免循环元素与字段赋值装箱。
type variable struct {
	value any
	ref   reflect.Value
	name  string
	isRef bool
}

// any materializes the variable as an interface, boxing only on first use. | any
// 把变量实体化为接口，只在首次使用时装箱。
func (v *variable) any() any {
	if v.isRef {
		if v.ref.IsValid() {
			v.value = v.ref.Interface()
		} else {
			v.value = nil
		}
		v.ref = reflect.Value{}
		v.isRef = false
	}
	return v.value
}

// execState is the mutable state of one execution. | execState 是一次执行的可变状态。
type execState struct {
	dot          any
	tr           Translator
	az           Authz
	out          *output
	tmpl         *compiledTemplate
	dotMap       map[string]any
	dotRef       reflect.Value
	vars         []variable
	scratch      []any
	scratchBytes []byte
	depth        int
	flow         flowKind
	dotMapOK     bool
	dotRefOK     bool
}

// dotSnapshot saves the three parts of the dot so a nested scope can restore
// them. | dotSnapshot 保存 dot 的三部分，供嵌套作用域恢复。
type dotSnapshot struct {
	value any
	m     map[string]any
	ref   reflect.Value
	ok    bool
	mapOK bool
}

// saveDot snapshots the current dot. | saveDot 保存当前 dot。
func (s *execState) saveDot() dotSnapshot {
	return dotSnapshot{value: s.dot, m: s.dotMap, ref: s.dotRef, ok: s.dotRefOK, mapOK: s.dotMapOK}
}

// restoreDot restores a dot snapshot. | restoreDot 恢复 dot 快照。
func (s *execState) restoreDot(d dotSnapshot) {
	s.dot, s.dotMap, s.dotRef, s.dotRefOK, s.dotMapOK = d.value, d.m, d.ref, d.ok, d.mapOK
}

// setDot sets an interface dot. | setDot 设置接口形式的 dot。
func (s *execState) setDot(v any) {
	s.dot = v
	s.dotMap = nil
	s.dotRef = reflect.Value{}
	s.dotMapOK = false
	s.dotRefOK = false
}

// setDotRef sets a reflect dot without boxing it. | setDotRef 设置反射形式的 dot，不做装箱。
func (s *execState) setDotRef(v reflect.Value) {
	s.dot = nil
	s.dotMap = nil
	s.dotRef = v
	s.dotMapOK = false
	s.dotRefOK = true
}

// setDotMap sets a map[string]any dot without boxing the map header. | setDotMap 设置 map[string]any 形式的 dot，不装箱映射头。
func (s *execState) setDotMap(m map[string]any) {
	s.dot = nil
	s.dotMap = m
	s.dotRef = reflect.Value{}
	s.dotMapOK = true
	s.dotRefOK = false
}

// dotAny materializes the dot as an interface, boxing a reflect dot only when
// a generic path actually needs it. | dotAny 把 dot 实体化为接口；只有通用路径确实
// 需要时才装箱反射 dot。
func (s *execState) dotAny() any {
	if s.dotMapOK {
		return s.dotMap
	}
	if s.dotRefOK {
		if !s.dotRef.IsValid() {
			return nil
		}
		return s.dotRef.Interface()
	}
	return s.dot
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
	s.dotMap = nil
	s.dotRef = reflect.Value{}
	s.dotMapOK = false
	s.dotRefOK = false
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
	s.dotMap = nil
	s.dotRef = reflect.Value{}
	s.dotMapOK = false
	s.dotRefOK = false
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

// pushRef appends a variable backed by a reflect value. | pushRef 追加一个由反射值支持的变量。
func (s *execState) pushRef(name string, ref reflect.Value) {
	s.vars = append(s.vars, variable{name: name, ref: ref, isRef: true})
}

// setVarRef assigns an existing variable from a reflect value. | setVarRef 用反射值给已有变量赋值。
func (s *execState) setVarRef(name string, ref reflect.Value) error {
	for i := len(s.vars) - 1; i >= 0; i-- {
		if s.vars[i].name == name {
			s.vars[i].value = nil
			s.vars[i].ref = ref
			s.vars[i].isRef = true
			return nil
		}
	}
	return execErr("undefined variable %[1]s | 未定义变量 %[1]s", name)
}

// setTopVarRef assigns the n-th variable from the top from a reflect value. | setTopVarRef 用反射值给从顶部数第 n 个变量赋值。
func (s *execState) setTopVarRef(n int, ref reflect.Value) {
	v := &s.vars[len(s.vars)-n]
	v.value = nil
	v.ref = ref
	v.isRef = true
}

// setVar assigns an existing variable. | setVar 给已有变量赋值。
func (s *execState) setVar(name string, v any) error {
	for i := len(s.vars) - 1; i >= 0; i-- {
		if s.vars[i].name == name {
			s.vars[i].value = v
			s.vars[i].ref = reflect.Value{}
			s.vars[i].isRef = false
			return nil
		}
	}
	return execErr("undefined variable %[1]s | 未定义变量 %[1]s", name)
}

// setTopVar assigns the n-th variable from the top. | setTopVar 给从顶部数第 n 个变量赋值。
func (s *execState) setTopVar(n int, v any) {
	cell := &s.vars[len(s.vars)-n]
	cell.value = v
	cell.ref = reflect.Value{}
	cell.isRef = false
}

// varCell returns the variable cell from the top of the stack. | varCell 从栈顶向下返回变量单元。
func (s *execState) varCell(name string) (*variable, bool) {
	for i := len(s.vars) - 1; i >= 0; i-- {
		if s.vars[i].name == name {
			return &s.vars[i], true
		}
	}
	return nil, false
}

// varValue looks a variable up from the top of the stack. | varValue 从栈顶向下查找变量。
func (s *execState) varValue(name string) (any, error) {
	cell, ok := s.varCell(name)
	if !ok {
		return nil, execErr("undefined variable %[1]s | 未定义变量 %[1]s", name)
	}
	return cell.any(), nil
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
	s.setDot(data)
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
	if n.declField != nil {
		if rv, ok := n.declField.value(s); ok {
			if n.declAssign {
				return s.setVarRef(n.declName, rv)
			}
			s.pushRef(n.declName, rv)
			return nil
		}
	}
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
	if v, handled := n.field.anyValue(s); handled {
		n.appendEscapedAny(s, v)
		return true, nil
	}
	rv, ok := n.field.value(s)
	if !ok {
		return false, nil
	}
	start := len(s.out.b)
	s.out.b = appendEscapedReflect(s.out.b, rv, n.field.escs[0])
	n.applyEscaperChain(s, start, n.field.escs[1:])
	return true, nil
}

// appendEscapedAny escapes a value that is already an interface, as map data
// is, without converting it through reflect. | appendEscapedAny 转义已经是接口的值
// （映射数据即如此），不经过反射转换。
func (n *actionNode) appendEscapedAny(s *execState, v any) {
	start := len(s.out.b)
	s.out.b = appendEscaped(s.out.b, v, n.field.escs[0])
	n.applyEscaperChain(s, start, n.field.escs[1:])
}

// applyEscaperChain applies the remaining escapers to the segment at start. | applyEscaperChain 对 start 处的片段应用剩余转义器。
func (n *actionNode) applyEscaperChain(s *execState, start int, escs []escKind) {
	for _, esc := range escs {
		s.scratchBytes = append(s.scratchBytes[:0], s.out.b[start:]...)
		s.out.b = s.out.b[:start]
		s.out.b = appendEscapedBytes(s.out.b, s.scratchBytes, esc)
	}
}

// anyValue resolves a chain over map[string]any data without reflection. | anyValue 在 map[string]any 数据上不经反射解析字段链。
func (f *fieldAction) anyValue(s *execState) (any, bool) {
	var cur any
	switch {
	case f.varName != "":
		cell, ok := s.varCell(f.varName)
		if !ok || cell.isRef {
			return nil, false
		}
		cur = cell.value
	case s.dotMapOK:
		cur = s.dotMap
	case s.dotRefOK:
		return nil, false
	default:
		cur = s.dot
	}
	for _, name := range f.chain {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, exists := m[name]
		if !exists {
			return nil, true
		}
		cur = v
	}
	return cur, true
}

// value resolves the field chain, reusing a per-type struct index path so the
// hot loop does not hash reflect types on every row. | value 解析字段链，并复用按类型
// 缓存的字段索引路径，使热循环不必每行哈希反射类型。
func (f *fieldAction) value(s *execState) (reflect.Value, bool) {
	var typ reflect.Type
	var root reflect.Value
	if f.varName != "" {
		cell, ok := s.varCell(f.varName)
		if !ok {
			return reflect.Value{}, false
		}
		if cell.isRef {
			root = cell.ref
		} else {
			root = reflect.ValueOf(cell.value)
		}
	} else if s.dotRefOK {
		root = s.dotRef
	} else {
		root = reflect.ValueOf(s.dot)
	}
	if root.IsValid() {
		typ = root.Type()
	}
	if typ != nil {
		path := f.path.Load()
		if path == nil || path.typ != typ {
			path = resolveStructPath(typ, f.chain)
			f.path.Store(path)
		}
		if path.ok {
			v := root
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
	if f.varName != "" {
		cell, ok := s.varCell(f.varName)
		if !ok {
			return reflect.Value{}, false
		}
		if cell.isRef {
			return resolveFieldRefValue(cell.ref, f.chain, f.keys)
		}
		return resolveFieldRef(cell.any(), f.chain, f.keys)
	}
	if s.dotRefOK {
		return resolveFieldRefValue(s.dotRef, f.chain, f.keys)
	}
	return resolveFieldRef(s.dotAny(), f.chain, f.keys)
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
	err := n.run(s)
	s.pop(mark)
	return err
}

// run is the body of exec; the pop lives outside so the defer cannot spill to
// the heap on the many-return path. | run 是 exec 的主体；pop 放在外层，使多返回
// 路径上的 defer 不会溢出到堆。
func (n *ifNode) run(s *execState) error {
	var truth bool
	var withDot any
	useWithDot := false
	switch {
	case n.quick != nil:
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
	case n.cond != nil:
		if v, handled := n.cond.anyValue(s); handled {
			truth = truthy(v)
			break
		}
		if rv, ok := n.cond.value(s); ok {
			truth = truthyReflect(rv)
			break
		}
		v, err := n.pipe.eval(s)
		if err != nil {
			return err
		}
		truth = truthy(v)
		withDot, useWithDot = v, true
	default:
		v, err := n.pipe.eval(s)
		if err != nil {
			return err
		}
		truth = truthy(v)
		withDot, useWithDot = v, true
	}
	if truth {
		if n.with && useWithDot {
			old := s.saveDot()
			s.setDot(withDot)
			err := n.body.exec(s)
			s.restoreDot(old)
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
	child.setDot(arg)
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
