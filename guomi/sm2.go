package guomi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"math/big"

	"github.com/tjfoc/gmsm/sm2"
	"github.com/tjfoc/gmsm/sm3"
)

var (
	// ErrInvalidPrivateKey 表示 SM2 私钥为空、标量越界或编码非法。
	ErrInvalidPrivateKey = errors.New("guomi: 非法的 SM2 私钥")

	// ErrInvalidPublicKey 表示 SM2 公钥为空、坐标不在曲线上或编码非法。
	ErrInvalidPublicKey = errors.New("guomi: 非法的 SM2 公钥")

	// ErrInvalidSignature 表示 SM2 签名编码非法（如非法十六进制）。
	ErrInvalidSignature = errors.New("guomi: 非法的 SM2 签名")

	// ErrDecrypt 表示 SM2 解密失败：C3 校验不通过或密钥派生函数输出异常。
	ErrDecrypt = errors.New("guomi: SM2 解密失败")
)

// PrivateKey 是 SM2 私钥，薄封装 gmsm 的 *sm2.PrivateKey。
// 通过 Raw() 可取回底层密钥，与既有 gmsm 代码/接口互操作。
type PrivateKey struct {
	raw *sm2.PrivateKey
}

// PublicKey 是 SM2 公钥，薄封装 gmsm 的 *sm2.PublicKey。
type PublicKey struct {
	raw *sm2.PublicKey
}

// NewPrivateKey 用底层 gmsm 私钥构造 PrivateKey；raw 为 nil 时返回 nil。
func NewPrivateKey(raw *sm2.PrivateKey) *PrivateKey {
	if raw == nil {
		return nil
	}
	return &PrivateKey{raw: raw}
}

// NewPublicKey 用底层 gmsm 公钥构造 PublicKey；raw 为 nil 时返回 nil。
func NewPublicKey(raw *sm2.PublicKey) *PublicKey {
	if raw == nil {
		return nil
	}
	return &PublicKey{raw: raw}
}

// Raw 返回底层 gmsm 私钥；接收者为 nil 时返回 nil。
func (k *PrivateKey) Raw() *sm2.PrivateKey {
	if k == nil {
		return nil
	}
	return k.raw
}

// Raw 返回底层 gmsm 公钥；接收者为 nil 时返回 nil。
func (k *PublicKey) Raw() *sm2.PublicKey {
	if k == nil {
		return nil
	}
	return k.raw
}

// Public 返回私钥对应的公钥；接收者为 nil 时返回 nil。
func (k *PrivateKey) Public() *PublicKey {
	if k == nil || k.raw == nil {
		return nil
	}
	return &PublicKey{raw: &k.raw.PublicKey}
}

// Hex 返回私钥标量 D 的 64 字符小写十六进制（32 字节大端，左补零）；
// 私钥非法或为 nil 时返回空串。
func (k *PrivateKey) Hex() string {
	if _, err := validPrivate(k); err != nil {
		return ""
	}
	return hex.EncodeToString(pad32(k.raw.D))
}

// Hex 返回公钥的未压缩点十六进制（04||X||Y，共 130 字符小写）；
// 密钥非法或为 nil 时返回空串。
func (k *PublicKey) Hex() string { return PublicKeyToHex(k) }

// Encrypt 使用公钥加密 data，输出 ASN.1（DER）编码的 C1C3C2 密文：
//
//	SEQUENCE { x INTEGER, y INTEGER, C3 OCTET STRING(32), C2 OCTET STRING }
//
// 该格式与 gmsm/Java 等实现的 ASN.1 密文互通；前端 JS 互操作请使用
// EncryptRaw 的定长拼接格式。密文可解出明文（含空明文）。
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
		return nil, fmt.Errorf("guomi: SM2 密文 ASN.1 编码失败: %w", err)
	}
	return der, nil
}

// EncryptRaw 使用公钥加密 data，输出**原始拼接**的 C1C3C2 密文，字节布局为：
//
//	X1(32) || Y1(32) || C3(32) || C2(len(data))
//
// 即前 64 字节是曲线点 C1 的 X、Y 分量（大端、左补零），**不带未压缩点标识
// 字节 0x04**；随后 32 字节为 C3 = SM3(x2 || data || y2)；其余为 C2，即明文与
// 密钥派生流 KDF(x2 || y2) 的异或结果。总长度恒为 96+len(data)。
//
// 该定长布局是前端纯 JS 实现的互操作基准（gmsm 的同名密文多一个前导 0x04，
// 去掉该字节即与本格式完全一致）；ASN.1 场景请用 Encrypt。
//
// 每次调用都重新选取随机数 k（来自 crypto/rand），同一明文两次加密结果不同。
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
			// 密钥派生结果全为零：按 GB/T 32918.4 重新选取 k。
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

