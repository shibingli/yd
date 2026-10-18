package jwt

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"math/big"
	"sort"
	"sync"
	"unicode/utf8"

	"github.com/shibingli/yd/core/b64x"
	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
)

// This file holds the signing and verification core. | 本文件是签发与校验的核心。
//
// Two rules shape it. First, the signature is always computed on the raw signing input, i.e. the exact
// "header.payload" ASCII bytes of the compact serialization, never on a re-encoded or re-serialized form.
// Second, every intermediate buffer comes from a sync.Pool, so a steady-state Parse or Sign performs no
// buffer allocation at all. | 两条规则贯穿全文：其一，签名始终在原始 signing input 上计算，
// 即紧凑序列化中 "header.payload" 的原始 ASCII 字节，绝不基于重新编码或重新序列化的形式；
// 其二，所有中间缓冲都来自 sync.Pool，因此稳态下 Parse 与 Sign 不产生任何缓冲分配。

// scratch holds the reusable buffers of one Parse or Sign call. | scratch 保存一次 Parse 或 Sign 调用中的可复用缓冲。
//
// A scratch never outlives the call that borrowed it, and never leaks into a Token, so returning it to the
// pool cannot hand out bytes that a caller still references. | scratch 的生命周期不超过借用它的那次调用，
// 也绝不会泄漏进 Token，因此归还池中不会把调用方仍在引用的字节再次分发出去。
type scratch struct {
	header  []byte  // decoded JOSE header | 解码后的 JOSE 头部
	payload []byte  // decoded payload | 解码后的载荷
	sig     []byte  // decoded signature | 解码后的签名
	input   []byte  // signing input: the bytes the signature covers | 签名输入：签名所覆盖的字节
	mac     []byte  // hash and MAC output space | 哈希与 MAC 的输出空间
	r, sv   big.Int // ECDSA verification operands reused across calls | ECDSA 校验操作数，跨调用复用
}

// scratchPool recycles *scratch values; pooling the pointer instead of the slices keeps the pool free of
// interface-boxing allocations. | scratchPool 回收 *scratch；池化指针而非切片，可避免接口装箱带来的分配。
var scratchPool = sync.Pool{New: func() any { return new(scratch) }}

// getScratch borrows a scratch and resets its slice lengths. | getScratch 借出一个 scratch 并把切片长度归零。
func getScratch() *scratch {
	s := scratchPool.Get().(*scratch)
	s.header, s.payload, s.sig, s.input, s.mac =
		s.header[:0], s.payload[:0], s.sig[:0], s.input[:0], s.mac[:0]
	return s
}

// putScratch returns a scratch to the pool. | putScratch 把 scratch 归还池中。
func putScratch(s *scratch) { scratchPool.Put(s) }

// decodeSegment base64url-decodes seg into dst without allocating an intermediate
// text buffer: AppendDecodeString reads the string directly. | decodeSegment 把 seg
// 以 base64url 解码到 dst，且不分配中间文本缓冲：AppendDecodeString 直接读取字符串。
func decodeSegment(s *scratch, dst *[]byte, seg string) bool {
	out, err := b64x.RawURLEncoding.AppendDecodeString((*dst)[:0], seg)
	if err != nil {
		return false
	}
	*dst = out
	return true
}

// headerFields holds the raw values located in the JOSE header. | headerFields 保存从 JOSE 头部中定位到的原始取值。
type headerFields struct {
	extra                  map[string]string
	alg                    Alg
	algRaw, kidRaw, typRaw []byte
	haveAlg                bool
}

