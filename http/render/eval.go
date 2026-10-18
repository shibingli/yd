package render

// Expression evaluation. | 表达式求值。
//
// Pipelines, commands and arguments are evaluated with the semantics of
// text/template: the previous command's value becomes the final argument of
// the next one, a pipeline may declare variables, and and/or short-circuit.
// | 流水线、命令与参数按 text/template 的语义求值：上一条命令的结果成为下一条的
// 最后一个参数，流水线可以声明变量，and/or 短路求值。

import "reflect"

// funcKind selects how a function reference is invoked. | funcKind 选择函数引用的调用方式。
type funcKind uint8

const (
	funcReflect funcKind = iota
	funcBuiltin
	funcBinding
	funcFast
	funcEscaper
)

// bindingKind identifies a per-execution helper. | bindingKind 标识按次执行的辅助函数。
type bindingKind uint8

const (
	bindingTr bindingKind = iota
	bindingCan
	bindingHasRole
	bindingPerm
	bindingRole
)

// fastFuncID identifies a default function with a typed fast path. | fastFuncID 标识带类型化快路径的默认函数。
type fastFuncID uint8

const (
	fastNone fastFuncID = iota
	fastDict
	fastList
	fastDefault
	fastContains
	fastHasPrefix
	fastHasSuffix
	fastLower
	fastUpper
	fastTrim
	fastJoin
	fastJSON
	fastHumanSize
	fastHumanTime
	fastHx
	fastAsset
)

// bindingFor maps a helper name to its binding kind. | bindingFor 把辅助函数名映射为绑定种类。
func bindingFor(name string) (bindingKind, bool) {
	switch name {
	case "tr":
		return bindingTr, true
	case "can":
		return bindingCan, true
	case "hasRole":
		return bindingHasRole, true
	case "perm":
		return bindingPerm, true
	case "role":
		return bindingRole, true
	}
	return 0, false
}

// fastFuncFor maps a default function name to its fast path. | fastFuncFor 把默认函数名映射为快路径。
func fastFuncFor(name string) (fastFuncID, bool) {
	switch name {
	case "dict":
		return fastDict, true
	case "list":
		return fastList, true
	case "default":
		return fastDefault, true
	case "contains":
		return fastContains, true
	case "hasPrefix":
		return fastHasPrefix, true
	case "hasSuffix":
		return fastHasSuffix, true
	case "lower":
		return fastLower, true
	case "upper":
		return fastUpper, true
	case "trim":
		return fastTrim, true
	case "join":
		return fastJoin, true
	case "json":
		return fastJSON, true
	case "humanSize":
		return fastHumanSize, true
	case "humanTime":
		return fastHumanTime, true
	case "hx":
		return fastHx, true
	case "asset":
		return fastAsset, true
	}
	return fastNone, false
}

// funcRef is a resolved function reference. | funcRef 是已解析的函数引用。
type funcRef struct {
	fn      reflect.Value
	name    string
	kind    funcKind
	builtin builtinID
	esc     escKind
	binding bindingKind
	fast    fastFuncID
}

// commandNode evaluates one command. | commandNode 求值单条命令。
func (c *commandNode) eval(s *execState, final any, hasFinal bool) (any, error) {
	switch c.kind {
	case cmdFunc:
		if c.fn.kind == funcBuiltin && (c.fn.builtin == builtinAnd || c.fn.builtin == builtinOr) {
			return evalAndOr(s, c.fn.builtin, c.args, final, hasFinal)
		}
		mark := len(s.scratch)
		args, err := s.evalArgs(c.args)
		if err != nil {
			s.scratch = s.scratch[:mark]
			return nil, err
		}
		if hasFinal {
			args = append(args, final)
		}
		v, err := callFunc(s, c.fn, args)
		s.scratch = s.scratch[:mark]
		return v, err
	case cmdField:
		return evalChain(s, s.dot, c.chain, c.args, final, hasFinal)
	case cmdVar:
		v, err := s.varValue(c.varName)
		if err != nil {
			return nil, err
		}
		return evalChain(s, v, c.chain, c.args, final, hasFinal)
	case cmdChain:
		v, err := c.base.eval(s)
		if err != nil {
			return nil, err
		}
		return evalChain(s, v, c.chain, c.args, final, hasFinal)
	case cmdValue:
		if hasFinal || len(c.args) > 0 {
			return nil, execErr("can't give an argument to a non-function | 不能给非函数传参")
		}
		return c.value.eval(s)
	}
	return nil, execErr("unknown command | 未知命令")
}

