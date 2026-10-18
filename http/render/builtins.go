package render

// Builtin template functions. | 内建模板函数。
//
// html/template leaves text/template's builtins available to every template:
// comparison, indexing, slicing, formatting and the legacy html/js/urlquery
// escapers. They are implemented here with the same observable behaviour so a
// template that works under the standard library works unchanged. | html/template
// 让每个模板都能使用 text/template 的内建函数：比较、索引、切片、格式化以及旧的
// html/js/urlquery 转义器。这里以相同的外部行为实现它们，使标准库下可用的模板无需
// 修改即可运行。

import (
	"fmt"
	"html/template"
	"reflect"
)

// builtinID identifies a builtin. | builtinID 标识一个内建函数。
type builtinID uint8

const (
	builtinAnd builtinID = iota
	builtinOr
	builtinNot
	builtinLen
	builtinIndex
	builtinSlice
	builtinPrint
	builtinPrintf
	builtinPrintln
	builtinHTML
	builtinJS
	builtinURLQuery
	builtinEq
	builtinNe
	builtinLt
	builtinLe
	builtinGt
	builtinGe
	builtinCall
	builtinEvalArgs
)

// builtinFor resolves a builtin name. | builtinFor 解析内建函数名。
func builtinFor(name string) (builtinID, bool) {
	switch name {
	case "and":
		return builtinAnd, true
	case "or":
		return builtinOr, true
	case "not":
		return builtinNot, true
	case "len":
		return builtinLen, true
	case "index":
		return builtinIndex, true
	case "slice":
		return builtinSlice, true
	case "print":
		return builtinPrint, true
	case "printf":
		return builtinPrintf, true
	case "println":
		return builtinPrintln, true
	case "html":
		return builtinHTML, true
	case "js":
		return builtinJS, true
	case "urlquery":
		return builtinURLQuery, true
	case "eq":
		return builtinEq, true
	case "ne":
		return builtinNe, true
	case "lt":
		return builtinLt, true
	case "le":
		return builtinLe, true
	case "gt":
		return builtinGt, true
	case "ge":
		return builtinGe, true
	case "call":
		return builtinCall, true
	case "_eval_args_":
		return builtinEvalArgs, true
	}
	return 0, false
}

// callBuiltin invokes a builtin. | callBuiltin 调用内建函数。
func callBuiltin(_ *execState, id builtinID, args []any) (any, error) {
	switch id {
	case builtinAnd:
		return evalAndOrValues(args, false), nil
	case builtinOr:
		return evalAndOrValues(args, true), nil
	case builtinNot:
		if len(args) != 1 {
			return nil, execErr("not requires exactly one argument | not 需要一个参数")
		}
		return !truthy(args[0]), nil
	case builtinLen:
		if len(args) != 1 {
			return nil, execErr("len requires exactly one argument | len 需要一个参数")
		}
		return builtinLenOf(args[0])
	case builtinIndex:
		return builtinIndexValues(args)
	case builtinSlice:
		return builtinSliceValues(args)
	case builtinPrint:
		return fmt.Sprint(args...), nil
	case builtinPrintf:
		if len(args) == 0 {
			return nil, execErr("printf requires a format | printf 需要格式串")
		}
		format, ok := args[0].(string)
		if !ok {
			return nil, execErr("printf format must be a string | printf 格式串必须是字符串")
		}
		return fmt.Sprintf(format, args[1:]...), nil
	case builtinPrintln:
		return fmt.Sprintln(args...), nil
	case builtinHTML:
		return template.HTMLEscaper(args...), nil
	case builtinJS:
		return template.JSEscaper(args...), nil
	case builtinURLQuery:
		return template.URLQueryEscaper(args...), nil
	case builtinEq:
		return builtinEqual(args)
	case builtinNe:
		if len(args) != 2 {
			return nil, execErr("ne requires exactly two arguments | ne 需要两个参数")
		}
		ok, err := builtinEqual(args)
		return !ok, err
	case builtinLt, builtinLe, builtinGt, builtinGe:
		return builtinOrder(id, args)
	case builtinCall:
		return builtinCallValue(args)
	case builtinEvalArgs:
		return evalArgsSlice(append([]any(nil), args...)), nil
	}
	return nil, execErr("unknown builtin | 未知内建函数")
}

// evalAndOrValues implements and/or for already evaluated arguments. | evalAndOrValues 对已求值的参数实现 and/or。
func evalAndOrValues(args []any, isOr bool) any {
	if len(args) == 0 {
		return nil
	}
	for _, v := range args {
		if truthy(v) == isOr {
			return v
		}
	}
	return args[len(args)-1]
}

// builtinLenOf implements len. | builtinLenOf 实现 len。
func builtinLenOf(v any) (any, error) {
	if isNilMarker(v) {
		return nil, execErr("len of untyped nil | 无类型 nil 没有长度")
	}
	rv, _ := derefValue(v)
	if !rv.IsValid() {
		return nil, execErr("len of untyped nil | 无类型 nil 没有长度")
	}
	switch rv.Kind() {
	case reflect.Array, reflect.Chan, reflect.Map, reflect.Slice, reflect.String:
		return rv.Len(), nil
	}
	return nil, execErr("len of type %[1]s | 类型 %[1]s 没有长度", rv.Type())
}