// scanHeader scans the decoded JOSE header for alg, typ and kid. | scanHeader 从解码后的 JOSE 头部中扫描 alg、typ 与 kid。
//
// The typ and kid values are returned as raw slices so the caller can copy them into token-owned
// storage instead of allocating a string here. Every other string-valued parameter lands in extra;
// extra stays nil when there is none. The reserved parameters can never be smuggled into extra, so a
// caller that reads it cannot be fooled by a duplicate of a reserved name. | typ 与 kid 以原始切片
// 返回，调用方可以把它们复制进 token 自有存储，而不必在此分配字符串。其余取值为字符串的参数进入
// extra；没有时 extra 保持 nil。保留参数绝不会被塞进 extra，因此读取 extra 的调用方不会被保留名
// 的重复项误导。
func scanHeader(raw []byte, f *headerFields) error {
	var s scanner
	if !s.init(raw) {
		return errx.Detail(ErrMalformed, "header is not a JSON object | 头部不是 JSON 对象")
	}
	for s.next() {
		switch headerParam(s.key) {
		case headerParamAlg:
			f.algRaw, f.haveAlg = s.val, true
		case headerParamTyp:
			f.typRaw = s.val
		case headerParamKid:
			f.kidRaw = s.val
		default:
			if v, ok := jsonStringValue(s.val); ok {
				if f.extra == nil {
					f.extra = make(map[string]string, 4)
				}
				f.extra[jsonKeyString(s.key)] = v
			}
		}
	}
	if s.err {
		return errx.Detail(ErrMalformed, "header is not well-formed JSON | 头部不是结构良好的 JSON")
	}
	if !f.haveAlg {
		return errx.Detail(ErrAlgNotAllowed, "header has no alg | 头部缺少 alg")
	}
	alg, ok := algFromRaw(f.algRaw)
	if !ok {
		return errx.Detail(ErrAlgNotAllowed, "alg=%[1]q is unsupported | alg=%[1]q 不受支持", string(f.algRaw))
	}
	f.alg = alg
	return nil
}

// algFromRaw maps the raw alg value to a supported algorithm without allocating a string. | algFromRaw 把原始 alg 值映射为受支持的算法，且不产生字符串分配。
func algFromRaw(raw []byte) (Alg, bool) {
	body, ok := jsonStringBody(raw)
	if !ok {
		return "", false
	}
	for _, a := range allAlgs {
		// The body is compared byte by byte, so no string has to be materialized for the comparison. | 逐字节比较，
		// 因此比较过程无需生成字符串。
		if rawEqualString(body, string(a)) {
			return a, true
		}
	}
	return "", false
}

// allAlgs lists every supported algorithm in a fixed order. | allAlgs 以固定顺序列出全部受支持的算法。
var allAlgs = []Alg{HS256, HS384, HS512, RS256, RS384, RS512, PS256, PS384, PS512, ES256, ES384, ES512, EdDSA}

// SignOptions carries the non-claim part of a token. | SignOptions 承载 token 中与声明无关的部分。
type SignOptions struct {
	// Header adds extra header parameters. | Header 附加额外的头部参数。
	//
	// The reserved names alg, typ and kid are ignored, so this map can never override them. | 保留名 alg、typ 与 kid 会被忽略，因此该 map 无法覆盖它们。
	Header map[string]string
	// Kid sets the "kid" header parameter; empty means the parameter is omitted. | Kid 设置 "kid" 头部参数；为空表示省略该参数。
	Kid string
}

// Sign builds a compact JWS from claims and key. | Sign 用 claims 与 key 生成紧凑 JWS。
//
// Claims may be any value jsonx.Marshal accepts; a struct is faster than a
// map[string]any because it avoids map reflection. For an already encoded JSON
// object use SignJSON, which skips serialization entirely. | claims 可以是
// jsonx.Marshal 接受的任意值；结构体比 map[string]any 更快，因为它避免 map 反射。
// 已经编码好的 JSON 对象请用 SignJSON，它完全跳过序列化。
//
// The key must match the algorithm: []byte or string for HSxxx, *rsa.PrivateKey for RSxxx and PSxxx,
// *ecdsa.PrivateKey for ESxxx, and ed25519.PrivateKey for EdDSA; a key from another family is refused with
// ErrSignatureInvalid. | 密钥必须与算法匹配：HSxxx 用 []byte 或 string，RSxxx 与 PSxxx 用 *rsa.PrivateKey，
// ESxxx 用 *ecdsa.PrivateKey，EdDSA 用 ed25519.PrivateKey；密钥族不符时以 ErrSignatureInvalid 拒绝。
//
// The header always carries alg and typ; the payload is jsonx.Marshal of claims, and the signature covers the
// raw "header.payload" bytes produced here. | 头部始终包含 alg 与 typ；载荷由 jsonx.Marshal 序列化 claims，
// 签名覆盖此处产出的原始 "header.payload" 字节。
func Sign(claims any, key any, alg Alg, o SignOptions) (string, error) {
	if !supported(alg) {
		return "", errx.Detail(ErrAlgNotAllowed, "alg=%[1]q | 算法=%[1]q", string(alg))
	}
	payload, err := jsonx.Marshal(claims)
	if err != nil {
		return "", errx.WrapDetail(err, ErrInvalidClaims, "claims | 声明")
	}
	return signPayload(payload, key, alg, o)
}

