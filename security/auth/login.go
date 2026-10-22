package auth

import (
	"context"
	"strings"
	"time"

	"github.com/shibingli/yd/security/guomi"
	"github.com/shibingli/ydx/base64"
	"github.com/shibingli/ydx/errs"
)

// LoginResult is the outcome of a successful sign-in. | LoginResult 是一次成功登录的结果。
type LoginResult struct {
	// ExpiresAt is the moment the token stops verifying. | ExpiresAt 是令牌失效的时刻。
	ExpiresAt time.Time
	// User is the authenticated user; it never carries a plaintext password or sec_key. | User 是通过认证的用户；它绝不携带明文口令或明文 sec_key。
	User *User
	// Token is the compact JWS signed with the sec_key of User. | Token 是用 User 的 sec_key 签发的紧凑 JWS。
	Token string
}

// Login authenticates a username and issues a token for it. | Login 认证用户名并为其签发令牌。
//
// An empty tenantID means a tenant-less sign-in: the username is globally unique
// and the user lands in its default tenant. A non-empty tenantID scopes the lookup
// to that tenant and therefore also checks membership. | 空 tenantID 表示无租户登录：
// 登录名全局唯一，用户落入其默认租户；非空 tenantID 把查询限定在该租户，因而同时校验成员关系。
//
// The password is verified by Authenticate, so a missing user and a wrong password
// are indistinguishable here as well, and the token is signed with the sec_key of
// the user that was just authenticated. | 口令由 Authenticate 校验，因此「用户不存在」与「口令错误」
// 在这里同样不可区分；令牌则用刚刚通过认证的那个用户的 sec_key 签发。
func (s *Service) Login(ctx context.Context, tenantID, username, password string, issuer *TokenIssuer) (*LoginResult, error) {
	if issuer == nil {
		return nil, errs.Detail(ErrTokenInvalid, "no token issuer configured | 未配置令牌签发器")
	}
	u, err := s.Authenticate(ctx, tenantID, username, password)
	if err != nil {
		return nil, err
	}
	token, expiresAt, err := issuer.Issue(ctx, u)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, ExpiresAt: expiresAt, User: u}, nil
}

// DecryptLoginPayload decrypts the password a browser sent under the
// "X-YD-Encrypted: sm2" protocol. | DecryptLoginPayload 解密浏览器在 "X-YD-Encrypted: sm2" 协议下提交的口令。
//
// The front end (web/static/js/yd-guomi-htmx.js) sends the standard Base64 of the
// raw SM2 C1C3C2 ciphertext produced by guomi.EncryptRaw; a raw (non-Base64) blob is
// accepted as well, so a Go caller that already decoded the body needs no round
// trip through Base64. | 前端（web/static/js/yd-guomi-htmx.js）提交的是 guomi.EncryptRaw 产出的
// 原始 SM2 C1C3C2 密文的标准 Base64；同时也接受未经 Base64 的原始密文，便于已经解码过请求体的 Go 调用方直接使用。
//
// A nil private key and an undecryptable payload both fail with an error wrapping
// ErrInvalidCredentials, so a caller can answer 400 or 401 without distinguishing
// the two. | 私钥为空与密文无法解密都以包装了 ErrInvalidCredentials 的错误失败，
// 调用方无需区分两者即可回答 400 或 401。
func DecryptLoginPayload(ciphertext []byte, priv *guomi.PrivateKey) (string, error) {
	if priv == nil {
		return "", errs.Detail(ErrInvalidCredentials, "no SM2 private key | 缺少 SM2 私钥")
	}
	raw, ok := decodeLoginCiphertext(ciphertext)
	if !ok {
		return "", errs.Detail(ErrInvalidCredentials, "payload is neither Base64 nor an SM2 ciphertext | 载荷既不是 Base64 也不是 SM2 密文")
	}
	plain, err := guomi.DecryptRaw(priv, raw)
	if err != nil {
		return "", errs.Wrap(err, ErrInvalidCredentials)
	}
	defer zeroBytes(plain)
	return string(plain), nil
}

// decodeLoginCiphertext accepts either the Base64 text or the raw bytes of an SM2
// ciphertext. | decodeLoginCiphertext 接受 SM2 密文的 Base64 文本或原始字节。
//
// The Base64 branch requires ASCII Base64 characters and a length that is a
// multiple of four, which a raw C1C3C2 blob of 96 bytes or more cannot satisfy
// except with negligible probability. | Base64 分支要求字符全部落在 ASCII Base64 字母表内且长度为 4 的倍数，
// 96 字节以上的原始 C1C3C2 数据满足该条件属于可忽略的小概率事件。
//
// Only the Base64 text is trimmed: a text field or a form submission may pad it with
// whitespace, while trimming raw binary ciphertext would shift C1 and turn a valid
// point into one that is not on the curve. | 只有 Base64 文本会被去除首尾空白：文本字段或表单提交
// 可能给它加上空白，而裁剪原始二进制密文会平移 C1，把合法曲线点变成非法点。
func decodeLoginCiphertext(ciphertext []byte) ([]byte, bool) {
	if len(ciphertext) == 0 {
		return nil, false
	}
	if trimmed := trimASCIISpace(ciphertext); len(trimmed) != 0 && len(trimmed)%4 == 0 && isBase64Alphabet(trimmed) {
		decoded, err := base64.StdEncoding.DecodeString(string(trimmed))
		if err != nil {
			return nil, false
		}
		return decoded, true
	}
	return ciphertext, true
}

// trimASCIISpace strips leading and trailing ASCII whitespace. | trimASCIISpace 去除首尾 ASCII 空白。
func trimASCIISpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && isASCIISpace(b[start]) {
		start++
	}
	for end > start && isASCIISpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

// isASCIISpace reports whether c is ASCII whitespace. | isASCIISpace 报告 c 是否为 ASCII 空白。
func isASCIISpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

// isBase64Alphabet reports whether every byte belongs to the standard Base64
// alphabet (padding included). | isBase64Alphabet 报告每个字节是否都属于标准 Base64 字母表（含填充符）。
func isBase64Alphabet(b []byte) bool {
	for _, c := range b {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/', c == '=':
		default:
			return false
		}
	}
	return true
}

// encryptedMarker documents the header value this package expects from the
// front-end encryptor. | encryptedMarker 记录本包期望前端加密器提交的请求头取值。
//
// The scheme is transport-level and enforced by the caller, exactly as the
// scaffold's login controller does with strings.EqualFold. | 该约定属于传输层，由调用方实施，
// 与脚手架登录控制器中的 strings.EqualFold 判法一致。
const encryptedMarker = "sm2"

// LoginEncryptedHeader is the request header a browser sets when the payload was
// encrypted by the front-end sandbox. | LoginEncryptedHeader 是浏览器在前端沙箱加密载荷后设置的请求头。
const LoginEncryptedHeader = "X-YD-Encrypted"

// LoginEncryptedScheme reports whether a header value marks an SM2 payload. | LoginEncryptedScheme 报告某个请求头取值是否标记了 SM2 载荷。
//
// The comparison is case-insensitive and ignores surrounding whitespace. | 比较忽略大小写与首尾空白。
func LoginEncryptedScheme(headerValue string) bool {
	return strings.EqualFold(strings.TrimSpace(headerValue), encryptedMarker)
}