// derefValue unwraps interfaces and pointers. | derefValue 解包接口与指针。
func derefValue(v any) (reflect.Value, bool) {
	rv := reflect.ValueOf(v)
	nilSeen := false
	for rv.IsValid() && (rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer) {
		if rv.IsNil() {
			nilSeen = true
			break
		}
		rv = rv.Elem()
	}
	return rv, nilSeen
}

// builtinIndexValues implements index. | builtinIndexValues 实现 index。
func builtinIndexValues(args []any) (any, error) {
	if len(args) < 2 {
		return nil, execErr("index requires an item and at least one index | index 需要被索引值与至少一个下标")
	}
	item, _ := derefValue(args[0])
	if !item.IsValid() {
		return nil, execErr("index of untyped nil | 对无类型 nil 取下标")
	}
	for _, raw := range args[1:] {
		idx, _ := derefValue(raw)
		if item.Kind() == reflect.Pointer {
			if item.IsNil() {
				return nil, execErr("index of nil pointer | 对 nil 指针取下标")
			}
			item = item.Elem()
		}
		switch item.Kind() {
		case reflect.Array, reflect.Slice, reflect.String:
			n, err := indexNumber(idx, item.Len())
			if err != nil {
				return nil, err
			}
			item = item.Index(n)
		case reflect.Map:
			key, err := prepareArg(elementValue(idx), item.Type().Key())
			if err != nil {
				return nil, err
			}
			next := item.MapIndex(key)
			if !next.IsValid() {
				item = reflect.Zero(item.Type().Elem())
			} else {
				item = next
			}
		default:
			return nil, execErr("can't index item of type %[1]s | 无法对类型 %[1]s 取下标", item.Type())
		}
	}
	return elementValue(item), nil
}

// indexNumber converts an index value to an int within [0, cap]. | indexNumber 把下标值转为 [0, cap] 内的 int。
func indexNumber(v reflect.Value, capacity int) (int, error) {
	var x int64
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		x = v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		x = int64(v.Uint())
	case reflect.Invalid:
		return 0, execErr("cannot index with nil | 不能用 nil 取下标")
	default:
		return 0, execErr("cannot index with type %[1]s | 不能用类型 %[1]s 取下标", v.Type())
	}
	if x < 0 || int(x) < 0 || int(x) > capacity {
		return 0, execErr("index out of range: %[1]d | 下标越界：%[1]d", x)
	}
	return int(x), nil
}

// builtinSliceValues implements slice. | builtinSliceValues 实现 slice。
func builtinSliceValues(args []any) (any, error) {
	if len(args) == 0 {
		return nil, execErr("slice requires an item | slice 需要被切片值")
	}
	item, _ := derefValue(args[0])
	if !item.IsValid() {
		return nil, execErr("slice of untyped nil | 对无类型 nil 切片")
	}
	indexes := args[1:]
	if len(indexes) > 3 {
		return nil, execErr("too many slice indexes: %[1]d | 切片下标过多：%[1]d", len(indexes))
	}
	var capacity int
	switch item.Kind() {
	case reflect.String:
		if len(indexes) == 3 {
			return nil, execErr("cannot 3-index slice a string | 字符串不支持三下标切片")
		}
		capacity = item.Len()
	case reflect.Array, reflect.Slice:
		capacity = item.Cap()
	default:
		return nil, execErr("can't slice item of type %[1]s | 无法对类型 %[1]s 切片", item.Type())
	}
	idx := [3]int{0, item.Len()}
	for i, raw := range indexes {
		v, _ := derefValue(raw)
		n, err := indexNumber(v, capacity)
		if err != nil {
			return nil, err
		}
		idx[i] = n
	}
	if idx[0] > idx[1] {
		return nil, execErr("invalid slice index: %[1]d > %[2]d | 非法切片下标：%[1]d > %[2]d", idx[0], idx[1])
	}
	if len(indexes) < 3 {
		return elementValue(item.Slice(idx[0], idx[1])), nil
	}
	if idx[1] > idx[2] {
		return nil, execErr("invalid slice index: %[1]d > %[2]d | 非法切片下标：%[1]d > %[2]d", idx[1], idx[2])
	}
	return elementValue(item.Slice3(idx[0], idx[1], idx[2])), nil
}

// builtinCallValue implements call. | builtinCallValue 实现 call。
func builtinCallValue(args []any) (any, error) {
	if len(args) == 0 {
		return nil, execErr("call requires a function | call 需要函数")
	}
	fn := reflect.ValueOf(args[0])
	if !fn.IsValid() || fn.Kind() != reflect.Func {
		return nil, execErr("call of non-function %[1]T | call 的目标不是函数：%[1]T", args[0])
	}
	return callReflect(fn, args[1:])
}

