// Package errx provides declared sentinel errors and context-preserving wrapping. | Package errx 提供声明式哨兵错误与保留上下文的包装。
//
// The repository forbids fmt.Errorf by default. Errors are declared once at
// package level and every failure path references one of those declarations,
// which keeps messages uniform, keeps them translatable, and makes errors.Is
// usable by callers without string matching. | 本仓库默认禁止 fmt.Errorf：错误在包级
// 声明一次，所有失败路径都引用这些声明，从而保证文案统一、可翻译，并让调用方
// 无需字符串匹配即可用 errors.Is 判定。
//
// Typical usage | 典型用法：
//
//	var ErrBadPattern = errx.New("invalid pattern | 非法的路由模式")
//
//	// add a formatted detail to a declared error | 为已声明错误附加格式化细节
//	return errx.Detail(ErrBadPattern, "%q must start with /", p)
//
//	// keep context while preserving the sentinel | 保留上下文同时保留哨兵
//	return errx.Wrap(err, ErrReadBody)
package errx

import (
	"errors"
	"fmt"
)

// New declares a sentinel error. | New 声明一个哨兵错误。
//
// The message must be bilingual, with the English text first and the Chinese
// text after a " | " separator, matching the repository-wide comment rule. | 文案必须
// 中英同步：英文在前，中文在后，中间用 " | " 分隔（与全仓注释规范一致）。
func New(msg string) error { return errors.New(msg) }

// detailError carries a declared sentinel plus a formatted detail. | detailError 承载已声明的哨兵错误与格式化细节。
type detailError struct {
	sentinel error
	detail   string
}

func (e *detailError) Error() string { return e.sentinel.Error() + ": " + e.detail }

// Unwrap exposes the sentinel so errors.Is keeps working. | Unwrap 暴露哨兵错误，保证 errors.Is 仍然生效。
func (e *detailError) Unwrap() error { return e.sentinel }

// Detail attaches a formatted detail to a declared sentinel error. | Detail 为已声明的哨兵错误附加格式化细节。
//
// It replaces the common fmt.Errorf("%w: ...", ErrX, ...) idiom while keeping
// errors.Is(err, ErrX) true. | 它替代常见的 fmt.Errorf("%w: ...", ErrX, ...) 写法，
// 同时保持 errors.Is(err, ErrX) 成立。
func Detail(sentinel error, format string, args ...any) error {
	if sentinel == nil {
		return nil
	}
	return &detailError{sentinel: sentinel, detail: fmt.Sprintf(format, args...)}
}

// wrapError keeps the underlying error and the sentinel on the same chain. | wrapError 把底层错误与哨兵错误保留在同一条链上。
type wrapError struct {
	sentinel error
	err      error
	detail   string
}

func (e *wrapError) Error() string {
	if e.detail == "" {
		return e.sentinel.Error() + ": " + e.err.Error()
	}
	return e.sentinel.Error() + ": " + e.detail + ": " + e.err.Error()
}

// Unwrap returns both branches so errors.Is matches either of them. | Unwrap 返回两条分支，使 errors.Is 能匹配任意一条。
func (e *wrapError) Unwrap() []error { return []error{e.err, e.sentinel} }

// Wrap annotates err with a declared sentinel error. | Wrap 用已声明的哨兵错误注解 err。
//
// Wrap returns nil when err is nil, so it can be used directly in return
// statements. | 当 err 为 nil 时返回 nil，可直接用于 return 语句。
func Wrap(err error, sentinel error) error {
	if err == nil {
		return nil
	}
	if sentinel == nil {
		return err
	}
	return &wrapError{err: err, sentinel: sentinel}
}

// WrapDetail is Wrap plus a formatted detail. | WrapDetail 是 Wrap 的增强版，附带格式化细节。
func WrapDetail(err error, sentinel error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	if sentinel == nil {
		return err
	}
	return &wrapError{err: err, sentinel: sentinel, detail: fmt.Sprintf(format, args...)}
}

// Note on scope | 关于本包边界：
//
// errx deliberately re-exports nothing else. errors.Is/errors.As/errors.Join
// are used directly by callers: a generic As[T] wrapper would panic for any T
// that does not implement error, which is a worse failure mode than the API it
// would save. | errx 有意不再导出其它封装：调用方直接使用 errors.Is/errors.As/
// errors.Join。泛型 As[T] 封装对未实现 error 的 T 会 panic，这种失败模式比它省下的
// 那点样板代码更糟。