// Decrypt 解密 Encrypt 产生的 ASN.1（DER）密文。
//
// 密文结构非法时返回 ErrInvalidCiphertext，认证（C3 校验）失败时返回 ErrDecrypt。
func Decrypt(priv *PrivateKey, ciphertext []byte) ([]byte, error) {
	if _, err := validPrivate(priv); err != nil {
		return nil, err
	}
	var c sm2CipherASN1
	if _, err := asn1.Unmarshal(ciphertext, &c); err != nil {
		return nil, fmt.Errorf("%w: ASN.1 解析失败: %v", ErrInvalidCiphertext, err)
	}
	if c.XCoordinate == nil || c.YCoordinate == nil {
		return nil, fmt.Errorf("%w: ASN.1 中缺少 C1 坐标", ErrInvalidCiphertext)
	}
	x, ok := coordBytes(c.XCoordinate)
	if !ok {
		return nil, fmt.Errorf("%w: C1 的 X 坐标超长", ErrInvalidCiphertext)
	}
	y, ok := coordBytes(c.YCoordinate)
	if !ok {
		return nil, fmt.Errorf("%w: C1 的 Y 坐标超长", ErrInvalidCiphertext)
	}
	if len(c.Hash) != SM3Size {
		return nil, fmt.Errorf("%w: C3 长度 %d, want %d", ErrInvalidCiphertext, len(c.Hash), SM3Size)
	}

	raw := make([]byte, 0, 2*SM2CoordinateSize+SM3Size+len(c.CipherText))
	raw = append(raw, x...)
	raw = append(raw, y...)
	raw = append(raw, c.Hash...)
	raw = append(raw, c.CipherText...)
	return DecryptRaw(priv, raw)
}

// DecryptRaw 解密 EncryptRaw 产生的原始拼接密文（X1||Y1||C3||C2，无 0x04 前缀）。
//
// 长度小于 96 字节或 C1 不在 SM2 曲线上时返回 ErrInvalidCiphertext；
// C3 校验失败（密文被篡改或私钥不匹配）时返回 ErrDecrypt。绝不 panic。
func DecryptRaw(priv *PrivateKey, ciphertext []byte) ([]byte, error) {
	k, err := validPrivate(priv)
	if err != nil {
		return nil, err
	}
	const minLen = 2*SM2CoordinateSize + SM3Size
	if len(ciphertext) < minLen {
		return nil, fmt.Errorf("%w: 长度 %d 小于最小长度 %d", ErrInvalidCiphertext, len(ciphertext), minLen)
	}

	curve := k.Curve
	x1 := new(big.Int).SetBytes(ciphertext[:SM2CoordinateSize])
	y1 := new(big.Int).SetBytes(ciphertext[SM2CoordinateSize : 2*SM2CoordinateSize])
	if !curve.IsOnCurve(x1, y1) {
		return nil, fmt.Errorf("%w: C1 不在 SM2 曲线上", ErrInvalidCiphertext)
	}
	c3 := ciphertext[2*SM2CoordinateSize : 2*SM2CoordinateSize+SM3Size]
	c2 := ciphertext[2*SM2CoordinateSize+SM3Size:]

	x2, y2 := curve.ScalarMult(x1, y1, k.D.Bytes())
	xs, ys := pad32(x2), pad32(y2)
	stream, ok := sm2KDF(xs, ys, len(c2))
	if !ok {
		return nil, fmt.Errorf("%w: 密钥派生结果全为零", ErrDecrypt)
	}

	plaintext := make([]byte, len(c2))
	for i := range c2 {
		plaintext[i] = c2[i] ^ stream[i]
	}
	if subtle.ConstantTimeCompare(sm3C3(xs, plaintext, ys), c3) != 1 {
		return nil, fmt.Errorf("%w: C3 校验失败", ErrDecrypt)
	}
	return plaintext, nil
}

// Sign 使用私钥对 data 做 SM2 签名，输出 ASN.1（DER）编码的 (r, s)：
//
//	SEQUENCE { r INTEGER, s INTEGER }
//
// 摘要按 GB/T 32918.2 计算为 SM3(ZA || data)，其中 ZA 使用标准默认用户标识
// "1234567812345678"（与 gmsm、多数服务端实现的默认行为一致，可与 gmsm 直接互验）。
func Sign(priv *PrivateKey, data []byte) ([]byte, error) {
	k, err := validPrivate(priv)
	if err != nil {
		return nil, err
	}
	sig, err := k.Sign(rand.Reader, data, nil)
	if err != nil {
		return nil, fmt.Errorf("guomi: SM2 签名失败: %w", err)
	}
	return sig, nil
}

