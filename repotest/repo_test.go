// Package repotest 校验仓库治理文件是否满足需求设计说明书的硬性约束。
package repotest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot 返回仓库根目录（本测试位于 <root>/repotest）。
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root, err := filepath.Abs(filepath.Join(wd, ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return root
}

func readRepoFile(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("读取 %s: %v", name, err)
	}
	return string(b)
}

func TestGovernanceFiles(t *testing.T) {
	root := repoRoot(t)

	t.Run("LICENSE 为完整 Apache-2.0", func(t *testing.T) {
		lic := readRepoFile(t, root, "LICENSE")
		for _, want := range []string{
			"Apache License",
			"Version 2.0, January 2004",
			"TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION",
			"Grant of Patent License",
			"Disclaimer of Warranty",
			"Limitation of Liability",
			"APPENDIX: How to apply the Apache License to your work",
		} {
			if !strings.Contains(lic, want) {
				t.Errorf("LICENSE 缺少片段: %q", want)
			}
		}
		if got := len(strings.Split(lic, "\n")); got < 180 {
			t.Errorf("LICENSE 行数 = %d，期望 >= 180（疑似被截断）", got)
		}
	})

	t.Run(".gitignore 逐行符合规范", func(t *testing.T) {
		content := readRepoFile(t, root, ".gitignore")
		lines := map[string]bool{}
		for _, l := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
			lines[strings.TrimSpace(l)] = true
		}
		// 需求设计说明书 §二.3 规定的模板行
		required := []string{
			"bin/", "dist/", "*.exe", "*.exe~", "*.dll", "*.so", "*.dylib", "/app_main",
			".env", ".env.*", "!*.template", "configs/config.local.yaml", "logs/", "*.log",
			".idea/", ".vscode/", "*.suo", "*.sw?", ".tailwindcss/", "node_modules/",
		}
		for _, want := range required {
			if !lines[want] {
				t.Errorf(".gitignore 缺少规范行: %q", want)
			}
		}
	})

	t.Run(".gitattributes 逐行符合规范", func(t *testing.T) {
		content := readRepoFile(t, root, ".gitattributes")
		required := []string{
			"* text=auto eol=lf", "*.go text", "*.html text", "*.css text", "*.js text",
			"*.png binary", "*.jpg binary", "*.jpeg binary", "*.gif binary", "*.ico binary",
		}
		lines := map[string]bool{}
		for _, l := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
			lines[strings.TrimSpace(l)] = true
		}
		for _, want := range required {
			if !lines[want] {
				t.Errorf(".gitattributes 缺少规范行: %q", want)
			}
		}
	})

	t.Run("CLAUDE.md 含技术栈/仓库/协议/归属/风格", func(t *testing.T) {
		md := readRepoFile(t, root, "CLAUDE.md")
		for _, want := range []string{
			"Apache-2.0", "YunDev", "go 1.27.3", "https://github.com/shibingli/yd",
			"Htmx", "Tailwind", "代码风格",
		} {
			if !strings.Contains(md, want) {
				t.Errorf("CLAUDE.md 缺少: %q", want)
			}
		}
	})

	t.Run("AGENTS.md 含权限边界与 Test-and-Fix", func(t *testing.T) {
		md := readRepoFile(t, root, "AGENTS.md")
		for _, want := range []string{"Test-and-Fix", "禁止修改", "安全合规"} {
			if !strings.Contains(md, want) {
				t.Errorf("AGENTS.md 缺少: %q", want)
			}
		}
	})

	t.Run("docs/developer_guidelines.md 存在", func(t *testing.T) {
		md := readRepoFile(t, root, filepath.Join("docs", "developer_guidelines.md"))
		if !strings.Contains(md, "前后端") {
			t.Error("developer_guidelines.md 未包含前后端协同约束")
		}
	})
}

func TestModuleIdentity(t *testing.T) {
	root := repoRoot(t)
	gomod := readRepoFile(t, root, "go.mod")
	if !strings.Contains(gomod, "module github.com/shibingli/yd") {
		t.Error("go.mod 模块路径必须为 github.com/shibingli/yd")
	}
	if !strings.Contains(gomod, "go 1.27.3") {
		t.Error("go.mod 必须声明 go 1.27.3（需求规定的版本底线）")
	}
}
