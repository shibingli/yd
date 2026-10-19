package cli

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestResolveUserPath pins the platform-specific shapes that must not become a child of the
// working directory. | TestResolveUserPath 固定那些绝不能变成工作目录子路径的平台特有形态。
func TestResolveUserPath(t *testing.T) {
	base := t.TempDir()

	if got, want := resolveUserPath(base, "sub"), filepath.Join(base, "sub"); got != want {
		t.Errorf("relative = %q，期望 %q | want %q", got, want, want)
	}
	if got, want := resolveUserPath(base, filepath.Join(base, "abs")), filepath.Join(base, "abs"); got != want {
		t.Errorf("absolute = %q，期望 %q | want %q", got, want, want)
	}
	if got := resolveUserPath(base, ""); got != "" {
		t.Errorf("empty = %q，期望空串 | want empty", got)
	}

	if runtime.GOOS != "windows" {
		// On POSIX a colon or a leading backslash is an ordinary file-name character, so these
		// must stay relative to the working directory. | POSIX 上冒号或开头的反斜杠是普通文件名字符，
		// 因此它们必须保持相对工作目录。
		for _, name := range []string{"C:proj", `\rooted`} {
			if got, want := resolveUserPath(base, name), filepath.Join(base, name); got != want {
				t.Errorf("%q = %q，期望 %q | want %q", name, got, want, want)
			}
		}
		return
	}

	// Drive-relative ("C:proj") must be resolved by Windows, not joined as a child containing a
	// colon. Root-relative ("/rooted") must become an absolute path on the current drive. |
	// 盘符相对路径（"C:proj"）必须交给 Windows 解析，而不是拼成含冒号的子路径；根相对路径
	// （"/rooted"）必须变成当前驱动器上的绝对路径。
	if got := resolveUserPath(base, `C:proj`); filepath.VolumeName(got) != "C:" || strings.HasPrefix(got, base) {
		t.Errorf("drive-relative = %q，期望位于 C: 且不是工作目录子路径 | want C: and not below workDir", got)
	}
	if got := resolveUserPath(base, `/rooted`); !filepath.IsAbs(got) {
		t.Errorf("root-relative = %q，期望绝对路径 | want an absolute path", got)
	}
}
