package scaffold_test

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/shibingli/yd/core/jsonx"
	"github.com/shibingli/yd/tooling/scaffold"
)

// probeCMSArticle is the article JSON used by the CMS probe. | probeCMSArticle 是 CMS 探针使用的文章 JSON。
type probeCMSArticle struct {
	ID       string `json:"ID"`
	TenantID string `json:"TenantID"`
	Slug     string `json:"Slug"`
	Title    string `json:"Title"`
	Status   string `json:"Status"`
	BodyHTML string `json:"BodyHTML"`
}

// probeCMSCategory is the category JSON used by the CMS probe. | probeCMSCategory 是 CMS 探针使用的分类 JSON。
type probeCMSCategory struct {
	ID     string `json:"ID"`
	Name   string `json:"Name"`
	Slug   string `json:"Slug"`
	Parent string `json:"ParentID"`
}

// probeCMSMedia is the media JSON used by the CMS probe. | probeCMSMedia 是 CMS 探针使用的媒体 JSON。
type probeCMSMedia struct {
	ID          string `json:"ID"`
	ContentType string `json:"ContentType"`
	Width       int    `json:"Width"`
	Height      int    `json:"Height"`
}

// probeCMSCommentPage is the comment page JSON used by the CMS probe. | probeCMSCommentPage 是 CMS 探针使用的评论分页 JSON。
type probeCMSCommentPage struct {
	Items []struct {
		ID       string `json:"ID"`
		Status   string `json:"Status"`
		BodyText string `json:"BodyText"`
	} `json:"items"`
	Total int `json:"total"`
}

// probeCMSArticlePage is the article page JSON used by the CMS probe. | probeCMSArticlePage 是 CMS 探针使用的文章分页 JSON。
type probeCMSArticlePage struct {
	Items []probeCMSArticle `json:"items"`
	Total int               `json:"total"`
}

