// Package jwt implements JWT (RFC 7519) signing, verification and HTTP middleware, with a validation path
// that never reflects and never builds a map of the claims. | Package jwt 实现 JWT（RFC 7519）的签发、校验与
// HTTP 中间件，其校验路径既不使用反射，也不构造声明 map。
//
// The design follows the framework rule that a decision about a claim must be taken on the bytes that were
// signed. Parse never decodes the payload into map[string]any: it scans the decoded payload bytes with the
// hand-written scanner in scan.go, so exp / nbf / iat / iss / aud / sub are validated against exactly the
// bytes the signature covers. | 本设计遵循框架约定：关于声明的判定必须落在被签名的字节上。
// Parse 绝不把载荷解码成 map[string]any，而是用 scan.go 中手写的扫描器读取解码后的载荷字节，
// 因此 exp / nbf / iat / iss / aud / sub 校验的正是签名所覆盖的那份字节。
//
// Supported algorithms | 受支持的算法：
//
//	HS256 HS384 HS512  HMAC with SHA-2; key is []byte or string | HMAC + SHA-2；密钥为 []byte 或 string
//	RS256 RS384 RS512  RSASSA-PKCS1-v1_5; key is *rsa.PrivateKey / *rsa.PublicKey | RSASSA-PKCS1-v1_5；密钥为 *rsa.PrivateKey / *rsa.PublicKey
//	PS256 PS384 PS512  RSASSA-PSS; key is *rsa.PrivateKey / *rsa.PublicKey | RSASSA-PSS；密钥为 *rsa.PrivateKey / *rsa.PublicKey
//	ES256 ES384 ES512  ECDSA over P-256 / P-384 / P-521, JWS fixed-width r||s | ECDSA + P-256 / P-384 / P-521，JWS 定宽 r||s
//	EdDSA              Ed25519 | Ed25519 签名
//
// "none" is never accepted, and a key from the wrong family is rejected with ErrSignatureInvalid, which closes
// the classic algorithm-confusion attack: an HMAC verification can never be fed an RSA public key. | 永不接受
// "none"；密钥族不符一律以 ErrSignatureInvalid 拒绝，从而封堵经典的算法混淆攻击：
// HMAC 校验路径不可能接受 RSA 公钥。
//
// Every failure is one of the declared sentinels below and stays matchable with errors.Is. | 所有失败都是下列
// 声明的哨兵之一，始终可用 errors.Is 判定：
//
//	var (
//		ErrMalformed        = errx.New("jwt: malformed token | jwt：令牌格式非法")
//		ErrSignatureInvalid = errx.New("jwt: invalid signature | jwt：签名校验失败")
//	)
//
// This package emits no log output of its own; every failure is returned to the caller. | 本包不产生任何日志输出：
// 所有失败都返回给调用方。
package jwt

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/http/router"
)

// Alg is a JWS algorithm name as it appears in the "alg" header parameter. | Alg 是出现在 "alg" 头部参数中的 JWS 算法名。
type Alg string

// The supported JWS algorithms; "none" is deliberately absent. | 受支持的 JWS 算法；有意不包含 "none"。
const (
	HS256 Alg = "HS256" // HMAC using SHA-256 | 使用 SHA-256 的 HMAC
	HS384 Alg = "HS384" // HMAC using SHA-384 | 使用 SHA-384 的 HMAC
	HS512 Alg = "HS512" // HMAC using SHA-512 | 使用 SHA-512 的 HMAC

	RS256 Alg = "RS256" // RSASSA-PKCS1-v1_5 using SHA-256 | 使用 SHA-256 的 RSASSA-PKCS1-v1_5
	RS384 Alg = "RS384" // RSASSA-PKCS1-v1_5 using SHA-384 | 使用 SHA-384 的 RSASSA-PKCS1-v1_5
	RS512 Alg = "RS512" // RSASSA-PKCS1-v1_5 using SHA-512 | 使用 SHA-512 的 RSASSA-PKCS1-v1_5

	PS256 Alg = "PS256" // RSASSA-PSS using SHA-256 | 使用 SHA-256 的 RSASSA-PSS
	PS384 Alg = "PS384" // RSASSA-PSS using SHA-384 | 使用 SHA-384 的 RSASSA-PSS
	PS512 Alg = "PS512" // RSASSA-PSS using SHA-512 | 使用 SHA-512 的 RSASSA-PSS

	ES256 Alg = "ES256" // ECDSA using P-256 and SHA-256 | 使用 P-256 与 SHA-256 的 ECDSA
	ES384 Alg = "ES384" // ECDSA using P-384 and SHA-384 | 使用 P-384 与 SHA-384 的 ECDSA
	ES512 Alg = "ES512" // ECDSA using P-521 and SHA-512 | 使用 P-521 与 SHA-512 的 ECDSA

	EdDSA Alg = "EdDSA" // Ed25519 | Ed25519
)

