package guomi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/asn1"
	"encoding/hex"
	"math/big"

	"github.com/tjfoc/gmsm/sm2"
	"github.com/yun-dev/yd/errx"
)

var (
	// ErrInvalidPrivateKey reports a nil, out-of-range or malformed SM2 private key. | ErrInvalidPrivateKey 表示 SM2 私钥为空、标量越界或编码非法。
	ErrInvalidPrivateKey = errx.New("guomi: invalid SM2 private key | guomi：非法的 SM2 私钥")

	// ErrInvalidPublicKey reports a nil, off-curve or malformed SM2 public key. | ErrInvalidPublicKey 表示 SM2 公钥为空、坐标不在曲线上或编码非法。
	ErrInvalidPublicKey = errx.New("guomi: invalid SM2 public key | guomi：非法的 SM2 公钥")

	// ErrInvalidSignature reports an SM2 signature that cannot be decoded. | ErrInvalidSignature 表示 SM2 签名编码非法（如非法十六进制）。
	ErrInvalidSignature = errx.New("guomi: invalid SM2 signature | guomi：非法的 SM2 签名")

	// ErrDecrypt reports a failed SM2 decryption. | ErrDecrypt 表示 SM2 解密失败。
	// It covers C3 verification failures and all-zero key derivation output. | 涵盖 C3 校验不通过与密钥派生结果全为零两种情况。
	ErrDecrypt = errx.New("guomi: SM2 decryption failed | guomi：SM2 解密失败")

	// ErrSign reports a failed SM2 signature generation. | ErrSign 表示 SM2 签名生成失败。
	ErrSign = errx.New("guomi: SM2 signing failed | guomi：SM2 签名失败")

	// ErrSM2ASN1 reports a failed ASN.1 (DER) encoding of SM2 material. | ErrSM2ASN1 表示 SM2 数据的 ASN.1（DER）编码失败。
	ErrSM2ASN1 = errx.New("guomi: SM2 ASN.1 encoding failed | guomi：SM2 ASN.1 编码失败")
)

// PrivateKey is an SM2 private key, a thin wrapper around gmsm's *sm2.PrivateKey. | PrivateKey 是 SM2 私钥，薄封装 gmsm 的 *sm2.PrivateKey。
// Raw returns the underlying key for interoperability with existing gmsm code. | 通过 Raw() 可取回底层密钥，与既有 gmsm 代码/接口互操作。
type PrivateKey struct {
	raw *sm2.PrivateKey
}

// PublicKey is an SM2 public key, a thin wrapper around gmsm's *sm2.PublicKey. | PublicKey 是 SM2 公钥，薄封装 gmsm 的 *sm2.PublicKey。
type PublicKey struct {
	raw *sm2.PublicKey
}

// NewPrivateKey wraps an underlying gmsm private key, returning nil when raw is nil. | NewPrivateKey 用底层 gmsm 私钥构造 PrivateKey；raw 为 nil 时返回 nil。
func NewPrivateKey(raw *sm2.PrivateKey) *PrivateKey {
	if raw == nil {
		return nil
	}
	return &PrivateKey{raw: raw}
}

// NewPublicKey wraps an underlying gmsm public key, returning nil when raw is nil. | NewPublicKey 用底层 gmsm 公钥构造 PublicKey；raw 为 nil 时返回 nil。
func NewPublicKey(raw *sm2.PublicKey) *PublicKey {
	if raw == nil {
		return nil
	}
	return &PublicKey{raw: raw}
}

// Raw returns the underlying gmsm private key, or nil when the receiver is nil. | Raw 返回底层 gmsm 私钥；接收者为 nil 时返回 nil。
func (k *PrivateKey) Raw() *sm2.PrivateKey {
	if k == nil {
		return nil
	}
	return k.raw
}

// Raw returns the underlying gmsm public key, or nil when the receiver is nil. | Raw 返回底层 gmsm 公钥；接收者为 nil 时返回 nil。
func (k *PublicKey) Raw() *sm2.PublicKey {
	if k == nil {
		return nil
	}
	return k.raw
}

// Public returns the public key matching the private key, or nil when the receiver is nil. | Public 返回私钥对应的公钥；接收者为 nil 时返回 nil。
func (k *PrivateKey) Public() *PublicKey {
	if k == nil || k.raw == nil {
		return nil
	}
	return &PublicKey{raw: &k.raw.PublicKey}
}

