package middleware

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/yun-dev/yd/router"
)

// uuidV4Pattern matches the canonical lowercase RFC 4122 version 4 textual form. | uuidV4Pattern 匹配 RFC 4122 版本 4 的标准小写文本形式。
var uuidV4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestRequestIDFromHeaderAndGenerated(t *testing.T) {
	t.Run("回显请求头中的 ID", func(t *testing.T) {
		var seen string
		r := router.New()
		r.Use(RequestID(""))
		r.GET("/x", func(c *router.Ctx) {
			seen = requestID(c)
			_ = c.NoContent()
		})

		rec := doReq(r, http.MethodGet, "/x", map[string]string{"X-Request-Id": "abc-123"}, "")
		if seen != "abc-123" {
			t.Errorf("ctx 中的 request id = %q, 期望 abc-123", seen)
		}
		if got := rec.Header().Get("X-Request-Id"); got != "abc-123" {
			t.Errorf("响应头 X-Request-Id = %q, 期望 abc-123", got)
		}
	})

	t.Run("缺失时生成 UUIDv4", func(t *testing.T) {
		ids := make([]string, 0, 2)
		r := router.New()
		r.Use(RequestID(""))
		r.GET("/x", func(c *router.Ctx) {
			ids = append(ids, requestID(c))
			_ = c.NoContent()
		})

		var respIDs []string
		for i := 0; i < 2; i++ {
			rec := doReq(r, http.MethodGet, "/x", nil, "")
			respIDs = append(respIDs, rec.Header().Get("X-Request-Id"))
		}
		for i, id := range ids {
			if !uuidV4Pattern.MatchString(id) {
				t.Fatalf("第 %d 个生成的 id = %q, 不是合法 UUIDv4", i, id)
			}
			if respIDs[i] != id {
				t.Fatalf("第 %d 个响应头 id = %q, 与 ctx 中的 %q 不一致", i, respIDs[i], id)
			}
		}
		if ids[0] == ids[1] {
			t.Fatalf("两次生成的 id 相同: %q", ids[0])
		}
	})

	t.Run("自定义请求头名", func(t *testing.T) {
		var seen string
		r := router.New()
		r.Use(RequestID("X-Trace-Id"))
		r.GET("/x", func(c *router.Ctx) {
			seen = requestID(c)
			_ = c.NoContent()
		})

		rec := doReq(r, http.MethodGet, "/x", map[string]string{"X-Trace-Id": "trace-9"}, "")
		if seen != "trace-9" {
			t.Errorf("ctx 中的 request id = %q, 期望 trace-9", seen)
		}
		if got := rec.Header().Get("X-Trace-Id"); got != "trace-9" {
			t.Errorf("响应头 X-Trace-Id = %q, 期望 trace-9", got)
		}
		if got := rec.Header().Get("X-Request-Id"); got != "" {
			t.Errorf("未配置的 X-Request-Id 不应被写入, 实际 %q", got)
		}
	})
}

// The generated UUID must carry the version/variant bits required by RFC 4122. | 生成的 UUID 必须带有 RFC 4122 要求的版本位与变体位。
func TestGeneratedUUIDVersionAndVariantBits(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id := newUUIDv4()
		if !uuidV4Pattern.MatchString(id) {
			t.Fatalf("newUUIDv4() = %q, 不是合法 UUIDv4", id)
		}
		if seen[id] {
			t.Fatalf("newUUIDv4() 产生重复值: %q", id)
		}
		seen[id] = true
	}
}