// The declared sentinels of this package. | 本包的哨兵错误声明。
//
// Every failure path returns one of them, either directly or through errx.Detail / errx.Wrap, so callers can
// classify failures with errors.Is instead of matching message text. | 所有失败路径都返回其中之一，
// 直接返回或经由 errx.Detail / errx.Wrap 包装，因此调用方可用 errors.Is 分类失败，而无需匹配文案。
var (
	// ErrMalformed reports a token that is not a well-formed compact JWS, or whose JSON is not well formed. | ErrMalformed 表示 token 不是结构良好的紧凑 JWS，或其 JSON 结构非法。
	ErrMalformed = errx.New("jwt: malformed token | jwt：令牌格式非法")
	// ErrSignatureInvalid reports that the signature did not verify, or that the key family does not match the algorithm. | ErrSignatureInvalid 表示签名校验未通过，或密钥族与算法不匹配。
	ErrSignatureInvalid = errx.New("jwt: invalid signature | jwt：签名校验失败")
	// ErrTokenExpired reports an exp claim in the past, beyond the leeway. | ErrTokenExpired 表示 exp 声明位于过去，且超出容差。
	ErrTokenExpired = errx.New("jwt: token expired | jwt：令牌已过期")
	// ErrTokenNotValidYet reports an nbf claim in the future, beyond the leeway. | ErrTokenNotValidYet 表示 nbf 声明位于未来，且超出容差。
	ErrTokenNotValidYet = errx.New("jwt: token not valid yet | jwt：令牌尚未生效")
	// ErrAlgNotAllowed reports an alg header that is unsupported, unknown, or outside the allow list. | ErrAlgNotAllowed 表示 alg 头部不受支持、未知，或不在白名单内。
	ErrAlgNotAllowed = errx.New("jwt: algorithm not allowed | jwt：算法不被允许")
	// ErrIssuerMismatch reports an iss claim that differs from the expected issuer. | ErrIssuerMismatch 表示 iss 声明与期望的签发方不一致。
	ErrIssuerMismatch = errx.New("jwt: issuer mismatch | jwt：签发方不匹配")
	// ErrAudienceMismatch reports an aud claim that does not contain the expected audience. | ErrAudienceMismatch 表示 aud 声明不包含期望的受众。
	ErrAudienceMismatch = errx.New("jwt: audience mismatch | jwt：受众不匹配")
	// ErrMissingExp reports a token without an exp claim while ParseOptions.RequireExp is set. | ErrMissingExp 表示已开启 ParseOptions.RequireExp 但 token 缺少 exp 声明。
	ErrMissingExp = errx.New("jwt: missing exp claim | jwt：缺少 exp 声明")
	// ErrClaimNotFound reports a claim that an accessor could not find. | ErrClaimNotFound 表示访问器找不到该声明。
	ErrClaimNotFound = errx.New("jwt: claim not found | jwt：声明不存在")
	// ErrInvalidClaims reports claims that jsonx cannot serialize. | ErrInvalidClaims 表示 jsonx 无法序列化的声明。
	ErrInvalidClaims = errx.New("jwt: claims cannot be encoded | jwt：声明无法编码")
	// ErrTokenNotFound reports that the middleware found no token at the configured lookup location. | ErrTokenNotFound 表示中间件在配置的提取位置没有找到 token。
	ErrTokenNotFound = errx.New("jwt: token not found | jwt：未找到令牌")
	// ErrNoKeyFunc reports a nil KeyFunc. | ErrNoKeyFunc 表示 KeyFunc 为空。
	ErrNoKeyFunc = errx.New("jwt: nil key function | jwt：密钥函数为空")
	// ErrBadTokenLookup reports a malformed MiddlewareOptions.TokenLookup. | ErrBadTokenLookup 表示 MiddlewareOptions.TokenLookup 配置非法。
	ErrBadTokenLookup = errx.New("jwt: invalid token lookup | jwt：令牌提取配置非法")
)

