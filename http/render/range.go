package render

// Range execution and deterministic iteration helpers. | range 执行与确定性遍历辅助。

import (
	"reflect"
	"sort"
)

// exec implements range. | exec 实现 range。
func (n *rangeNode) exec(s *execState) error {
	mark := s.mark()
	defer s.pop(mark)
	val, err := n.pipe.eval(s)
	if err != nil {
		return err
	}
	iterated := false

	// Fast paths for the two shapes the framework data uses most. | 框架数据最常用的两种形状走快路径。
	switch items := val.(type) {
	case []any:
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
	case []string:
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
	case map[string]any:
		keys := make([]string, 0, len(items))
		for k := range items {
			keys = append(keys, k)
		}
		sort.Strings(keys)
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

	rv := reflect.ValueOf(val)
	switch rv.Kind() {
	case reflect.Invalid:
		// nil behaves as an empty collection. | nil 视为空集合。
	case reflect.Array, reflect.Slice:
		for i := 0; i < rv.Len(); i++ {
			iterated = true
			stop, err := n.next(s, i, elementValue(rv.Index(i)))
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
			stop, err := n.next(s, elementValue(k), elementValue(rv.MapIndex(k)))
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
			stop, err := n.next(s, i, elementValue(elem))
			if err != nil {
				return err
			}
			if stop {
				return nil
			}
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if len(n.pipe.decl) > 1 {
			return execErr("can't use %[1]v to iterate over more than one variable | %[1]v 无法用两个变量遍历", rv.Type())
		}
		for v := range rv.Seq() {
			iterated = true
			stop, err := n.next(s, nil, elementValue(v))
			if err != nil {
				return err
			}
			if stop {
				return nil
			}
		}
	case reflect.Func:
		if rv.Type().CanSeq() {
			if len(n.pipe.decl) > 1 {
				return execErr("can't use %[1]v to iterate over more than one variable | %[1]v 无法用两个变量遍历", rv.Type())
			}
			for v := range rv.Seq() {
				iterated = true
				stop, err := n.next(s, nil, elementValue(v))
				if err != nil {
					return err
				}
				if stop {
					return nil
				}
			}
		} else if rv.Type().CanSeq2() {
			for i, v := range rv.Seq2() {
				iterated = true
				var stop bool
				var err error
				if len(n.pipe.decl) > 1 {
					stop, err = n.next(s, elementValue(i), elementValue(v))
				} else {
					stop, err = n.next(s, nil, elementValue(i))
				}
				if err != nil {
					return err
				}
				if stop {
					return nil
				}
			}
		} else {
			return execErr("range can't iterate over %[1]v | range 无法遍历 %[1]v", rv.Type())
		}
	default:
		return execErr("range can't iterate over %[1]v | range 无法遍历 %[1]v", rv.Type())
	}

	if !iterated && n.elseBody != nil {
		return n.elseBody.exec(s)
	}
	return nil
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
	if len(n.pipe.decl) > 0 {
		if n.pipe.assign {
			if len(n.pipe.decl) > 1 {
				if err := s.setVar(n.pipe.decl[0], index); err != nil {
					return err
				}
			} else {
				if err := s.setVar(n.pipe.decl[0], elem); err != nil {
					return err
				}
			}
		} else if len(n.pipe.decl) > 1 {
			s.setTopVar(2, index)
		} else {
			s.setTopVar(1, elem)
		}
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
	bodyMark := s.mark()
	old := s.dot
	s.dot = elem
	s.flow = flowNone
	err := n.body.exec(s)
	s.dot = old
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
