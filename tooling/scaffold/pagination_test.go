package scaffold_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/shibingli/yd/tooling/scaffold"
)

// createProbeUserForPaging creates one account without asserting that the refreshed first page contains it. |
// createProbeUserForPaging 新建一个账号，但不要求刷新后的第一页必须包含它。
//
// The shared createProbeUser helper asserts visibility, which stops holding once the roster outgrows one page. |
// 共用的 createProbeUser 会断言新账号出现在第一页；名册超过一页后这个断言不再成立。
func createProbeUserForPaging(t *testing.T, client *http.Client, base, token, username string) {
	t.Helper()
	resp, body := postFormValues(t, client, base+"/mgr/users", url.Values{
		"username":     {username},
		"email":        {username + "@example.com"},
		"display_name": {username},
		"password":     {sm2Cipher(t, client, base, probeUserPassword)},
		"roles":        {"member"},
	}, map[string]string{
		"HX-Request":     "true",
		"X-YD-Encrypted": "sm2",
		"Authorization":  "Bearer " + token,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("建号 %s 失败（状态码 %d）:\n%s", username, resp.StatusCode, body)
	}
}

// TestGeneratedAppPaginatesAdminUsers proves the admin user list is server-paged: page one holds the twenty
// newest rows, page two the remainder, and the pager links carry the page number. |
// TestGeneratedAppPaginatesAdminUsers 证明管理用户列表由服务端分页：第一页是最新的二十行，
// 第二页是其余记录，分页链接携带页码。
func TestGeneratedAppPaginatesAdminUsers(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实服务验证 | skipped in short mode")
	}
	_, base := startProbeAppDBWith(t, "yd-pagination-probe", scaffold.DBSQLite, nil)

	admin := newProbeClient(t, httpTimeout)
	token := signIn(t, admin, base, probeTenant, probeAdmin, probeAdminPassword)
	for i := 0; i < 21; i++ {
		createProbeUserForPaging(t, admin, base, token, fmt.Sprintf("bulk-%02d", i))
	}

	hx := map[string]string{"HX-Request": "true"}
	resp, body := getText(t, admin, base+"/mgr/users", hx)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("第一页状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Page 1 of 2") {
		t.Errorf("第一页缺少分页摘要:\n%s", body)
	}
	if !strings.Contains(body, "bulk-20") {
		t.Errorf("第一页缺少最新账号 bulk-20:\n%s", body)
	}
	if strings.Contains(body, "bulk-00") {
		t.Errorf("第一页包含了应落在第二页的 bulk-00:\n%s", body)
	}
	if !strings.Contains(body, "page=2") {
		t.Errorf("第一页缺少指向第二页的链接:\n%s", body)
	}

	resp, body = getText(t, admin, base+"/mgr/users?page=2", hx)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("第二页状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Page 2 of 2") {
		t.Errorf("第二页缺少分页摘要:\n%s", body)
	}
	if !strings.Contains(body, "bulk-00") {
		t.Errorf("第二页缺少最旧的自建账号 bulk-00:\n%s", body)
	}
	if strings.Contains(body, "bulk-20") {
		t.Errorf("第二页包含了第一页的 bulk-20:\n%s", body)
	}
}