// supported reports whether a is one of the algorithms this package implements. | supported 报告 a 是否为本包实现的算法之一。
func supported(a Alg) bool {
	switch a {
	case HS256, HS384, HS512, RS256, RS384, RS512, PS256, PS384, PS512, ES256, ES384, ES512, EdDSA:
		return true
	}
	return false
}

// Header is the decoded JOSE header of a token. | Header 是 token 解码后的 JOSE 头部。
type Header struct {
	// Extra holds the string-valued header parameters other than alg, typ and kid; it is nil when there is none. | Extra 保存 alg、typ、kid 之外取值为字符串的头部参数；没有时该字段为 nil。
	Extra map[string]string
	// Alg is the "alg" parameter. | Alg 是 "alg" 参数。
	Alg Alg
	// Typ is the "typ" parameter; it is informational and never validated. | Typ 是 "typ" 参数；它仅供参考，从不参与校验。
	Typ string
	// Kid is the "kid" parameter, handed to KeyFunc so that keys can rotate. | Kid 是 "kid" 参数，会交给 KeyFunc 以支持密钥轮换。
	Kid string
}

// KeyFunc resolves the verification key of a token from its header. | KeyFunc 依据 token 头部为其解析出校验密钥。
//
// The returned key must be []byte or string for HSxxx, *rsa.PublicKey for RSxxx and PSxxx, *ecdsa.PublicKey
// for ESxxx, and ed25519.PublicKey for EdDSA; any other type is refused with ErrSignatureInvalid. | HSxxx 必须返回
// []byte 或 string，RSxxx 与 PSxxx 必须返回 *rsa.PublicKey，ESxxx 必须返回 *ecdsa.PublicKey，
// EdDSA 必须返回 ed25519.PublicKey；其它类型一律以 ErrSignatureInvalid 拒绝。
//
// An error returned here is propagated unchanged by Parse, so a caller keeps errors.Is on its own sentinels. | 此处返回的错误会被 Parse 原样透出，调用方仍可用 errors.Is 判定自己的哨兵。
//
// Performance note: returning a []byte or string literal from a closure boxes it into a fresh interface
// value on every call, which adds one allocation to every verification. Store the key in an `any` field
// (or return a pointer) and hand that value back unchanged. | 性能提示：在闭包里返回 []byte 或字符串字面量
// 会在每次调用时把它装箱成新的接口值，从而给每次验签增加一次分配。应把密钥存放在 `any` 字段中
// （或返回指针），并原样交回该值。
type KeyFunc func(header Header) (any, error)

// ParseOptions controls verification. | ParseOptions 控制校验行为。
type ParseOptions struct {
	// Now overrides the clock; it lets tests run deterministically. | Now 用于替换时钟，便于实现确定性测试。
	Now func() time.Time
	// Issuer, when non-empty, must equal the iss claim. | Issuer 非空时必须与 iss 声明相等。
	Issuer string
	// Audience, when non-empty, must be contained in the aud claim. | Audience 非空时必须包含于 aud 声明。
	Audience string
	// ValidAlgs restricts the accepted algorithms; nil means every supported algorithm. | ValidAlgs 限定可接受的算法；nil 表示全部受支持的算法。
	//
	// Setting it is recommended: the allow list is the first line of defence against algorithm confusion. | 建议显式设置：
	// 白名单是抵御算法混淆攻击的第一道防线。
	ValidAlgs []Alg
	// Leeway is the tolerance applied to exp and nbf to absorb clock skew. | Leeway 是施加于 exp 与 nbf 的容差，用于吸收时钟漂移。
	Leeway time.Duration
	// RequireExp rejects a token without an exp claim. | RequireExp 拒绝缺少 exp 声明的 token。
	RequireExp bool
}