// TestGeneratedAppCMS 端到端验证生成项目的 CMS：无限级分类、文章状态机、公开站点、搜索、RSS、媒体、
// 评论审核、修订与站点设置。 | TestGeneratedAppCMS runs the generated project's CMS end to end: the unlimited category
// tree, the article state machine, the public site, search, RSS, media, comment moderation, revisions and site settings.
func TestGeneratedAppPortalAuthoring(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实服务验证 | skipped in short mode")
	}

	_, base := startProbeAppDBWith(t, "yd-portal-probe", scaffold.DBSQLite, nil)
	admin := newProbeClient(t, httpTimeout)
	adminToken := signIn(t, admin, base, probeTenant, probeAdmin, probeAdminPassword)
	createProbeUser(t, admin, base, adminToken, "writer", "member", "writer@example.com")
	writer := newProbeClient(t, httpTimeout)
	writerToken := signIn(t, writer, base, probeTenant, "writer", probeUserPassword)
	anon := newProbeClient(t, httpTimeout)

	// 根路由默认是匿名可达的内容门户，而不是登录页或后台首页。 |
	// The root route is an anonymously reachable content portal by default, not the sign-in page or the back office.
	resp, body := probeGet(t, anon, base+"/", "")
	requireProbeStatus(t, resp, body, http.StatusOK, "匿名打开根路由")
	for _, want := range []string{`data-yd-page="portal-home"`, `href="/login"`, `href="/blog"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("公开门户首页缺少 %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `id="yd-sidebar"`) || strings.Contains(body, "Writer Name") {
		t.Fatalf("匿名访客看到了登录后的外壳或用户数据:\n%s", body)
	}

	// 个人中心与写作中心对普通成员可达。
	// The personal centre and writing centre are reachable for an ordinary member.
	resp, body = probeGet(t, writer, base+"/account", writerToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "成员打开个人中心")
	for _, want := range []string{`action="/account/profile"`, `href="/account/articles"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("个人中心缺少 %q:\n%s", want, body)
		}
	}
	resp, body = probeGet(t, writer, base+"/account/articles/new", writerToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "成员打开编辑器")
	if !strings.Contains(body, `action="/account/articles"`) || !strings.Contains(body, "data-yd-editor") {
		t.Fatalf("门户编辑器缺少创建表单或富文本编辑器:\n%s", body)
	}
	if !strings.Contains(body, `data-yd-editor-upload="/account/media/upload"`) {
		t.Fatalf("门户编辑器仍指向上传后台端点:\n%s", body)
	}
	resp, body = probePostFile(t, writer, base+"/account/media/upload", writerToken, "portal.png", probePNG(t))
	requireProbeStatus(t, resp, body, http.StatusOK, "门户编辑器上传图片")
	if !strings.Contains(body, `/blog/media/`) {
		t.Fatalf("门户上传响应缺少媒体地址:\n%s", body)
	}

	// 资料编辑只改显示名与邮箱；重新读取页面后立即生效。
	// Profile editing changes only the display name and e-mail, and the next read reflects it.
	resp, body = probePostForm(t, writer, base+"/account/profile", writerToken, url.Values{
		"display_name": {"Writer Name"},
		"email":        {"writer@example.com"},
	})
	requireProbeStatus(t, resp, body, http.StatusSeeOther, "成员修改个人资料")
	if loc := resp.Header.Get("Location"); loc != "/account" {
		t.Fatalf("修改资料后的 Location = %q, 期望 /account", loc)
	}
	resp, body = probeGet(t, writer, base+"/account", writerToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "重新读取个人中心")
	if !strings.Contains(body, "Writer Name") {
		t.Fatalf("个人中心没有显示新的显示名:\n%s", body)
	}

	// 建立一二级分类，后续文章挂在二级分类下；分类页必须包含子树。 |
	// Create a first/second-level pair and attach the article to the second level; the category page must include the
	// subtree.
	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/categories", adminToken, map[string]any{
		"name": "News", "slug": "news", "visible": true,
	})
	requireProbeStatus(t, resp, body, http.StatusCreated, "创建顶级分类")
	root := decodeProbeJSON[probeCMSCategory](t, body)
	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/categories", adminToken, map[string]any{
		"name": "World", "slug": "world", "parent_id": root.ID, "visible": true,
	})
	requireProbeStatus(t, resp, body, http.StatusCreated, "创建二级分类")
	child := decodeProbeJSON[probeCMSCategory](t, body)

	// 普通成员通过门户创建草稿：此时公开门户还看不到它。 |
	// The member creates a draft through the portal; the public portal cannot see it yet.
	resp, body = probePostForm(t, writer, base+"/account/articles", writerToken, url.Values{
		"title":         {"Portal Story"},
		"body_markdown": {"# Portal Story\n\nA story written in the portal."},
		"category_id":   {child.ID},
		"kind":          {"post"},
		"visibility":    {"public"},
		"allow_comment": {"on"},
	})
	requireProbeStatus(t, resp, body, http.StatusSeeOther, "成员创建门户文章")
	editPath := resp.Header.Get("Location")
	if !strings.HasPrefix(editPath, "/account/articles/") || !strings.HasSuffix(editPath, "/edit") {
		t.Fatalf("创建后的 Location = %q, 期望 /account/articles/<id>/edit", editPath)
	}
	articleID := strings.TrimSuffix(strings.TrimPrefix(editPath, "/account/articles/"), "/edit")
	articlePath := "/account/articles/" + articleID
	resp, body = probeGet(t, anon, base+"/", "")
	requireProbeStatus(t, resp, body, http.StatusOK, "草稿状态下读取公开门户")
	if strings.Contains(body, "Portal Story") {
		t.Fatalf("草稿在审核发布前出现在公开门户:\n%s", body)
	}
	resp, body = probeGet(t, writer, base+"/account/articles", writerToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "成员读取我的文章")
	if !strings.Contains(body, "Portal Story") {
		t.Fatalf("我的文章列表缺少刚创建的草稿:\n%s", body)
	}

	// 作者可以更新自己的草稿，更新后仍是草稿；他人打开或写入同一文章一律 404，不泄露存在性。 |
	// The author can update the draft and it stays a draft; another member opening or writing the same article always gets
	// 404, so the portal never reveals that the draft exists.
	resp, body = probePostForm(t, writer, base+articlePath, writerToken, url.Values{
		"title":         {"Portal Story Updated"},
		"body_markdown": {"# Portal Story Updated\n\nThe edited body stays a draft."},
		"category_id":   {child.ID},
		"kind":          {"post"},
		"visibility":    {"public"},
		"allow_comment": {"on"},
	})
	requireProbeStatus(t, resp, body, http.StatusSeeOther, "作者更新自己的草稿")
	createProbeUser(t, admin, base, adminToken, "other", "member", "other@example.com")
	other := newProbeClient(t, httpTimeout)
	otherToken := signIn(t, other, base, probeTenant, "other", probeUserPassword)
	resp, body = probeGet(t, other, base+editPath, otherToken)
	requireProbeStatus(t, resp, body, http.StatusNotFound, "他人打开我的文章编辑器")
	resp, body = probePostForm(t, other, base+articlePath, otherToken, url.Values{
		"title": {"Stolen"}, "body_markdown": {"stolen"},
	})
	requireProbeStatus(t, resp, body, http.StatusNotFound, "他人更新我的文章")

	// 作者只能提交审核，不能从门户直接发布。
	// The author can submit for review but cannot publish directly from the portal.
	resp, body = probePostForm(t, writer, base+articlePath+"/transition", writerToken, url.Values{"action": {"submit"}})
	requireProbeStatus(t, resp, body, http.StatusSeeOther, "成员提交审核")
	resp, body = probePostForm(t, writer, base+articlePath+"/transition", writerToken, url.Values{"action": {"publish"}})
	requireProbeStatus(t, resp, body, http.StatusUnprocessableEntity, "成员从门户直接发布")
	resp, body = probeGet(t, anon, base+"/", "")
	requireProbeStatus(t, resp, body, http.StatusOK, "待审状态下读取公开门户")
	if strings.Contains(body, "Portal Story") {
		t.Fatalf("待审文章在审核通过前出现在公开门户:\n%s", body)
	}

	// 管理员在后台看到待审队列并通过审核，随后公开站点与分类页才出现文章。 |
	// The administrator sees the pending queue and approves it; only then do the public site and category page show it.
	resp, body = probeGet(t, admin, base+"/mgr/cms/articles?status=pending", adminToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "管理员读取待审队列")
	if !strings.Contains(body, "Portal Story") {
		t.Fatalf("后台待审队列缺少文章:\n%s", body)
	}
	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/articles/"+articleID+"/transition", adminToken, map[string]any{"action": "approve"})
	requireProbeStatus(t, resp, body, http.StatusOK, "管理员审核通过")
	resp, body = probeGet(t, anon, base+"/", "")
	requireProbeStatus(t, resp, body, http.StatusOK, "发布后读取公开门户")
	if !strings.Contains(body, "Portal Story") {
		t.Fatalf("审核通过后公开门户仍缺少文章:\n%s", body)
	}
	sectionStart := strings.Index(body, `id="yd-portal-section-news"`)
	if sectionStart < 0 {
		t.Fatalf("公开门户缺少一级分类区块 News:\n%s", body)
	}
	sectionEnd := strings.Index(body[sectionStart:], "</section>")
	if sectionEnd < 0 || !strings.Contains(body[sectionStart:sectionStart+sectionEnd], "Portal Story") {
		t.Fatalf("一级分类区块没有包含二级分类下的已发布文章:\n%s", body[sectionStart:])
	}
	resp, body = probeGet(t, anon, base+"/blog/category/world", "")
	requireProbeStatus(t, resp, body, http.StatusOK, "二级分类页")
	if !strings.Contains(body, "Portal Story") {
		t.Fatalf("二级分类页缺少文章:\n%s", body)
	}
	resp, body = probeGet(t, anon, base+"/blog/search?q=Portal&category=news", "")
	requireProbeStatus(t, resp, body, http.StatusOK, "按分类条件搜索")
	if !strings.Contains(body, "Portal Story") {
		t.Fatalf("组合条件搜索缺少文章:\n%s", body)
	}
	resp, body = probeGet(t, anon, base+"/blog/search?q=Portal&category=missing", "")
	requireProbeStatus(t, resp, body, http.StatusNotFound, "不存在分类的搜索")

	// 作者可以退役、恢复、撤回自己的文章，但不能发布；删除后列表不再出现。 |
	// The author can archive, restore and withdraw their article but never publish it; after deletion the list no longer
	// carries it.
	resp, body = probePostForm(t, writer, base+articlePath+"/transition", writerToken, url.Values{"action": {"archive"}})
	requireProbeStatus(t, resp, body, http.StatusSeeOther, "作者退役已发布文章")
	resp, body = probeGet(t, anon, base+"/", "")
	requireProbeStatus(t, resp, body, http.StatusOK, "退役后读取公开门户")
	if strings.Contains(body, "Portal Story") {
		t.Fatalf("退役后的文章仍出现在公开门户:\n%s", body)
	}
	resp, body = probePostForm(t, writer, base+articlePath+"/transition", writerToken, url.Values{"action": {"restore"}})
	requireProbeStatus(t, resp, body, http.StatusSeeOther, "作者恢复文章")
	resp, body = probePostForm(t, writer, base+articlePath+"/transition", writerToken, url.Values{"action": {"submit"}})
	requireProbeStatus(t, resp, body, http.StatusSeeOther, "作者重新提交审核")
	resp, body = probePostForm(t, writer, base+articlePath+"/transition", writerToken, url.Values{"action": {"withdraw"}})
	requireProbeStatus(t, resp, body, http.StatusSeeOther, "作者撤回审核")
	resp, body = probePostForm(t, writer, base+articlePath+"/delete", writerToken, url.Values{})
	requireProbeStatus(t, resp, body, http.StatusSeeOther, "作者删除自己的文章")
	resp, body = probeGet(t, writer, base+"/account/articles", writerToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "删除后读取我的文章")
	if strings.Contains(body, "Portal Story") {
		t.Fatalf("删除后我的文章列表仍包含文章:\n%s", body)
	}
}