// SignJSON signs an already encoded JSON object. | SignJSON 对已经编码好的 JSON 对象签名。
//
// The payload must be a single well-formed JSON object; it is validated
// structurally before signing, so a caller cannot accidentally mint a token
// whose bytes are not a JWT payload. The signature covers exactly payload, the
// bytes passed here, with no re-encoding. | 载荷必须是单个结构良好的 JSON 对象；签名前
// 会做结构校验，因此调用方不可能误签出不是 JWT 载荷的字节。签名精确覆盖传入的 payload，
// 不做任何重新编码。
func SignJSON(payload []byte, key any, alg Alg, o SignOptions) (string, error) {
	if !supported(alg) {
		return "", errx.Detail(ErrAlgNotAllowed, "alg=%[1]q | 算法=%[1]q", string(alg))
	}
	if !validJSONObject(payload) {
		return "", errx.Detail(ErrInvalidClaims, "payload is not a JSON object | 载荷不是 JSON 对象")
	}
	return signPayload(payload, key, alg, o)
}

// signPayload encodes the header and signs header.payload. | signPayload 编码头部并签名 header.payload。
func signPayload(payload []byte, key any, alg Alg, o SignOptions) (string, error) {
	s := getScratch()
	defer putScratch(s)

	// Encode both segments into pooled buffers and keep the signing input in one contiguous slice. | 把两段编码进池化缓冲，并让签名输入保持为一段连续内存。
	s.header = appendHeader(s.header[:0], alg, o)
	s.input = b64x.RawURLEncoding.AppendEncode(s.input[:0], s.header)
	s.input = append(s.input, '.')
	s.input = b64x.RawURLEncoding.AppendEncode(s.input, payload)
	sig, err := signRaw(alg, key, s.input, s)
	if err != nil {
		return "", err
	}

	// The signature is appended straight after the signing input, so the compact token occupies one buffer. | 签名直接追加在签名输入之后，因此紧凑 token 只占一个缓冲。
	s.input = append(s.input, '.')
	s.input = b64x.RawURLEncoding.AppendEncode(s.input, sig)
	return string(s.input), nil
}

// appendHeader serializes the protected header into dst without a map or reflection. | appendHeader
// 把受保护头部序列化进 dst，不构造 map，也不使用反射。
//
// The reserved names alg, typ and kid are controlled by SignOptions and can never be smuggled in
// through Header. Keys are sorted so that identical options produce identical header bytes. | 保留名
// alg、typ 与 kid 由 SignOptions 控制，绝不可能经由 Header 混入。键会排序，因此相同选项产生
// 相同的头部字节。
func appendHeader(dst []byte, alg Alg, o SignOptions) []byte {
	dst = append(dst, `{"alg":"`...)
	dst = append(dst, alg...)
	dst = append(dst, `","typ":"JWT"`...)
	if o.Kid != "" {
		dst = append(dst, `,"kid":`...)
		dst = appendJSONStringLiteral(dst, o.Kid)
	}
	if len(o.Header) > 0 {
		keys := make([]string, 0, len(o.Header))
		for k := range o.Header {
			switch k {
			case "alg", "typ", "kid":
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			dst = append(dst, ',')
			dst = appendJSONStringLiteral(dst, k)
			dst = append(dst, ':')
			dst = appendJSONStringLiteral(dst, o.Header[k])
		}
	}
	return append(dst, '}')
}

// appendJSONStringLiteral appends s as a JSON string literal, escaping control characters and quotes and
// replacing invalid UTF-8 bytes with U+FFFD, matching encoding/json's replacement rule. | appendJSONString
// 把 s 作为 JSON 字符串字面量追加，转义控制字符与引号，并把非法 UTF-8 字节替换为 U+FFFD，
// 与 encoding/json 的替换规则一致。
func appendJSONStringLiteral(dst []byte, s string) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch c {
			case '"', '\\':
				dst = append(dst, '\\', c)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				if c < 0x20 {
					dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0x0f])
				} else {
					dst = append(dst, c)
				}
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			dst = append(dst, `�`...)
			i++
			continue
		}
		dst = append(dst, s[i:i+size]...)
		i += size
	}
	return append(dst, '"')
}