// Verify 校验 data 的 SM2 签名 sig（ASN.1/DER 格式，由 Sign 或 gmsm 等同构实现产生）。
//
// 签名结构非法或验签不通过时返回 (false, nil)（不把签名内容差异暴露为错误）；
// 公钥非法（nil、坐标不在曲线上）时返回 (false, error)。
func Verify(pub *PublicKey, data, sig []byte) (bool, error) {
	p, err := validPublic(pub)
	if err != nil {
		return false, err
	}
	return p.Verify(data, sig), nil
}

// SignHex 使用私钥对 data 签名，返回小写十六进制编码的 ASN.1 签名。
func SignHex(priv *PrivateKey, data []byte) (string, error) {
	sig, err := Sign(priv, data)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sig), nil
}

// VerifyHex 校验十六进制编码的 SM2 签名；sigHex 不是合法十六进制时返回错误。
func VerifyHex(pub *PublicKey, data []byte, sigHex string) (bool, error) {
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	return Verify(pub, data, sig)
}

// sm2CipherASN1 是 SM2 密文的 ASN.1 结构（与 gmsm/x509 的表示一致）：
// 坐标用 INTEGER、C3 与 C2 用 OCTET STRING。
type sm2CipherASN1 struct {
	XCoordinate *big.Int
	YCoordinate *big.Int
	Hash        []byte
	CipherText  []byte
}

// validPublic 校验公钥非空且点位于 SM2 曲线上，返回底层 gmsm 公钥。
func validPublic(pub *PublicKey) (*sm2.PublicKey, error) {
	p := pub.Raw()
	if p == nil || p.Curve == nil || p.X == nil || p.Y == nil {
		return nil, fmt.Errorf("%w: 公钥为空", ErrInvalidPublicKey)
	}
	if !p.Curve.IsOnCurve(p.X, p.Y) {
		return nil, fmt.Errorf("%w: 公钥不在 SM2 曲线上", ErrInvalidPublicKey)
	}
	return p, nil
}

// validPrivate 校验私钥非空且标量在 [1, n-1] 内，返回底层 gmsm 私钥。
func validPrivate(priv *PrivateKey) (*sm2.PrivateKey, error) {
	k := priv.Raw()
	if k == nil || k.Curve == nil || k.D == nil {
		return nil, fmt.Errorf("%w: 私钥为空", ErrInvalidPrivateKey)
	}
	n := k.Curve.Params().N
	if k.D.Sign() <= 0 || k.D.Cmp(n) >= 0 {
		return nil, fmt.Errorf("%w: 标量 D 越界", ErrInvalidPrivateKey)
	}
	return k, nil
}

// randScalar 使用 crypto/rand 生成 [1, n-1] 内的随机标量（拒绝采样）。
func randScalar(n *big.Int) (*big.Int, error) {
	buf := make([]byte, (n.BitLen()+7)/8)
	for {
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("guomi: 读取随机数失败: %w", err)
		}
		k := new(big.Int).SetBytes(buf)
		if k.Sign() > 0 && k.Cmp(n) < 0 {
			return k, nil
		}
	}
}

// sm2KDF 实现 GB/T 32918.4 §5.4.3 的密钥派生函数：
// 以 ct 从 1 递增拼接 x2||y2||ct 后做 SM3，直到输出长度达到 klen。
//
// 返回 ok=false 表示派生结果全为零（klen == 0 时空密钥流视为成功）。
func sm2KDF(x2, y2 []byte, klen int) ([]byte, bool) {
	if klen == 0 {
		return []byte{}, true
	}
	h := sm3.New()
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

// sm3C3 计算 C3 = SM3(x2 || data || y2)。
func sm3C3(x2, data, y2 []byte) []byte {
	h := hash.Hash(sm3.New())
	h.Write(x2)
	h.Write(data)
	h.Write(y2)
	return h.Sum(nil)
}

// pad32 把曲线坐标或标量转为 32 字节大端表示（左补零）。
// 数值超过 32 字节时保留低 32 字节，绝不 panic。
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

// coordBytes 把 ASN.1 解析出的整数坐标转为 32 字节大端表示；
// 若数值为负或超过 32 字节（即 >= 2^256）则返回 ok=false。
func coordBytes(x *big.Int) ([]byte, bool) {
	if x.Sign() < 0 || x.BitLen() > 8*SM2CoordinateSize {
		return nil, false
	}
	return pad32(x), true
}
