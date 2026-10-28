package scaffold_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shibingli/yd/security/authz"
	"github.com/shibingli/yd/tooling/scaffold"
	"github.com/shibingli/ydx/json"
)

// doJSON performs one JSON request with an explicit method. | doJSON 以显式方法执行一次 JSON 请求。
func doJSON(t *testing.T, client *http.Client, method, target, body string, headers map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("构造 JSON 请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return doText(t, client, req)
}

// probeDictionaryBatch is the portal batch response shape used by the test. | probeDictionaryBatch 是测试使用的
// 门户批量响应结构。
type probeDictionaryBatch struct {
	Dictionaries []struct {
		Code  string `json:"code"`
		Name  string `json:"name"`
		Scope string `json:"scope"`
		Items []struct {
			Value     string `json:"value"`
			Label     string `json:"label"`
			IsDefault bool   `json:"default"`
		} `json:"items"`
	} `json:"dictionaries"`
	Missing []string `json:"missing"`
}

// probeDictionaryType is the type response shape used by the test. | probeDictionaryType 是测试使用的类型响应结构。
type probeDictionaryType struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Scope     string `json:"scope"`
	ItemCount int    `json:"item_count"`
	Builtin   bool   `json:"builtin"`
}

// probeDictionaryItem is the item response shape used by the test. | probeDictionaryItem 是测试使用的字典项响应结构。
type probeDictionaryItem struct {
	ID        string `json:"id"`
	Value     string `json:"value"`
	Label     string `json:"label"`
	Status    string `json:"status"`
	IsDefault bool   `json:"default"`
}

