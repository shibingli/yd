package yd_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/shibingli/yd/security/guomi"
	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/router"
)

// ErrUnknownField is a declared sentinel error. | ErrUnknownField 是一个已声明的哨兵错误。
//
// Errors are declared once per package and compared with errors.Is, never by
// matching their text — which also means the bilingual wording may change
// without breaking callers. | 错误在每个包中声明一次，并用 errors.Is 比较而非匹配文案；
// 这也意味着中英文案的调整不会破坏调用方。
var ErrUnknownField = errs.New("unknown field | 未知字段")

// Example_errxSentinel demonstrates declared errors with extra context. | Example_errxSentinel 演示带上下文的声明式错误。
func Example_errxSentinel() {
	err := errs.Detail(ErrUnknownField, "field %q", "nickname")

	fmt.Println(errors.Is(err, ErrUnknownField))
	fmt.Println(err)
	// Output:
	// true
	// unknown field | 未知字段: field "nickname"
}

// Example_routerMixedSegment demonstrates the headline routing feature: one
// path segment may mix a parameter with static text, and the static suffix is
// stripped from the captured value. | Example_routerMixedSegment 演示路由的核心特性：
// 单个路径段可以混合参数与静态文本，捕获值会自动剥离静态后缀。
func Example_routerMixedSegment() {
	r := router.New()
	r.GET("/report/:id-xx.png", func(c *router.Ctx) {
		_, _ = fmt.Fprintf(c.Writer, "id=%s", c.Param("id"))
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/report/42-xx.png", nil))
	fmt.Println(rec.Body.String())

	// A path that does not carry the static suffix falls through to 404. | 不带静态后缀的路径会落到 404。
	miss := httptest.NewRecorder()
	r.ServeHTTP(miss, httptest.NewRequest(http.MethodGet, "/report/42-yy.png", nil))
	fmt.Println(miss.Code)

	// Output:
	// id=42
	// 404
}

// Example_guomiPassword demonstrates salted SM3 password hashing. | Example_guomiPassword 演示加盐 SM3 口令散列。
//
// The encoded string carries the iteration count and the salt, so verification
// needs nothing but the stored value. | 编码串自带迭代次数与盐，校验只需这一个已存值。
func Example_guomiPassword() {
	encoded, err := guomi.HashPassword("correct horse", guomi.PasswordOptions{Iterations: 1000})
	if err != nil {
		fmt.Println("hash error")
		return
	}

	ok, _ := guomi.VerifyPassword(encoded, "correct horse")
	bad, _ := guomi.VerifyPassword(encoded, "wrong horse")
	fmt.Println(ok, bad)

	// Output:
	// true false
}
