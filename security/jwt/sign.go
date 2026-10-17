package jwt

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"hash"
	"math/big"
	"sync"

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
	b64    []byte // base64 text of one segment | 单个段的 base64 文本
	header []byte // decoded JOSE header | 解码后的 JOSE 头部
	input  []byte // signing input: the bytes the signature covers | 签名输入：签名所覆盖的字节
	mac    []byte // hash and MAC output space | 哈希与 MAC 的输出空间
}

// scratchPool recycles *scratch values; pooling the pointer instead of the slices keeps the pool free of
// interface-boxing allocations. | scratchPool 回收 *scratch；池化指针而非切片，可避免接口装箱带来的分配。
var scratchPool = sync.Pool{New: func() any { return new(scratch) }}

// getScratch borrows a scratch and resets its slice lengths. | getScratch 借出一个 scratch 并把切片长度归零。
func getScratch() *scratch {
	s := scratchPool.Get().(*scratch)
	s.b64, s.header, s.input, s.mac = s.b64[:0], s.header[:0], s.input[:0], s.mac[:0]
	return s
}

// putScratch returns a scratch to the pool. | putScratch 把 scratch 归还池中。
func putScratch(s *scratch) { scratchPool.Put(s) }

// decodeSegment base64url-decodes seg into s.header, using s.b64 as the text buffer. | decodeSegment 把 seg 以 base64url 解码到 s.header，并使用 s.b64 作为文本缓冲。
//
// The text is copied with append(dst, string...) so that a string segment never has to be converted into a
// fresh []byte, and AppendDecode then reuses the destination array. | 文本通过 append(dst, string...) 拷贝，
// 因此字符串段无需转换成新的 []byte，随后 AppendDecode 复用目标数组。
func decodeSegment(s *scratch, seg string) bool {
	s.b64 = append(s.b64[:0], seg...)
	out, err := b64x.RawURLEncoding.AppendDecode(s.header[:0], s.b64)
	if err != nil {
		return false
	}
	s.header = out
	return true
}