// evalArgs evaluates all argument expressions into the state's scratch stack.
// The caller must truncate the scratch stack after invoking the function. | evalArgs
// 把全部参数表达式求值到状态的暂存栈；调用方在函数返回后必须截断暂存栈。
func (s *execState) evalArgs(args []expr) ([]any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	mark := len(s.scratch)
	for _, a := range args {
		v, err := a.eval(s)
		if err != nil {
			return nil, err
		}
		s.scratch = append(s.scratch, v)
	}
	return s.scratch[mark:], nil
}

// evalAndOr implements the short-circuiting and/or builtins. | evalAndOr 实现短路求值的 and/or 内建。
func evalAndOr(s *execState, id builtinID, args []expr, final any, hasFinal bool) (any, error) {
	isOr := id == builtinOr
	var last any
	for _, a := range args {
		v, err := a.eval(s)
		if err != nil {
			return nil, err
		}
		last = v
		if truthy(v) == isOr {
			return v, nil
		}
	}
	if hasFinal {
		return final, nil
	}
	return last, nil
}

// evalChain walks fields from receiver and optionally calls the last element as
// a method with args and the pipeline's final value. | evalChain 从 receiver 遍历字段，
// 必要时把最后一个元素作为方法调用，并带上参数与流水线的最终值。
func evalChain(s *execState, receiver any, chain []string, args []expr, final any, hasFinal bool) (any, error) {
	if len(chain) == 0 {
		if len(args) > 0 || hasFinal {
			return nil, execErr("can't give an argument to a non-function | 不能给非函数传参")
		}
		return normalizeValue(receiver), nil
	}
	for i := 0; i < len(chain); i++ {
		switch receiver.(type) {
		case missingValue:
			// An absent map key propagates silently, exactly as an invalid value
			// does in text/template. | 缺失的映射键静默传播，与 text/template 的无效值一致。
			return nil, nil
		case nilValue:
			return nil, execErr("nil pointer evaluating field %[1]s | nil 指针上求值字段 %[1]s", chain[i])
		}
		if i < len(chain)-1 {
			v, err := fieldOrMethod(s, receiver, chain[i], nil, false)
			if err != nil {
				return nil, err
			}
			receiver = v
			continue
		}
		if len(args) == 0 && !hasFinal {
			v, err := fieldOrMethod(s, receiver, chain[i], nil, false)
			if err != nil {
				return nil, err
			}
			return normalizeValue(v), nil
		}
		mark := len(s.scratch)
		argv, err := s.evalArgs(args)
		if err != nil {
			s.scratch = s.scratch[:mark]
			return nil, err
		}
		if hasFinal {
			argv = append(argv, final)
		}
		v, err := fieldOrMethod(s, receiver, chain[i], argv, true)
		s.scratch = s.scratch[:mark]
		if err != nil {
			return nil, err
		}
		return normalizeValue(v), nil
	}
	return nil, nil
}

// pipeNode evaluates a pipeline and records its declarations. | pipeNode 求值流水线并记录其声明。
func (p *pipeNode) eval(s *execState) (any, error) {
	if len(p.cmds) == 0 {
		return nil, nil
	}
	var value any
	has := false
	for _, cmd := range p.cmds {
		v, err := cmd.eval(s, value, has)
		if err != nil {
			return nil, err
		}
		value, has = v, true
	}
	value = normalizeValue(value)
	for _, name := range p.decl {
		if p.assign {
			if err := s.setVar(name, value); err != nil {
				return nil, err
			}
			continue
		}
		s.push(name, value)
	}
	return value, nil
}

// literalExpr.eval returns its constant. | literalExpr.eval 返回常量。
func (e literalExpr) eval(*execState) (any, error) { return e.v, nil }

// dotExpr.eval returns the current dot. | dotExpr.eval 返回当前 dot。
func (dotExpr) eval(s *execState) (any, error) { return s.dot, nil }

// nilExpr.eval returns nil. | nilExpr.eval 返回 nil。
func (nilExpr) eval(*execState) (any, error) { return nil, nil }

