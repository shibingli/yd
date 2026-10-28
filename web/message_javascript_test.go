package web_test

import (
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/web"
)

// TestMessageJSHooks pins the message-centre glue that the templates rely on. The compact composer must
// upgrade eagerly (a focus-triggered upgrade changed the composer height under the pointer), uploads must
// start on file selection with a visible status, and attachments must open the shared in-page preview dialog
// instead of navigating away. | TestMessageJSHooks 固定消息中心模板依赖的胶水行为：紧凑撰写区必须立即升级
// （聚焦后才升级会让撰写区在指针下方改变高度），选中文件后必须自动上传并显示状态，附件必须在当前页的共享
// 预览对话框中打开，而不是跳转到新页面。
func TestMessageJSHooks(t *testing.T) {
	editorRaw, err := afero.ReadFile(web.FS(), "static/js/yd-message-editor.js")
	if err != nil {
		t.Fatalf("读取 yd-message-editor.js 失败: %v", err)
	}
	editor := string(editorRaw)
	for _, want := range []string{
		"data-yd-editor-compact",
		`eagerEditor(element) || element.hasAttribute("data-yd-editor-compact")`,
	} {
		if !strings.Contains(editor, want) {
			t.Errorf("yd-message-editor.js 缺少 %q；紧凑编辑器会在首次聚焦时才升级并改变高度", want)
		}
	}

	messageRaw, err := afero.ReadFile(web.FS(), "static/js/yd-message.js")
	if err != nil {
		t.Fatalf("读取 yd-message.js 失败: %v", err)
	}
	message := string(messageRaw)
	for _, want := range []string{
		"data-yd-upload-form",
		"data-yd-upload-file",
		"data-yd-upload-status-target",
		"data-yd-upload-error-target",
		"submitUpload",
		"clearUploadError",
		"htmx:finally:request",
		"data-yd-attachment-preview",
		"openAttachmentPreview",
		"showModal",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("yd-message.js 缺少 %q；自动上传或页内附件预览未接线", want)
		}
	}
}
