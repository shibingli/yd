//go:build amd64 && !jsonx_std

package jsonx

import (
	"reflect"
	"sync"
)

// estimateCache caches the JSON size estimate for each type. | estimateCache 缓存每个类型的 JSON
// 体积估算值。
var estimateCache sync.Map

// estimateTypeSize returns a rough encoded size for one value of t. | estimateTypeSize 返回 t 的
// 单个值的大致编码体积。
func estimateTypeSize(t reflect.Type) int {
	if v, ok := estimateCache.Load(t); ok {
		return v.(int)
	}
	estimateCache.Store(t, 16)
	n := computeTypeSize(t)
	estimateCache.Store(t, n)
	return n
}

func computeTypeSize(t reflect.Type) int {
	if t == timeType {
		return 24
	}
	switch t.Kind() {
	case reflect.Bool:
		return 5
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return 12
	case reflect.Float32, reflect.Float64:
		return 24
	case reflect.String:
		return 8
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return 8
		}
		return 2 + 3*estimateTypeSize(t.Elem())
	case reflect.Array:
		return 2 + t.Len()*estimateTypeSize(t.Elem())
	case reflect.Map:
		return 24
	case reflect.Struct:
		fields := cachedStruct(t).fields
		n := 2
		for _, f := range fields {
			n += len(f.name) + 3 + estimateTypeSize(f.typ)
		}
		return n
	case reflect.Pointer, reflect.Interface:
		return 8
	}
	return 8
}

// estimateValueSize returns a rough encoded size for v. | estimateValueSize 返回 v 的大致编码体积。
func estimateValueSize(v reflect.Value) int {
	if !v.IsValid() {
		return 4
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return 4
		}
		return estimateValueSize(v.Elem())
	case reflect.String:
		return v.Len() + 2
	case reflect.Slice:
		if v.IsNil() {
			return 4
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return (v.Len()+2)/3*4 + 2
		}
		return 2 + v.Len()*estimateTypeSize(v.Type().Elem())
	case reflect.Array:
		return 2 + v.Len()*estimateTypeSize(v.Type().Elem())
	case reflect.Map:
		if v.IsNil() {
			return 4
		}
		return 2 + v.Len()*24
	}
	return estimateTypeSize(v.Type())
}