// Kind classes used by comparisons. | 比较使用的种类分组。
type compareKind uint8

const (
	compareInvalid compareKind = iota
	compareBool
	compareComplex
	compareInt
	compareFloat
	compareString
	compareUint
)

// compareKindOf classifies a value for comparison. | compareKindOf 为比较归类一个值。
func compareKindOf(v any) compareKind {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return compareInvalid
	}
	switch rv.Kind() {
	case reflect.Bool:
		return compareBool
	case reflect.Complex64, reflect.Complex128:
		return compareComplex
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return compareInt
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return compareUint
	case reflect.Float32, reflect.Float64:
		return compareFloat
	case reflect.String:
		return compareString
	}
	return compareInvalid
}

// builtinEqual implements eq/ne. | builtinEqual 实现 eq/ne。
func builtinEqual(args []any) (bool, error) {
	if len(args) < 2 {
		return false, execErr("comparison requires at least two arguments | 比较至少需要两个参数")
	}
	a := args[0]
	for _, b := range args[1:] {
		ok, err := equalPair(a, b)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// equalPair compares two values. | equalPair 比较两个值。
func equalPair(a, b any) (bool, error) {
	ka, kb := compareKindOf(a), compareKindOf(b)
	if ka == compareInvalid || kb == compareInvalid {
		an := a == nil || isNilMarker(a)
		bn := b == nil || isNilMarker(b)
		return an && bn, nil
	}
	if ka != kb {
		switch {
		case ka == compareInt && kb == compareUint:
			ai := reflect.ValueOf(a).Int()
			return ai >= 0 && uint64(ai) == reflect.ValueOf(b).Uint(), nil
		case ka == compareUint && kb == compareInt:
			bi := reflect.ValueOf(b).Int()
			return bi >= 0 && reflect.ValueOf(a).Uint() == uint64(bi), nil
		}
		return false, execErr("incompatible types for comparison | 比较的类型不兼容")
	}
	switch ka {
	case compareBool:
		return a.(bool) == b.(bool), nil
	case compareComplex:
		return reflect.ValueOf(a).Complex() == reflect.ValueOf(b).Complex(), nil
	case compareInt:
		return reflect.ValueOf(a).Int() == reflect.ValueOf(b).Int(), nil
	case compareUint:
		return reflect.ValueOf(a).Uint() == reflect.ValueOf(b).Uint(), nil
	case compareFloat:
		return reflect.ValueOf(a).Float() == reflect.ValueOf(b).Float(), nil
	case compareString:
		return a.(string) == b.(string), nil
	}
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	if ta != tb {
		return false, execErr("incompatible types for comparison | 比较的类型不兼容")
	}
	if !ta.Comparable() {
		return false, execErr("non-comparable type %[1]s | 类型 %[1]s 不可比较", ta)
	}
	return a == b, nil
}

// builtinOrder implements lt/le/gt/ge. | builtinOrder 实现 lt/le/gt/ge。
func builtinOrder(id builtinID, args []any) (bool, error) {
	if len(args) != 2 {
		return false, execErr("comparison requires two arguments | 比较需要两个参数")
	}
	less, err := lessPair(args[0], args[1])
	if err != nil {
		return false, err
	}
	switch id {
	case builtinLt:
		return less, nil
	case builtinLe:
		if less {
			return true, nil
		}
		return equalPair(args[0], args[1])
	case builtinGt:
		le, err := builtinOrder(builtinLe, args)
		return !le, err
	case builtinGe:
		return !less, nil
	}
	return false, nil
}

// lessPair reports whether a < b under text/template's ordering. | lessPair 报告在 text/template 排序下 a < b。
func lessPair(a, b any) (bool, error) {
	ka, kb := compareKindOf(a), compareKindOf(b)
	if ka == compareInvalid || kb == compareInvalid {
		return false, execErr("invalid type for comparison | 比较的类型非法")
	}
	if ka != kb {
		switch {
		case ka == compareInt && kb == compareUint:
			ai := reflect.ValueOf(a).Int()
			return ai < 0 || uint64(ai) < reflect.ValueOf(b).Uint(), nil
		case ka == compareUint && kb == compareInt:
			bi := reflect.ValueOf(b).Int()
			return bi >= 0 && reflect.ValueOf(a).Uint() < uint64(bi), nil
		}
		return false, execErr("incompatible types for comparison | 比较的类型不兼容")
	}
	switch ka {
	case compareBool, compareComplex:
		return false, execErr("invalid type for comparison | 比较的类型非法")
	case compareFloat:
		return reflect.ValueOf(a).Float() < reflect.ValueOf(b).Float(), nil
	case compareInt:
		return reflect.ValueOf(a).Int() < reflect.ValueOf(b).Int(), nil
	case compareString:
		return a.(string) < b.(string), nil
	case compareUint:
		return reflect.ValueOf(a).Uint() < reflect.ValueOf(b).Uint(), nil
	}
	return false, nil
}