// Token is a verified token. | Token 是一个已通过校验的 token。
//
// Callers that do not call Release may retain a Token indefinitely. Release is an opt-in fast path:
// it returns the Token to an internal pool so the next Parse on the same P can reuse its storage,
// which keeps the per-request path allocation-free under high concurrency. | 不调用 Release 的调用方
// 可以无限期持有 Token。Release 是可选的快速路径：它把 Token 归还内部池，使同一 P 上的下一次
// Parse 复用其存储，从而让高并发下的每请求路径保持零分配。
type Token struct {
	// Header is the decoded JOSE header. | Header 是解码后的 JOSE 头部。
	Header Header
	// Claims is the read-only view over the payload. | Claims 是载荷的只读视图。
	Claims Claims
	// Signature is the raw signature bytes. | Signature 是原始签名字节。
	Signature []byte

	// data owns the payload, signature, kid and typ bytes referenced above; it survives pool reuse. | data
	// 持有上面引用的载荷、签名、kid 与 typ 字节，并在池复用间保留。
	data []byte
	// released is 1 once the token has been returned to the pool. | released 在 token 归还池后置 1。
	released uint32
}

// tokenPool recycles Tokens for callers that use Release. | tokenPool 为使用 Release 的调用方回收 Token。
var tokenPool = sync.Pool{New: func() any { return new(Token) }}

// tokenDataRetainLimit bounds the storage a released Token may keep for reuse; a one-off huge
// token does not pin its buffer in the pool. | tokenDataRetainLimit 限制已释放 Token 为复用而保留的
// 存储；一次性的超大 token 不会把缓冲长期钉在池中。
const tokenDataRetainLimit = 64 << 10

// Release returns the Token to the internal pool, if the caller opted into reuse. | Release 把 Token
// 归还内部池（前提是调用方选择复用）。
//
// After Release the Token and every string or slice obtained from it must not be used again: a later
// Parse may overwrite its storage. A caller that retains the Token (or its Header.Kid, Header.Typ,
// Claims.Raw or Signature) beyond the current request must not call Release. Release is idempotent
// and nil-safe. | 调用 Release 后，不得再使用该 Token 及其派生的任何字符串或切片：后续 Parse 可能
// 覆盖其存储。若调用方需要在当前请求结束后继续持有 Token（或其 Header.Kid、Header.Typ、
// Claims.Raw、Signature），则不得调用 Release。Release 幂等且对 nil 安全。
func (t *Token) Release() {
	if t == nil || !atomic.CompareAndSwapUint32(&t.released, 0, 1) {
		return
	}
	t.Header = Header{}
	t.Claims = Claims{}
	t.Signature = nil
	if cap(t.data) > tokenDataRetainLimit {
		t.data = nil
	}
	tokenPool.Put(t)
}