// scanHeader scans the decoded JOSE header for alg, typ and kid. | scanHeader 从解码后的 JOSE 头部中扫描 alg、typ 与 kid。
//
// Every other string-valued parameter lands in Header.Extra; Header.Extra stays nil when there is none. | 其余取值为字符串的参数都会进入 Header.Extra；没有这类参数时 Header.Extra 保持为 nil。
//
// The reserved parameters alg, typ and kid can never be smuggled into Extra, so a caller that reads Extra
// cannot be fooled by a duplicate of a reserved name. | 保留参数 alg、typ 与 kid 绝不会被塞进 Extra，
// 因此读取 Extra 的调用方不会被保留名的重复项误导。
func scanHeader(raw []byte, h *Header) error {
	var s scanner
	if !s.init(raw) {
		return errx.Detail(ErrMalformed, "header is not a JSON object | 头部不是 JSON 对象")
	}
	var (
		algRaw  []byte
		haveAlg bool
		extra   map[string]string
	)
	for s.next() {
		switch {
		case keyMatches(s.key, "alg"):
			algRaw, haveAlg = s.val, true
		case keyMatches(s.key, "typ"):
			h.Typ = jsonStringValueOrEmpty(s.val)
		case keyMatches(s.key, "kid"):
			h.Kid, _ = jsonStringValue(s.val)
		default:
			if v, ok := jsonStringValue(s.val); ok {
				if extra == nil {
					extra = make(map[string]string, 4)
				}
				extra[jsonKeyString(s.key)] = v
			}
		}
	}
	if s.err {
		return errx.Detail(ErrMalformed, "header is not well-formed JSON | 头部不是结构良好的 JSON")
	}
	if !haveAlg {
		return errx.Detail(ErrAlgNotAllowed, "header has no alg | 头部缺少 alg")
	}
	alg, ok := algFromRaw(algRaw)
	if !ok {
		return errx.Detail(ErrAlgNotAllowed, "alg=%[1]q is unsupported | alg=%[1]q 不受支持", string(algRaw))
	}
	h.Alg = alg
	h.Extra = extra
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
// The key must match the algorithm: []byte or string for HSxxx, *rsa.PrivateKey for RSxxx and PSxxx,
// *ecdsa.PrivateKey for ESxxx, and ed25519.PrivateKey for EdDSA; a key from another family is refused with
// ErrSignatureInvalid. | 密钥必须与算法匹配：HSxxx 用 []byte 或 string，RSxxx 与 PSxxx 用 *rsa.PrivateKey，
// ESxxx 用 *ecdsa.PrivateKey，EdDSA 用 ed25519.PrivateKey；密钥族不符时以 ErrSignatureInvalid 拒绝。
//
// The header always carries alg and typ; the payload is jsonx.Marshal of claims, and the signature covers the
// raw "header.payload" bytes produced here. | 头部始终包含 alg 与 typ；载荷由 jsonx.Marshal 序列化 claims，
// 签名覆盖此处产出的原始 "header.payload" 字节。
func Sign(claims map[string]any, key any, alg Alg, o SignOptions) (string, error) {
	if !supported(alg) {
		return "", errx.Detail(ErrAlgNotAllowed, "alg=%[1]q | 算法=%[1]q", string(alg))
	}
	hdr, err := marshalHeader(alg, o)
	if err != nil {
		return "", err
	}
	payload, err := jsonx.Marshal(claims)
	if err != nil {
		return "", errx.WrapDetail(err, ErrInvalidClaims, "claims | 声明")
	}

	s := getScratch()
	defer putScratch(s)

	// Encode both segments into pooled buffers and keep the signing input in one contiguous slice. | 把两段编码进池化缓冲，并让签名输入保持为一段连续内存。
	s.input = b64x.RawURLEncoding.AppendEncode(s.input[:0], hdr)
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

// marshalHeader serializes the protected header. | marshalHeader 序列化受保护头部。
func marshalHeader(alg Alg, o SignOptions) ([]byte, error) {
	m := make(map[string]string, 3+len(o.Header))
	for k, v := range o.Header {
		switch k {
		case "alg", "typ", "kid":
			continue
		}
		m[k] = v
	}
	m["alg"] = string(alg)
	m["typ"] = "JWT"
	if o.Kid != "" {
		m["kid"] = o.Kid
	}
	raw, err := jsonx.Marshal(m)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrMalformed, "header | 头部")
	}
	return raw, nil
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

// algHash returns the standard-library hash of an algorithm. | algHash 返回算法对应的标准库哈希。
func algHash(alg Alg) (crypto.Hash, func() hash.Hash, bool) {
	switch alg {
	case HS256, RS256, PS256, ES256:
		return crypto.SHA256, sha256.New, true
	case HS384, RS384, PS384, ES384:
		return crypto.SHA384, sha512.New384, true
	case HS512, RS512, PS512, ES512:
		return crypto.SHA512, sha512.New, true
	}
	return 0, nil, false
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

	h, hashFn, ok := algHash(alg)
	if !ok {
		return nil, errx.Detail(ErrAlgNotAllowed, "alg=%[1]q | 算法=%[1]q", string(alg))
	}

	switch alg {
	case HS256, HS384, HS512:
		secret, ok := hmacSecret(key)
		if !ok {
			return nil, keyTypeError(alg)
		}
		mac := hmac.New(hashFn, secret)
		mac.Write(input)
		return mac.Sum(nil), nil
	case RS256, RS384, RS512:
		k, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, keyTypeError(alg)
		}
		digest, err := digestOf(hashFn, input, s)
		if err != nil {
			return nil, err
		}
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
		digest, err := digestOf(hashFn, input, s)
		if err != nil {
			return nil, err
		}
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
		digest, err := digestOf(hashFn, input, s)
		if err != nil {
			return nil, err
		}
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

	_, hashFn, ok := algHash(alg)
	if !ok {
		return false
	}

	switch alg {
	case HS256, HS384, HS512:
		secret, ok := hmacSecret(key)
		if !ok {
			return false
		}
		mac := hmac.New(hashFn, secret)
		mac.Write(input)
		s.mac = mac.Sum(s.mac[:0])
		return hmac.Equal(s.mac, sig)
	case RS256, RS384, RS512:
		k, ok := key.(*rsa.PublicKey)
		if !ok {
			return false
		}
		h, _, _ := algHash(alg)
		digest := digestInto(hashFn, input, s.mac[:0])
		return rsa.VerifyPKCS1v15(k, h, digest, sig) == nil
	case PS256, PS384, PS512:
		k, ok := key.(*rsa.PublicKey)
		if !ok {
			return false
		}
		h, _, _ := algHash(alg)
		digest := digestInto(hashFn, input, s.mac[:0])
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
		digest := digestInto(hashFn, input, s.mac[:0])
		r := new(big.Int).SetBytes(sig[:width])
		sv := new(big.Int).SetBytes(sig[width:])
		return ecdsa.Verify(k, digest, r, sv)
	}
	return false
}

// digestOf returns the digest of input, writing into the pooled buffer. | digestOf 返回 input 的摘要，并写入池化缓冲。
func digestOf(hashFn func() hash.Hash, input []byte, s *scratch) ([]byte, error) {
	h := hashFn()
	if _, err := h.Write(input); err != nil {
		return nil, errx.WrapDetail(err, ErrMalformed, "hash | 哈希")
	}
	s.mac = h.Sum(s.mac[:0])
	return s.mac, nil
}

// digestInto returns the digest of input appended to dst. | digestInto 把 input 的摘要追加到 dst 并返回。
func digestInto(hashFn func() hash.Hash, input, dst []byte) []byte {
	h := hashFn()
	h.Write(input)
	return h.Sum(dst)
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
