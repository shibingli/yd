package guomi

import (
	"crypto/elliptic"
	"crypto/rand"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"

	"github.com/tjfoc/gmsm/sm2"
)

// 国密相关对象标识符（OID）。
var (
	// oidSM2Curve 是 SM2 推荐曲线 sm2p256v1 的 OID：1.2.156.10197.1.301。
	oidSM2Curve = asn1.ObjectIdentifier{1, 2, 156, 10197, 1, 301}

	// oidECPublicKey 是 X.509 中椭圆曲线公钥算法标识的 OID：1.2.840.10045.2.1。
	oidECPublicKey = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
)

// algorithmIdentifier 是 X.509 的 AlgorithmIdentifier（RFC 5280）。
//
// 参数按 EC 密钥的惯例使用命名曲线 OID（RFC 5480），编码后与 gmsm、gmssl 等
// 实现的 parameters 字段（内嵌的曲线 OID）逐字节一致。
type algorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.ObjectIdentifier `asn1:"optional"`
}

// pkcs8PrivateKey 是 PKCS#8 PrivateKeyInfo 的最小结构（RFC 5208）。
type pkcs8PrivateKey struct {
	Version    int
	Algorithm  algorithmIdentifier
	PrivateKey []byte
}

// ecPrivateKey 是 SEC1 ECPrivateKey 结构（RFC 5915）。
type ecPrivateKey struct {
	Version       int
	PrivateKey    []byte
	NamedCurveOID asn1.ObjectIdentifier `asn1:"optional,explicit,tag:0"`
	PublicKey     asn1.BitString        `asn1:"optional,explicit,tag:1"`
}

// subjectPublicKeyInfo 是 X.509 SubjectPublicKeyInfo 结构（RFC 5280）。
type subjectPublicKeyInfo struct {
	Algorithm algorithmIdentifier
	PublicKey asn1.BitString
}

// GenerateKey 使用 crypto/rand 生成一对新的 SM2 密钥。
func GenerateKey() (*PrivateKey, error) {
	raw, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("guomi: 生成 SM2 密钥失败: %w", err)
	}
	return &PrivateKey{raw: raw}, nil
}