func TestGeneratedAppCMS(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实服务验证 | skipped in short mode")
	}

	_, base := startProbeAppDBWith(t, "yd-cms-probe", scaffold.DBSQLite, nil)

	admin := newProbeClient(t, httpTimeout)
	adminToken := signIn(t, admin, base, probeTenant, probeAdmin, probeAdminPassword)
	createProbeUser(t, admin, base, adminToken, "carol", "member", "carol@example.com")
	carol := newProbeClient(t, httpTimeout)
	carolToken := signIn(t, carol, base, probeTenant, "carol", probeUserPassword)
	public := newProbeClient(t, httpTimeout)

	resp, body := probeGet(t, admin, base+"/api/v1/cms/dashboard", adminToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "读取 CMS 概览")

	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/categories", adminToken, map[string]any{
		"name": "News", "slug": "news", "visible": true,
	})
	requireProbeStatus(t, resp, body, http.StatusCreated, "创建分类")
	category := decodeProbeJSON[probeCMSCategory](t, body)
	if category.ID == "" || category.Slug != "news" {
		t.Fatalf("分类响应不完整: %s", body)
	}

	resp, body = probePostJSON(t, carol, base+"/api/v1/cms/categories", carolToken, map[string]any{
		"name": "Forbidden", "slug": "forbidden",
	})
	requireProbeStatus(t, resp, body, http.StatusForbidden, "成员创建分类")

	resp, body = probePostJSON(t, carol, base+"/api/v1/cms/articles", carolToken, map[string]any{
		"category_id":   category.ID,
		"title":         "CMS Probe Article",
		"body_markdown": "**bold** body with 中文",
		"kind":          "post",
		"visibility":    "public",
		"allow_comment": true,
		"tag_names":     []string{"probe-tag"},
	})
	requireProbeStatus(t, resp, body, http.StatusCreated, "创建文章")
	article := decodeProbeJSON[probeCMSArticle](t, body)
	if article.ID == "" || article.Slug == "" || article.Status != "draft" {
		t.Fatalf("文章响应不完整: %s", body)
	}

	resp, body = probePostJSON(t, carol, base+"/api/v1/cms/articles/"+article.ID+"/transition", carolToken, map[string]any{"action": "publish"})
	requireProbeStatus(t, resp, body, http.StatusForbidden, "成员发布文章")

	// 成员可以把草稿提交审核（submit 只需要 update），审核者驳回后回到草稿。 |
	// A member may submit a draft (submit only needs update); a reviewer rejects it back to draft.
	resp, body = probePostJSON(t, carol, base+"/api/v1/cms/articles/"+article.ID+"/transition", carolToken, map[string]any{"action": "submit"})
	requireProbeStatus(t, resp, body, http.StatusOK, "成员提交审核")
	pending := decodeProbeJSON[probeCMSArticle](t, body)
	if pending.Status != "pending" {
		t.Fatalf("提交后状态 = %q, 期望 pending", pending.Status)
	}
	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/articles/"+article.ID+"/transition", adminToken, map[string]any{"action": "reject"})
	requireProbeStatus(t, resp, body, http.StatusOK, "审核者驳回")
	resp, body = probeGet(t, carol, base+"/mgr/cms/articles/"+article.ID+"/edit", carolToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "成员打开自己的编辑器")
	if !strings.Contains(body, `value="submit"`) {
		t.Fatalf("成员编辑器缺少提交审核入口:\n%s", body)
	}

	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/articles/"+article.ID+"/transition", adminToken, map[string]any{"action": "publish"})
	requireProbeStatus(t, resp, body, http.StatusOK, "管理员发布文章")
	article = decodeProbeJSON[probeCMSArticle](t, body)
	if article.Status != "published" {
		t.Fatalf("发布后状态 = %q, 期望 published: %s", article.Status, body)
	}

	for _, path := range []string{
		"/mgr/cms", "/mgr/cms/articles", "/mgr/cms/articles/new", "/mgr/cms/articles/" + article.ID + "/edit",
		"/mgr/cms/categories", "/mgr/cms/categories/new", "/mgr/cms/categories/" + category.ID + "/edit",
		"/mgr/cms/tags", "/mgr/cms/tags/new", "/mgr/cms/media", "/mgr/cms/comments", "/mgr/cms/settings", "/mgr/cms/audit",
		"/mgr/cms/articles/" + article.ID + "/revisions",
	} {
		resp, body = probeGet(t, admin, base+path, adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "管理页 "+path)
		if len(body) == 0 {
			t.Fatalf("管理页 %s 返回空正文", path)
		}
	}

	for _, path := range []string{
		"/mgr/cms/categories/new",
		"/mgr/cms/categories/" + category.ID + "/edit",
		"/mgr/cms/tags/new",
		"/mgr/cms/articles/" + article.ID + "/revisions",
	} {
		headers := probeAuthHeader(adminToken)
		headers["HX-Request"] = "true"
		headers["HX-Target"] = "yd-drawer-body"
		resp, body = getText(t, admin, base+path, headers)
		requireProbeStatus(t, resp, body, http.StatusOK, "抽屉片段 "+path)
		if len(strings.TrimSpace(body)) == 0 || !strings.Contains(body, "yd-") {
			t.Fatalf("抽屉片段 %s 为空或缺少内容: %q", path, body)
		}
	}

	// 普通内容作者从门户进入写作中心：入口留在门户侧，门户不再把成员送进后台；概览本身仍然 403。
	// A content author enters the writing centre from the portal: the entry stays in the portal and the portal no longer
	// sends a member into the back office; the overview itself still answers 403.
	resp, body = probeGet(t, carol, base+"/", carolToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "成员打开门户首页")
	if !strings.Contains(body, `href="/account/articles"`) {
		t.Fatalf("成员门户缺少写作中心入口:\n%s", body)
	}
	if strings.Contains(body, `href="/mgr`) {
		t.Fatalf("普通成员的门户不应出现任何后台入口:\n%s", body)
	}
	resp, body = probeGet(t, carol, base+"/mgr", carolToken)
	requireProbeStatus(t, resp, body, http.StatusForbidden, "成员打开管理概览")
	resp, body = probeGet(t, carol, base+"/mgr/cms/articles", carolToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "成员打开文章管理")
	if !strings.Contains(body, "CMS Probe Article") {
		t.Fatalf("成员文章列表缺少自己的文章:\n%s", body)
	}

	// 审计员只读：能打开概览，不能创建分类。
	// The auditor is read-only: it opens the overview but cannot create a category.
	createProbeUser(t, admin, base, adminToken, "aud", "auditor", "aud@example.com")
	auditor := newProbeClient(t, httpTimeout)
	auditorToken := signIn(t, auditor, base, probeTenant, "aud", probeUserPassword)
	resp, body = probeGet(t, auditor, base+"/mgr/cms", auditorToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "审计员打开 CMS 概览")
	resp, body = probeGet(t, auditor, base+"/mgr/cms/settings", auditorToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "审计员打开站点设置")
	if strings.Contains(body, `action="/mgr/cms/settings"`) {
		t.Fatalf("审计员看到了可编辑的站点设置表单")
	}

	resp, body = probePostJSON(t, auditor, base+"/api/v1/cms/categories", auditorToken, map[string]any{
		"name": "Auditor", "slug": "auditor",
	})
	requireProbeStatus(t, resp, body, http.StatusForbidden, "审计员创建分类")
	if !strings.Contains(body, `"error"`) || strings.Contains(body, "<html") {
		t.Fatalf("API 拒绝未返回 JSON: %s", body)
	}

	resp, body = getText(t, public, base+"/blog", nil)
	requireProbeStatus(t, resp, body, http.StatusOK, "公开列表")
	if !strings.Contains(body, "CMS Probe Article") {
		t.Fatalf("公开列表缺少文章: %s", body)
	}
	// OpenAPI 路径参数必须与真实路由一致：/blog、分类页与标签页没有路径页码。 |
	// OpenAPI path parameters must match the real routes: /blog, category and tag pages have no path page number.
	resp, docBody := getText(t, public, base+"/api/v1/openapi.json", nil)
	requireProbeStatus(t, resp, docBody, http.StatusOK, "公开 OpenAPI 文档")
	var doc struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name string `json:"name"`
				In   string `json:"in"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := jsonx.Unmarshal([]byte(docBody), &doc); err != nil {
		t.Fatalf("解析 OpenAPI 文档: %v", err)
	}
	// 所有 $ref 必须指向真实注册的组件：CMS 领域类型位于框架模块，注解扫描不会自动补上它们，
	// 缺少注册时启动日志会告警，文档查看器也会出现空白的响应模型。 |
	// Every $ref must point at a registered component: the CMS domain types live in the framework module and the
	// annotation scan cannot register them by itself, so a missing registration warns at start-up and leaves blank
	// response models in the document viewer.
	var rawDoc map[string]any
	if err := jsonx.Unmarshal([]byte(docBody), &rawDoc); err != nil {
		t.Fatalf("解析 OpenAPI 原始文档: %v", err)
	}
	schemas := map[string]any{}
	if components, ok := rawDoc["components"].(map[string]any); ok {
		if registered, ok := components["schemas"].(map[string]any); ok {
			schemas = registered
		}
	}
	var unresolved []string
	seenRef := map[string]bool{}
	var walkRefs func(any)
	walkRefs = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			if ref, ok := value["$ref"].(string); ok && strings.HasPrefix(ref, "#/components/schemas/") {
				name := strings.TrimPrefix(ref, "#/components/schemas/")
				if _, ok := schemas[name]; !ok && !seenRef[name] {
					seenRef[name] = true
					unresolved = append(unresolved, name)
				}
			}
			for _, child := range value {
				walkRefs(child)
			}
		case []any:
			for _, child := range value {
				walkRefs(child)
			}
		}
	}
	walkRefs(rawDoc)
	if len(unresolved) > 0 {
		t.Fatalf("OpenAPI 引用了未注册的 Schema: %s", strings.Join(unresolved, ", "))
	}
	pathParams := func(path string) []string {
		var out []string
		for _, param := range doc.Paths[path]["get"].Parameters {
			if param.In == "path" {
				out = append(out, param.Name)
			}
		}
		return out
	}
	if got := pathParams("/blog"); len(got) != 0 {
		t.Fatalf("/blog 的路径参数 = %v, 期望为空", got)
	}
	for path, want := range map[string]string{
		"/blog/category/{slug}": "slug",
		"/blog/tag/{slug}":      "slug",
		"/blog/page/{page}":     "page",
	} {
		got := pathParams(path)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("%s 的路径参数 = %v, 期望 [%s]", path, got, want)
		}
	}
	resp, body = probeGet(t, public, base+"/blog/no-such-article", "")
	requireProbeStatus(t, resp, body, http.StatusNotFound, "公开文章缺失")
	if !strings.Contains(body, `href="/blog"`) || strings.Contains(body, `id="yd-sidebar"`) {
		t.Fatalf("公开 404 未使用公开站点外壳:\n%s", body)
	}

	resp, body = getText(t, public, base+"/blog/"+article.Slug, nil)
	requireProbeStatus(t, resp, body, http.StatusOK, "公开文章")
	if !strings.Contains(body, "<strong>bold</strong>") {
		t.Fatalf("公开文章缺少净化后的正文: %s", body)
	}
	resp, body = getText(t, public, base+"/blog/category/news", nil)
	requireProbeStatus(t, resp, body, http.StatusOK, "分类页")
	if !strings.Contains(body, "CMS Probe Article") {
		t.Fatalf("分类页缺少文章: %s", body)
	}
	resp, body = getText(t, public, base+"/blog/tag/probe-tag", nil)
	requireProbeStatus(t, resp, body, http.StatusOK, "标签页")
	if !strings.Contains(body, "CMS Probe Article") {
		t.Fatalf("标签页缺少文章: %s", body)
	}
	resp, body = getText(t, public, base+"/blog/search?q=bold", nil)
	requireProbeStatus(t, resp, body, http.StatusOK, "搜索页")
	if !strings.Contains(body, "CMS Probe Article") {
		t.Fatalf("搜索结果缺少文章: %s", body)
	}
	resp, body = getText(t, public, base+"/blog/rss.xml", nil)
	requireProbeStatus(t, resp, body, http.StatusOK, "RSS")
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/rss+xml") {
		t.Fatalf("RSS Content-Type = %q", resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, "CMS Probe Article") {
		t.Fatalf("RSS 缺少文章: %s", body)
	}
	resp, body = getText(t, public, base+"/blog/sitemap.xml", nil)
	requireProbeStatus(t, resp, body, http.StatusOK, "站点地图")
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/xml") {
		t.Fatalf("站点地图 Content-Type = %q", resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, article.Slug) {
		t.Fatalf("站点地图缺少文章别名: %s", body)
	}
	if !strings.Contains(body, `xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"`) {
		t.Fatalf("站点地图缺少标准命名空间: %s", body)
	}

	// 公开文章的会话按可选方式解析：已登录读者看到评论表单与后台入口，匿名读者只看到登录入口。 |
	// The public article resolves the session optionally: a signed-in reader gets the comment form and the backstage
	// entry, while an anonymous reader only gets the sign-in entry.
	resp, body = probeGet(t, carol, base+"/blog/"+article.Slug, carolToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "已登录读者打开公开文章")
	if !strings.Contains(body, `action="/blog/`+article.Slug+`/comments"`) {
		t.Fatalf("已登录读者的公开文章缺少评论表单:\n%s", body)
	}
	if !strings.Contains(body, `href="/account/articles"`) {
		t.Fatalf("已登录读者的公开外壳缺少写作中心入口:\n%s", body)
	}
	if strings.Contains(body, `href="/mgr`) {
		t.Fatalf("普通成员的公开外壳不应出现任何后台入口:\n%s", body)
	}
	resp, body = probeGet(t, public, base+"/blog/"+article.Slug, "")
	requireProbeStatus(t, resp, body, http.StatusOK, "匿名读者打开公开文章")
	if strings.Contains(body, `action="/blog/`+article.Slug+`/comments"`) {
		t.Fatalf("匿名读者的公开文章渲染了评论表单:\n%s", body)
	}

	resp, body = probePostForm(t, carol, base+"/blog/"+article.Slug+"/comments", carolToken, url.Values{"body": {"hello comment"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("发表评论状态码 = %d, 期望 303\n%s", resp.StatusCode, body)
	}
	resp, body = probeGet(t, admin, base+"/api/v1/cms/comments?status=pending", adminToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "读取待审评论")
	comments := decodeProbeJSON[probeCMSCommentPage](t, body)
	if comments.Total != 1 || len(comments.Items) != 1 {
		t.Fatalf("待审评论 = %s, 期望 1 条", body)
	}
	commentID := comments.Items[0].ID
	resp, body = probeGet(t, carol, base+"/api/v1/cms/comments", carolToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "成员评论列表")
	memberComments := decodeProbeJSON[probeCMSCommentPage](t, body)
	if memberComments.Total != 1 || len(memberComments.Items) != 1 {
		t.Fatalf("成员评论列表 = %s, 期望只有本人评论", body)
	}
	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/comments/"+commentID+"/moderate", adminToken, map[string]any{"status": "approved"})
	requireProbeStatus(t, resp, body, http.StatusOK, "审核评论")
	resp, body = getText(t, public, base+"/blog/"+article.Slug, nil)
	requireProbeStatus(t, resp, body, http.StatusOK, "公开文章含评论")
	if !strings.Contains(body, "hello comment") {
		t.Fatalf("公开文章缺少已通过评论: %s", body)
	}

	media, mediaBody := probePostFile(t, admin, base+"/api/v1/cms/media", adminToken, "probe.png", probePNG(t))
	requireProbeStatus(t, media, mediaBody, http.StatusCreated, "上传媒体")
	uploaded := decodeProbeJSON[probeCMSMedia](t, mediaBody)
	if uploaded.ID == "" || uploaded.ContentType != "image/png" || uploaded.Width != 2 || uploaded.Height != 3 {
		t.Fatalf("媒体响应不完整: %s", mediaBody)
	}
	resp, payload := getBytes(t, public, base+"/blog/media/"+uploaded.ID)
	requireProbeStatus(t, resp, string(payload), http.StatusOK, "读取媒体")
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("媒体 Content-Type = %q, 期望 image/png", resp.Header.Get("Content-Type"))
	}
	resp, payload = getBytesWithToken(t, admin, base+"/mgr/cms/media/"+uploaded.ID+"/file", adminToken)
	requireProbeStatus(t, resp, string(payload), http.StatusOK, "后台媒体预览")
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("后台媒体 Content-Type = %q, 期望 image/png", resp.Header.Get("Content-Type"))
	}
	editorResp, editorBody := probePostFile(t, admin, base+"/mgr/cms/media/upload", adminToken, "editor.png", probePNG(t))
	requireProbeStatus(t, editorResp, editorBody, http.StatusOK, "编辑器图片上传")
	editorUpload := decodeProbeJSON[struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}](t, editorBody)
	if editorUpload.ID == "" || !strings.Contains(editorUpload.URL, "/blog/media/") {
		t.Fatalf("编辑器上传响应不完整: %s", editorBody)
	}
	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/media/"+uploaded.ID+"/delete", adminToken, map[string]any{})
	requireProbeStatus(t, resp, body, http.StatusOK, "删除媒体")
	resp, _ = getText(t, public, base+"/blog/media/"+uploaded.ID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("删除后媒体状态码 = %d, 期望 404", resp.StatusCode)
	}

	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/settings", adminToken, map[string]any{
		"site_name": "Probe Site", "posts_per_page": 1,
		"comments_enabled": true, "comment_moderation": true, "rss_enabled": true,
	})
	requireProbeStatus(t, resp, body, http.StatusOK, "保存站点设置")
	resp, body = getText(t, public, base+"/blog", nil)
	requireProbeStatus(t, resp, body, http.StatusOK, "公开列表读取设置")
	if !strings.Contains(body, "Probe Site") {
		t.Fatalf("公开站点未使用新站点名: %s", body)
	}

	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/articles", adminToken, map[string]any{
		"category_id": category.ID, "title": "Admin Article", "body_markdown": "admin body",
		"kind": "post", "visibility": "public", "allow_comment": true,
	})
	requireProbeStatus(t, resp, body, http.StatusCreated, "管理员创建文章")
	adminArticle := decodeProbeJSON[probeCMSArticle](t, body)
	resp, body = probePostJSON(t, carol, base+"/api/v1/cms/articles/"+adminArticle.ID, carolToken, map[string]any{
		"category_id": category.ID, "title": "Hijacked", "body_markdown": "hijack",
	})
	requireProbeStatus(t, resp, body, http.StatusForbidden, "成员编辑他人文章")
	resp, body = probeGet(t, carol, base+"/api/v1/cms/articles/"+adminArticle.ID, carolToken)
	requireProbeStatus(t, resp, body, http.StatusNotFound, "成员读取他人文章")
	resp, body = probeGet(t, carol, base+"/api/v1/cms/articles", carolToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "成员文章列表")
	own := decodeProbeJSON[probeCMSArticlePage](t, body)
	if own.Total != 1 || len(own.Items) != 1 || own.Items[0].ID != article.ID {
		t.Fatalf("成员文章列表 = %s, 期望只有本人文章 %s", body, article.ID)
	}
	resp, body = probeGet(t, carol, base+"/api/v1/cms/dashboard", carolToken)
	requireProbeStatus(t, resp, body, http.StatusForbidden, "成员读取概览")
	resp, body = probeGet(t, carol, base+"/api/v1/cms/audit", carolToken)
	requireProbeStatus(t, resp, body, http.StatusForbidden, "成员读取审计")
	resp, body = probeGet(t, carol, base+"/api/v1/cms/media", carolToken)
	requireProbeStatus(t, resp, body, http.StatusForbidden, "成员读取媒体库")

	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/articles", adminToken, map[string]any{
		"category_id": category.ID, "title": "About Probe", "body_markdown": "about body",
		"kind": "page", "visibility": "public",
	})
	requireProbeStatus(t, resp, body, http.StatusCreated, "创建独立单页")
	standalone := decodeProbeJSON[probeCMSArticle](t, body)
	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/articles/"+standalone.ID+"/transition", adminToken, map[string]any{"action": "publish"})
	requireProbeStatus(t, resp, body, http.StatusOK, "发布独立单页")
	resp, body = getText(t, public, base+"/blog/p/"+standalone.Slug, nil)
	requireProbeStatus(t, resp, body, http.StatusOK, "公开独立单页")
	if !strings.Contains(body, "about body") {
		t.Fatalf("公开单页缺少正文: %s", body)
	}
	resp, _ = getText(t, public, base+"/blog/"+standalone.Slug, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("单页经文章地址的状态码 = %d, 期望 404", resp.StatusCode)
	}
	resp, body = probePostForm(t, carol, base+"/blog/"+standalone.Slug+"/comments", carolToken, url.Values{"body": {"page comment"}})
	if resp.StatusCode < http.StatusBadRequest {
		t.Fatalf("单页评论状态码 = %d, 期望 4xx: %s", resp.StatusCode, body)
	}

	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/articles/"+article.ID, adminToken, map[string]any{
		"category_id": category.ID, "title": "CMS Probe Article",
		"body_markdown": "**changed** body", "kind": "post", "visibility": "public", "allow_comment": true,
	})
	requireProbeStatus(t, resp, body, http.StatusOK, "更新文章")
	resp, body = probeGet(t, admin, base+"/api/v1/cms/articles/"+article.ID+"/revisions", adminToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "读取修订")
	revisions := decodeProbeJSON[struct {
		Items []struct {
			ID string `json:"ID"`
		} `json:"items"`
	}](t, body)
	if len(revisions.Items) == 0 {
		t.Fatalf("更新后没有修订: %s", body)
	}
	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/articles/"+article.ID+"/revisions/"+revisions.Items[0].ID+"/restore", adminToken, map[string]any{})
	requireProbeStatus(t, resp, body, http.StatusOK, "恢复修订")
	resp, body = probeGet(t, admin, base+"/api/v1/cms/articles?status=published&kind=post", adminToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "文章筛选")
	list := decodeProbeJSON[probeCMSArticlePage](t, body)
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("已发布文章筛选 = %s, 期望 1 条", body)
	}

	// 路径分页也接受 ?page=N：分页器输出的链接必须真的推进，而不是反复读取路径里的固定页码。 |
	// Path-paged lists also honour ?page=N: pager links must advance instead of re-reading the frozen path segment.
	resp, body = probePostJSON(t, admin, base+"/api/v1/cms/articles/"+adminArticle.ID+"/transition", adminToken, map[string]any{"action": "publish"})
	requireProbeStatus(t, resp, body, http.StatusOK, "发布管理员文章")
	resp, body = getText(t, public, base+"/blog/page/1", nil)
	requireProbeStatus(t, resp, body, http.StatusOK, "公开路径分页第 1 页")
	if !strings.Contains(body, "Admin Article") || !strings.Contains(body, "?page=2") {
		t.Fatalf("路径分页第 1 页缺少文章或下一页链接:\n%s", body)
	}
	resp, body = getText(t, public, base+"/blog/page/1?page=2", nil)
	requireProbeStatus(t, resp, body, http.StatusOK, "公开路径分页第 2 页")
	if !strings.Contains(body, "CMS Probe Article") || strings.Contains(body, "Admin Article") {
		t.Fatalf("路径分页未按查询参数推进:\n%s", body)
	}
}

// probePostForm performs an authorised form POST. | probePostForm 发起带令牌的表单 POST。
func probePostForm(t *testing.T, client *http.Client, target, token string, values url.Values) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("构造表单请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	return doText(t, client, req)
}

// getBytes performs an anonymous GET and returns the response and body. | getBytes 发起匿名 GET 并返回响应与正文。
func getBytes(t *testing.T, client *http.Client, target string) (*http.Response, []byte) {
	t.Helper()
	return getBytesWithToken(t, client, target, "")
}

// getBytesWithToken performs an authorised GET and returns the response and body. |
// getBytesWithToken 发起带令牌的 GET 并返回响应与正文。
func getBytesWithToken(t *testing.T, client *http.Client, target, token string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("读取 %s 响应失败: %v", target, err)
	}
	return resp, buf.Bytes()
}

// probePNG returns a decodable 2x3 PNG. | probePNG 返回一张可解码的 2x3 PNG。
func probePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("编码 PNG 失败: %v", err)
	}
	return buf.Bytes()
}
