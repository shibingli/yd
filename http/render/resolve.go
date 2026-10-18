package render

// Function resolution for the compiler. | 编译器的函数解析。

import (
	"html/template"
	"reflect"
	"sort"

	"github.com/shibingli/yd/core/errx"
)

// resolveFunc resolves an identifier to a compiled function reference. | resolveFunc 把标识符解析为已编译的函数引用。
func (c *compiler) resolveFunc(name string) (*funcRef, error) {
	if kind, ok := escapers[name]; ok {
		return &funcRef{name: name, kind: funcEscaper, esc: kind}, nil
	}
	if name == "_eval_args_" {
		return &funcRef{name: name, kind: funcBuiltin, builtin: builtinEvalArgs}, nil
	}
	fn, hasFn := c.funcs[name]
	if hasFn {
		rv, err := funcValue(name, fn)
		if err != nil {
			return nil, err
		}
		ref := &funcRef{name: name, kind: funcReflect, fn: rv}
		if !c.owned[name] {
			if b, ok := bindingFor(name); ok {
				ref.kind = funcBinding
				ref.binding = b
			} else if f, ok := fastFuncFor(name); ok {
				ref.kind = funcFast
				ref.fast = f
			}
		}
		return ref, nil
	}
	if b, ok := builtinFor(name); ok {
		return &funcRef{name: name, kind: funcBuiltin, builtin: b}, nil
	}
	return nil, errx.Detail(ErrParseTemplate, "function=%[1]s | 函数=%[1]s", name)
}

// funcValue validates a template function and returns its reflect value. | funcValue 校验模板函数并返回其反射值。
func funcValue(name string, fn any) (reflect.Value, error) {
	rv := reflect.ValueOf(fn)
	if !rv.IsValid() || rv.Kind() != reflect.Func {
		return reflect.Value{}, errx.Detail(ErrInvalidFunc, "function=%[1]s | 函数=%[1]s", name)
	}
	typ := rv.Type()
	switch typ.NumOut() {
	case 1:
	case 2:
		if typ.Out(1) != reflect.TypeFor[error]() {
			return reflect.Value{}, errx.Detail(ErrInvalidFunc, "function=%[1]s | 函数=%[1]s", name)
		}
	default:
		return reflect.Value{}, errx.Detail(ErrInvalidFunc, "function=%[1]s | 函数=%[1]s", name)
	}
	return rv, nil
}

// knownNames returns every template name in a parsed set, sorted. | knownNames 返回解析集合中的全部模板名，已排序。
func knownNames(set []*template.Template) []string {
	names := make([]string, 0, len(set))
	for _, t := range set {
		if t.Tree != nil {
			names = append(names, t.Name())
		}
	}
	sort.Strings(names)
	return names
}
