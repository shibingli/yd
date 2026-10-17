package regexpx

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMustCompileAndMatch covers the basic compile and match contract. | TestMustCompileAndMatch 覆盖基本的编译与匹配约定。
func TestMustCompileAndMatch(t *testing.T) {
	re := MustCompile(`^[a-z][a-z0-9_-]*$`)
	cases := map[string]bool{
		"tenant-admin": true,
		"a":            true,
		"":             false,
		"Admin":        false,
		"a b":          false,
		"abc\n":        false, // RE2 mode: $ anchors at end of text | RE2 模式：$ 只匹配文本末尾
	}
	for in, want := range cases {
		if got := re.MatchString(in); got != want {
			t.Errorf("MatchString(%q) = %v, want %v", in, got, want)
		}
		if got := re.Match([]byte(in)); got != want {
			t.Errorf("Match(%q) = %v, want %v", in, got, want)
		}
	}
	if re.String() != `^[a-z][a-z0-9_-]*$` {
		t.Errorf("String() = %q", re.String())
	}
}

// TestCompileErrorIsSentinel verifies that syntax errors wrap ErrCompile. | TestCompileErrorIsSentinel 验证语法错误包装 ErrCompile。
func TestCompileErrorIsSentinel(t *testing.T) {
	_, err := Compile(`(unclosed`)
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("err = %v, want ErrCompile", err)
	}
}

// TestByteIndicesWithCJK proves that rune indices are converted to byte offsets. | TestByteIndicesWithCJK 证明符文下标已换算为字节偏移。
//
// "前缀" is 6 bytes and "用户" occupies bytes [6,12); a rune-based index would report [2,4] and slice
// in the middle of a character. | 「前缀」占 6 字节，「用户」占字节区间 [6,12)；若直接用符文下标
// 会得到 [2,4]，从而切在字符中间。
func TestByteIndicesWithCJK(t *testing.T) {
	text := "前缀用户后缀"
	re := MustCompile(`用户`)
	got := re.FindStringSubmatchIndex(text)
	want := []int{6, 12}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("FindStringSubmatchIndex = %v, want %v", got, want)
	}
	if s := strings.Count(text[got[0]:got[1]], "用户"); s != 1 {
		t.Fatalf("sliced %q, want 用户", text[got[0]:got[1]])
	}

	all := re.FindAllStringIndex(text, -1)
	if len(all) != 1 || all[0][0] != 6 || all[0][1] != 12 {
		t.Fatalf("FindAllStringIndex = %v", all)
	}
}

// TestSubmatchIndicesMatchStdlibSemantics pins the -1 contract and the last-capture rule. | TestSubmatchIndicesMatchStdlibSemantics 钉住 -1 约定与「最后一次捕获」规则。
func TestSubmatchIndicesMatchStdlibSemantics(t *testing.T) {
	re := MustCompile(`(a+)(b)?`)
	if got := re.FindStringSubmatchIndex("xaay"); !equalInts(got, []int{1, 3, 1, 3, -1, -1}) {
		t.Errorf("FindStringSubmatchIndex = %v", got)
	}
	rep := MustCompile(`(a)+`)
	if got := rep.FindStringSubmatchIndex("aaa"); !equalInts(got, []int{0, 3, 2, 3}) {
		t.Errorf("repeated group = %v, want [0 3 2 3]", got)
	}
	if got := rep.FindStringSubmatch("aaa"); len(got) != 2 || got[0] != "aaa" || got[1] != "a" {
		t.Errorf("FindStringSubmatch = %v", got)
	}
}

// TestFindAll covers the limit and multi-match behaviour. | TestFindAll 覆盖数量限制与多匹配行为。
func TestFindAll(t *testing.T) {
	re := MustCompile(`\d+`)
	in := "a1 b22 c333"
	idx := re.FindAllStringIndex(in, -1)
	if len(idx) != 3 || in[idx[2][0]:idx[2][1]] != "333" {
		t.Fatalf("FindAllStringIndex = %v", idx)
	}
	if idx := re.FindAllStringIndex(in, 2); len(idx) != 2 {
		t.Fatalf("limited FindAllStringIndex = %v", idx)
	}
	subs := MustCompile(`([a-z])(\d+)`).FindAllStringSubmatch(in, -1)
	if len(subs) != 3 || subs[1][0] != "b22" || subs[1][1] != "b" || subs[1][2] != "22" {
		t.Fatalf("FindAllStringSubmatch = %v", subs)
	}
	subIdx := MustCompile(`([a-z])(\d+)`).FindAllStringSubmatchIndex(in, 1)
	if len(subIdx) != 1 || !equalInts(subIdx[0], []int{0, 2, 0, 1, 1, 2}) {
		t.Fatalf("FindAllStringSubmatchIndex = %v", subIdx)
	}
	if got := re.FindString(in); got != "1" {
		t.Fatalf("FindString = %q", got)
	}
	if got := re.FindAllString(in, -1); len(got) != 3 || got[1] != "22" {
		t.Fatalf("FindAllString = %v", got)
	}
	if got := re.FindAllString(in, 1); len(got) != 1 || got[0] != "1" {
		t.Fatalf("limited FindAllString = %v", got)
	}
	if got := re.FindStringIndex(in); !equalInts(got, []int{1, 2}) {
		t.Fatalf("FindStringIndex = %v", got)
	}
	if got := MustCompile(`\d+`).ReplaceAllString(in, "#"); got != "a# b# c#" {
		t.Fatalf("ReplaceAllString = %q", got)
	}
}