// TestGeneratedDictionaryManagement proves the built-in seed, the portal resolver and the back-office
// CRUD all work against a real database. | TestGeneratedDictionaryManagement 证明内置种子、门户解析与后台
// CRUD 在真实数据库上全部可用。
func TestGeneratedDictionaryManagement(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实启动 | skipped in short mode")
	}

	_, base := startProbeAppDBWith(t, "yd-dictionary-probe", scaffold.DBSQLite, func(projDir string) {
		path := filepath.Join(projDir, "configs", "config.yaml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取生成配置失败: %v", err)
		}
		data = []byte(strings.ReplaceAll(string(data), `system_tenant: ""`, `system_tenant: "default"`))
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("写入系统字典租户配置失败: %v", err)
		}
	})

	admin := newProbeClient(t, httpTimeout)
	adminToken := signIn(t, admin, base, probeTenant, probeAdmin, probeAdminPassword)
	createProbeRole(t, admin, base, adminToken, "dictionary-reader", authz.PermDictionaryRead)
	createProbeUser(t, admin, base, adminToken, "probe-dict-reader", "dictionary-reader", "probe-dict-reader@example.com")
	reader := newProbeClient(t, httpTimeout)
	readerToken := signIn(t, reader, base, probeTenant, "probe-dict-reader", probeUserPassword)
	createProbeRole(t, admin, base, adminToken, "dictionary-resolver", authz.PermDictionaryResolve)
	createProbeUser(t, admin, base, adminToken, "probe-dict-resolver", "dictionary-resolver", "probe-dict-resolver@example.com")
	resolver := newProbeClient(t, httpTimeout)
	resolverToken := signIn(t, resolver, base, probeTenant, "probe-dict-resolver", probeUserPassword)

	authHeaders := func(token string) map[string]string {
		return map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json"}
	}

	// The built-in seed is present and the portal resolver returns enabled items with defaults. |
	// 内置种子已写入，门户解析返回启用字典项与默认标记。
	resp, body := getText(t, admin, base+"/api/portal/v1/dictionaries?codes=gender,priority", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("批量解析状态码 = %d:\n%s", resp.StatusCode, body)
	}
	var batch probeDictionaryBatch
	if err := json.Unmarshal([]byte(body), &batch); err != nil {
		t.Fatalf("批量解析响应不是 JSON: %v\n%s", err, body)
	}
	if len(batch.Dictionaries) != 2 || len(batch.Missing) != 0 {
		t.Fatalf("批量解析 = %+v", batch)
	}
	gender := batch.Dictionaries[0]
	if gender.Code != "gender" || len(gender.Items) != 3 {
		t.Fatalf("gender = %+v", gender)
	}
	if gender.Items[2].Value != "unknown" || !gender.Items[2].IsDefault {
		t.Fatalf("gender default = %+v", gender.Items)
	}
	resp, body = getText(t, admin, base+"/api/portal/v1/dictionaries?codes=gender,missing_code", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("含缺失编码的批量解析状态码 = %d:\n%s", resp.StatusCode, body)
	}
	var partial probeDictionaryBatch
	if err := json.Unmarshal([]byte(body), &partial); err != nil || len(partial.Missing) != 1 || partial.Missing[0] != "missing_code" {
		t.Fatalf("批量解析 missing = %q, %v", body, err)
	}
	resp, _ = getText(t, admin, base+"/api/portal/v1/dictionaries?codes=", authHeaders(adminToken))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("空 codes 状态码 = %d，期望 422", resp.StatusCode)
	}

	// The administrator sees the workbench with the built-in dictionaries; the back-office reader sees
	// it without write controls. | 管理员看到带内置字典的工作台；后台只读角色看到的工作台没有写控件。
	resp, page := getText(t, admin, base+"/mgr/dictionaries", map[string]string{"Authorization": "Bearer " + adminToken})
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "gender") || (!strings.Contains(page, "性别") && !strings.Contains(page, "Gender")) || !strings.Contains(page, "task_status") {
		t.Fatalf("管理员字典页状态码 %d:\n%s", resp.StatusCode, page)
	}
	resp, page = getText(t, reader, base+"/mgr/dictionaries", map[string]string{"Authorization": "Bearer " + readerToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("后台只读角色字典页状态码 = %d", resp.StatusCode)
	}
	if strings.Contains(page, `hx-get="/mgr/dictionaries/new"`) || (strings.Contains(page, `/items/`) && strings.Contains(page, `/delete`)) {
		t.Fatalf("后台只读角色看到了写控件:\n%s", page)
	}

	// dictionary:read opens the back office but not the portal resolver; dictionary:resolve opens the
	// resolver but not the back office. | dictionary:read 打开后台但不打开门户解析；dictionary:resolve
	// 打开门户解析但不打开后台。
	resp, _ = getText(t, reader, base+"/api/portal/v1/dictionaries/gender", authHeaders(readerToken))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("后台只读角色解析状态码 = %d，期望 403", resp.StatusCode)
	}
	resp, _ = getText(t, resolver, base+"/mgr/dictionaries", map[string]string{"Authorization": "Bearer " + resolverToken})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("门户解析角色访问后台状态码 = %d，期望 403", resp.StatusCode)
	}
	resp, body = getText(t, resolver, base+"/api/portal/v1/dictionaries/gender", authHeaders(resolverToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"code":"gender"`) {
		t.Fatalf("门户解析角色解析状态码 = %d:\n%s", resp.StatusCode, body)
	}
	resp, _ = doJSON(t, resolver, http.MethodPost, base+"/api/v1/dictionaries",
		`{"code":"forbidden","name":"禁止"}`, authHeaders(resolverToken))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("门户解析角色新建状态码 = %d，期望 403", resp.StatusCode)
	}

	// The system layer is seeded globally. Every back-office reader can see it, but only the
	// configured system tenant (the default tenant in this probe) may write it. Tenant dictionaries
	// win over a system dictionary with the same code. | 系统层全局播种。每个后台读者都能看到它，
	// 但只有配置的系统租户（本探针中的默认租户）可以写入；同编码的租户字典优先于系统字典。
	resp, page = getText(t, admin, base+"/mgr/system-dictionaries", map[string]string{"Authorization": "Bearer " + adminToken})
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "gender") || !strings.Contains(page, "/mgr/system-dictionaries/new") {
		t.Fatalf("管理员系统字典页状态码 %d:\n%s", resp.StatusCode, page)
	}
	resp, page = getText(t, admin, base+"/mgr/system-dictionaries/new", map[string]string{"Authorization": "Bearer " + adminToken})
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, `action="/mgr/system-dictionaries"`) {
		t.Fatalf("系统类型抽屉状态码 %d:\n%s", resp.StatusCode, page)
	}
	resp, page = getText(t, reader, base+"/mgr/system-dictionaries", map[string]string{"Authorization": "Bearer " + readerToken})
	if resp.StatusCode != http.StatusOK || strings.Contains(page, "/mgr/system-dictionaries/new") {
		t.Fatalf("只读角色系统字典页状态码 %d:\n%s", resp.StatusCode, page)
	}
	resp, _ = getText(t, resolver, base+"/mgr/system-dictionaries", map[string]string{"Authorization": "Bearer " + resolverToken})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("解析角色访问系统字典页状态码 = %d，期望 403", resp.StatusCode)
	}
	var systemTypes struct {
		Items []probeDictionaryType `json:"items"`
		Total int                   `json:"total"`
	}
	resp, body = getText(t, admin, base+"/api/v1/system-dictionaries", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || json.Unmarshal([]byte(body), &systemTypes) != nil || systemTypes.Total < 4 {
		t.Fatalf("系统字典列表状态码 = %d:\n%s", resp.StatusCode, body)
	}
	foundSystemGender := false
	for _, item := range systemTypes.Items {
		if item.Code == "gender" && item.Scope == "system" {
			foundSystemGender = true
		}
	}
	if !foundSystemGender {
		t.Fatalf("系统字典列表缺少 gender/system: %+v", systemTypes.Items)
	}
	resp, _ = getText(t, resolver, base+"/api/v1/system-dictionaries", authHeaders(resolverToken))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("解析角色读取系统字典列表状态码 = %d，期望 403", resp.StatusCode)
	}
	resp, body = getText(t, admin, base+"/api/portal/v1/dictionaries/gender", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"scope":"tenant"`) {
		t.Fatalf("租户优先解析 = %d:\n%s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, admin, http.MethodPost, base+"/api/v1/system-dictionaries",
		`{"code":"global_status_probe","name":"全局状态","name_en":"Global status","status":"enabled","sort":900}`,
		authHeaders(adminToken))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("新建系统类型状态码 = %d:\n%s", resp.StatusCode, body)
	}
	var systemType probeDictionaryType
	if err := json.Unmarshal([]byte(body), &systemType); err != nil || systemType.ID == "" || systemType.Scope != "system" {
		t.Fatalf("新建系统类型响应 = %q, %v", body, err)
	}
	resp, page = getText(t, admin, base+"/mgr/system-dictionaries/"+systemType.ID+"/items/new", map[string]string{"Authorization": "Bearer " + adminToken})
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, `action="/mgr/system-dictionaries/`+systemType.ID+`/items"`) {
		t.Fatalf("系统字典项抽屉状态码 %d:\n%s", resp.StatusCode, page)
	}
	resp, body = doJSON(t, admin, http.MethodPost, base+"/api/v1/system-dictionaries/"+systemType.ID+"/items",
		`{"value":"active","label":"启用","label_en":"Active","default":true,"sort":10}`,
		authHeaders(adminToken))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("新建系统字典项状态码 = %d:\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/portal/v1/dictionaries/global_status_probe", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"scope":"system"`) {
		t.Fatalf("系统兜底解析 = %d:\n%s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, admin, http.MethodPost, base+"/api/v1/dictionaries",
		`{"code":"global_status_probe","name":"租户覆盖","status":"enabled","sort":900}`,
		authHeaders(adminToken))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("新建租户覆盖状态码 = %d:\n%s", resp.StatusCode, body)
	}
	var tenantOverride probeDictionaryType
	if err := json.Unmarshal([]byte(body), &tenantOverride); err != nil || tenantOverride.ID == "" {
		t.Fatalf("租户覆盖响应 = %q, %v", body, err)
	}
	resp, body = getText(t, admin, base+"/api/portal/v1/dictionaries/global_status_probe", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"scope":"tenant"`) {
		t.Fatalf("租户覆盖解析 = %d:\n%s", resp.StatusCode, body)
	}
	resp, _ = doJSON(t, admin, http.MethodDelete, base+"/api/v1/dictionaries/"+tenantOverride.ID, "", authHeaders(adminToken))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("删除租户覆盖状态码 = %d", resp.StatusCode)
	}
	resp, body = getText(t, admin, base+"/api/portal/v1/dictionaries/global_status_probe", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"scope":"system"`) {
		t.Fatalf("删除租户覆盖后的系统兜底 = %d:\n%s", resp.StatusCode, body)
	}
	resp, _ = doJSON(t, admin, http.MethodDelete, base+"/api/v1/system-dictionaries/"+systemType.ID, "", authHeaders(adminToken))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("删除系统类型状态码 = %d", resp.StatusCode)
	}
	resp, _ = getText(t, admin, base+"/api/portal/v1/dictionaries/global_status_probe", authHeaders(adminToken))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("删除系统类型后解析状态码 = %d，期望 404", resp.StatusCode)
	}

	// Create a type and two default items; the second default replaces the first. Then resolve through
	// the cache to prove the write invalidated it. | 新建一个类型与两个默认项；第二个默认项替换第一个。
	// 随后经缓存解析，证明写入已经失效缓存。
	resp, body = doJSON(t, admin, http.MethodPost, base+"/api/v1/dictionaries",
		`{"code":"invoice_status","name":"发票状态","name_en":"Invoice status","description":"发票生命周期","status":"enabled","sort":100}`,
		authHeaders(adminToken))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("新建类型状态码 = %d:\n%s", resp.StatusCode, body)
	}
	var created probeDictionaryType
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID == "" {
		t.Fatalf("新建类型响应 = %q, %v", body, err)
	}
	resp, body = doJSON(t, admin, http.MethodPost, base+"/api/v1/dictionaries/"+created.ID+"/items",
		`{"value":"paid","label":"已支付","label_en":"Paid","status":"enabled","default":true,"sort":10}`,
		authHeaders(adminToken))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("新建字典项状态码 = %d:\n%s", resp.StatusCode, body)
	}
	var firstItem probeDictionaryItem
	if err := json.Unmarshal([]byte(body), &firstItem); err != nil || firstItem.ID == "" {
		t.Fatalf("新建字典项响应 = %q, %v", body, err)
	}
	resp, body = doJSON(t, admin, http.MethodPost, base+"/api/v1/dictionaries/"+created.ID+"/items",
		`{"value":"unpaid","label":"未支付","status":"enabled","default":true,"sort":20}`,
		authHeaders(adminToken))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("新建第二个字典项状态码 = %d:\n%s", resp.StatusCode, body)
	}
	var secondItem probeDictionaryItem
	if err := json.Unmarshal([]byte(body), &secondItem); err != nil || secondItem.ID == "" {
		t.Fatalf("第二个字典项响应 = %q, %v", body, err)
	}
	resp, body = getText(t, admin, base+"/api/portal/v1/dictionaries/invoice_status", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("解析新建类型状态码 = %d:\n%s", resp.StatusCode, body)
	}
	var resolved probeDictionaryBatch
	if err := json.Unmarshal([]byte(`{"dictionaries":[`+body+`]}`), &resolved); err != nil {
		t.Fatalf("单个解析响应 = %q: %v", body, err)
	}
	if len(resolved.Dictionaries) != 1 || len(resolved.Dictionaries[0].Items) != 2 {
		t.Fatalf("单个解析 = %+v", resolved)
	}
	for _, item := range resolved.Dictionaries[0].Items {
		if item.Value == "paid" && item.IsDefault {
			t.Fatalf("旧的默认项没有被替换: %+v", resolved.Dictionaries[0].Items)
		}
		if item.Value == "unpaid" && !item.IsDefault {
			t.Fatalf("新的默认项没有生效: %+v", resolved.Dictionaries[0].Items)
		}
	}

	// The HTML workbench shares the same rules: create a type and an item through the drawer form
	// routes, then read the refreshed fragment. | HTML 工作台共用同一套规则：经抽屉表单路由新建类型与
	// 字典项，再读取刷新后的片段。
	resp, body = postForm(t, admin, base+"/mgr/dictionaries", map[string]string{
		"code": "html_status", "name": "HTML 状态", "name_en": "HTML status", "status": "enabled", "sort": "5",
	}, map[string]string{"Authorization": "Bearer " + adminToken, "HX-Request": "true"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "html_status") {
		t.Fatalf("HTML 新建类型状态码 = %d:\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/v1/dictionaries?q=html_status&page_size=1", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("查询 HTML 类型状态码 = %d:\n%s", resp.StatusCode, body)
	}
	var htmlTypes struct {
		Items []probeDictionaryType `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &htmlTypes); err != nil || len(htmlTypes.Items) != 1 {
		t.Fatalf("HTML 类型列表 = %q, %v", body, err)
	}
	resp, body = getText(t, admin, base+"/mgr/dictionaries/"+htmlTypes.Items[0].ID+"/edit", map[string]string{"Authorization": "Bearer " + adminToken, "HX-Request": "true"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "html_status") {
		t.Fatalf("HTML 编辑抽屉状态码 = %d:\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/mgr/dictionaries/"+htmlTypes.Items[0].ID+"/items/new", map[string]string{"Authorization": "Bearer " + adminToken, "HX-Request": "true"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "html_status") {
		t.Fatalf("HTML 新建字典项抽屉状态码 = %d:\n%s", resp.StatusCode, body)
	}
	resp, body = postForm(t, admin, base+"/mgr/dictionaries/"+htmlTypes.Items[0].ID+"/items", map[string]string{
		"value": "html_value", "label": "HTML 值", "status": "enabled", "sort": "1",
	}, map[string]string{"Authorization": "Bearer " + adminToken, "HX-Request": "true"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "html_value") {
		t.Fatalf("HTML 新建字典项状态码 = %d:\n%s", resp.StatusCode, body)
	}
	resp, page = getText(t, admin, base+"/mgr/dictionaries?type="+htmlTypes.Items[0].ID, map[string]string{"Authorization": "Bearer " + adminToken, "HX-Request": "true"})
	if resp.StatusCode != http.StatusOK || (!strings.Contains(page, "HTML 状态") && !strings.Contains(page, "HTML status")) || !strings.Contains(page, "html_value") {
		t.Fatalf("HTML 工作台状态码 = %d:\n%s", resp.StatusCode, page)
	}

	// The item search field and the back-office item search filter share the same query. |
	// 字典项搜索框与后台字典项检索共用同一套查询。
	resp, body = getText(t, admin, base+"/api/v1/dictionaries/"+htmlTypes.Items[0].ID+"/items?q=html_value", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTML 类型字典项列表状态码 = %d:\n%s", resp.StatusCode, body)
	}
	var htmlItems struct {
		Items []probeDictionaryItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &htmlItems); err != nil || len(htmlItems.Items) != 1 {
		t.Fatalf("HTML 类型字典项列表 = %q, %v", body, err)
	}
	resp, body = getText(t, admin, base+"/mgr/dictionaries/"+htmlTypes.Items[0].ID+"/items/"+htmlItems.Items[0].ID+"/edit", map[string]string{"Authorization": "Bearer " + adminToken, "HX-Request": "true"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "html_value") {
		t.Fatalf("HTML 编辑字典项抽屉状态码 = %d:\n%s", resp.StatusCode, body)
	}
	resp, page = getText(t, admin, base+"/mgr/dictionaries?type="+htmlTypes.Items[0].ID+"&iq=html_value", map[string]string{"Authorization": "Bearer " + adminToken, "HX-Request": "true"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "html_value") {
		t.Fatalf("HTML 字典项搜索结果状态码 = %d:\n%s", resp.StatusCode, page)
	}
	resp, page = getText(t, admin, base+"/mgr/dictionaries?type="+htmlTypes.Items[0].ID+"&iq=absent-value", map[string]string{"Authorization": "Bearer " + adminToken, "HX-Request": "true"})
	if resp.StatusCode != http.StatusOK || strings.Contains(page, "html_value") {
		t.Fatalf("HTML 字典项搜索过滤失败（状态码 %d）:\n%s", resp.StatusCode, page)
	}

	// The HTML update routes share the same immutable code/value rule as the API. |
	// HTML 更新路由与 API 共用同一套不可变编码/机器值规则。
	resp, body = postForm(t, admin, base+"/mgr/dictionaries/"+htmlTypes.Items[0].ID, map[string]string{
		"name": "HTML 状态（新）", "name_en": "HTML status updated", "status": "enabled", "sort": "5",
	}, map[string]string{"Authorization": "Bearer " + adminToken, "HX-Request": "true"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "HTML status updated") {
		t.Fatalf("HTML 更新类型状态码 = %d:\n%s", resp.StatusCode, body)
	}
	resp, body = postForm(t, admin, base+"/mgr/dictionaries/"+htmlTypes.Items[0].ID+"/items", map[string]string{
		"id": htmlItems.Items[0].ID, "value": "html_value", "label": "HTML 值（新）", "label_en": "HTML value updated", "status": "enabled", "sort": "2",
	}, map[string]string{"Authorization": "Bearer " + adminToken, "HX-Request": "true"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "HTML value updated") {
		t.Fatalf("HTML 更新字典项状态码 = %d:\n%s", resp.StatusCode, body)
	}

	resp, _ = doJSON(t, admin, http.MethodDelete, base+"/api/v1/dictionaries/"+htmlTypes.Items[0].ID, `{}`, authHeaders(adminToken))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("清理 HTML 类型状态码 = %d，期望 204", resp.StatusCode)
	}

	// Duplicate code and value are reported as conflicts. | 重复编码与重复值都返回冲突。
	resp, _ = doJSON(t, admin, http.MethodPost, base+"/api/v1/dictionaries",
		`{"code":"invoice_status","name":"重复"}`, authHeaders(adminToken))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("重复编码状态码 = %d，期望 409", resp.StatusCode)
	}
	resp, _ = doJSON(t, admin, http.MethodPost, base+"/api/v1/dictionaries/"+created.ID+"/items",
		`{"value":"paid","label":"重复"}`, authHeaders(adminToken))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("重复值状态码 = %d，期望 409", resp.StatusCode)
	}

	// Updating the type and an item is visible through the cache, and a built-in type cannot be deleted. |
	// 更新类型与字典项后经缓存可见；内置类型不可删除。
	resp, body = doJSON(t, admin, http.MethodPut, base+"/api/v1/dictionaries/"+created.ID,
		`{"name":"发票状态（新）","status":"enabled","sort":100}`, authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "发票状态（新）") {
		t.Fatalf("更新类型状态码 = %d:\n%s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, admin, http.MethodPut, base+"/api/v1/dictionaries/"+created.ID+"/items/"+secondItem.ID,
		`{"label":"未支付（新）","status":"disabled","default":false,"sort":20}`, authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"status":"disabled"`) {
		t.Fatalf("更新字典项状态码 = %d:\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/v1/dictionaries/"+created.ID, authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "unpaid") {
		t.Fatalf("后台类型详情默认应包含停用项（状态码 %d）:\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/v1/dictionaries/"+created.ID+"?include_disabled=false", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || strings.Contains(body, "unpaid") {
		t.Fatalf("include_disabled=false 未过滤停用项（状态码 %d）:\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/v1/dictionaries/"+created.ID+"/items?status=enabled", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || strings.Contains(body, "unpaid") {
		t.Fatalf("字典项 status 过滤未生效（状态码 %d）:\n%s", resp.StatusCode, body)
	}
	resp, _ = doJSON(t, admin, http.MethodDelete, base+"/api/v1/dictionaries/"+created.ID, `{}`, authHeaders(adminToken))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("删除类型状态码 = %d，期望 204", resp.StatusCode)
	}
	resp, body = getText(t, admin, base+"/api/portal/v1/dictionaries/invoice_status", authHeaders(adminToken))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("删除后解析状态码 = %d:\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/v1/dictionaries?q=gender&page_size=1", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"builtin":true`) {
		t.Fatalf("内置类型列表状态码 = %d:\n%s", resp.StatusCode, body)
	}
	var typeList struct {
		Items []probeDictionaryType `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &typeList); err != nil || len(typeList.Items) != 1 {
		t.Fatalf("内置类型列表 = %q, %v", body, err)
	}
	resp, _ = doJSON(t, admin, http.MethodDelete, base+"/api/v1/dictionaries/"+typeList.Items[0].ID, `{}`, authHeaders(adminToken))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("删除内置类型状态码 = %d，期望 409", resp.StatusCode)
	}
}

// TestGeneratedDictionarySystemReadOnly proves the default (no configured system tenant) keeps the
// system layer readable but immutable, even for an owner holding dictionary:system_manage. |
// TestGeneratedDictionarySystemReadOnly 证明默认（未配置系统租户）时系统层可读但不可改，
// 即使所有者持有 dictionary:system_manage。
func TestGeneratedDictionarySystemReadOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实启动 | skipped in short mode")
	}

	_, base := startProbeAppDBWith(t, "yd-dictionary-readonly", scaffold.DBSQLite, nil)
	admin := newProbeClient(t, httpTimeout)
	adminToken := signIn(t, admin, base, probeTenant, probeAdmin, probeAdminPassword)
	authHeaders := map[string]string{"Authorization": "Bearer " + adminToken, "Accept": "application/json"}

	resp, page := getText(t, admin, base+"/mgr/system-dictionaries", authHeaders)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "gender") {
		t.Fatalf("系统字典只读页状态码 = %d:\n%s", resp.StatusCode, page)
	}
	if strings.Contains(page, "/mgr/system-dictionaries/new") {
		t.Fatalf("未配置系统租户时仍渲染了系统写控件:\n%s", page)
	}
	resp, body := doJSON(t, admin, http.MethodPost, base+"/api/v1/system-dictionaries",
		`{"code":"readonly_probe","name":"只读"}`, authHeaders)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("未配置系统租户时新建系统类型状态码 = %d，期望 403:\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/v1/system-dictionaries", authHeaders)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"scope":"system"`) {
		t.Fatalf("系统字典只读列表状态码 = %d:\n%s", resp.StatusCode, body)
	}
}