// hmacSecret accepts the symmetric key forms and rejects every other type. | hmacSecret 接受对称密钥的形式，并拒绝其它所有类型。
//
// Rejecting non-symmetric types here is what defeats algorithm confusion: an RSA or ECDSA public key can
// never be reinterpreted as an HMAC secret. | 在此拒绝非对称类型正是击破算法混淆的关键：RSA 或 ECDSA 公钥永远不会被当作 HMAC 密钥使用。
func hmacSecret(key any) ([]byte, bool) {
	switch k := key.(type) {
	case []byte:
		return k, true
	case string:
		return []byte(k), true
	}
	return nil, false
}

// algHash returns the standard-library hash identity of an algorithm. | algHash 返回算法对应的标准库哈希标识。
func algHash(alg Alg) (crypto.Hash, bool) {
	switch alg {
	case HS256, RS256, PS256, ES256:
		return crypto.SHA256, true
	case HS384, RS384, PS384, ES384:
		return crypto.SHA384, true
	case HS512, RS512, PS512, ES512:
		return crypto.SHA512, true
	}
	return 0, false
}

// algCurve returns the NIST curve an ECDSA algorithm requires. | algCurve 返回 ECDSA 算法要求的 NIST 曲线。
func algCurve(alg Alg) elliptic.Curve {
	switch alg {
	case ES256:
		return elliptic.P256()
	case ES384:
		return elliptic.P384()
	case ES512:
		return elliptic.P521()
	}
	return nil
}

// signRaw signs input with key for alg. | signRaw 用 key 按 alg 对 input 签名。
func signRaw(alg Alg, key any, input []byte, s *scratch) ([]byte, error) {
	switch alg {
	case EdDSA:
		k, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, keyTypeError(alg)
		}
		return ed25519.Sign(k, input), nil
	}

	h, ok := algHash(alg)
	if !ok {
		return nil, errx.Detail(ErrAlgNotAllowed, "alg=%[1]q | 算法=%[1]q", string(alg))
	}

	switch alg {
	case HS256, HS384, HS512:
		secret, ok := hmacSecret(key)
		if !ok {
			return nil, keyTypeError(alg)
		}
		s.mac = algMACPool(alg).sum(secret, input, s.mac[:0])
		return s.mac, nil
	case RS256, RS384, RS512:
		k, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, keyTypeError(alg)
		}
		digest := algHashPool(alg).sum(input, s.mac[:0])
		sig, err := rsa.SignPKCS1v15(rand.Reader, k, h, digest)
		if err != nil {
			return nil, errx.WrapDetail(err, ErrSignatureInvalid, "RSA signing | RSA 签名")
		}
		return sig, nil
	case PS256, PS384, PS512:
		k, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, keyTypeError(alg)
		}
		digest := algHashPool(alg).sum(input, s.mac[:0])
		opts := &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: h}
		sig, err := rsa.SignPSS(rand.Reader, k, h, digest, opts)
		if err != nil {
			return nil, errx.WrapDetail(err, ErrSignatureInvalid, "RSA-PSS signing | RSA-PSS 签名")
		}
		return sig, nil
	case ES256, ES384, ES512:
		k, ok := key.(*ecdsa.PrivateKey)
		if !ok || !curveMatches(k.Curve, algCurve(alg)) {
			return nil, keyTypeError(alg)
		}
		digest := algHashPool(alg).sum(input, s.mac[:0])
		r, sv, err := ecdsa.Sign(rand.Reader, k, digest)
		if err != nil {
			return nil, errx.WrapDetail(err, ErrSignatureInvalid, "ECDSA signing | ECDSA 签名")
		}
		return appendFixedWidth(nil, r, sv, curveWidth(alg)), nil
	}
	return nil, errx.Detail(ErrAlgNotAllowed, "alg=%[1]q | 算法=%[1]q", string(alg))
}

