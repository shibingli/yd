package render

// Function invocation. | 函数调用。
//
// Default helpers get typed fast paths because the framework templates call
// tr/dict/default hundreds of times per page; arbitrary user functions and
// methods go through reflection with the same argument-coercion rules as
// text/template, including panic-to-error conversion. | 默认辅助函数走类型化快路径，
// 因为框架模板每页会调用 tr/dict/default 数百次；任意用户函数与方法走反射，并沿用
// text/template 的参数转换规则，包括把 panic 转成错误。

import (
	"reflect"
	"strings"
	"time"
)

// callReflect invokes fn with text/template's coercion rules. | callReflect 按 text/template 的转换规则调用 fn。
func callReflect(fn reflect.Value, args []any) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			result, err = nil, execErr("error calling function: %[1]v | 调用函数出错：%[1]v", r)
		}
	}()
	if !fn.IsValid() || fn.Kind() != reflect.Func {
		return nil, execErr("value is not a function | 值不是函数")
	}
	typ := fn.Type()
	numIn := len(args)
	if typ.IsVariadic() {
		if numIn < typ.NumIn()-1 {
			return nil, execErr("wrong number of args: want at least %[1]d got %[2]d | 参数个数错误：至少 %[1]d 个，实际 %[2]d 个", typ.NumIn()-1, numIn)
		}
	} else if numIn != typ.NumIn() {
		return nil, execErr("wrong number of args: want %[1]d got %[2]d | 参数个数错误：需要 %[1]d 个，实际 %[2]d 个", typ.NumIn(), numIn)
	}
	argv := make([]reflect.Value, numIn)
	for i, a := range args {
		var want reflect.Type
		if typ.IsVariadic() && i >= typ.NumIn()-1 {
			want = typ.In(typ.NumIn() - 1).Elem()
		} else {
			want = typ.In(i)
		}
		rv, cerr := prepareArg(a, want)
		if cerr != nil {
			return nil, cerr
		}
		argv[i] = rv
	}
	out := fn.Call(argv)
	if len(out) == 2 {
		if !out[1].IsNil() {
			return nil, out[1].Interface().(error)
		}
		return out[0].Interface(), nil
	}
	return out[0].Interface(), nil
}

// prepareArg converts v to want with the widening text/template permits. | prepareArg 按 text/template 允许的拓宽规则把 v 转为 want。
func prepareArg(v any, want reflect.Type) (reflect.Value, error) {
	if v == nil || isNilMarker(v) {
		if canBeNil(want) {
			return reflect.Zero(want), nil
		}
		return reflect.Value{}, execErr("value is nil, expected %[1]s | 值为 nil，期望 %[1]s", want)
	}
	rv := reflect.ValueOf(v)
	if rv.Type().AssignableTo(want) {
		return rv, nil
	}
	if intLike(rv.Kind()) && intLike(want.Kind()) && rv.Type().ConvertibleTo(want) {
		return rv.Convert(want), nil
	}
	return reflect.Value{}, execErr("value has type %[1]s, expected %[2]s | 值类型 %[1]s，期望 %[2]s", rv.Type(), want)
}

// canBeNil reports whether nil can be assigned to typ. | canBeNil 报告 nil 能否赋给 typ。
func canBeNil(typ reflect.Type) bool {
	switch typ.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	}
	return false
}

// intLike reports whether kind is an integer kind. | intLike 报告 kind 是否为整数种类。
func intLike(kind reflect.Kind) bool {
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	}
	return false
}

// callFast invokes a default helper through its typed path, falling back to
// reflection when the argument shapes do not match. | callFast 通过类型化路径调用默认辅助函数；
// 参数形状不匹配时退回反射。
func callFast(s *execState, ref *funcRef, args []any) (any, error) {
	switch ref.fast {
	case fastDict:
		return dict(args...)
	case fastList:
		// Copy: args points into the execution scratch stack, which is reused
		// once this call returns. | 必须复制：args 指向执行暂存栈，调用返回后会被复用。
		out := make([]any, len(args))
		copy(out, args)
		return out, nil
	case fastDefault:
		if len(args) != 2 {
			return callReflect(ref.fn, args)
		}
		return dfault(args[0], args[1]), nil
	case fastContains:
		a, ok1 := stringArg(args, 0)
		b, ok2 := stringArg(args, 1)
		if !ok1 || !ok2 || len(args) != 2 {
			return callReflect(ref.fn, args)
		}
		return strings.Contains(a, b), nil
	case fastHasPrefix:
		a, ok1 := stringArg(args, 0)
		b, ok2 := stringArg(args, 1)
		if !ok1 || !ok2 || len(args) != 2 {
			return callReflect(ref.fn, args)
		}
		return strings.HasPrefix(a, b), nil
	case fastHasSuffix:
		a, ok1 := stringArg(args, 0)
		b, ok2 := stringArg(args, 1)
		if !ok1 || !ok2 || len(args) != 2 {
			return callReflect(ref.fn, args)
		}
		return strings.HasSuffix(a, b), nil
	case fastLower:
		a, ok := stringArg(args, 0)
		if !ok || len(args) != 1 {
			return callReflect(ref.fn, args)
		}
		return strings.ToLower(a), nil
	case fastUpper:
		a, ok := stringArg(args, 0)
		if !ok || len(args) != 1 {
			return callReflect(ref.fn, args)
		}
		return strings.ToUpper(a), nil
	case fastTrim:
		a, ok := stringArg(args, 0)
		if !ok || len(args) != 1 {
			return callReflect(ref.fn, args)
		}
		return strings.TrimSpace(a), nil
	case fastJoin:
		if len(args) != 2 {
			return callReflect(ref.fn, args)
		}
		sep, ok := args[1].(string)
		if !ok {
			return callReflect(ref.fn, args)
		}
		switch elems := args[0].(type) {
		case []string:
			return strings.Join(elems, sep), nil
		case []any:
			parts := make([]string, len(elems))
			for i, e := range elems {
				parts[i], ok = e.(string)
				if !ok {
					return callReflect(ref.fn, args)
				}
			}
			return strings.Join(parts, sep), nil
		}
		return callReflect(ref.fn, args)
	case fastJSON:
		if len(args) != 1 {
			return callReflect(ref.fn, args)
		}
		return toJSON(args[0])
	case fastHumanSize:
		n, ok := int64Arg(args, 0)
		if !ok || len(args) != 1 {
			return callReflect(ref.fn, args)
		}
		return humanSize(n), nil
	case fastHumanTime:
		if len(args) != 1 {
			return callReflect(ref.fn, args)
		}
		t, ok := args[0].(time.Time)
		if !ok {
			return callReflect(ref.fn, args)
		}
		return humanTime(t), nil
	case fastHx:
		return htmxAttrs(args...)
	case fastAsset:
		return callReflect(ref.fn, args)
	}
	return callReflect(ref.fn, args)
}
