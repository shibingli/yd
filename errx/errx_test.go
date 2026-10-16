package errx_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yun-dev/yd/errx"
)

var (
	errSentinel = errx.New("invalid pattern | 非法的路由模式")
	errOther    = errx.New("other failure | 其它失败")
	errInner    = errors.New("inner cause")
)

func TestNewIsMatchableByErrorsIs(t *testing.T) {
	if !errors.Is(errSentinel, errSentinel) {
		t.Fatal("errors.Is 应能匹配哨兵自身")
	}
	if errors.Is(errSentinel, errOther) {
		t.Fatal("不同哨兵之间不应互相匹配")
	}
	if !strings.Contains(errSentinel.Error(), " | ") {
		t.Fatalf("错误文案必须中英同步，实际 %q", errSentinel.Error())
	}
}

func TestDetailKeepsSentinelAndRendersDetail(t *testing.T) {
	err := errx.Detail(errSentinel, "%q must start with /", "/x")
	if !errors.Is(err, errSentinel) {
		t.Fatalf("Detail 结果必须能匹配哨兵: %v", err)
	}
	if errors.Is(err, errOther) {
		t.Fatal("Detail 结果不应匹配其它哨兵")
	}
	msg := err.Error()
	if !strings.Contains(msg, errSentinel.Error()) || !strings.Contains(msg, `"/x"`) {
		t.Fatalf("文案应同时包含哨兵与细节，实际 %q", msg)
	}
}

func TestWrapPreservesBothBranches(t *testing.T) {
	err := errx.Wrap(errInner, errSentinel)
	if !errors.Is(err, errSentinel) {
		t.Fatalf("Wrap 结果应能匹配哨兵: %v", err)
	}
	if !errors.Is(err, errInner) {
		t.Fatalf("Wrap 结果应保留底层错误: %v", err)
	}
	if !strings.Contains(err.Error(), "inner cause") {
		t.Fatalf("文案应包含底层错误: %q", err.Error())
	}
}

func TestWrapNilReturnsNil(t *testing.T) {
	if err := errx.Wrap(nil, errSentinel); err != nil {
		t.Fatalf("nil 输入应返回 nil，实际 %v", err)
	}
	if err := errx.WrapDetail(nil, errSentinel, "x=%d", 1); err != nil {
		t.Fatalf("nil 输入应返回 nil，实际 %v", err)
	}
	if err := errx.Detail(nil, "x"); err != nil {
		t.Fatalf("nil 哨兵应返回 nil，实际 %v", err)
	}
}

func TestWrapDetailIncludesAllThreeParts(t *testing.T) {
	err := errx.WrapDetail(errInner, errSentinel, "record %d", 42)
	msg := err.Error()
	for _, want := range []string{errSentinel.Error(), "record 42", "inner cause"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("文案缺少 %q: %q", want, msg)
		}
	}
	if !errors.Is(err, errSentinel) || !errors.Is(err, errInner) {
		t.Fatalf("两条分支都必须可匹配: %v", err)
	}
}

// customErr 是用于验证 errors.As 的自定义错误类型。
// customErr is a custom error type used to exercise errors.As.
type customErr struct{ code int }

func (e *customErr) Error() string { return fmt.Sprintf("custom error, code=%d", e.code) }

// TestWorksWithStdlibErrorsHelpers 验证 errx 构造的错误可与标准库 errors 助手直接配合。
// TestWorksWithStdlibErrorsHelpers verifies errx errors interoperate with the stdlib helpers.
func TestWorksWithStdlibErrorsHelpers(t *testing.T) {
	err := errx.WrapDetail(&customErr{code: 7}, errSentinel, "record %d", 42)

	var target *customErr
	if !errors.As(err, &target) || target.code != 7 {
		t.Fatalf("errors.As 未能提取自定义类型: %v", err)
	}
	joined := errors.Join(err, errOther)
	if !errors.Is(joined, errSentinel) || !errors.Is(joined, errOther) {
		t.Fatalf("errors.Join 结果应匹配全部分支: %v", joined)
	}
	if !strings.Contains(err.Error(), " | ") {
		t.Fatal("错误文案必须中英同步")
	}
}