// verify checks the signature of a compact token. | verify 校验紧凑 token 的签名。
//
// input is the raw "header.payload" signing input and sig is the decoded signature. | input 是原始的
// "header.payload" 签名输入，sig 是解码后的签名。
//
// A key from the wrong family is reported as ErrSignatureInvalid, never as an internal panic or a confusing
// success; that single rule closes algorithm confusion in both directions. | 密钥族不符一律报告
// ErrSignatureInvalid，既不 panic 也不会误判成功；这一条规则同时封堵了两个方向的算法混淆。
func verify(alg Alg, key any, input, sig []byte, s *scratch) bool {
	switch alg {
	case EdDSA:
		k, ok := key.(ed25519.PublicKey)
		if !ok {
			return false
		}
		return ed25519.Verify(k, input, sig)
	}

	h, ok := algHash(alg)
	if !ok {
		return false
	}

	switch alg {
	case HS256, HS384, HS512:
		secret, ok := hmacSecret(key)
		if !ok {
			return false
		}
		s.mac = algMACPool(alg).sum(secret, input, s.mac[:0])
		return hmac.Equal(s.mac, sig)
	case RS256, RS384, RS512:
		k, ok := key.(*rsa.PublicKey)
		if !ok {
			return false
		}
		digest := algHashPool(alg).sum(input, s.mac[:0])
		return rsa.VerifyPKCS1v15(k, h, digest, sig) == nil
	case PS256, PS384, PS512:
		k, ok := key.(*rsa.PublicKey)
		if !ok {
			return false
		}
		digest := algHashPool(alg).sum(input, s.mac[:0])
		opts := &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: h}
		return rsa.VerifyPSS(k, h, digest, sig, opts) == nil
	case ES256, ES384, ES512:
		k, ok := key.(*ecdsa.PublicKey)
		if !ok || !curveMatches(k.Curve, algCurve(alg)) {
			return false
		}
		width := curveWidth(alg)
		if len(sig) != 2*width {
			return false
		}
		digest := algHashPool(alg).sum(input, s.mac[:0])
		// Reuse the scratch big.Ints; ecdsa.Verify only reads them. | 复用 scratch 中的 big.Int；
		// ecdsa.Verify 只读取它们。
		s.r.SetBytes(sig[:width])
		s.sv.SetBytes(sig[width:])
		return ecdsa.Verify(k, digest, &s.r, &s.sv)
	}
	return false
}

// curveMatches reports whether c is the required curve, comparing by bit size. | curveMatches 报告 c 是否为要求的曲线，按位宽比较。
func curveMatches(c, want elliptic.Curve) bool {
	if c == nil || want == nil {
		return false
	}
	return c.Params().BitSize == want.Params().BitSize
}

// curveWidth returns the byte width of one ECDSA signature half for alg. | curveWidth 返回 alg 对应的 ECDSA 签名半值字节宽度。
func curveWidth(alg Alg) int {
	if c := algCurve(alg); c != nil {
		return (c.Params().BitSize + 7) / 8
	}
	return 0
}

// appendFixedWidth appends r and s as two fixed-width big-endian integers, the JWS ECDSA signature form. | appendFixedWidth 把 r 与 s 按定宽大端整数追加，即 JWS 的 ECDSA 签名形式。
//
// JWS requires this fixed-width form rather than the DER encoding produced by ecdsa.SignASN1, which is why the
// sign side does not use SignASN1. | JWS 要求定宽形式，而非 ecdsa.SignASN1 产出的 DER 编码，
// 因此签发侧不使用 SignASN1。
func appendFixedWidth(dst []byte, r, s *big.Int, width int) []byte {
	base := len(dst)
	dst = append(dst, make([]byte, 2*width)...)
	r.FillBytes(dst[base : base+width])
	s.FillBytes(dst[base+width:])
	return dst
}

// keyTypeError reports a key whose family does not match the algorithm. | keyTypeError 报告密钥族与算法不匹配。
func keyTypeError(alg Alg) error {
	return errx.Detail(ErrSignatureInvalid, "key type does not match alg=%[1]q | 密钥类型与 alg=%[1]q 不匹配", string(alg))
}
