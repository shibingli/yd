package scaffold_test

import (
	"strings"
	"testing"

	"github.com/shibingli/yd/tooling/scaffold"
)

// TestGeneratedDomainEnumsStayBehindConstants asserts that domain machine values live in Go
// constants (or in the framework packages) and never in the generated templates. Every option
// value is supplied through page data, so a rename cannot leave one page behind. |
// TestGeneratedDomainEnumsStayBehindConstants 断言领域机器值只存在于 Go 常量（或框架包）中，
// 绝不写在生成模板里。每个选项值都由页面数据下发，改名时不会漏掉某个页面。
func TestGeneratedDomainEnumsStayBehindConstants(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("domain-enums-app"))

	templates := []struct {
		file      string
		forbidden []string
		required  []string
	}{
		{
			file: "web/templates/mgr/pages/attachments.html",
			forbidden: []string{
				`(dict "Value" "pending"`,
				`(dict "Value" "referenced"`,
				`(dict "Value" "deleted"`,
				`(dict "Value" "yes"`,
				`(dict "Value" "no"`,
			},
			required: []string{`"Options" .StatusOptions`, `.ReferencedYes`, `.ReferencedNo`},
		},
		{
			file: "web/templates/portal/pages/attachments.html",
			forbidden: []string{
				`(dict "Value" "pending"`,
				`(dict "Value" "referenced"`,
				`(dict "Value" "deleted"`,
			},
			required: []string{`"Options" .StatusOptions`},
		},
		{
			file: "web/templates/portal/pages/messages.html",
			forbidden: []string{
				`(dict "Value" "all"`,
				`(dict "Value" "unread"`,
				`(dict "Value" "sent"`,
				`(dict "Value" "system"`,
				`(dict "Value" "pinned"`,
			},
			required: []string{`"Options" .Folders`},
		},
		{
			file: "web/templates/mgr/partials/messages/audit.html",
			forbidden: []string{
				`(dict "Value" "all"`,
				`(dict "Value" "sent"`,
				`(dict "Value" "system"`,
				`(dict "Value" "pinned"`,
			},
			required: []string{`"Options" .Folders`},
		},
		{
			file: "web/templates/mgr/partials/message_settings/tag_form.html",
			forbidden: []string{
				`"Value" "group"`,
				`"Value" "tag"`,
			},
			required: []string{`"Options" .Kinds`},
		},
		{
			file: "web/templates/portal/partials/messages/composer.html",
			forbidden: []string{
				`$defaultType := "normal"`,
				`$defaultType = "announcement"`,
			},
			required: []string{`.TypeNormal`, `.TypeAnnouncement`},
		},
		{
			file:      "web/templates/portal/partials/friends/request.html",
			forbidden: []string{`eq .Relation "self"`},
			required:  []string{`.RelationSelf`},
		},
		{
			file:      "web/templates/portal/partials/friends/search.html",
			forbidden: []string{`eq .Relation "friend"`},
			required:  []string{`$.RelationFriend`},
		},
		{
			file: "web/templates/mgr/partials/dictionaries/item_form.html",
			forbidden: []string{
				`"Value" "enabled"`,
				`"Value" "disabled"`,
			},
			required: []string{`.StatusEnabled`, `.StatusDisabled`},
		},
		{
			file: "web/templates/mgr/partials/dictionaries/type_form.html",
			forbidden: []string{
				`"Value" "enabled"`,
				`"Value" "disabled"`,
			},
			required: []string{`.StatusEnabled`, `.StatusDisabled`},
		},
		{
			file: "web/templates/partials/shell-switches.html",
			forbidden: []string{
				`data-yd-theme-glyph="light"`,
				`data-yd-theme-glyph="dark"`,
				`data-yd-theme-glyph="system"`,
				`data-yd-theme-state="light"`,
				`data-yd-theme-state="dark"`,
				`data-yd-theme-state="auto"`,
			},
			required: []string{`.ThemeLight`, `.ThemeDark`, `.ThemeSystem`, `.ThemeAuto`},
		},
	}

	for _, tc := range templates {
		body := pr.read(tc.file)
		for _, want := range tc.forbidden {
			if strings.Contains(body, want) {
				t.Errorf("%s still carries the bare domain value %q | %s 仍写有领域裸值 %q", tc.file, want, tc.file, want)
			}
		}
		for _, want := range tc.required {
			if !strings.Contains(body, want) {
				t.Errorf("%s is missing the controller-supplied value %q | %s 缺少控制器下发的 %q", tc.file, want, tc.file, want)
			}
		}
	}

	sources := []struct {
		file      string
		forbidden []string
		required  []string
	}{
		{
			file: "internal/controller/attachment_controller.go",
			forbidden: []string{
				`Referenced = "no"`,
				`withReferenced("no")`,
				`case "yes":`,
				`case "no":`,
			},
			required: []string{`attachmentReferencedYes`, `attachmentReferencedNo`, `attachmentStatusOptions`},
		},
		{
			file:     "internal/controller/message_controller.go",
			required: []string{`messageFolderOptions`, `messaging.FolderAll`, `messaging.TypeNormal`, `messaging.TypeAnnouncement`},
		},
		{
			file:     "internal/controller/message_admin.go",
			required: []string{`messageTagKindOptions`, `"Folders"`},
		},
		{
			file:     "internal/controller/friend_controller.go",
			required: []string{`"RelationSelf"`, `"RelationFriend"`, `messaging.RelationSelf`, `messaging.RelationFriend`},
		},
		{
			file:     "internal/controller/dictionary_controller.go",
			required: []string{`"StatusEnabled"`, `"StatusDisabled"`, `dictionary.StatusEnabled`, `dictionary.StatusDisabled`},
		},
		{
			file:     "internal/controller/dictionary_item_controller.go",
			required: []string{`"StatusEnabled"`, `"StatusDisabled"`},
		},
		{
			file:     "internal/middleware/observability.go",
			required: []string{`NameCacheBypass`, `NamePromote`, `NameAuthz`, `NameTenant`},
		},
		{
			file: "internal/router/router.go",
			forbidden: []string{
				`MetricsDescriptor("cache-bypass")`,
				`MetricsDescriptor("promote")`,
				`MetricsDescriptor("auth")`,
				`MetricsDescriptor("authz")`,
				`MetricsDescriptor("tenant")`,
			},
			required: []string{
				`middleware.NameCacheBypass`,
				`middleware.NamePromote`,
				`middleware.NameAuth)`,
				`middleware.NameAuthz`,
				`middleware.NameTenant`,
			},
		},
		{
			file:      "internal/controller/workflow_task_controller.go",
			forbidden: []string{`{"todo",`},
			required:  []string{`{workflowTabTodo,`},
		},
		{
			file: "internal/service/workflow.go",
			forbidden: []string{
				`[]string{"message"}`,
				`enabled["message"]`,
				`enabled["log"]`,
				`return "message"`,
			},
			required: []string{`workflowChannelMessage`, `workflowChannelLog`},
		},
		{
			file:     "internal/controller/controller.go",
			required: []string{`themeSystem = "system"`, `"ThemeLight"`, `"ThemeDark"`, `"ThemeSystem"`, `"ThemeAuto"`},
		},
	}

	for _, tc := range sources {
		body := pr.read(tc.file)
		for _, want := range tc.forbidden {
			if strings.Contains(body, want) {
				t.Errorf("%s still carries the bare domain value %q | %s 仍写有领域裸值 %q", tc.file, want, tc.file, want)
			}
		}
		for _, want := range tc.required {
			if !strings.Contains(body, want) {
				t.Errorf("%s is missing %q | %s 缺少 %q", tc.file, want, tc.file, want)
			}
		}
	}
}
