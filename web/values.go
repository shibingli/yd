package web

import (
	"github.com/shibingli/yd/security/auth"
	"github.com/shibingli/yd/security/authz"
)

// Machine values rendered by the framework's demo templates. They mirror the domain constants so
// the templates never repeat a literal; a downstream renderer can pass TemplateValues() as the
// "Values" key of its page data. | 框架演示模板渲染的机器值。它们镜像领域常量，使模板不再重复字面量；
// 下游渲染器可以把 TemplateValues() 作为页面数据的 "Values" 键传入。
const (
	// StatusActive is the active user/tenant status. | StatusActive 是用户/租户的激活状态。
	StatusActive = string(auth.StatusActive)
	// StatusDisabled is the disabled user/tenant status. | StatusDisabled 是用户/租户的停用状态。
	StatusDisabled = string(auth.StatusDisabled)
	// ThemeLight is the explicit light theme. | ThemeLight 是显式浅色主题。
	ThemeLight = "light"
	// ThemeDark is the explicit dark theme. | ThemeDark 是显式深色主题。
	ThemeDark = "dark"
	// ThemeSystem follows the operating system preference. | ThemeSystem 跟随操作系统偏好。
	ThemeSystem = "system"
	// ThemeAuto is the generated projects' spelling of ThemeSystem. | ThemeAuto 是生成项目对 ThemeSystem 的写法。
	ThemeAuto = "auto"
	// LangZhCN is the Chinese catalogue tag. | LangZhCN 是中文目录标签。
	LangZhCN = "zh-CN"
	// LangEN is the English catalogue tag. | LangEN 是英文目录标签。
	LangEN = "en"
	// ConnectionIdle is the idle realtime connection state. | ConnectionIdle 是实时连接的空闲状态。
	ConnectionIdle = "idle"
	// GuardModeDisable greys a control out instead of hiding it. | GuardModeDisable 把控件置灰而不是隐藏。
	GuardModeDisable = "disable"
	// GuardModeHide hides a control entirely. | GuardModeHide 完全隐藏控件。
	GuardModeHide = "hide"
)

// TemplateValues returns the machine values the demo templates read from .Values. Permission and
// role entries come straight from the authz registry, so a template never spells a token itself. |
// TemplateValues 返回演示模板从 .Values 读取的机器值。权限与角色条目直接来自 authz 注册表，
// 因此模板从不自己拼写标识。
func TemplateValues() map[string]any {
	return map[string]any{
		"StatusActive":   StatusActive,
		"StatusDisabled": StatusDisabled,
		"ThemeLight":     ThemeLight,
		"ThemeDark":      ThemeDark,
		"ThemeSystem":    ThemeSystem,
		"ThemeAuto":      ThemeAuto,
		"LangZhCN":       LangZhCN,
		"LangEN":         LangEN,
		"ConnectionIdle": ConnectionIdle,
		"GuardDisable":   GuardModeDisable,
		"GuardHide":      GuardModeHide,
		"PermUserUpdate": authz.PermUserUpdate,
		"PermUserDelete": authz.PermUserDelete,
		"RoleAdmin":      authz.RoleAdmin,
	}
}
