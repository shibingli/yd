package scaffold_test

import (
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "github.com/glebarez/sqlite"
	"github.com/shibingli/yd/tooling/scaffold"
	"github.com/shibingli/ydx/json"
	"github.com/shibingli/ydx/websocket"
)

// 消息中心端到端探针使用的载荷与断言常量。
// Payload and assertion constants of the message-centre end-to-end probe.
const (
	// probeFriendMessage is the greeting of the friend request. | probeFriendMessage 是好友申请的招呼语。
	probeFriendMessage = "probe says hello"
	// probeMessageToken is the unique body token the mixed search has to find. | probeMessageToken 是混合检索必须命中的唯一正文标记。
	probeMessageToken = "probe-unique-message-token"
	// probeSearchTitle is the title token the mixed search has to find as well. | probeSearchTitle 是混合检索同样必须命中的标题标记。
	probeSearchTitle = "ProbeSearchTitle"
	// probeAttachmentText is the attachment body. | probeAttachmentText 是附件正文。
	probeAttachmentText = "message centre attachment probe\n"
)

// 消息中心 JSON API 的最小响应结构：只声明断言真正读取的字段，服务端多返回的字段不会让用例失效。
// Minimal response shapes of the message-centre JSON API: only the fields the assertions actually read are declared, so
// extra fields returned by the server never break a case.
type probeProfile struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Active      bool   `json:"active"`
}

// probeCandidate is one user-search result. | probeCandidate 是一条用户检索结果。
type probeCandidate struct {
	Profile  probeProfile `json:"profile"`
	Relation string       `json:"relation"`
}

// probeCandidates is the user-search envelope. | probeCandidates 是用户检索的响应信封。
type probeCandidates struct {
	Candidates []probeCandidate `json:"candidates"`
}

// probeFriendRequest is one friend request. | probeFriendRequest 是一条好友申请。
type probeFriendRequest struct {
	ID          string `json:"id"`
	RequesterID string `json:"requester_id"`
	AddresseeID string `json:"addressee_id"`
	Status      string `json:"status"`
}

// probeFriendSummary is the friend summary envelope. | probeFriendSummary 是好友汇总的响应信封。
type probeFriendSummary struct {
	Friends []struct {
		Profile probeProfile `json:"profile"`
	} `json:"friends"`
	Incoming []probeFriendRequest `json:"incoming"`
}

// probeConversation is one conversation with its unread count. | probeConversation 是一个会话及其未读数。
type probeConversation struct {
	ID     string        `json:"id"`
	Kind   string        `json:"kind"`
	Title  string        `json:"title"`
	Unread int           `json:"unread"`
	Peer   *probeProfile `json:"peer"`
}

// probeConversations is the conversation-list envelope. | probeConversations 是会话列表的响应信封。
type probeConversations struct {
	Conversations []probeConversation `json:"conversations"`
}

// probeTag is one group or label. | probeTag 是一个分组或标签。
type probeTag struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

// probeAttachment is one attachment record. | probeAttachment 是一条附件记录。
type probeAttachment struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	ContentType string  `json:"content_type"`
	Size        int64   `json:"size"`
	Status      string  `json:"status"`
	MessageID   *string `json:"message_id"`
}

// probeMessage is one message view. | probeMessage 是一条消息视图。
type probeMessage struct {
	CreatedAt        string            `json:"created_at"`
	ID               string            `json:"id"`
	ConversationID   string            `json:"conversation_id"`
	SenderID         string            `json:"sender_id"`
	Status           string            `json:"status"`
	TypeKey          string            `json:"type_key"`
	Title            string            `json:"title"`
	BodyMarkdown     string            `json:"body_markdown"`
	Pinned           bool              `json:"pinned"`
	Attachments      []probeAttachment `json:"attachments"`
	Tags             []probeTag        `json:"tags"`
	Groups           []probeTag        `json:"groups"`
	ReadCount        int               `json:"read_count"`
	RecipientCount   int               `json:"recipient_count"`
	Mine             bool              `json:"mine"`
	ConversationKind string            `json:"conversation_kind"`
}

// probeMessagePage is one page of the mixed search. | probeMessagePage 是混合检索的一页。
type probeMessagePage struct {
	Messages []probeMessage `json:"messages"`
	Total    int            `json:"total"`
}

// probeAuthHeader 返回携带令牌的请求头。 | probeAuthHeader returns the headers carrying the bearer token.
func probeAuthHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// probeGet 发起带令牌的 GET 请求。 | probeGet performs an authorised GET request.
func probeGet(t *testing.T, client *http.Client, target, token string) (*http.Response, string) {
	t.Helper()

	return getText(t, client, target, probeAuthHeader(token))
}