// unsafeString returns s as a string backed by b without copying. b must never be mutated while the
// string is reachable; Token.data is only rewritten after Release, which invalidates all such strings. | unsafeString
// 返回由 b 支撑、不复制内容的字符串。字符串可达期间绝不能修改 b；Token.data 只在 Release 之后
// 重写，而 Release 会使所有这类字符串失效。
func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// Parse verifies token and validates its registered claims. | Parse 校验 token 并验证其登记声明。
//
// The compact serialization is split on its two dots; the JOSE header is decoded into a pooled buffer and
// scanned for alg / typ / kid, and the payload is scanned for the registered claims without ever becoming a
// map. The signature is verified on the raw signing input, that is the exact "header.payload" bytes of the
// token. | 紧凑序列化按其两个点号切分；头部解码进池化缓冲并扫描出 alg / typ / kid，
// 载荷直接扫描登记声明而绝不构造 map；签名则在原始 signing input（即 token 中 "header.payload" 的原始字节）上校验。
//
// The order of the checks is deliberate | 各项检查的顺序是刻意安排的：
//
//  1. the header must be well formed and its alg must be supported and allowed, | 头部必须结构良好，且 alg 必须受支持并落在白名单内；
//  2. the payload must be a well-formed JSON object, | 载荷必须是结构良好的 JSON 对象；
//  3. only then is the key requested and the signature verified, | 此后才索取密钥并校验签名；
//  4. only then are exp / nbf / iss / aud validated, because an unverified claim must never influence a
//     decision. | 最后才校验 exp / nbf / iss / aud，因为未经校验的声明绝不应影响任何判定。
func Parse(token string, keyFn KeyFunc, o ParseOptions) (*Token, error) {
	if keyFn == nil {
		return nil, ErrNoKeyFunc
	}
	token = strings.TrimSpace(token)

	first := strings.IndexByte(token, '.')
	if first < 0 {
		return nil, errx.Detail(ErrMalformed, "missing payload separator | 缺少载荷段分隔符")
	}
	rest := token[first+1:]
	second := strings.IndexByte(rest, '.')
	if second < 0 {
		return nil, errx.Detail(ErrMalformed, "missing signature separator | 缺少签名段分隔符")
	}
	if strings.IndexByte(rest[second+1:], '.') >= 0 {
		return nil, errx.Detail(ErrMalformed, "too many segments | 段数过多")
	}
	payloadSeg, sigSeg := rest[:second], rest[second+1:]

	s := getScratch()
	defer putScratch(s)

	// Step 1: header structure and algorithm. | 第一步：头部结构与算法。
	if !decodeSegment(s, &s.header, token[:first]) {
		return nil, errx.Detail(ErrMalformed, "header is not base64url | 头部不是 base64url")
	}
	var hf headerFields
	if err := scanHeader(s.header, &hf); err != nil {
		return nil, err
	}
	// A hostile token must not be able to pick its own algorithm, so the allow list is enforced before any
	// key material is requested. | 恶意 token 不得自行选择算法，因此白名单在任何密钥材料被索取之前强制执行。
	if !algAllowed(o.ValidAlgs, hf.alg) {
		return nil, errx.Detail(ErrAlgNotAllowed, "alg=%[1]q | 算法=%[1]q", string(hf.alg))
	}
	if payloadSeg == "" {
		return nil, errx.Detail(ErrMalformed, "empty payload segment | 载荷段为空")
	}
	if sigSeg == "" {
		return nil, errx.Detail(ErrMalformed, "empty signature segment | 签名段为空")
	}

	// Step 2: payload structure. | 第二步：载荷结构。
	if !decodeSegment(s, &s.payload, payloadSeg) {
		return nil, errx.Detail(ErrMalformed, "payload is not base64url | 载荷不是 base64url")
	}
	var fields claimFields
	if !scanRegistered(s.payload, &fields) {
		return nil, errx.Detail(ErrMalformed, "payload is not a JSON object | 载荷不是 JSON 对象")
	}
	if !decodeSegment(s, &s.sig, sigSeg) {
		return nil, errx.Detail(ErrMalformed, "signature is not base64url | 签名不是 base64url")
	}

	// Acquire the Token before KeyFunc so Header.Kid and Header.Typ can point at token-owned storage
	// instead of allocating. One capacity calculation keeps a fresh token at a single data allocation;
	// a released token reuses its buffer and allocates nothing. A failed Parse drops the Token rather
	// than pooling it, so a KeyFunc that retains the header strings can never observe another request's
	// bytes. | 在 KeyFunc 之前取得 Token，使 Header.Kid 与 Header.Typ 指向 token 自有存储而不是另行
	// 分配。一次容量计算使新 token 只产生一次数据分配；已释放的 token 复用缓冲，零分配。失败的
	// Parse 会丢弃 Token 而不归还池，因此即使 KeyFunc 保留了头部字符串，也绝不会看到其他请求的字节。
	t := tokenPool.Get().(*Token)
	t.Header, t.Claims, t.Signature = Header{}, Claims{}, nil
	need := len(s.payload) + len(s.sig) + len(hf.kidRaw) + len(hf.typRaw)
	if cap(t.data) < need {
		t.data = make([]byte, 0, need)
	}
	data := t.data[:0]
	kidStart := len(data)
	data, _ = appendJSONString(data, hf.kidRaw)
	kidEnd := len(data)
	typStart := len(data)
	data, _ = appendJSONString(data, hf.typRaw)
	typEnd := len(data)
	hdr := Header{
		Alg:   hf.alg,
		Typ:   unsafeString(data[typStart:typEnd]),
		Kid:   unsafeString(data[kidStart:kidEnd]),
		Extra: hf.extra,
	}

	// Step 3: key resolution and signature. | 第三步：密钥解析与签名校验。
	key, err := keyFn(hdr)
	if err != nil {
		return nil, err
	}
	// The signing input is copied into a pooled buffer with append(dst, string...) so that no intermediate
	// []byte conversion happens; the bytes are the ones from the token, unchanged. | 签名输入通过
	// append(dst, string...) 拷入池化缓冲，不产生中间的 []byte 转换；其字节与 token 中的完全一致。
	s.input = append(s.input[:0], token[:first+1+second]...)
	if !verify(hdr.Alg, key, s.input, s.sig, s) {
		return nil, ErrSignatureInvalid
	}

	// Step 4: registered claims. | 第四步：登记声明。
	if err := checkRegistered(&fields, o); err != nil {
		return nil, err
	}

	// Everything verified: append the payload and signature after the header strings. Both views are
	// capped so that append by a caller can never reach into another region. | 全部校验通过：把载荷与
	// 签名追加到头部字符串之后。两个视图都做了容量限制，调用方 append 时绝不会写进其它区域。
	payloadStart := len(data)
	data = append(data, s.payload...)
	sigStart := len(data)
	data = append(data, s.sig...)
	t.data = data
	t.Header = hdr
	t.Claims = Claims{raw: data[payloadStart:sigStart:sigStart]}
	t.Signature = data[sigStart:]
	atomic.StoreUint32(&t.released, 0)
	return t, nil
}