// Hex returns the private scalar D as 64 lowercase hex characters, that is 32 big-endian bytes left-padded with zeros. | Hex 返回私钥标量 D 的 64 字符小写十六进制（32 字节大端，左补零）。
// It returns an empty string for a nil or invalid private key. | 私钥非法或为 nil 时返回空串。
func (k *PrivateKey) Hex() string {
	if _, err := validPrivate(k); err != nil {
		return ""
	}
	return hex.EncodeToString(pad32(k.raw.D))
}

// Hex returns the uncompressed point hex of the public key, 04||X||Y in 130 lowercase characters. | Hex 返回公钥的未压缩点十六进制（04||X||Y，共 130 字符小写）。
// It returns an empty string for a nil or invalid public key. | 密钥非法或为 nil 时返回空串。
func (k *PublicKey) Hex() string { return PublicKeyToHex(k) }

// Encrypt encrypts data with the public key and returns an ASN.1 (DER) encoded C1C3C2 ciphertext: | Encrypt 使用公钥加密 data，输出 ASN.1（DER）编码的 C1C3C2 密文：
//
//	SEQUENCE { x INTEGER, y INTEGER, C3 OCTET STRING(32), C2 OCTET STRING }
//
// This form interoperates with gmsm and Java style ASN.1 ciphertext. | 该格式与 gmsm/Java 等实现的 ASN.1 密文互通。
// For front-end JavaScript interoperability use the fixed-width layout of EncryptRaw instead. | 前端 JS 互操作请使用 EncryptRaw 的定长拼接格式。
// The plaintext, including the empty plaintext, always round-trips. | 密文可解出明文（含空明文）。
func Encrypt(pub *PublicKey, data []byte) ([]byte, error) {
	raw, err := EncryptRaw(pub, data)
	if err != nil {
		return nil, err
	}
	der, err := asn1.Marshal(sm2CipherASN1{
		XCoordinate: new(big.Int).SetBytes(raw[:SM2CoordinateSize]),
		YCoordinate: new(big.Int).SetBytes(raw[SM2CoordinateSize : 2*SM2CoordinateSize]),
		Hash:        raw[2*SM2CoordinateSize : 2*SM2CoordinateSize+SM3Size],
		CipherText:  raw[2*SM2CoordinateSize+SM3Size:],
	})
	if err != nil {
		return nil, errx.Wrap(err, ErrSM2ASN1)
	}
	return der, nil
}

// EncryptRaw encrypts data with the public key and returns the raw concatenated C1C3C2 ciphertext in this byte layout: | EncryptRaw 使用公钥加密 data，输出**原始拼接**的 C1C3C2 密文，字节布局为：
//
//	X1(32) || Y1(32) || C3(32) || C2(len(data))
//
// The first 64 bytes are the X and Y components of the curve point C1 in big-endian order, left-padded with zeros. | 前 64 字节是曲线点 C1 的 X、Y 分量（大端、左补零）。
// There is no uncompressed point marker byte 0x04 in front of them. | 不带未压缩点标识字节 0x04。
// The next 32 bytes are C3 = SM3(x2 || data || y2). | 随后 32 字节为 C3 = SM3(x2 || data || y2)。
// The remainder is C2, the plaintext XORed with the key derivation stream KDF(x2 || y2). | 其余为 C2，即明文与密钥派生流 KDF(x2 || y2) 的异或结果。
// The total length is always 96+len(data). | 总长度恒为 96+len(data)。
//
// This fixed-width layout is the interoperability baseline for a pure JavaScript front end. | 该定长布局是前端纯 JS 实现的互操作基准。
// gmsm's ciphertext of the same name carries one leading 0x04 byte, which equals this format once that byte is removed. | gmsm 的同名密文多一个前导 0x04，去掉该字节即与本格式完全一致。
// Use Encrypt for ASN.1 scenarios. | ASN.1 场景请用 Encrypt。
//
// Every call picks a fresh random k from crypto/rand, so encrypting the same plaintext twice gives different ciphertext. | 每次调用都重新选取随机数 k（来自 crypto/rand），同一明文两次加密结果不同。
func EncryptRaw(pub *PublicKey, data []byte) ([]byte, error) {
	p, err := validPublic(pub)
	if err != nil {
		return nil, err
	}
	curve := p.Curve
	n := curve.Params().N
	klen := len(data)

	for {
		k, err := randScalar(n)
		if err != nil {
			return nil, err
		}
		x1, y1 := curve.ScalarBaseMult(k.Bytes())
		x2, y2 := curve.ScalarMult(p.X, p.Y, k.Bytes())
		xs, ys := pad32(x2), pad32(y2)

		stream, ok := sm2KDF(xs, ys, klen)
		if !ok {
			// All-zero key derivation output: pick a new k as GB/T 32918.4 requires. | 密钥派生结果全为零：按 GB/T 32918.4 重新选取 k。
			continue
		}

		c2 := make([]byte, klen)
		for i := 0; i < klen; i++ {
			c2[i] = data[i] ^ stream[i]
		}
		c3 := sm3C3(xs, data, ys)

		out := make([]byte, 0, 2*SM2CoordinateSize+SM3Size+klen)
		out = append(out, pad32(x1)...)
		out = append(out, pad32(y1)...)
		out = append(out, c3...)
		out = append(out, c2...)
		return out, nil
	}
}

