// This file tests detection: which shell the environment points at, where that shell's startup file
// lives and which line loads an installed script. | 本文件测试探测：环境指向哪个 shell、该 shell 的启动文件
// 在哪里，以及哪一行加载已安装的脚本。

package completion

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

// envFunc returns a getenv function backed by a map, so no test reads the process environment. | envFunc
// 返回由 map 支撑的 getenv 函数，因此没有任何测试会读取进程环境。
func envFunc(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

// TestDetect 表驱动校验探测规则。
// TestDetect checks the detection rules with a table.
//
// The table is the whole point of the injected goos/getenv pair: every branch - a POSIX shell in an
// absolute path, an embedded argument, a Windows style path, an empty or unrecognised $SHELL, a Windows
// machine whose shell comes from PSModulePath instead - is decided here, on any machine. | 这张表正是注入
// goos/getenv 的意义所在：每个分支——绝对路径下的 POSIX shell、内嵌参数、Windows 风格路径、空或无法识别的
// $SHELL、shell 来自 PSModulePath 的 Windows 机器——都在任何机器上都能在这里判定。
func TestDetect(t *testing.T) {
	cases := []struct {
		name       string
		goos       string
		env        map[string]string
		want       Shell
		wantSource string
		wantProbe  string
		wantErr    bool
	}{
		{
			name: "posix absolute path | POSIX 绝对路径", goos: "linux",
			env:        map[string]string{"SHELL": "/bin/bash"},
			want:       Bash,
			wantSource: SourceShellEnv, wantProbe: "/bin/bash",
		},
		{
			name: "login shell with an argument | 带登录参数的 shell", goos: "linux",
			env:        map[string]string{"SHELL": "/bin/bash -l"},
			want:       Bash,
			wantSource: SourceShellEnv, wantProbe: "/bin/bash -l",
		},
		{
			name: "windows style path with .exe | Windows 风格路径与 .exe", goos: "linux",
			env:        map[string]string{"SHELL": `C:\Program Files\Git\bin\bash.exe`},
			want:       Bash,
			wantSource: SourceShellEnv, wantProbe: `C:\Program Files\Git\bin\bash.exe`,
		},
		{
			name: "uppercase executable name | 大写可执行文件名", goos: "darwin",
			env:        map[string]string{"SHELL": "/usr/bin/ZSH"},
			want:       Zsh,
			wantSource: SourceShellEnv, wantProbe: "/usr/bin/ZSH",
		},
		{
			name: "tcsh counts as csh | tcsh 归入 csh", goos: "linux",
			env:        map[string]string{"SHELL": "/bin/tcsh"},
			want:       Csh,
			wantSource: SourceShellEnv, wantProbe: "/bin/tcsh",
		},
		{
			name: "quoted pwsh | 带引号的 pwsh", goos: "linux",
			env:        map[string]string{"SHELL": `"/usr/local/bin/pwsh"`},
			want:       PowerShell,
			wantSource: SourceShellEnv, wantProbe: `"/usr/local/bin/pwsh"`,
		},
		{
			name: "unknown posix shell is refused | POSIX 上未知 shell 被拒绝", goos: "linux",
			env: map[string]string{"SHELL": "/bin/fish"}, wantErr: true,
		},
		{
			name: "empty posix shell is refused | POSIX 上空 shell 被拒绝", goos: "linux",
			env: map[string]string{"SHELL": "   "}, wantErr: true,
		},
		{
			name: "missing posix shell is refused | POSIX 上缺少 shell 被拒绝", goos: "linux",
			env: nil, wantErr: true,
		},
		{
			name: "windows without SHELL falls back to the platform | Windows 无 SHELL 回退平台默认", goos: "windows",
			env:        nil,
			want:       PowerShell,
			wantSource: SourcePlatform,
		},
		{
			name: "windows recognises PSModulePath | Windows 识别 PSModulePath", goos: "windows",
			env:        map[string]string{ModuleEnvName: `C:\Program Files\PowerShell\7\Modules`},
			want:       PowerShell,
			wantSource: SourceModulePath,
		},
		{
			name: "windows keeps an unknown SHELL as the probe | Windows 保留未知 SHELL 作为依据", goos: "windows",
			env:        map[string]string{"SHELL": "/bin/fish"},
			want:       PowerShell,
			wantSource: SourcePlatform, wantProbe: "/bin/fish",
		},
		{
			name: "windows honours a known SHELL | Windows 上已知 SHELL 生效", goos: "windows",
			env:        map[string]string{"SHELL": "/usr/bin/zsh"},
			want:       Zsh,
			wantSource: SourceShellEnv, wantProbe: "/usr/bin/zsh",
		},
		{
			name: "goos is case-insensitive | GOOS 大小写不敏感", goos: "Windows",
			env:        nil,
			want:       PowerShell,
			wantSource: SourcePlatform,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Detect(tc.goos, envFunc(tc.env))
			if tc.wantErr {
				if !errors.Is(err, ErrShellUndetected) {
					t.Fatalf("want ErrShellUndetected, got %v | 期望哨兵错误", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error | 意外错误: %v", err)
			}
			if got.Shell != tc.want {
				t.Errorf("shell = %q, want %q | shell 不符", got.Shell, tc.want)
			}
			if got.Source != tc.wantSource {
				t.Errorf("source = %q, want %q | 依据不符", got.Source, tc.wantSource)
			}
			if got.Probe != tc.wantProbe {
				t.Errorf("probe = %q, want %q | 原始取值不符", got.Probe, tc.wantProbe)
			}
		})
	}
}

// TestDetectWithNilGetenv nil getenv 必须回退到进程环境并给出受支持的答案。
// TestDetectWithNilGetenv requires a nil getenv to fall back to the process environment and answer with a supported shell.
//
// Which shell that is depends on the machine the test runs on - a Windows developer with Git Bash in
// $SHELL is answered bash, which is right - so only the fallback itself is asserted, never a particular
// shell. | 具体是哪个 shell 取决于跑测试的机器——Windows 开发者的 $SHELL 里若有 Git Bash，答案就是 bash，而那是对的
// ——因此这里只断言回退本身，不断言某个具体 shell。
func TestDetectWithNilGetenv(t *testing.T) {
	got, err := Detect("windows", nil)
	if err != nil {
		t.Fatalf("unexpected error | 意外错误: %v", err)
	}
	if !slices.Contains(Shells(), got.Shell) {
		t.Errorf("shell = %q, not one of the supported ones | 不是受支持的 shell", got.Shell)
	}
	if got.Source == "" {
		t.Error("detection has no source | 探测缺少依据")
	}
}

// TestDetectionDescribe 依据文案必须点明信号来源。
// TestDetectionDescribe requires the evidence line to name its signal.
func TestDetectionDescribe(t *testing.T) {
	cases := []struct {
		det  Detection
		want string
	}{
		{Detection{Shell: Bash, Source: SourceShellEnv, Probe: "/bin/bash"}, "$SHELL=/bin/bash"},
		{Detection{Shell: Zsh, Source: SourceOverride, Probe: "zsh"}, "given on the command line | 由命令行指定"},
		{Detection{Shell: PowerShell, Source: SourcePlatform}, "platform default | 平台默认值"},
		{Detection{Shell: PowerShell, Source: SourceModulePath}, ModuleEnvName},
	}
	for _, tc := range cases {
		if got := tc.det.Describe(); got != tc.want {
			t.Errorf("Describe() = %q, want %q | 依据文案不符", got, tc.want)
		}
	}
}

// TestOverrideKeepsTheProbe 命令行指定的 shell 必须被标记为依据来自命令行。
// TestOverrideKeepsTheProbe marks a caller supplied shell as coming from the command line.
func TestOverrideKeepsTheProbe(t *testing.T) {
	got, err := override("pwsh")
	if err != nil {
		t.Fatalf("unexpected error | 意外错误: %v", err)
	}
	if got.Shell != PowerShell || got.Source != SourceOverride || got.Probe != string(PowerShell) {
		t.Errorf("override(pwsh) = %+v | 覆盖结果不符", got)
	}
	if _, err := override("fish"); err == nil {
		t.Error("override(fish) should fail | 未知 shell 应当失败")
	}
}

// TestStartupFile 表驱动校验启动文件路径。
// TestStartupFile checks the startup file paths with a table.
func TestStartupFile(t *testing.T) {
	home := filepath.Join("home", "yduser")
	cases := []struct {
		name  string
		shell Shell
		goos  string
		env   map[string]string
		want  string
	}{
		{"bash | bash", Bash, "linux", nil, filepath.Join(home, ".bashrc")},
		{"zsh | zsh", Zsh, "darwin", nil, filepath.Join(home, ".zshrc")},
		{"csh | csh", Csh, "linux", nil, filepath.Join(home, ".cshrc")},
		{
			name: "powershell 7 profile on windows | Windows 上的 PowerShell 7 profile", shell: PowerShell, goos: "windows",
			env:  map[string]string{ModuleEnvName: `C:\Program Files\PowerShell\7\Modules`},
			want: filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		},
		{
			name: "windows powershell 5.1 profile | Windows PowerShell 5.1 的 profile", shell: PowerShell, goos: "windows",
			env:  map[string]string{ModuleEnvName: `C:\Windows\system32\WindowsPowerShell\v1.0\Modules`},
			want: filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		},
		{
			name: "powershell 7 profile on posix | POSIX 上的 PowerShell 7 profile", shell: PowerShell, goos: "linux",
			want: filepath.Join(home, ".config", "powershell", "profile.ps1"),
		},
		{
			name: "XDG_CONFIG_HOME wins | XDG_CONFIG_HOME 优先", shell: PowerShell, goos: "linux",
			env:  map[string]string{"XDG_CONFIG_HOME": filepath.Join("xdg", "cfg")},
			want: filepath.Join("xdg", "cfg", "powershell", "profile.ps1"),
		},
		{"unknown shell has no startup file | 未知 shell 没有启动文件", Shell("fish"), "linux", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StartupFile(tc.shell, home, tc.goos, envFunc(tc.env)); got != tc.want {
				t.Errorf("StartupFile() = %q, want %q | 启动文件路径不符", got, tc.want)
			}
		})
	}
	if got := StartupFile(Bash, "", "linux", nil); got != "" {
		t.Errorf("StartupFile without home = %q, want empty | 无主目录时应为空", got)
	}
	if got := StartupFile(Bash, home, "linux", nil); got != filepath.Join(home, ".bashrc") {
		t.Errorf("StartupFile with nil getenv = %q | nil getenv 结果不符", got)
	}
}

// TestLoadLine 加载行的语法与引用必须随 shell 变化。
// TestLoadLine requires the loading line's syntax and quoting to follow the shell.
func TestLoadLine(t *testing.T) {
	posix := "/home/yd user/.yd/completion.bash"
	if got, want := LoadLine(Bash, posix), `source "`+posix+`"`; got != want {
		t.Errorf("LoadLine(bash) = %q, want %q | 加载行不符", got, want)
	}
	ps := "/home/yd user/.yd/completion.powershell"
	if got, want := LoadLine(PowerShell, ps), `. '`+ps+`'`; got != want {
		t.Errorf("LoadLine(powershell) = %q, want %q | 加载行不符", got, want)
	}
	if got, want := LoadLine(Zsh, `$HOME/.yd/x"y`), `source "\$HOME/.yd/x\"y"`; got != want {
		t.Errorf("LoadLine escaping = %q, want %q | 转义不符", got, want)
	}
}
