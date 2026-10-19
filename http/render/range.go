package render

// Range execution and deterministic iteration helpers. | range 执行与确定性遍历辅助。

import (
	"reflect"
	"sort"
)

// exec implements range. | exec 实现 range。
func (n *rangeNode) exec(s *execState) error {
	mark := s.mark()
	err := n.run(s)
	s.pop(mark)
	return err
}

// run is the allocation-free body of exec; keeping the pop outside removes a
// heap-allocated defer from the hot loop. | run 是 exec 的无分配主体；把 pop 放在
// 外层可避免热循环中出现堆分配的 defer。
func (n *rangeNode) run(s *execState) error {
	var rv reflect.Value
	var val any
	fromAny := false
	handled := false
	if n.field != nil {
		if v, ok := n.field.anyValue(s); ok {
			val, fromAny, handled = v, true, true
		} else if v, _, ok := n.field.value(s); ok {
			rv, handled = v, true
		}
	}
	if !handled {
		v, err := n.pipe.eval(s)
		if err != nil {
			return err
		}
		val = v
	}
	if !(handled && !fromAny) {
		// Fast paths for the shapes the framework data uses most. | 框架数据最常用的形状走快路径。
		switch items := val.(type) {
		case []any:
			iterated := false
			for i, elem := range items {
				iterated = true
				stop, err := n.next(s, i, elem)
				if err != nil {
					return err
				}
				if stop {
					return nil
				}
			}
			if !iterated && n.elseBody != nil {
				return n.elseBody.exec(s)
			}
			return nil
		case []map[string]any:
			iterated := false
			for i, elem := range items {
				iterated = true
				stop, err := n.nextMap(s, i, elem)
				if err != nil {
					return err
				}
				if stop {
					return nil
				}
			}
			if !iterated && n.elseBody != nil {
				return n.elseBody.exec(s)
			}
			return nil
		case map[string]any:
			keys := make([]string, 0, len(items))
			for k := range items {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			iterated := false
			for _, k := range keys {
				iterated = true
				stop, err := n.next(s, k, items[k])
				if err != nil {
					return err
				}
				if stop {
					return nil
				}
			}
			if !iterated && n.elseBody != nil {
				return n.elseBody.exec(s)
			}
			return nil
		}
		rv = reflect.ValueOf(val)
	}

	// Unwrap interface and pointer shells before dispatch, as text/template's
	// indirect does. | 分派前解包接口与指针外壳，与 text/template 的 indirect 一致。
	for rv.IsValid() && (rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer) {
		if rv.IsNil() {
			rv = reflect.Value{}
			break
		}
		rv = rv.Elem()
	}

	iterated := false
	switch rv.Kind() {
	case reflect.Invalid:
		// nil behaves as an empty collection. | nil 视为空集合。
	case reflect.Array, reflect.Slice:
		for i := 0; i < rv.Len(); i++ {
			iterated = true
			stop, err := n.nextRef(s, i, rv.Index(i))
			if err != nil {
				return err
			}
			if stop {
				return nil
			}
		}
	case reflect.Map:
		keys := sortedMapKeys(rv)
		for _, k := range keys {
			iterated = true
			var index any
			if len(n.pipe.decl) > 0 {
				index = elementValue(k)
			}
			stop, err := n.nextRef(s, index, rv.MapIndex(k))
			if err != nil {
				return err
			}
			if stop {
				return nil
			}
		}
	case reflect.Chan:
		if rv.IsNil() {
			break
		}
		if rv.Type().ChanDir() == reflect.SendDir {
			return execErr("range over send-only channel | 不能遍历只写通道")
		}
		for i := 0; ; i++ {
			elem, ok := rv.Recv()
			if !ok {
				break
			}
			iterated = true
			stop, err := n.nextRef(s, i, elem)
			if err != nil {
				return err
			}
			if stop {
				return nil
			}
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Func:
		// Keep range-over-func closures in a separate method: their captured
		// loop values would otherwise be heap-allocated on every execution, even
		// when the collection is a plain slice. | 把 range-over-func 闭包放到独立
		// 方法：否则其捕获的循环值会在每次执行时堆分配，即使集合是普通切片。
		done, stopped, err := n.runSeq(s, rv)
		if err != nil {
			return err
		}
		iterated = done
		if stopped {
			return nil
		}
	default:
		return execErr("range can't iterate over %[1]v | range 无法遍历 %[1]v", rv.Type())
	}

	if !iterated && n.elseBody != nil {
		return n.elseBody.exec(s)
	}
	return nil
}

// runSeq handles integer ranges and range-over-func. | runSeq 处理整数 range 与 range-over-func。
func (n *rangeNode) runSeq(s *execState, rv reflect.Value) (iterated, stopped bool, err error) {
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if len(n.pipe.decl) > 1 {
			return false, false, execErr("can't use %[1]v to iterate over more than one variable | %[1]v 无法用两个变量遍历", rv.Type())
		}
		for v := range rv.Seq() {
			iterated = true
			stop, ierr := n.nextRef(s, nil, v)
			if ierr != nil {
				return iterated, false, ierr
			}
			if stop {
				return iterated, true, nil
			}
		}
	case reflect.Func:
		if rv.Type().CanSeq() {
			if len(n.pipe.decl) > 1 {
				return false, false, execErr("can't use %[1]v to iterate over more than one variable | %[1]v 无法用两个变量遍历", rv.Type())
			}
			for v := range rv.Seq() {
				iterated = true
				stop, ierr := n.nextRef(s, nil, v)
				if ierr != nil {
					return iterated, false, ierr
				}
				if stop {
					return iterated, true, nil
				}
			}
			return iterated, false, nil
		}
		if !rv.Type().CanSeq2() {
			return false, false, execErr("range can't iterate over %[1]v | range 无法遍历 %[1]v", rv.Type())
		}
		for i, v := range rv.Seq2() {
			iterated = true
			var stop bool
			var ierr error
			if len(n.pipe.decl) > 1 {
				stop, ierr = n.nextRef(s, elementValue(i), v)
			} else {
				stop, ierr = n.nextRef(s, nil, i)
			}
			if ierr != nil {
				return iterated, false, ierr
			}
			if stop {
				return iterated, true, nil
			}
		}
	}
	return iterated, false, nil
}

// nextRef executes one iteration over a reflect element. | nextRef 对反射元素执行一次迭代。
func (n *rangeNode) nextRef(s *execState, index any, elem reflect.Value) (bool, error) {
	if err := n.iterateRef(s, index, elem); err != nil {
		return false, err
	}
	if s.flow == flowBreak {
		s.flow = flowNone
		return true, nil
	}
	s.flow = flowNone
	return false, nil
}

// iterateRef binds loop variables and sets the dot without boxing the element.
// | iterateRef 绑定循环变量并设置 dot，且不对元素装箱。
func (n *rangeNode) iterateRef(s *execState, index any, elem reflect.Value) error {
	if len(n.pipe.decl) > 0 {
		if n.pipe.assign {
			if len(n.pipe.decl) > 1 {
				if err := s.setVar(n.pipe.decl[0], index); err != nil {
					return err
				}
				if err := s.setVarRef(n.pipe.decl[1], elem); err != nil {
					return err
				}
			} else if err := s.setVarRef(n.pipe.decl[0], elem); err != nil {
				return err
			}
		} else if len(n.pipe.decl) > 1 {
			s.setTopVar(2, index)
			s.setTopVarRef(1, elem)
		} else {
			s.setTopVarRef(1, elem)
		}
	}
	bodyMark := s.mark()
	old := s.saveDot()
	s.setDotRef(elem)
	s.flow = flowNone
	err := n.body.exec(s)
	s.restoreDot(old)
	s.pop(bodyMark)
	return err
}

// next executes one iteration and consumes its loop control. | next 执行一次迭代并消费循环控制信号。
func (n *rangeNode) next(s *execState, index, elem any) (bool, error) {
	if err := n.iterate(s, index, elem); err != nil {
		return false, err
	}
	if s.flow == flowBreak {
		s.flow = flowNone
		return true, nil
	}
	s.flow = flowNone
	return false, nil
}

// iterate binds the loop variables and executes the body once. | iterate 绑定循环变量并执行一次循环体。
func (n *rangeNode) iterate(s *execState, index, elem any) error {
	if err := n.assignVars(s, index, elem); err != nil {
		return err
	}
	bodyMark := s.mark()
	old := s.saveDot()
	s.setDot(elem)
	s.flow = flowNone
	err := n.body.exec(s)
	s.restoreDot(old)
	s.pop(bodyMark)
	return err
}

// assignVars binds the loop variables of one iteration. | assignVars 绑定一次迭代的循环变量。
func (n *rangeNode) assignVars(s *execState, index, elem any) error {
	if len(n.pipe.decl) == 0 {
		return nil
	}
	if n.pipe.assign {
		if len(n.pipe.decl) > 1 {
			if err := s.setVar(n.pipe.decl[0], index); err != nil {
				return err
			}
		} else if err := s.setVar(n.pipe.decl[0], elem); err != nil {
			return err
		}
	} else if len(n.pipe.decl) > 1 {
		s.setTopVar(2, index)
	} else {
		s.setTopVar(1, elem)
	}
	if len(n.pipe.decl) > 1 {
		if n.pipe.assign {
			if err := s.setVar(n.pipe.decl[1], elem); err != nil {
				return err
			}
		} else {
			s.setTopVar(1, elem)
		}
	}
	return nil
}

// nextMap executes one iteration over a map row without boxing its header. | nextMap 对映射行执行一次迭代，且不装箱映射头。
func (n *rangeNode) nextMap(s *execState, index int, elem map[string]any) (bool, error) {
	if err := n.iterateMap(s, index, elem); err != nil {
		return false, err
	}
	if s.flow == flowBreak {
		s.flow = flowNone
		return true, nil
	}
	s.flow = flowNone
	return false, nil
}

// iterateMap sets a map dot directly. | iterateMap 直接设置映射形式的 dot。
func (n *rangeNode) iterateMap(s *execState, index int, elem map[string]any) error {
	if len(n.pipe.decl) > 0 {
		if err := n.assignVars(s, index, elem); err != nil {
			return err
		}
	}
	bodyMark := s.mark()
	old := s.saveDot()
	s.setDotMap(elem)
	s.flow = flowNone
	err := n.body.exec(s)
	s.restoreDot(old)
	s.pop(bodyMark)
	return err
}

// elementValue unwraps an interface value produced by reflection. | elementValue 解包反射产生的接口值。
func elementValue(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		return v.Elem().Interface()
	}
	return v.Interface()
}

// sortedMapKeys returns map keys in the deterministic order text/template uses.
// | sortedMapKeys 按 text/template 的确定性顺序返回映射键。
func sortedMapKeys(rv reflect.Value) []reflect.Value {
	keys := rv.MapKeys()
	sort.SliceStable(keys, func(i, j int) bool { return lessValue(keys[i], keys[j]) })
	return keys
}

// lessValue orders two reflect values of comparable kinds. | lessValue 对可比较种类的两个反射值排序。
func lessValue(a, b reflect.Value) bool {
	ak, bk := a.Kind(), b.Kind()
	if ak == reflect.Interface {
		a, ak = a.Elem(), a.Elem().Kind()
	}
	if bk == reflect.Interface {
		b, bk = b.Elem(), b.Elem().Kind()
	}
	if ak != bk {
		return ak < bk
	}
	switch ak {
	case reflect.String:
		return a.String() < b.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return a.Int() < b.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return a.Uint() < b.Uint()
	case reflect.Float32, reflect.Float64:
		return a.Float() < b.Float()
	case reflect.Bool:
		return !a.Bool() && b.Bool()
	}
	return false
}