// Decrypt decrypts an ASN.1 (DER) ciphertext produced by Encrypt. | Decrypt 解密 Encrypt 产生的 ASN.1（DER）密文。
//
// It reports ErrInvalidCiphertext for a malformed structure and ErrDecrypt when the C3 check fails. | 密文结构非法时返回 ErrInvalidCiphertext，认证（C3 校验）失败时返回 ErrDecrypt。
func Decrypt(priv *PrivateKey, ciphertext []byte) ([]byte, error) {
	if _, err := validPrivate(priv); err != nil {
		return nil, err
	}
	var c sm2CipherASN1
	if _, err := asn1.Unmarshal(ciphertext, &c); err != nil {
		return nil, errx.WrapDetail(err, ErrInvalidCiphertext, "ASN.1 parse failed | ASN.1 解析失败")
	}
	if c.XCoordinate == nil || c.YCoordinate == nil {
		return nil, errx.Detail(ErrInvalidCiphertext, "C1 coordinates are missing from the ASN.1 structure | ASN.1 中缺少 C1 坐标")
	}
	x, ok := coordBytes(c.XCoordinate)
	if !ok {
		return nil, errx.Detail(ErrInvalidCiphertext, "C1 X coordinate is too long | C1 的 X 坐标超长")
	}
	y, ok := coordBytes(c.YCoordinate)
	if !ok {
		return nil, errx.Detail(ErrInvalidCiphertext, "C1 Y coordinate is too long | C1 的 Y 坐标超长")
	}
	if len(c.Hash) != SM3Size {
		return nil, errx.Detail(ErrInvalidCiphertext, "C3 length %[1]d, want %[2]d | C3 长度 %[1]d，期望 %[2]d", len(c.Hash), SM3Size)
	}

	raw := make([]byte, 0, 2*SM2CoordinateSize+SM3Size+len(c.CipherText))
	raw = append(raw, x...)
	raw = append(raw, y...)
	raw = append(raw, c.Hash...)
	raw = append(raw, c.CipherText...)
	return DecryptRaw(priv, raw)
}

// DecryptRaw decrypts the raw concatenated ciphertext X1||Y1||C3||C2 produced by EncryptRaw, which has no 0x04 prefix. | DecryptRaw 解密 EncryptRaw 产生的原始拼接密文（X1||Y1||C3||C2，无 0x04 前缀）。
//
// A ciphertext shorter than 96 bytes or a C1 point outside the SM2 curve yields ErrInvalidCiphertext. | 长度小于 96 字节或 C1 不在 SM2 曲线上时返回 ErrInvalidCiphertext。
// A failed C3 check, caused by tampering or a wrong private key, yields ErrDecrypt. | C3 校验失败（密文被篡改或私钥不匹配）时返回 ErrDecrypt。
// It never panics. | 绝不 panic。
func DecryptRaw(priv *PrivateKey, ciphertext []byte) ([]byte, error) {
	k, err := validPrivate(priv)
	if err != nil {
		return nil, err
	}
	const minLen = 2*SM2CoordinateSize + SM3Size
	if len(ciphertext) < minLen {
		return nil, errx.Detail(ErrInvalidCiphertext, "length %[1]d is below the minimum %[2]d | 长度 %[1]d 小于最小长度 %[2]d", len(ciphertext), minLen)
	}

	curve := k.Curve
	x1 := new(big.Int).SetBytes(ciphertext[:SM2CoordinateSize])
	y1 := new(big.Int).SetBytes(ciphertext[SM2CoordinateSize : 2*SM2CoordinateSize])
	if !curve.IsOnCurve(x1, y1) {
		return nil, errx.Detail(ErrInvalidCiphertext, "C1 is not on the SM2 curve | C1 不在 SM2 曲线上")
	}
	c3 := ciphertext[2*SM2CoordinateSize : 2*SM2CoordinateSize+SM3Size]
	c2 := ciphertext[2*SM2CoordinateSize+SM3Size:]

	x2, y2 := curve.ScalarMult(x1, y1, k.D.Bytes())
	xs, ys := pad32(x2), pad32(y2)
	stream, ok := sm2KDF(xs, ys, len(c2))
	if !ok {
		return nil, errx.Detail(ErrDecrypt, "key derivation output is all zero | 密钥派生结果全为零")
	}

	plaintext := make([]byte, len(c2))
	for i := range c2 {
		plaintext[i] = c2[i] ^ stream[i]
	}
	if subtle.ConstantTimeCompare(sm3C3(xs, plaintext, ys), c3) != 1 {
		return nil, errx.Detail(ErrDecrypt, "C3 verification failed | C3 校验失败")
	}
	return plaintext, nil
}