// TestQuoteMeta escapes metacharacters and round-trips through a match. | TestQuoteMeta 转义元字符并通过匹配往返验证。
func TestQuoteMeta(t *testing.T) {
	raw := `a.b*c(d)[e]`
	quoted := QuoteMeta(raw)
	re := MustCompile(`^` + quoted + `$`)
	if !re.MatchString(raw) {
		t.Fatalf("quoted pattern %q does not match %q", quoted, raw)
	}
	if re.MatchString("aXb*c(d)[e]") {
		t.Fatal("quoted pattern still treats '.' as a wildcard")
	}
}

// TestMatchTimeoutFailsClosed proves that catastrophic backtracking is cut off on the regexp2
// path. | TestMatchTimeoutFailsClosed 证明 regexp2 路径上的灾难性回溯会被截断。
//
// The lookahead keeps the pattern out of the RE2 fast path; without it the standard library would
// answer in linear time and no timeout would ever fire. | 环视让该模式无法进入 RE2 快路径；
// 否则标准库会在线性时间内返回，超时永远不会触发。
func TestMatchTimeoutFailsClosed(t *testing.T) {
	re := MustCompile(`^(a+)+(?=b)`)
	if re.re2 != nil {
		t.Fatal("lookahead pattern unexpectedly compiled on the RE2 fast path")
	}
	in := strings.Repeat("a", 64) + "!"
	start := time.Now()
	if re.MatchString(in) {
		t.Fatal("hostile input matched")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("match took %v, timeout did not fire", elapsed)
	}
}

// TestEngineSelection pins which engine each pattern takes. | TestEngineSelection 钉住每种模式走哪个引擎。
func TestEngineSelection(t *testing.T) {
	if re := MustCompile(`^[a-z]+$`); re.re2 == nil {
		t.Error("ordinary pattern did not take the RE2 fast path")
	}
	if re := MustCompile(`^(?=a)a+$`); re.re2 != nil || re.re2x == nil {
		t.Error("lookahead pattern did not take the regexp2 path")
	}
}

// TestDotNetConstructsSupported proves that the compatibility path really supports .NET syntax. | TestDotNetConstructsSupported 证明兼容路径确实支持 .NET 语法。
func TestDotNetConstructsSupported(t *testing.T) {
	backref := MustCompile(`^(a+)\1$`)
	if backref.re2 != nil {
		t.Fatal("backreference pattern unexpectedly compiled on the RE2 fast path")
	}
	if !backref.MatchString("aaaa") || backref.MatchString("aaa") {
		t.Errorf("backreference match = %v/%v, want true/false", backref.MatchString("aaaa"), backref.MatchString("aaa"))
	}
}

// TestByteIndicesOnCompatibilityPath proves the rune-to-byte conversion on the regexp2 path. | TestByteIndicesOnCompatibilityPath 证明 regexp2 路径上的符文到字节换算。
func TestByteIndicesOnCompatibilityPath(t *testing.T) {
	re := MustCompile(`用户(?=后)`)
	if re.re2 != nil {
		t.Fatal("lookahead pattern unexpectedly compiled on the RE2 fast path")
	}
	text := "前缀用户后缀"
	got := re.FindStringSubmatchIndex(text)
	if len(got) != 2 || got[0] != 6 || got[1] != 12 {
		t.Fatalf("FindStringSubmatchIndex = %v, want [6 12]", got)
	}
}

// TestConcurrentUse exercises the wrapper under the race detector. | TestConcurrentUse 在竞态检测器下验证并发使用。
func TestConcurrentUse(t *testing.T) {
	re := MustCompile(`^[a-z]+:[a-z]+$`)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if !re.MatchString("user:create") || re.MatchString("bad") {
					t.Errorf("concurrent match returned the wrong result")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// equalInts reports whether a and b hold the same integers. | equalInts 报告 a 与 b 是否包含相同的整数。
func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
