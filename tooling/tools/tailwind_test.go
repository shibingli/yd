package tools

import (
	"errors"
	"os"
	"testing"
)

// TestTailwindAssetName pins the release asset names of every supported platform. | TestTailwindAssetName
// 固定每个受支持平台的发布资产名。
func TestTailwindAssetName(t *testing.T) {
	musl := ""
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		musl = "-musl"
	}
	for _, tc := range []struct {
		goos, goarch, want string
	}{
		{"windows", "amd64", "tailwindcss-windows-x64.exe"},
		{"windows", "arm64", "tailwindcss-windows-arm64.exe"},
		{"darwin", "amd64", "tailwindcss-macos-x64"},
		{"darwin", "arm64", "tailwindcss-macos-arm64"},
		{"linux", "amd64", "tailwindcss-linux-x64" + musl},
		{"linux", "arm64", "tailwindcss-linux-arm64" + musl},
	} {
		got, err := tailwindAssetName(tc.goos, tc.goarch)
		if err != nil {
			t.Errorf("%s/%s returned an error: %v | 返回错误", tc.goos, tc.goarch, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s/%s = %q，期望 %q | want %q", tc.goos, tc.goarch, got, tc.want, tc.want)
		}
	}
	if _, err := tailwindAssetName("freebsd", "amd64"); !errors.Is(err, ErrNotInstallable) {
		t.Fatalf("unsupported platform error = %v，期望 ErrNotInstallable | want ErrNotInstallable", err)
	}
}