// algAllowed reports whether alg passes the allow list; an empty list allows every supported algorithm. | algAllowed 报告 alg 是否通过白名单；空列表表示允许全部受支持的算法。
func algAllowed(valid []Alg, alg Alg) bool {
	if len(valid) == 0 {
		return true
	}
	for _, a := range valid {
		if a == alg {
			return true
		}
	}
	return false
}

// DefaultTokenLookup is the lookup used when MiddlewareOptions.TokenLookup is empty. | DefaultTokenLookup 是 MiddlewareOptions.TokenLookup 为空时使用的提取方式。
const DefaultTokenLookup = "header:Authorization:Bearer "

// DefaultCtxKey is the context key used when MiddlewareOptions.CtxKey is empty. | DefaultCtxKey 是 MiddlewareOptions.CtxKey 为空时使用的上下文键。
const DefaultCtxKey = "jwt.token"

// MiddlewareOptions configures Middleware. | MiddlewareOptions 配置 Middleware。
type MiddlewareOptions struct {
	// KeyFunc resolves the verification key; a nil KeyFunc fails every request instead of panicking. | KeyFunc 解析校验密钥；为空时每个请求都失败，而不是 panic。
	KeyFunc KeyFunc
	// ErrorHandler renders the failure; nil means 401 with a short plain-text body. | ErrorHandler 负责输出失败响应；为空表示返回 401 与简短纯文本正文。
	ErrorHandler func(*router.Ctx, error)
	// TokenLookup selects where the token is read from; the accepted forms are listed on Middleware. | TokenLookup 指定 token 的读取位置；可用形式见 Middleware 文档。
	TokenLookup string
	// CtxKey is the router context key holding the *Token; see DefaultCtxKey. | CtxKey 是保存 *Token 的路由上下文键；见 DefaultCtxKey。
	CtxKey string
	// ParseOptions is forwarded to Parse verbatim. | ParseOptions 原样转发给 Parse。
	ParseOptions ParseOptions
}

// Middleware verifies the token of a request and stores the *Token in the router context. | Middleware 校验请求携带的令牌，并把 *Token 存入路由上下文。
//
// A request without a token is rejected with ErrTokenNotFound and a request whose token fails Parse is
// rejected with the error Parse returned, so errors.Is works inside ErrorHandler. | 未携带 token 的请求以
// ErrTokenNotFound 拒绝；token 未通过 Parse 的请求以 Parse 返回的错误拒绝，因此 ErrorHandler 内可用 errors.Is 判定。
//
// TokenLookup accepts these forms, defaulting to DefaultTokenLookup | TokenLookup 接受以下形式，默认为 DefaultTokenLookup：
//
//	"header:Authorization:Bearer "  the named header, with the scheme prefix removed | 指定头部，去掉 scheme 前缀
//	"header:X-Token:"               the named header read as-is | 指定头部，原样读取
//	"X-Token"                       a bare name is treated as a header | 裸名称按头部处理
//	"query:token"                   a query parameter | 查询参数
//	"cookie:jwt"                    a cookie | 从 Cookie 读取
//
// The chain is stopped with Ctx.Abort before the error handler runs, so a rejected request never reaches the
// business handler. | 错误处理器执行前，链路会通过 Ctx.Abort 终止，因此被拒绝的请求绝不会到达业务处理器。
func Middleware(o MiddlewareOptions) router.Middleware {
	lookup, lookupErr := parseTokenLookup(o.TokenLookup)
	ctxKey := o.CtxKey
	if ctxKey == "" {
		ctxKey = DefaultCtxKey
	}
	handle := o.ErrorHandler
	if handle == nil {
		handle = defaultErrorHandler
	}
	keyFn := o.KeyFunc
	return func(c *router.Ctx) {
		if lookupErr != nil {
			c.Abort()
			handle(c, lookupErr)
			return
		}
		if keyFn == nil {
			c.Abort()
			handle(c, ErrNoKeyFunc)
			return
		}
		raw, ok := lookup(c)
		if !ok {
			c.Abort()
			handle(c, ErrTokenNotFound)
			return
		}
		tok, err := Parse(raw, keyFn, o.ParseOptions)
		if err != nil {
			c.Abort()
			handle(c, err)
			return
		}
		c.Set(ctxKey, tok)
		c.Next()
	}
}