// fieldExpr.eval walks fields from dot. | fieldExpr.eval 从 dot 遍历字段。
func (e fieldExpr) eval(s *execState) (any, error) {
	return evalChain(s, s.dot, e.chain, nil, nil, false)
}

// varExpr.eval reads a variable and walks fields. | varExpr.eval 读取变量并遍历字段。
func (e varExpr) eval(s *execState) (any, error) {
	v, err := s.varValue(e.name)
	if err != nil {
		return nil, err
	}
	return evalChain(s, v, e.chain, nil, nil, false)
}

// chainExpr.eval evaluates its base and walks fields. | chainExpr.eval 求值基表达式并遍历字段。
func (e chainExpr) eval(s *execState) (any, error) {
	v, err := e.base.eval(s)
	if err != nil {
		return nil, err
	}
	return evalChain(s, v, e.chain, nil, nil, false)
}

// pipeExpr.eval evaluates a parenthesized pipeline. | pipeExpr.eval 求值括号内流水线。
func (e pipeExpr) eval(s *execState) (any, error) { return e.pipe.eval(s) }

// callExpr.eval invokes a zero-argument function used as an argument. | callExpr.eval 调用作为参数出现的零参函数。
func (e callExpr) eval(s *execState) (any, error) { return callFunc(s, e.ref, nil) }

// callFunc invokes a resolved function reference. | callFunc 调用已解析的函数引用。
func callFunc(s *execState, ref *funcRef, args []any) (any, error) {
	switch ref.kind {
	case funcEscaper:
		return applyEscaper(ref.esc, args), nil
	case funcBinding:
		return callBinding(s, ref, args)
	case funcFast:
		return callFast(s, ref, args)
	case funcBuiltin:
		return callBuiltin(s, ref.builtin, args)
	case funcReflect:
		return callReflect(ref.fn, args)
	}
	return nil, execErr("unknown function kind | 未知函数种类")
}

// applyEscaper applies one contextual escaper in ordinary function position. | applyEscaper 在普通函数位置应用一个上下文转义器。
func applyEscaper(kind escKind, args []any) string {
	var v any
	if len(args) == 1 {
		v = args[0]
	} else {
		v = evalArgsSlice(append([]any(nil), args...))
	}
	return string(appendEscaped(nil, v, kind))
}

// callBinding invokes a per-execution helper, falling back to its default
// implementation when no translator or authority was injected. | callBinding 调用按次执行
// 的辅助函数；未注入译者或判定主体时退回默认实现。
func callBinding(s *execState, ref *funcRef, args []any) (any, error) {
	switch ref.binding {
	case bindingTr:
		key, ok := stringArg(args, 0)
		if !ok {
			return callReflect(ref.fn, args)
		}
		if s.tr != nil {
			var extra []any
			if len(args) > 1 {
				extra = args[1:]
			}
			return s.tr.T(key, extra...), nil
		}
		return key, nil
	case bindingCan, bindingHasRole:
		if len(args) != 1 {
			return callReflect(ref.fn, args)
		}
		refName := refString(args[0])
		if s.az == nil {
			return false, nil
		}
		if ref.binding == bindingCan {
			return s.az.Can(refName), nil
		}
		return s.az.HasRole(refName), nil
	case bindingPerm, bindingRole:
		if len(args) != 1 {
			return callReflect(ref.fn, args)
		}
		refName := refString(args[0])
		if s.az == nil {
			return refName, nil
		}
		if ref.binding == bindingPerm {
			return s.az.Perm(refName), nil
		}
		return s.az.Role(refName), nil
	}
	return nil, execErr("unknown binding | 未知绑定")
}

// stringArg extracts a string argument. | stringArg 提取字符串参数。
func stringArg(args []any, i int) (string, bool) {
	if i >= len(args) {
		return "", false
	}
	s, ok := args[i].(string)
	return s, ok
}

// int64Arg extracts an integer argument with text/template's widening rules.
// | int64Arg 按 text/template 的拓宽规则提取整数参数。
func int64Arg(args []any, i int) (int64, bool) {
	if i >= len(args) {
		return 0, false
	}
	switch v := args[i].(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		return int64(v), true
	}
	return 0, false
}