// MarshalPrivateKeyPEM 把私钥编码为 PEM 块，类型为 "PRIVATE KEY"，
// 内容为 PKCS#8（PrivateKeyInfo）包裹的 RFC 5915 ECPrivateKey，
// 算法标识与曲线参数均使用 SM2 的 OID，可与 gmsm/gmssl 等实现互认。
func MarshalPrivateKeyPEM(priv *PrivateKey) ([]byte, error) {
	raw := priv.Raw()
	if raw == nil || raw.D == nil || raw.Curve == nil {
		return nil, fmt.Errorf("%w: 私钥为空", ErrInvalidPrivateKey)
	}
	n := raw.Curve.Params().N
	if raw.D.Sign() <= 0 || raw.D.Cmp(n) >= 0 {
		return nil, fmt.Errorf("%w: 标量 D 越界", ErrInvalidPrivateKey)
	}
	pubBytes, ok := pointBytes(raw.X, raw.Y)
	if !ok {
		return nil, fmt.Errorf("%w: 公钥坐标非法", ErrInvalidPrivateKey)
	}

	sec1, err := asn1.Marshal(ecPrivateKey{
		Version:       1,
		PrivateKey:    pad32(raw.D),
		NamedCurveOID: oidSM2Curve,
		PublicKey:     asn1.BitString{Bytes: pubBytes, BitLength: 8 * len(pubBytes)},
	})
	if err != nil {
		return nil, fmt.Errorf("guomi: ECPrivateKey DER 编码失败: %w", err)
	}
	der, err := asn1.Marshal(pkcs8PrivateKey{
		Version:    0,
		Algorithm:  algorithmIdentifier{Algorithm: oidSM2Curve, Parameters: oidSM2Curve},
		PrivateKey: sec1,
	})
	if err != nil {
		return nil, fmt.Errorf("guomi: PKCS#8 DER 编码失败: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// MarshalPublicKeyPEM 把公钥编码为 PEM 块，类型为 "PUBLIC KEY"，
// 内容为 X.509 SubjectPublicKeyInfo：算法参数为 SM2 曲线 OID，
// 公钥位串为未压缩点 04||X||Y。
func MarshalPublicKeyPEM(pub *PublicKey) ([]byte, error) {
	p, err := validPublic(pub)
	if err != nil {
		return nil, err
	}
	point, ok := pointBytes(p.X, p.Y)
	if !ok {
		return nil, fmt.Errorf("%w: 公钥坐标非法", ErrInvalidPublicKey)
	}
	der, err := asn1.Marshal(subjectPublicKeyInfo{
		Algorithm: algorithmIdentifier{Algorithm: oidECPublicKey, Parameters: oidSM2Curve},
		PublicKey: asn1.BitString{Bytes: point, BitLength: 8 * len(point)},
	})
	if err != nil {
		return nil, fmt.Errorf("guomi: SubjectPublicKeyInfo DER 编码失败: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

// ParsePrivateKeyPEM 解析 MarshalPrivateKeyPEM 的输出（PKCS#8，"PRIVATE KEY"），
// 同时兼容 SEC1 的 "EC PRIVATE KEY" 块。
//
// 解析成功后会用曲线基点重新推导公钥并校验标量在 [1, n-1] 内，
// 任何结构或取值非法都返回包装了 ErrInvalidPrivateKey 的错误。
func ParsePrivateKeyPEM(pemBytes []byte) (*PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("%w: PEM 解码失败", ErrInvalidPrivateKey)
	}

	var inner []byte
	switch block.Type {
	case "PRIVATE KEY":
		var info pkcs8PrivateKey
		if _, err := asn1.Unmarshal(block.Bytes, &info); err != nil {
			return nil, fmt.Errorf("%w: PKCS#8 解析失败: %v", ErrInvalidPrivateKey, err)
		}
		if !info.Algorithm.Algorithm.Equal(oidSM2Curve) {
			return nil, fmt.Errorf("%w: 算法 OID %v 不是 SM2(%v)", ErrInvalidPrivateKey, info.Algorithm.Algorithm, oidSM2Curve)
		}
		inner = info.PrivateKey
	case "EC PRIVATE KEY":
		inner = block.Bytes
	default:
		return nil, fmt.Errorf("%w: 不支持的 PEM 类型 %q", ErrInvalidPrivateKey, block.Type)
	}

	var sec1 ecPrivateKey
	if _, err := asn1.Unmarshal(inner, &sec1); err != nil {
		return nil, fmt.Errorf("%w: ECPrivateKey 解析失败: %v", ErrInvalidPrivateKey, err)
	}
	if len(sec1.NamedCurveOID) > 0 && !sec1.NamedCurveOID.Equal(oidSM2Curve) {
		return nil, fmt.Errorf("%w: 曲线 OID %v 不是 SM2(%v)", ErrInvalidPrivateKey, sec1.NamedCurveOID, oidSM2Curve)
	}
	return privateKeyFromScalar(sec1.PrivateKey)
}

// ParsePublicKeyPEM 解析 MarshalPublicKeyPEM 的输出（"PUBLIC KEY"，SPKI）。
//
// 曲线参数必须是 SM2，位串必须是 65 字节的未压缩点且落在曲线上，
// 否则返回包装了 ErrInvalidPublicKey 的错误。
func ParsePublicKeyPEM(pemBytes []byte) (*PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("%w: PEM 解码失败", ErrInvalidPublicKey)
	}
	if block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("%w: 不支持的 PEM 类型 %q", ErrInvalidPublicKey, block.Type)
	}
	var spki subjectPublicKeyInfo
	if _, err := asn1.Unmarshal(block.Bytes, &spki); err != nil {
		return nil, fmt.Errorf("%w: SubjectPublicKeyInfo 解析失败: %v", ErrInvalidPublicKey, err)
	}
	if !spki.Algorithm.Algorithm.Equal(oidECPublicKey) {
		return nil, fmt.Errorf("%w: 算法 OID %v 不是 id-ecPublicKey", ErrInvalidPublicKey, spki.Algorithm.Algorithm)
	}
	point := spki.PublicKey.Bytes
	if len(point) != 1+2*SM2CoordinateSize || point[0] != 0x04 || spki.PublicKey.BitLength != 8*len(point) {
		return nil, fmt.Errorf("%w: 公钥点编码非法（%d 字节，%d 位）", ErrInvalidPublicKey, len(point), spki.PublicKey.BitLength)
	}
	return publicKeyFromBytes(point[1:])
}

// PrivateKeyFromHex 解析 64 个十六进制字符（32 字节标量）表示的 SM2 私钥。
//
// 允许前置 "0x"/"0X" 与首尾空白，允许大小写混用；长度不符或标量不在
// [1, n-1] 内返回包装了 ErrInvalidPrivateKey 的错误。
func PrivateKeyFromHex(s string) (*PrivateKey, error) {
	clean := strings.TrimSpace(s)
	clean = strings.TrimPrefix(strings.TrimPrefix(clean, "0x"), "0X")
	if len(clean) != 2*SM2CoordinateSize {
		return nil, fmt.Errorf("%w: 十六进制长度 %d, want %d", ErrInvalidPrivateKey, len(clean), 2*SM2CoordinateSize)
	}
	scalar, err := hex.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPrivateKey, err)
	}
	return privateKeyFromScalar(scalar)
}

// PublicKeyFromHex 解析十六进制公钥：接受 130 字符的未压缩点 04||X||Y，
// 也接受省略 0x04 前缀的 128 字符 X||Y；允许前置 "0x"/"0X" 与首尾空白。
//
// 解析后会校验点落在 SM2 曲线上，失败返回包装了 ErrInvalidPublicKey 的错误。
func PublicKeyFromHex(s string) (*PublicKey, error) {
	clean := strings.TrimSpace(s)
	clean = strings.TrimPrefix(strings.TrimPrefix(clean, "0x"), "0X")
	raw, err := hex.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPublicKey, err)
	}
	switch len(raw) {
	case 1 + 2*SM2CoordinateSize:
		if raw[0] != 0x04 {
			return nil, fmt.Errorf("%w: 未压缩点标识 0x%02x 非法", ErrInvalidPublicKey, raw[0])
		}
		raw = raw[1:]
	case 2 * SM2CoordinateSize:
	default:
		return nil, fmt.Errorf("%w: 十六进制长度 %d 非法（需要 64 或 65 字节）", ErrInvalidPublicKey, len(raw))
	}
	return publicKeyFromBytes(raw)
}