// FromContext returns the *Token stored by Middleware for DefaultCtxKey. | FromContext 返回 Middleware 以 DefaultCtxKey 存入的 *Token。
func FromContext(c *router.Ctx) (*Token, bool) {
	v, ok := c.Get(DefaultCtxKey)
	if !ok {
		return nil, false
	}
	tok, ok := v.(*Token)
	return tok, ok
}

// tokenLookup extracts a token from a request. | tokenLookup 从请求中提取 token。
type tokenLookup func(c *router.Ctx) (string, bool)

// parseTokenLookup compiles a TokenLookup specification into an extractor. | parseTokenLookup 把 TokenLookup 配置编译成提取器。
//
// The scheme prefix of a header lookup is compared case-insensitively, so "bearer " and "Bearer " behave
// alike, and the extracted value is trimmed before the emptiness check. | 头部提取的 scheme 前缀按大小写不敏感比较，
// 因此 "bearer " 与 "Bearer " 行为一致；提取出的取值先去除首尾空白，再判断是否为空。
func parseTokenLookup(spec string) (tokenLookup, error) {
	if spec == "" {
		spec = DefaultTokenLookup
	}
	kind, rest := spec, ""
	if i := strings.IndexByte(spec, ':'); i >= 0 {
		kind, rest = spec[:i], spec[i+1:]
	}
	switch kind {
	case "header":
		name, prefix := rest, ""
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			name, prefix = rest[:i], rest[i+1:]
		}
		if name == "" {
			return nil, errx.Detail(ErrBadTokenLookup, "empty header name | 头部名称为空")
		}
		return func(c *router.Ctx) (string, bool) {
			return headerToken(c.Header(name), prefix)
		}, nil
	case "query":
		if rest == "" {
			return nil, errx.Detail(ErrBadTokenLookup, "empty query name | 查询参数名为空")
		}
		return func(c *router.Ctx) (string, bool) { return nonEmptyToken(c.Query(rest)) }, nil
	case "cookie":
		if rest == "" {
			return nil, errx.Detail(ErrBadTokenLookup, "empty cookie name | Cookie 名为空")
		}
		return func(c *router.Ctx) (string, bool) { return nonEmptyToken(c.Cookie(rest)) }, nil
	}
	// No recognised kind: the whole specification is a header name. | 未识别出类型：整个配置就是一个头部名称。
	return func(c *router.Ctx) (string, bool) { return headerToken(c.Header(spec), "") }, nil
}

// headerToken strips prefix from value and reports whether a token remains. | headerToken 从 value 中去掉 prefix，并报告是否还剩 token。
func headerToken(value, prefix string) (string, bool) {
	if prefix == "" {
		return nonEmptyToken(value)
	}
	if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return "", false
	}
	return nonEmptyToken(value[len(prefix):])
}

// nonEmptyToken trims surrounding whitespace and reports whether anything is left. | nonEmptyToken 去除首尾空白，并报告是否还有内容。
func nonEmptyToken(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false
	}
	return v, true
}

// defaultErrorHandler writes a 401 with a short plain-text body. | defaultErrorHandler 写出 401 与简短纯文本正文。
//
// The body never echoes the failure reason, so an attacker learns nothing about which check rejected the
// token. | 正文绝不回显失败原因，攻击者无法从中得知是哪一项校验拒绝了 token。
func defaultErrorHandler(c *router.Ctx, _ error) {
	_ = c.String(401, "unauthorized | 未授权")
}