// Sign signs data with the private key and returns an ASN.1 (DER) encoded (r, s) pair: | Sign 使用私钥对 data 做 SM2 签名，输出 ASN.1（DER）编码的 (r, s)：
//
//	SEQUENCE { r INTEGER, s INTEGER }
//
// Per GB/T 32918.2 the digest is SM3(ZA || data), where ZA uses the standard default user ID "1234567812345678". | 摘要按 GB/T 32918.2 计算为 SM3(ZA || data)，其中 ZA 使用标准默认用户标识 "1234567812345678"。
// That matches the default behaviour of gmsm and of most server implementations, so signatures verify across them. | 与 gmsm、多数服务端实现的默认行为一致，可与 gmsm 直接互验。
func Sign(priv *PrivateKey, data []byte) ([]byte, error) {
	k, err := validPrivate(priv)
	if err != nil {
		return nil, err
	}
	sig, err := k.Sign(rand.Reader, data, nil)
	if err != nil {
		return nil, errx.Wrap(err, ErrSign)
	}
	return sig, nil
}

// Verify checks the SM2 signature sig over data, in the ASN.1/DER form produced by Sign or an equivalent gmsm implementation. | Verify 校验 data 的 SM2 签名 sig（ASN.1/DER 格式，由 Sign 或 gmsm 等同构实现产生）。
//
// A malformed signature or a failed verification returns (false, nil), so signature content differences are never exposed as errors. | 签名结构非法或验签不通过时返回 (false, nil)（不把签名内容差异暴露为错误）。
// An invalid public key, meaning nil or off-curve, returns (false, error). | 公钥非法（nil、坐标不在曲线上）时返回 (false, error)。
func Verify(pub *PublicKey, data, sig []byte) (bool, error) {
	p, err := validPublic(pub)
	if err != nil {
		return false, err
	}
	return p.Verify(data, sig), nil
}

// SignHex signs data with the private key and returns the ASN.1 signature in lowercase hex. | SignHex 使用私钥对 data 签名，返回小写十六进制编码的 ASN.1 签名。
func SignHex(priv *PrivateKey, data []byte) (string, error) {
	sig, err := Sign(priv, data)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sig), nil
}

// VerifyHex checks a hex encoded SM2 signature. | VerifyHex 校验十六进制编码的 SM2 签名。
// It returns an error when sigHex is not valid hexadecimal. | sigHex 不是合法十六进制时返回错误。
func VerifyHex(pub *PublicKey, data []byte, sigHex string) (bool, error) {
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return false, errx.WrapDetail(err, ErrInvalidSignature, "signature is not valid hexadecimal | 签名不是合法十六进制")
	}
	return Verify(pub, data, sig)
}

// sm2CipherASN1 is the ASN.1 structure of an SM2 ciphertext, matching gmsm/x509. | sm2CipherASN1 是 SM2 密文的 ASN.1 结构（与 gmsm/x509 的表示一致）。
// Coordinates are INTEGERs, while C3 and C2 are OCTET STRINGs. | 坐标用 INTEGER、C3 与 C2 用 OCTET STRING。
type sm2CipherASN1 struct {
	XCoordinate *big.Int
	YCoordinate *big.Int
	Hash        []byte
	CipherText  []byte
}

// validPublic checks that the key is non-nil and its point lies on the SM2 curve, returning the underlying gmsm key. | validPublic 校验公钥非空且点位于 SM2 曲线上，返回底层 gmsm 公钥。
func validPublic(pub *PublicKey) (*sm2.PublicKey, error) {
	p := pub.Raw()
	if p == nil || p.Curve == nil || p.X == nil || p.Y == nil {
		return nil, errx.Detail(ErrInvalidPublicKey, "public key is nil | 公钥为空")
	}
	if !p.Curve.IsOnCurve(p.X, p.Y) {
		return nil, errx.Detail(ErrInvalidPublicKey, "public key is not on the SM2 curve | 公钥不在 SM2 曲线上")
	}
	return p, nil
}