// PublicKeyToHex 返回公钥的未压缩点十六进制表示：04||X||Y，共 130 字符小写。
// 公钥为 nil、坐标非法或点不在曲线上时返回空串。
func PublicKeyToHex(pub *PublicKey) string {
	p := pub.Raw()
	if p == nil || p.Curve == nil || p.X == nil || p.Y == nil {
		return ""
	}
	if !p.Curve.IsOnCurve(p.X, p.Y) {
		return ""
	}
	point, ok := pointBytes(p.X, p.Y)
	if !ok {
		return ""
	}
	return hex.EncodeToString(point)
}

// privateKeyFromScalar 由 1~32 字节的大端标量构造私钥，并推导出公钥。
func privateKeyFromScalar(scalar []byte) (*PrivateKey, error) {
	if len(scalar) == 0 || len(scalar) > SM2CoordinateSize {
		return nil, fmt.Errorf("%w: 标量长度 %d 非法", ErrInvalidPrivateKey, len(scalar))
	}
	curve := sm2.P256Sm2()
	d := new(big.Int).SetBytes(scalar)
	if d.Sign() <= 0 || d.Cmp(curve.Params().N) >= 0 {
		return nil, fmt.Errorf("%w: 标量 D 越界", ErrInvalidPrivateKey)
	}
	raw := &sm2.PrivateKey{PublicKey: sm2.PublicKey{Curve: curve}, D: d}
	raw.X, raw.Y = curve.ScalarBaseMult(d.Bytes())
	return &PrivateKey{raw: raw}, nil
}

// publicKeyFromBytes 由 64 字节的 X||Y 构造公钥，并校验点落在 SM2 曲线上。
func publicKeyFromBytes(xy []byte) (*PublicKey, error) {
	curve := sm2.P256Sm2()
	point := make([]byte, 0, 1+len(xy))
	point = append(point, 0x04)
	point = append(point, xy...)
	x, y := elliptic.Unmarshal(curve, point)
	if x == nil || y == nil {
		return nil, fmt.Errorf("%w: 点不在 SM2 曲线上", ErrInvalidPublicKey)
	}
	return &PublicKey{raw: &sm2.PublicKey{Curve: curve, X: x, Y: y}}, nil
}

// pointBytes 把公钥坐标编码为未压缩点 04||X||Y（65 字节）；
// 任一坐标超过 32 字节或为 nil 时返回 ok=false。
func pointBytes(x, y *big.Int) ([]byte, bool) {
	if x == nil || y == nil || x.Sign() < 0 || y.Sign() < 0 {
		return nil, false
	}
	if x.BitLen() > 8*SM2CoordinateSize || y.BitLen() > 8*SM2CoordinateSize {
		return nil, false
	}
	out := make([]byte, 1+2*SM2CoordinateSize)
	out[0] = 0x04
	copy(out[1:1+SM2CoordinateSize], pad32(x))
	copy(out[1+SM2CoordinateSize:], pad32(y))
	return out, true
}