// probePostJSON 以 JSON 载荷发起带令牌的 POST 请求。 | probePostJSON performs an authorised POST with a JSON payload.
func probePostJSON(t *testing.T, client *http.Client, target, token string, payload any) (*http.Response, string) {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("序列化请求载荷失败: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	return doText(t, client, req)
}

// probePostFile 以 multipart 表单上传一个文件。 | probePostFile uploads one file as a multipart form.
func probePostFile(t *testing.T, client *http.Client, target, token, name string, content []byte) (*http.Response, string) {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("构造上传表单失败: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入上传内容失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭上传表单失败: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, target, &buf)
	if err != nil {
		t.Fatalf("构造上传请求失败: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return doText(t, client, req)
}

// decodeProbeJSON 解析一份 JSON 响应；失败时把原始响应体一并打印。 | decodeProbeJSON decodes a JSON response and prints the raw body on failure.
func decodeProbeJSON[T any](t *testing.T, body string) T {
	t.Helper()

	var value T
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatalf("解析 JSON 响应失败: %v\n%s", err, body)
	}
	return value
}

// requireProbeStatus 断言状态码并打印响应体。 | requireProbeStatus asserts the status code and prints the body on failure.
func requireProbeStatus(t *testing.T, resp *http.Response, body string, want int, action string) {
	t.Helper()

	if resp.StatusCode != want {
		t.Fatalf("%s 状态码 = %d, 期望 %d\n%s", action, resp.StatusCode, want, body)
	}
}

// hasProbeMessage 报告一页消息里是否包含指定 ID。 | hasProbeMessage reports whether a page contains the given message id.
func hasProbeMessage(page probeMessagePage, id string) bool {
	for _, message := range page.Messages {
		if message.ID == id {
			return true
		}
	}
	return false
}

// hasProbeTag 报告标签集合里是否包含指定 ID。 | hasProbeTag reports whether a tag list contains the given id.
func hasProbeTag(tags []probeTag, id string) bool {
	for _, tag := range tags {
		if tag.ID == id {
			return true
		}
	}
	return false
}

// TestGeneratedAppMessageCentre 端到端验证生成项目的消息中心：好友生命周期、单聊、未读/已读、混合检索、
// 编辑与撤回、管理员治理、系统公告与置顶、类型/分组/标签生命周期、附件与 WebSocket 实时推送。
// TestGeneratedAppMessageCentre runs the generated project's message centre end to end: the friend lifecycle, direct
// chat, unread/read state, mixed search, edit and recall, administrative moderation, announcements and pinning, the
// type/group/tag lifecycle, attachments and the WebSocket push.
//
// 项目必须带真实数据库：消息中心的 12 张表与种子数据都落在 SQLite 上，`--db none` 的生成项目按设计报告不可用。
// The project needs a real database: the message centre's twelve tables and the seed data live in SQLite, while a
// project generated with `--db none` reports the centre as unavailable by design.
func TestGeneratedAppMessageCentre(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实服务验证 | skipped in short mode")
	}

	_, base := startProbeAppDBWith(t, "yd-message-probe", scaffold.DBSQLite, nil)

	admin := newProbeClient(t, httpTimeout)
	adminToken := signIn(t, admin, base, probeTenant, probeAdmin, probeAdminPassword)

	// 第二个账号是普通成员：拥有消息自助能力（读、发、改自己的、撤自己的），但没有系统与管理能力。
	// The second account is a plain member: it holds the self-service message capabilities (read, send, edit and recall
	// its own) and none of the system or management capabilities.
	createProbeUser(t, admin, base, adminToken, "bob", "member", "bob@example.com")
	bob := newProbeClient(t, httpTimeout)
	bobToken := signIn(t, bob, base, probeTenant, "bob", probeUserPassword)

	var (
		adminID      string
		bobID        string
		conversation string
		firstMessage string
		adminMessage string
		announcement string
		systemRoom   string
	)

	t.Run("好友生命周期", func(t *testing.T) {
		resp, body := probeGet(t, bob, base+"/api/portal/v1/friends/search?q="+probeAdmin, bobToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "检索管理员")
		candidates := decodeProbeJSON[probeCandidates](t, body)
		for _, candidate := range candidates.Candidates {
			if candidate.Profile.Username == probeAdmin {
				adminID = candidate.Profile.ID
				break
			}
		}
		if adminID == "" {
			t.Fatalf("用户检索未返回管理员 %q: %s", probeAdmin, body)
		}

		// 普通用户先打开一次会话列表：公告频道按需懒加入，之后管理员发布的系统公告才能被该用户检索到。
		// The plain user opens the conversation list once: the announcement channel joins lazily, after which an
		// administrator announcement is visible to that user.
		resp, body = probeGet(t, bob, base+"/api/portal/v1/messages/conversations", bobToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "打开会话列表")

		resp, body = probePostJSON(t, bob, base+"/api/portal/v1/friends/requests", bobToken, map[string]any{
			"friend_id": adminID,
			"message":   probeFriendMessage,
		})
		requireProbeStatus(t, resp, body, http.StatusCreated, "发送好友申请")
		request := decodeProbeJSON[probeFriendRequest](t, body)
		if request.ID == "" || request.RequesterID == "" || request.Status != "pending" {
			t.Fatalf("好友申请响应不完整: %s", body)
		}
		bobID = request.RequesterID

		resp, body = probeGet(t, admin, base+"/api/portal/v1/friends", adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "读取好友汇总")
		summary := decodeProbeJSON[probeFriendSummary](t, body)
		incoming := false
		for _, item := range summary.Incoming {
			if item.ID == request.ID {
				incoming = true
				break
			}
		}
		if !incoming {
			t.Fatalf("管理员的好友汇总里没有待处理申请 %s: %s", request.ID, body)
		}

		resp, body = probePostJSON(t, admin, base+"/api/portal/v1/friends/requests/"+request.ID+"/accept", adminToken, map[string]any{})
		requireProbeStatus(t, resp, body, http.StatusNoContent, "接受好友申请")

		resp, body = probeGet(t, bob, base+"/api/portal/v1/friends", bobToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "读取好友列表")
		summary = decodeProbeJSON[probeFriendSummary](t, body)
		friends := false
		for _, friend := range summary.Friends {
			if friend.Profile.ID == adminID {
				friends = true
				break
			}
		}
		if !friends {
			t.Fatalf("接受申请后 bob 的好友列表不含管理员: %s", body)
		}

		// 检索自己不应返回自己；已是好友的候选关系必须标记为 friend。
		// A search for oneself must not return oneself; an accepted friend must be reported with the friend relation.
		resp, body = probeGet(t, admin, base+"/api/portal/v1/friends/search?q="+probeAdmin, adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "检索自己")
		selfCandidates := decodeProbeJSON[probeCandidates](t, body)
		for _, candidate := range selfCandidates.Candidates {
			if candidate.Profile.ID == adminID {
				t.Errorf("好友检索返回了当前用户自己: %s", body)
			}
		}

		resp, body = probeGet(t, admin, base+"/api/portal/v1/friends/search?q=bob", adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "检索已是好友的候选")
		bobCandidates := decodeProbeJSON[probeCandidates](t, body)
		foundBob := false
		for _, candidate := range bobCandidates.Candidates {
			if candidate.Profile.ID != bobID {
				continue
			}
			foundBob = true
			if candidate.Relation != "friend" {
				t.Errorf("已是好友的关系 = %q, 期望 friend: %s", candidate.Relation, body)
			}
		}
		if !foundBob {
			t.Fatalf("好友检索未返回 bob: %s", body)
		}

		// 添加抽屉必须自行复核关系：已是好友与自己都不能再看到申请表单。
		// The add drawer re-checks the relation itself: neither an existing friend nor the actor may see the request form.
		resp, body = probeGet(t, admin, base+"/friends/new?friend_id="+bobID, adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "打开已是好友的添加抽屉")
		if strings.Contains(body, `action="/friends/requests"`) {
			t.Errorf("已是好友仍渲染好友申请表单:\n%s", body)
		}
		if !strings.Contains(body, "Already friends") {
			t.Errorf("已是好友的添加抽屉缺少关系说明:\n%s", body)
		}

		resp, body = probeGet(t, admin, base+"/friends/new?friend_id="+adminID, adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "打开自己的添加抽屉")
		if strings.Contains(body, `action="/friends/requests"`) {
			t.Errorf("自己仍渲染好友申请表单:\n%s", body)
		}
		if !strings.Contains(body, "You cannot add yourself as a friend") {
			t.Errorf("自己的添加抽屉缺少提示:\n%s", body)
		}
	})

	// 实时通道在发送之前建立：先读一帧初始未读徽标，确认订阅已经生效，避免握手与订阅之间的竞争。
	// The realtime channel opens before the send: reading one initial unread-badge frame proves the subscription is in
	// place, which removes the race between the handshake and the subscription.
	socket := dialMessageSocket(t, base, adminToken)
	defer func() { _ = socket.CloseNow() }()

	t.Run("单聊与实时推送", func(t *testing.T) {
		resp, body := probePostJSON(t, bob, base+"/api/portal/v1/messages", bobToken, map[string]any{
			"recipient_id":  adminID,
			"type_key":      "normal",
			"title":         probeSearchTitle,
			"body_markdown": "Hello **" + probeMessageToken + "**",
		})
		requireProbeStatus(t, resp, body, http.StatusCreated, "发送单聊消息")
		sent := decodeProbeJSON[probeMessage](t, body)
		if sent.ID == "" || sent.ConversationID == "" {
			t.Fatalf("消息响应缺少 ID 或会话: %s", body)
		}
		conversation = sent.ConversationID
		firstMessage = sent.ID
		if sent.ConversationKind != "direct" {
			t.Errorf("单聊会话类型 = %q, 期望 direct", sent.ConversationKind)
		}
		if sent.SenderID != bobID || !sent.Mine {
			t.Errorf("消息发送者或归属标记异常: %s", body)
		}
		if !strings.HasSuffix(sent.CreatedAt, "Z") {
			t.Errorf("消息时间戳 = %q, 期望 UTC（Z 结尾）", sent.CreatedAt)
		}

		// 实时事件只携带 ID 与元数据；接收方据此重新读取，不把正文放进推送。
		// The realtime event carries ids and metadata only; receivers refetch, so no body ever travels in the push.
		deadline := time.Now().Add(sseTimeout)
		var eventFrame string
		for {
			if err := socket.SetReadDeadline(deadline); err != nil {
				t.Fatalf("设置读超时失败: %v", err)
			}
			_, frame, err := socket.ReadMessage()
			if err != nil {
				t.Fatalf("未收到 message.created 实时事件: %v\nno message.created event was pushed: %v", err, err)
			}
			var event struct {
				YdEvent   string `json:"yd_event"`
				MessageID string `json:"message_id"`
			}
			if json.Unmarshal(frame, &event) != nil {
				continue
			}
			if event.YdEvent == "message.created" && event.MessageID == sent.ID {
				eventFrame = string(frame)
				break
			}
		}
		if strings.Contains(eventFrame, probeMessageToken) {
			t.Errorf("实时事件携带了消息正文，应只有 ID 与元数据: %s", eventFrame)
		}
	})

	t.Run("群发", func(t *testing.T) {
		// 两个以上收件人走群会话；成员必须都是发起人的好友，服务端逐人校验。
		// Two or more recipients take the group path; every member must be a friend of the sender, checked server-side.
		resp, body := probePostJSON(t, bob, base+"/api/portal/v1/messages", bobToken, map[string]any{
			"group_title":   "Probe group",
			"group_members": []string{adminID, bobID},
			"body_markdown": "hello group",
		})
		requireProbeStatus(t, resp, body, http.StatusCreated, "群发消息")
		groupMessage := decodeProbeJSON[probeMessage](t, body)
		if groupMessage.ConversationKind != "group" || groupMessage.ConversationID == "" {
			t.Fatalf("群发响应不是群会话: %s", body)
		}

		resp, body = probeGet(t, admin, base+"/api/portal/v1/messages/conversations", adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "群发后读取管理员会话列表")
		list := decodeProbeJSON[probeConversations](t, body)
		found := false
		for _, item := range list.Conversations {
			if item.ID == groupMessage.ConversationID && item.Kind == "group" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("管理员会话列表里没有群 %s: %s", groupMessage.ConversationID, body)
		}
	})

	t.Run("未读与已读水位", func(t *testing.T) {
		resp, body := probeGet(t, admin, base+"/api/portal/v1/messages/conversations", adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "读取管理员会话列表")
		list := decodeProbeJSON[probeConversations](t, body)
		found := false
		for _, item := range list.Conversations {
			if item.ID != conversation {
				continue
			}
			found = true
			if item.Unread != 1 {
				t.Errorf("打开前未读数 = %d, 期望 1", item.Unread)
			}
		}
		if !found {
			t.Fatalf("管理员会话列表里没有刚建立的单聊 %s: %s", conversation, body)
		}

		resp, body = probePostJSON(t, admin, base+"/api/portal/v1/messages/"+conversation+"/read", adminToken, map[string]any{})
		requireProbeStatus(t, resp, body, http.StatusNoContent, "标记会话已读")

		// 读者本人也必须收到已读事件；事件到达后 deliverMessageEvent 会立即推送刷新后的角标帧。
		// 否则右上角角标与菜单强调要等整页刷新才消失。
		// The reader must receive its own read event; deliverMessageEvent pushes the refreshed badges right after it.
		// Otherwise the corner badge and the menu emphasis only clear after a full page reload.
		readDeadline := time.Now().Add(sseTimeout)
		for {
			if err := socket.SetReadDeadline(readDeadline); err != nil {
				t.Fatalf("设置读超时失败: %v", err)
			}
			_, frame, err := socket.ReadMessage()
			if err != nil {
				t.Fatalf("未收到读者自己的已读事件: %v", err)
			}
			if strings.Contains(string(frame), `"yd_event":"message.read"`) && strings.Contains(string(frame), conversation) {
				break
			}
		}

		resp, body = probeGet(t, admin, base+"/api/portal/v1/messages/conversations", adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "读取已读后的会话列表")
		list = decodeProbeJSON[probeConversations](t, body)
		for _, item := range list.Conversations {
			if item.ID == conversation && item.Unread != 0 {
				t.Errorf("已读后未读数 = %d, 期望 0", item.Unread)
			}
		}

		// 发送者看到的已读人数由对方的已读水位推导：这正是「不维护每消息回执表」仍能回答已读状态的方式。
		// The sender's read count is derived from the peer's watermark: that is how read state stays answerable without a
		// per-message receipt table.
		resp, body = probeGet(t, bob, base+"/api/portal/v1/messages/"+firstMessage, bobToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "作者读取已读回执")
		receipt := decodeProbeJSON[probeMessage](t, body)
		if receipt.RecipientCount != 1 || receipt.ReadCount != 1 {
			t.Errorf("已读计数 = %d/%d, 期望 1/1: %s", receipt.ReadCount, receipt.RecipientCount, body)
		}
	})

	t.Run("WebSocket 快捷发送", func(t *testing.T) {
		// 同一条连接也可以上行快捷发送；服务端不回显入站内容，消息落库后经 API 与事件可见。
		// The same connection also carries an inbound quick reply; the server never echoes inbound content, and the
		// message becomes visible through the API and the event stream once stored.
		raw, err := json.Marshal(map[string]any{
			"action":          "send",
			"conversation_id": conversation,
			"body":            "quick reply over websocket",
		})
		if err != nil {
			t.Fatalf("序列化快捷发送失败: %v", err)
		}
		if err := socket.SetWriteDeadline(time.Now().Add(sseTimeout)); err != nil {
			t.Fatalf("设置写超时失败: %v", err)
		}
		if err := socket.WriteMessage(websocket.TextMessage, raw); err != nil {
			t.Fatalf("WebSocket 快捷发送失败: %v", err)
		}
		deadline := time.Now().Add(sseTimeout)
		for {
			resp, body := probeGet(t, admin, base+"/api/portal/v1/messages?q=quick+reply", adminToken)
			if resp.StatusCode == http.StatusOK {
				if page := decodeProbeJSON[probeMessagePage](t, body); page.Total > 0 {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("WebSocket 快捷发送未落库: %s", body)
			}
			time.Sleep(50 * time.Millisecond)
		}
	})

	t.Run("只读账号不能经 WebSocket 发送", func(t *testing.T) {
		// 聊天通道只要求 message:read 即可建立；写入能力必须在处理入站 send 时单独校验，
		// 否则审计员可以绕过 HTTP 路由上的 message:send 守卫。
		// The socket only requires message:read to connect; the write capability has to be checked separately when an
		// inbound send arrives, otherwise an auditor can bypass the message:send guard on the HTTP route.
		createProbeUser(t, admin, base, adminToken, "ws-auditor", "auditor", "ws-auditor@example.com")
		auditor := newProbeClient(t, httpTimeout)
		auditorToken := signIn(t, auditor, base, probeTenant, "ws-auditor", probeUserPassword)
		auditorSocket := dialMessageSocket(t, base, auditorToken)
		defer func() { _ = auditorSocket.CloseNow() }()

		const probeBody = "auditor must not write through websocket"
		raw, err := json.Marshal(map[string]any{
			"action":          "send",
			"conversation_id": conversation,
			"body":            probeBody,
		})
		if err != nil {
			t.Fatalf("序列化 auditor 发送失败: %v", err)
		}
		if err := auditorSocket.SetWriteDeadline(time.Now().Add(sseTimeout)); err != nil {
			t.Fatalf("设置写超时失败: %v", err)
		}
		if err := auditorSocket.WriteMessage(websocket.TextMessage, raw); err != nil {
			t.Fatalf("auditor WebSocket 写入失败: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
		resp, body := probeGet(t, admin, base+"/api/portal/v1/messages?q="+url.QueryEscape(probeBody), adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "检索 auditor 尝试发送的消息")
		if page := decodeProbeJSON[probeMessagePage](t, body); page.Total != 0 {
			t.Fatalf("只读账号经 WebSocket 写入成功: %s", body)
		}
	})

	t.Run("混合全文检索", func(t *testing.T) {
		// 正文标记、标题标记、发送人与类型是四个独立条件，必须都能命中同一条消息。
		// The body token, the title token, the sender and the type are four independent conditions, and all of them have
		// to hit the very same message.
		queries := map[string]string{
			"正文":  "q=" + probeMessageToken,
			"标题":  "q=" + probeSearchTitle,
			"发送人": "sender=" + bobID,
			"类型":  "type_key=normal",
			"组合":  "q=" + probeMessageToken + "&type_key=normal&sender=" + bobID,
		}
		for name, query := range queries {
			resp, body := probeGet(t, admin, base+"/api/portal/v1/messages?"+query, adminToken)
			requireProbeStatus(t, resp, body, http.StatusOK, "混合检索·"+name)
			page := decodeProbeJSON[probeMessagePage](t, body)
			if page.Total < 1 || !hasProbeMessage(page, firstMessage) {
				t.Errorf("混合检索·%s 未命中消息 %s:\n%s", name, firstMessage, body)
			}
		}

		// 不存在的词不得命中：检索按空白分词后逐词 AND，不是「有任意一个词就算」。
		// A term that does not exist must not match: the query is tokenised and every term is ANDed, rather than any one
		// of them sufficing.
		resp, body := probeGet(t, admin, base+"/api/portal/v1/messages?q="+probeMessageToken+"+no-such-term", adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "混合检索·无命中")
		page := decodeProbeJSON[probeMessagePage](t, body)
		if page.Total != 0 {
			t.Errorf("不存在的检索词命中了 %d 条消息, 期望 0:\n%s", page.Total, body)
		}
	})

	t.Run("编辑与撤回", func(t *testing.T) {
		resp, body := probePostJSON(t, bob, base+"/api/portal/v1/messages/"+firstMessage, bobToken, map[string]any{
			"type_key":      "normal",
			"title":         probeSearchTitle,
			"body_markdown": "Edited **" + probeMessageToken + "**",
		})
		requireProbeStatus(t, resp, body, http.StatusOK, "作者编辑消息")
		edited := decodeProbeJSON[probeMessage](t, body)
		if !strings.Contains(edited.BodyMarkdown, "Edited") {
			t.Errorf("编辑后正文 = %q, 期望包含 Edited", edited.BodyMarkdown)
		}

		// 管理员先发一条单聊消息，bob 既不是作者也没有管理能力，编辑与撤回都必须被拒绝。
		// The administrator sends a direct message first; bob is neither the author nor a moderator, so both editing and
		// recalling it have to be refused.
		resp, body = probePostJSON(t, admin, base+"/api/portal/v1/messages", adminToken, map[string]any{
			"recipient_id":  bobID,
			"body_markdown": "administrator direct message",
		})
		requireProbeStatus(t, resp, body, http.StatusCreated, "管理员发送单聊消息")
		adminMessage = decodeProbeJSON[probeMessage](t, body).ID

		resp, body = probePostJSON(t, bob, base+"/api/portal/v1/messages/"+adminMessage, bobToken, map[string]any{
			"body_markdown": "bob must not edit this",
		})
		requireProbeStatus(t, resp, body, http.StatusForbidden, "非作者编辑消息")

		resp, body = probePostJSON(t, bob, base+"/api/portal/v1/messages/"+adminMessage+"/recall", bobToken, map[string]any{})
		requireProbeStatus(t, resp, body, http.StatusForbidden, "非作者撤回消息")

		// 管理员撤回 bob 的消息：作者本人已读过的副本也只是墓碑，正文与附件不再下发。
		// The administrator recalls bob's message: the author's copy becomes a tombstone too, and neither the body nor
		// the attachments are handed out any more.
		resp, body = probePostJSON(t, admin, base+"/api/portal/v1/messages/"+firstMessage+"/recall", adminToken, map[string]any{})
		requireProbeStatus(t, resp, body, http.StatusNoContent, "管理员撤回消息")

		resp, body = probeGet(t, bob, base+"/api/portal/v1/messages/"+firstMessage, bobToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "作者读取撤回消息")
		recalled := decodeProbeJSON[probeMessage](t, body)
		if recalled.Status != "recalled" {
			t.Errorf("撤回后的状态 = %q, 期望 recalled", recalled.Status)
		}
		if recalled.BodyMarkdown != "" || len(recalled.Attachments) != 0 {
			t.Errorf("普通读者仍能看到撤回消息的正文或附件: %s", body)
		}

		// 审计副本保留在管理员视图里：撤回是软墓碑，不是物理删除。
		// The audit copy stays visible to the administrator: recall is a soft tombstone, not a physical delete.
		resp, body = probeGet(t, admin, base+"/api/portal/v1/messages/"+firstMessage, adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "管理员读取撤回消息")
		audited := decodeProbeJSON[probeMessage](t, body)
		if !strings.Contains(audited.BodyMarkdown, "Edited") {
			t.Errorf("管理员视图未保留撤回前的正文: %s", body)
		}
	})

	t.Run("系统公告与置顶", func(t *testing.T) {
		resp, body := probePostJSON(t, bob, base+"/api/v1/messages/system", bobToken, map[string]any{
			"title":         "bob must not announce",
			"body_markdown": "nope",
		})
		requireProbeStatus(t, resp, body, http.StatusForbidden, "普通成员发布系统公告")

		resp, body = probePostJSON(t, admin, base+"/api/v1/messages/system", adminToken, map[string]any{
			"title":         "Probe announcement",
			"body_markdown": "System **notice** for every member",
		})
		requireProbeStatus(t, resp, body, http.StatusCreated, "管理员发布系统公告")
		message := decodeProbeJSON[probeMessage](t, body)
		if message.ConversationKind != "system" || message.ID == "" {
			t.Fatalf("系统公告响应异常: %s", body)
		}
		announcement = message.ID
		systemRoom = message.ConversationID

		end := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		resp, body = probePostJSON(t, admin, base+"/api/v1/messages/"+announcement+"/pin", adminToken, map[string]any{
			"pin_end": end,
		})
		requireProbeStatus(t, resp, body, http.StatusNoContent, "置顶系统公告")

		// 置顶是管理员能力：普通成员即使能读公告，也不能改变它的置顶状态。
		// Pinning is a management capability: a plain member may read the announcement but must not change its pin state.
		resp, body = probePostJSON(t, bob, base+"/api/v1/messages/"+announcement+"/pin", bobToken, map[string]any{})
		requireProbeStatus(t, resp, body, http.StatusForbidden, "普通成员置顶公告")

		resp, body = probeGet(t, admin, base+"/api/portal/v1/messages?folder=pinned", adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "管理员读取置顶公告")
		pinned := decodeProbeJSON[probeMessagePage](t, body)
		found := false
		for _, item := range pinned.Messages {
			if item.ID == announcement && item.Pinned {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("置顶列表里没有公告 %s: %s", announcement, body)
		}

		resp, body = probeGet(t, bob, base+"/api/portal/v1/messages?folder=pinned", bobToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "普通成员读取置顶公告")
		pinned = decodeProbeJSON[probeMessagePage](t, body)
		if !hasProbeMessage(pinned, announcement) {
			t.Errorf("普通成员的置顶列表里没有公告 %s: %s", announcement, body)
		}

		resp, body = probePostJSON(t, admin, base+"/api/v1/messages/"+announcement+"/unpin", adminToken, map[string]any{})
		requireProbeStatus(t, resp, body, http.StatusNoContent, "取消置顶")

		resp, body = probeGet(t, admin, base+"/api/portal/v1/messages?folder=pinned", adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "取消置顶后读取置顶列表")
		pinned = decodeProbeJSON[probeMessagePage](t, body)
		if hasProbeMessage(pinned, announcement) {
			t.Errorf("取消置顶后公告 %s 仍在置顶列表里: %s", announcement, body)
		}

		// 撤销系统公告同样走软墓碑：普通成员拿到空正文，管理员仍能审计原文。
		// Revoking an announcement takes the same soft-tombstone path: a plain member gets an empty body while the
		// administrator can still audit the original text.
		resp, body = probePostJSON(t, admin, base+"/api/v1/messages/"+announcement+"/revoke", adminToken, map[string]any{})
		requireProbeStatus(t, resp, body, http.StatusNoContent, "撤销系统公告")

		resp, body = probeGet(t, bob, base+"/api/portal/v1/messages/"+announcement, bobToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "普通成员读取已撤销公告")
		revoked := decodeProbeJSON[probeMessage](t, body)
		if revoked.Status != "recalled" || revoked.BodyMarkdown != "" {
			t.Errorf("已撤销公告对普通成员应为空墓碑: %s", body)
		}

		resp, body = probeGet(t, admin, base+"/api/portal/v1/messages/"+announcement, adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "管理员读取已撤销公告")
		revoked = decodeProbeJSON[probeMessage](t, body)
		if !strings.Contains(revoked.BodyMarkdown, "System") {
			t.Errorf("撤销后管理员未保留公告原文: %s", body)
		}
	})

	t.Run("类型与标签生命周期", func(t *testing.T) {
		resp, body := probePostJSON(t, bob, base+"/api/v1/message-types", bobToken, map[string]any{
			"key":     "probe-type",
			"name":    "探针类型",
			"name_en": "Probe type",
			"tone":    "info",
		})
		requireProbeStatus(t, resp, body, http.StatusForbidden, "普通成员创建类型")

		resp, body = probePostJSON(t, admin, base+"/api/v1/message-types", adminToken, map[string]any{
			"key":     "probe-type",
			"name":    "探针类型",
			"name_en": "Probe type",
			"tone":    "info",
		})
		requireProbeStatus(t, resp, body, http.StatusOK, "管理员创建类型")

		resp, body = probePostJSON(t, admin, base+"/api/v1/message-tags", adminToken, map[string]any{
			"kind":    "group",
			"key":     "probe-group",
			"name":    "探针分组",
			"name_en": "Probe group",
			"tone":    "warn",
		})
		requireProbeStatus(t, resp, body, http.StatusOK, "管理员创建分组")
		group := decodeProbeJSON[probeTag](t, body)

		resp, body = probePostJSON(t, admin, base+"/api/v1/message-tags", adminToken, map[string]any{
			"kind":    "tag",
			"key":     "probe-label",
			"name":    "探针标签",
			"name_en": "Probe label",
			"tone":    "success",
		})
		requireProbeStatus(t, resp, body, http.StatusOK, "管理员创建标签")
		label := decodeProbeJSON[probeTag](t, body)

		resp, body = probePostJSON(t, bob, base+"/api/portal/v1/messages", bobToken, map[string]any{
			"recipient_id":  adminID,
			"type_key":      "probe-type",
			"body_markdown": "typed and tagged",
			"group_ids":     []string{group.ID},
			"tag_ids":       []string{label.ID},
		})
		requireProbeStatus(t, resp, body, http.StatusCreated, "发送带类型与标签的消息")
		tagged := decodeProbeJSON[probeMessage](t, body)
		if tagged.TypeKey != "probe-type" {
			t.Errorf("消息类型 = %q, 期望 probe-type", tagged.TypeKey)
		}
		if !hasProbeTag(tagged.Groups, group.ID) || !hasProbeTag(tagged.Tags, label.ID) {
			t.Errorf("消息未带上分组与标签: %s", body)
		}

		resp, body = probeGet(t, admin, base+"/api/portal/v1/messages?type_key=probe-type", adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "按类型检索")
		page := decodeProbeJSON[probeMessagePage](t, body)
		if !hasProbeMessage(page, tagged.ID) {
			t.Errorf("按类型检索未命中消息 %s: %s", tagged.ID, body)
		}

		resp, body = probeGet(t, admin, base+"/api/portal/v1/messages?tag="+group.ID, adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "按分组检索")
		page = decodeProbeJSON[probeMessagePage](t, body)
		if !hasProbeMessage(page, tagged.ID) {
			t.Errorf("按分组检索未命中消息 %s: %s", tagged.ID, body)
		}

		resp, body = probePostJSON(t, admin, base+"/api/v1/message-types/probe-type/archive", adminToken, map[string]any{})
		requireProbeStatus(t, resp, body, http.StatusNoContent, "归档类型")

		resp, body = probePostJSON(t, bob, base+"/api/portal/v1/messages", bobToken, map[string]any{
			"recipient_id":  adminID,
			"type_key":      "probe-type",
			"body_markdown": "archived type must not be usable",
		})
		requireProbeStatus(t, resp, body, http.StatusUnprocessableEntity, "使用已归档类型发送")
	})

	t.Run("附件", func(t *testing.T) {
		resp, body := probePostFile(t, bob, base+"/api/portal/v1/messages/attachments", bobToken, "probe.txt", []byte(probeAttachmentText))
		requireProbeStatus(t, resp, body, http.StatusCreated, "上传附件")
		attachment := decodeProbeJSON[probeAttachment](t, body)
		if attachment.ID == "" || attachment.Status != "pending" || attachment.ContentType != "text/plain" {
			t.Fatalf("附件记录异常: %s", body)
		}

		// 客户端声明的类型不作数：字节嗅探决定内容类型，HTML 不在白名单里。
		// The client-declared type does not count: bytes decide the content type, and HTML is not on the allow list.
		resp, body = probePostFile(t, bob, base+"/api/portal/v1/messages/attachments", bobToken, "probe.html", []byte("<html><body>unsafe</body></html>"))
		requireProbeStatus(t, resp, body, http.StatusUnsupportedMediaType, "上传 HTML 附件")

		resp, body = probePostJSON(t, bob, base+"/api/portal/v1/messages", bobToken, map[string]any{
			"recipient_id":   adminID,
			"body_markdown":  "message with attachment",
			"attachment_ids": []string{attachment.ID},
		})
		requireProbeStatus(t, resp, body, http.StatusCreated, "发送带附件的消息")
		withFile := decodeProbeJSON[probeMessage](t, body)
		if len(withFile.Attachments) != 1 || withFile.Attachments[0].ID != attachment.ID {
			t.Fatalf("消息未挂载附件: %s", body)
		}

		resp, body = probeGet(t, admin, base+"/api/portal/v1/messages/attachments/"+attachment.ID, adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "下载附件")
		if body != probeAttachmentText {
			t.Errorf("附件内容 = %q, 期望 %q", body, probeAttachmentText)
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("附件下载缺少 nosniff 头: %v", resp.Header)
		}
	})

	t.Run("消息中心页面", func(t *testing.T) {
		resp, body := probeGet(t, admin, base+"/messages", adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "打开消息中心")
		for _, want := range []string{
			`id="yd-msg-app"`,
			`id="yd-conversation-list"`,
			`hx-ws:connect="/ws/messages"`,
			"yd-message-editor.js",
			"yd-message.js",
			`data-yd-msg-tab="conversations"`,
			`data-yd-msg-tab="contacts"`,
			`id="yd-contacts-panel"`,
			`id="yd-contact-list"`,
			`data-yd-contact-search`,
			`action="/messages/groups"`,
			`data-yd-user-menu`,
			`id="yd-unread-badge"`,
			`id="yd-unread-menu-badge"`,
			`id="yd-sidebar-unread"`,
			`id="yd-sidebar-friend-requests"`,
			`yd-user-menu__item--unread`,
			`href="/messages?folder=unread"`,
			`href="/account"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("消息中心页面缺少 %q", want)
			}
		}
		if strings.Contains(body, `hx-get="/messages/new"`) {
			t.Errorf("消息中心仍从抽屉打开新建会话:\n%s", body)
		}
		// 消息中心与消息治理是两项菜单：进入治理页时只有治理项高亮，返回消息中心时只有消息项高亮。
		// Messages and moderation are two separate entries: the moderation page highlights only moderation, and the
		// message centre highlights only messages.
		if !strings.Contains(body, `data-yd-nav="messages" aria-current="page"`) {
			t.Errorf("消息中心页未高亮消息菜单:\n%s", body)
		}
		if strings.Contains(body, `data-yd-nav="mgr-messages" aria-current="page"`) {
			t.Errorf("消息中心页同时高亮了消息治理菜单:\n%s", body)
		}

		resp, body = probeGet(t, admin, base+"/messages/"+conversation, adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "打开会话线程")
		for _, want := range []string{
			`id="yd-message-thread"`,
			`id="yd-message-thread-body"`,
			`id="yd-msg-composer-form"`,
			`id="yd-msg-composer-body"`,
			`action="/messages"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("会话页缺少 %q", want)
			}
		}

		// 历史刷新片段只含消息列表：撰写区不在其中，实时刷新因此不会清掉草稿。
		// The history-refresh fragment carries the message list only: the composer is absent, so a realtime refresh never
		// clears the draft.
		resp, body = probeGet(t, admin, base+"/messages/"+conversation+"/history", adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "读取历史刷新片段")
		if strings.Contains(body, "yd-msg-composer") {
			t.Errorf("历史刷新片段不应包含撰写区:\n%s", body)
		}
		if !strings.Contains(body, `data-yd-message-id`) {
			t.Errorf("历史刷新片段缺少消息条目:\n%s", body)
		}

		// 公告频道使用同一套内联撰写区，但提交地址是管理员公告端点。
		// The announcement channel uses the same inline composer but posts to the administrator endpoint.
		resp, body = probeGet(t, admin, base+"/messages/"+systemRoom, adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "打开公告频道")
		if !strings.Contains(body, `action="/mgr/messages/system"`) {
			t.Errorf("公告频道缺少公告撰写表单:\n%s", body)
		}

		resp, body = probeGet(t, admin, base+"/mgr/messages", adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "打开消息治理")
		if !strings.Contains(body, `data-yd-nav="mgr-messages" aria-current="page"`) {
			t.Errorf("消息治理页未高亮治理菜单:\n%s", body)
		}
		if strings.Contains(body, `data-yd-nav="messages" aria-current="page"`) {
			t.Errorf("消息治理页同时高亮了消息中心菜单:\n%s", body)
		}

		resp, body = probeGet(t, admin, base+"/friends", adminToken)
		requireProbeStatus(t, resp, body, http.StatusOK, "打开好友页")
		if !strings.Contains(body, `id="yd-friends-panel"`) {
			t.Errorf("好友页缺少好友面板:\n%s", body)
		}
	})
}

// TestGeneratedAppReconcilesBuiltinRoleGrants 模拟旧库升级：先正常启动一次，删掉内置角色上的 message:* 绑定，
// 再启动一次，验证 Seed 只补缺失能力就能让管理员重新访问消息中心。
// TestGeneratedAppReconcilesBuiltinRoleGrants simulates a database upgrade: start once, delete the message:* bindings
// from the built-in roles, start again, and verify that Seed's add-only reconciliation restores the administrator's
// access to the message centre.
func TestGeneratedAppReconcilesBuiltinRoleGrants(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实服务验证 | skipped in short mode")
	}

	const name = "yd-reconcile-probe"
	projDir, base, stop := startProbeAppDBWithStop(t, name, scaffold.DBSQLite, nil)
	admin := newProbeClient(t, httpTimeout)
	token := signIn(t, admin, base, probeTenant, probeAdmin, probeAdminPassword)
	resp, body := probeGet(t, admin, base+"/api/portal/v1/messages", token)
	requireProbeStatus(t, resp, body, http.StatusOK, "初始消息中心")
	stop()

	dbFile := filepath.Join(projDir, "data", name+".db")
	db, err := sql.Open("sqlite", filepath.ToSlash(dbFile))
	if err != nil {
		t.Fatalf("打开生成的 SQLite 库失败: %v", err)
	}
	result, err := db.Exec("DELETE FROM yd_auth_role_permissions WHERE permission LIKE 'message%'")
	if err != nil {
		_ = db.Close()
		t.Fatalf("删除 message 能力失败: %v", err)
	}
	removed, err := result.RowsAffected()
	if err != nil || removed == 0 {
		_ = db.Close()
		t.Fatalf("没有删除任何 message 能力（rows=%d, err=%v），初始种子可能不含消息权限", removed, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("关闭 SQLite 库失败: %v", err)
	}

	// 第二次启动触发 Seed 的补齐逻辑；登录使用同一份库与开发主密钥。
	// The second start triggers Seed's reconciliation; sign-in uses the same database and the development master key.
	bin := filepath.Join(projDir, "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	restartedBase, restartedStop := startGeneratedAppWithStop(t, bin, projDir)
	defer restartedStop()
	restarted := newProbeClient(t, httpTimeout)
	restartedToken := signIn(t, restarted, restartedBase, probeTenant, probeAdmin, probeAdminPassword)
	resp, body = probeGet(t, restarted, restartedBase+"/api/portal/v1/messages", restartedToken)
	requireProbeStatus(t, resp, body, http.StatusOK, "补齐内置角色能力后的消息中心")

	// 四个内置角色都应重新拿到各自的消息能力。
	// All four built-in roles must have their message capabilities back.
	db, err = sql.Open("sqlite", filepath.ToSlash(dbFile))
	if err != nil {
		t.Fatalf("重新打开 SQLite 库失败: %v", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`
SELECT r.name, COUNT(p.permission)
FROM yd_auth_roles r
LEFT JOIN yd_auth_role_permissions p
  ON p.tenant_id = r.tenant_id AND p.role_id = r.id AND p.permission LIKE 'message%'
GROUP BY r.name`)
	if err != nil {
		t.Fatalf("统计补齐后的能力失败: %v", err)
	}
	defer func() { _ = rows.Close() }()
	counts := map[string]int{}
	for rows.Next() {
		var role string
		var count int
		if err := rows.Scan(&role, &count); err != nil {
			t.Fatalf("读取能力计数失败: %v", err)
		}
		counts[role] = count
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历能力计数失败: %v", err)
	}
	for _, role := range []string{"owner", "admin", "auditor", "member"} {
		if counts[role] == 0 {
			t.Errorf("内置角色 %q 没有补齐消息能力: %v", role, counts)
		}
	}
}

// dialMessageSocket 建立消息 WebSocket 并读掉两个初始未读徽标（用户名角标与用户菜单计数），返回已订阅的连接。
// dialMessageSocket opens the message WebSocket, consumes both initial unread badges (the user-name corner and the
// user-menu count) and returns the subscribed connection.
func dialMessageSocket(t *testing.T, base, token string) *websocket.Conn {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), sseTimeout)
	defer cancel()
	conn, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/ws/messages", websocket.DialOptions{
		Subprotocols: []string{"htmx"},
		Headers:      http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		t.Fatalf("消息 WebSocket 握手失败: %v\n消息 WebSocket handshake failed: %v", err, err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(sseTimeout)); err != nil {
		_ = conn.CloseNow()
		t.Fatalf("设置读超时失败: %v", err)
	}
	for _, target := range []string{`hx-target="#yd-unread-badge"`, `hx-target="#yd-unread-menu-badge"`} {
		_, frame, err := conn.ReadMessage()
		if err != nil {
			_ = conn.CloseNow()
			t.Fatalf("未收到初始未读徽标 %s: %v\ninitial unread badge was not pushed: %v", target, err, err)
		}
		if !strings.Contains(string(frame), target) {
			_ = conn.CloseNow()
			t.Fatalf("未读徽标帧缺少目标 %s: %s\nunread badge frame lacks target %s: %s", target, frame, target, frame)
		}
	}
	return conn
}