// validPrivate checks that the key is non-nil and its scalar lies in [1, n-1], returning the underlying gmsm key. | validPrivate 校验私钥非空且标量在 [1, n-1] 内，返回底层 gmsm 私钥。
func validPrivate(priv *PrivateKey) (*sm2.PrivateKey, error) {
	k := priv.Raw()
	if k == nil || k.Curve == nil || k.D == nil {
		return nil, errx.Detail(ErrInvalidPrivateKey, "private key is nil | 私钥为空")
	}
	n := k.Curve.Params().N
	if k.D.Sign() <= 0 || k.D.Cmp(n) >= 0 {
		return nil, errx.Detail(ErrInvalidPrivateKey, "scalar D is out of range | 标量 D 越界")
	}
	return k, nil
}

// randScalar draws a uniform random scalar in [1, n-1] from crypto/rand by rejection sampling. | randScalar 使用 crypto/rand 生成 [1, n-1] 内的随机标量（拒绝采样）。
func randScalar(n *big.Int) (*big.Int, error) {
	buf := make([]byte, (n.BitLen()+7)/8)
	for {
		if _, err := rand.Read(buf); err != nil {
			return nil, errx.Wrap(err, ErrRandomSource)
		}
		k := new(big.Int).SetBytes(buf)
		if k.Sign() > 0 && k.Cmp(n) < 0 {
			return k, nil
		}
	}
}

// sm2KDF implements the GB/T 32918.4 §5.4.3 key derivation function. | sm2KDF 实现 GB/T 32918.4 §5.4.3 的密钥派生函数。
// It hashes x2||y2||ct with SM3 for an incrementing counter ct until the output reaches klen bytes. | 以 ct 从 1 递增拼接 x2||y2||ct 后做 SM3，直到输出长度达到 klen。
//
// ok=false means the derived output is all zeros. | 返回 ok=false 表示派生结果全为零。
// An empty stream for klen == 0 counts as a success. | klen == 0 时空密钥流视为成功。
func sm2KDF(x2, y2 []byte, klen int) ([]byte, bool) {
	if klen == 0 {
		return []byte{}, true
	}
	h := New()
	out := make([]byte, 0, klen)
	var counter [4]byte
	for ct := 1; len(out) < klen; ct++ {
		h.Reset()
		h.Write(x2)
		h.Write(y2)
		counter[0] = byte(ct >> 24)
		counter[1] = byte(ct >> 16)
		counter[2] = byte(ct >> 8)
		counter[3] = byte(ct)
		h.Write(counter[:])
		out = h.Sum(out)
	}
	out = out[:klen]

	allZero := true
	for _, b := range out {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return out, false
	}
	return out, true
}

// sm3C3 computes C3 = SM3(x2 || data || y2). | sm3C3 计算 C3 = SM3(x2 || data || y2)。
func sm3C3(x2, data, y2 []byte) []byte {
	h := New()
	h.Write(x2)
	h.Write(data)
	h.Write(y2)
	return h.Sum(nil)
}

// pad32 converts a curve coordinate or scalar to its 32-byte big-endian form, left-padded with zeros. | pad32 把曲线坐标或标量转为 32 字节大端表示（左补零）。
// A value wider than 32 bytes keeps its low 32 bytes and never panics. | 数值超过 32 字节时保留低 32 字节，绝不 panic。
func pad32(x *big.Int) []byte {
	out := make([]byte, SM2CoordinateSize)
	b := x.Bytes()
	if len(b) >= SM2CoordinateSize {
		copy(out, b[len(b)-SM2CoordinateSize:])
		return out
	}
	copy(out[SM2CoordinateSize-len(b):], b)
	return out
}

// coordBytes converts an integer coordinate parsed from ASN.1 into its 32-byte big-endian form. | coordBytes 把 ASN.1 解析出的整数坐标转为 32 字节大端表示。
// It returns ok=false for a negative value or one of 32 bytes or more, that is >= 2^256. | 若数值为负或超过 32 字节（即 >= 2^256）则返回 ok=false。
func coordBytes(x *big.Int) ([]byte, bool) {
	if x.Sign() < 0 || x.BitLen() > 8*SM2CoordinateSize {
		return nil, false
	}
	return pad32(x), true
}
