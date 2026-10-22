package guomi

import (
	"crypto/rand"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"strings"

	esm2 "github.com/emmansun/gmsm/sm2"

	"github.com/shibingli/ydx/errs"
)

// Object identifiers related to national cryptography. | 国密相关对象标识符（OID）。
var (
	// oidSM2Curve is the OID of the SM2 recommended curve sm2p256v1: 1.2.156.10197.1.301. | oidSM2Curve 是 SM2 推荐曲线 sm2p256v1 的 OID：1.2.156.10197.1.301。
	oidSM2Curve = asn1.ObjectIdentifier{1, 2, 156, 10197, 1, 301}

	// oidECPublicKey is the OID of the elliptic curve public key algorithm in X.509: 1.2.840.10045.2.1. | oidECPublicKey 是 X.509 中椭圆曲线公钥算法标识的 OID：1.2.840.10045.2.1。
	oidECPublicKey = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
)

// algorithmIdentifier is the X.509 AlgorithmIdentifier of RFC 5280. | algorithmIdentifier 是 X.509 的 AlgorithmIdentifier（RFC 5280）。
//
// Following RFC 5480 for EC keys, the parameters field holds the named curve OID. | 参数按 EC 密钥的惯例使用命名曲线 OID（RFC 5480）。
// The encoding is byte-identical to that of gmsm, gmssl and friends. | 编码后与 gmsm、gmssl 等实现的 parameters 字段逐字节一致。
type algorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.ObjectIdentifier `asn1:"optional"`
}

// pkcs8PrivateKey is the minimal PKCS#8 PrivateKeyInfo structure of RFC 5208. | pkcs8PrivateKey 是 PKCS#8 PrivateKeyInfo 的最小结构（RFC 5208）。
type pkcs8PrivateKey struct {
	Algorithm  algorithmIdentifier
	PrivateKey []byte
	Version    int
}

// ecPrivateKey is the SEC1 ECPrivateKey structure of RFC 5915. | ecPrivateKey 是 SEC1 ECPrivateKey 结构（RFC 5915）。
type ecPrivateKey struct {
	PrivateKey    []byte
	NamedCurveOID asn1.ObjectIdentifier `asn1:"optional,explicit,tag:0"`
	PublicKey     asn1.BitString        `asn1:"optional,explicit,tag:1"`
	Version       int
}

// subjectPublicKeyInfo is the X.509 SubjectPublicKeyInfo structure of RFC 5280. | subjectPublicKeyInfo 是 X.509 SubjectPublicKeyInfo 结构（RFC 5280）。
type subjectPublicKeyInfo struct {
	Algorithm algorithmIdentifier
	PublicKey asn1.BitString
}

// GenerateKey generates a fresh SM2 key pair from crypto/rand. | GenerateKey 使用 crypto/rand 生成一对新的 SM2 密钥。
func GenerateKey() (*PrivateKey, error) {
	raw, err := esm2.GenerateKey(rand.Reader)
	if err != nil {
		return nil, errs.Wrap(err, ErrRandomSource)
	}
	return &PrivateKey{raw: raw}, nil
}

// MarshalPrivateKeyPEM encodes the private key as a PEM block of type "PRIVATE KEY". | MarshalPrivateKeyPEM 把私钥编码为 PEM 块，类型为 "PRIVATE KEY"。
// Its body is a PKCS#8 PrivateKeyInfo wrapping an RFC 5915 ECPrivateKey. | 内容为 PKCS#8（PrivateKeyInfo）包裹的 RFC 5915 ECPrivateKey。
// Both the algorithm identifier and the curve parameters use the SM2 OID, so gmsm and gmssl accept the output. | 算法标识与曲线参数均使用 SM2 的 OID，可与 gmsm/gmssl 等实现互认。
func MarshalPrivateKeyPEM(priv *PrivateKey) ([]byte, error) {
	raw := priv.Raw()
	if raw == nil || raw.D == nil || raw.Curve == nil {
		return nil, errs.Detail(ErrInvalidPrivateKey, "private key is nil | 私钥为空")
	}
	n := raw.Curve.Params().N
	if raw.D.Sign() <= 0 || raw.D.Cmp(n) >= 0 {
		return nil, errs.Detail(ErrInvalidPrivateKey, "scalar D is out of range | 标量 D 越界")
	}
	pubBytes, ok := pointBytes(raw.X, raw.Y)
	if !ok {
		return nil, errs.Detail(ErrInvalidPrivateKey, "invalid public key coordinates | 公钥坐标非法")
	}

	sec1, err := asn1.Marshal(ecPrivateKey{
		Version:       1,
		PrivateKey:    pad32(raw.D),
		NamedCurveOID: oidSM2Curve,
		PublicKey:     asn1.BitString{Bytes: pubBytes, BitLength: 8 * len(pubBytes)},
	})
	if err != nil {
		return nil, errs.WrapDetail(err, ErrInvalidPrivateKey, "ECPrivateKey DER encoding failed | ECPrivateKey 的 DER 编码失败")
	}
	der, err := asn1.Marshal(pkcs8PrivateKey{
		Version:    0,
		Algorithm:  algorithmIdentifier{Algorithm: oidSM2Curve, Parameters: oidSM2Curve},
		PrivateKey: sec1,
	})
	if err != nil {
		return nil, errs.WrapDetail(err, ErrInvalidPrivateKey, "PKCS#8 DER encoding failed | PKCS#8 的 DER 编码失败")
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// MarshalPublicKeyPEM encodes the public key as a PEM block of type "PUBLIC KEY". | MarshalPublicKeyPEM 把公钥编码为 PEM 块，类型为 "PUBLIC KEY"。
// Its body is an X.509 SubjectPublicKeyInfo whose algorithm parameters are the SM2 curve OID and whose bit string is the uncompressed point 04||X||Y. | 内容为 X.509 SubjectPublicKeyInfo：算法参数为 SM2 曲线 OID，公钥位串为未压缩点 04||X||Y。
func MarshalPublicKeyPEM(pub *PublicKey) ([]byte, error) {
	p, err := validPublic(pub)
	if err != nil {
		return nil, err
	}
	point, ok := pointBytes(p.X, p.Y)
	if !ok {
		return nil, errs.Detail(ErrInvalidPublicKey, "invalid public key coordinates | 公钥坐标非法")
	}
	der, err := asn1.Marshal(subjectPublicKeyInfo{
		Algorithm: algorithmIdentifier{Algorithm: oidECPublicKey, Parameters: oidSM2Curve},
		PublicKey: asn1.BitString{Bytes: point, BitLength: 8 * len(point)},
	})
	if err != nil {
		return nil, errs.WrapDetail(err, ErrInvalidPublicKey, "SubjectPublicKeyInfo DER encoding failed | SubjectPublicKeyInfo 的 DER 编码失败")
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

// ParsePrivateKeyPEM parses the output of MarshalPrivateKeyPEM, a PKCS#8 "PRIVATE KEY" block, and also accepts a SEC1 "EC PRIVATE KEY" block. | ParsePrivateKeyPEM 解析 MarshalPrivateKeyPEM 的输出（PKCS#8，"PRIVATE KEY"），同时兼容 SEC1 的 "EC PRIVATE KEY" 块。
//
// On success it re-derives the public key from the curve base point and checks that the scalar lies in [1, n-1]. | 解析成功后会用曲线基点重新推导公钥并校验标量在 [1, n-1] 内。
// Any structural or value error returns an error that wraps ErrInvalidPrivateKey. | 任何结构或取值非法都返回包装了 ErrInvalidPrivateKey 的错误。
func ParsePrivateKeyPEM(pemBytes []byte) (*PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errs.Detail(ErrInvalidPrivateKey, "PEM decoding failed | PEM 解码失败")
	}

	var inner []byte
	switch block.Type {
	case "PRIVATE KEY":
		var info pkcs8PrivateKey
		if _, err := asn1.Unmarshal(block.Bytes, &info); err != nil {
			return nil, errs.WrapDetail(err, ErrInvalidPrivateKey, "PKCS#8 parsing failed | PKCS#8 解析失败")
		}
		if !info.Algorithm.Algorithm.Equal(oidSM2Curve) {
			return nil, errs.Detail(ErrInvalidPrivateKey, "algorithm OID %[1]v is not SM2(%[2]v) | 算法 OID %[1]v 不是 SM2(%[2]v)", info.Algorithm.Algorithm, oidSM2Curve)
		}
		inner = info.PrivateKey
	case "EC PRIVATE KEY":
		inner = block.Bytes
	default:
		return nil, errs.Detail(ErrInvalidPrivateKey, "unsupported PEM type %[1]q | 不支持的 PEM 类型 %[1]q", block.Type)
	}

	var sec1 ecPrivateKey
	if _, err := asn1.Unmarshal(inner, &sec1); err != nil {
		return nil, errs.WrapDetail(err, ErrInvalidPrivateKey, "ECPrivateKey parsing failed | ECPrivateKey 解析失败")
	}
	if len(sec1.NamedCurveOID) > 0 && !sec1.NamedCurveOID.Equal(oidSM2Curve) {
		return nil, errs.Detail(ErrInvalidPrivateKey, "curve OID %[1]v is not SM2(%[2]v) | 曲线 OID %[1]v 不是 SM2(%[2]v)", sec1.NamedCurveOID, oidSM2Curve)
	}
	return privateKeyFromScalar(sec1.PrivateKey)
}

// ParsePublicKeyPEM parses the output of MarshalPublicKeyPEM, a "PUBLIC KEY" SPKI block. | ParsePublicKeyPEM 解析 MarshalPublicKeyPEM 的输出（"PUBLIC KEY"，SPKI）。
//
// The curve parameters must be SM2 and the bit string must be a 65-byte uncompressed point on the curve. | 曲线参数必须是 SM2，位串必须是 65 字节的未压缩点且落在曲线上。
// Otherwise it returns an error that wraps ErrInvalidPublicKey. | 否则返回包装了 ErrInvalidPublicKey 的错误。
func ParsePublicKeyPEM(pemBytes []byte) (*PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errs.Detail(ErrInvalidPublicKey, "PEM decoding failed | PEM 解码失败")
	}
	if block.Type != "PUBLIC KEY" {
		return nil, errs.Detail(ErrInvalidPublicKey, "unsupported PEM type %[1]q | 不支持的 PEM 类型 %[1]q", block.Type)
	}
	var spki subjectPublicKeyInfo
	if _, err := asn1.Unmarshal(block.Bytes, &spki); err != nil {
		return nil, errs.WrapDetail(err, ErrInvalidPublicKey, "SubjectPublicKeyInfo parsing failed | SubjectPublicKeyInfo 解析失败")
	}
	if !spki.Algorithm.Algorithm.Equal(oidECPublicKey) {
		return nil, errs.Detail(ErrInvalidPublicKey, "algorithm OID %[1]v is not id-ecPublicKey | 算法 OID %[1]v 不是 id-ecPublicKey", spki.Algorithm.Algorithm)
	}
	point := spki.PublicKey.Bytes
	if len(point) != 1+2*SM2CoordinateSize || point[0] != 0x04 || spki.PublicKey.BitLength != 8*len(point) {
		return nil, errs.Detail(ErrInvalidPublicKey, "invalid public key point encoding (%[1]d bytes, %[2]d bits) | 公钥点编码非法（%[1]d 字节，%[2]d 位）", len(point), spki.PublicKey.BitLength)
	}
	return publicKeyFromBytes(point[1:])
}

// PrivateKeyFromHex parses an SM2 private key given as 64 hex characters, that is a 32-byte scalar. | PrivateKeyFromHex 解析 64 个十六进制字符（32 字节标量）表示的 SM2 私钥。
//
// A leading "0x"/"0X" and surrounding whitespace are allowed, and letter case may be mixed. | 允许前置 "0x"/"0X" 与首尾空白，允许大小写混用。
// A wrong length or a scalar outside [1, n-1] returns an error that wraps ErrInvalidPrivateKey. | 长度不符或标量不在 [1, n-1] 内返回包装了 ErrInvalidPrivateKey 的错误。
func PrivateKeyFromHex(s string) (*PrivateKey, error) {
	clean := strings.TrimSpace(s)
	clean = strings.TrimPrefix(strings.TrimPrefix(clean, "0x"), "0X")
	if len(clean) != 2*SM2CoordinateSize {
		return nil, errs.Detail(ErrInvalidPrivateKey, "hex length %[1]d, want %[2]d | 十六进制长度 %[1]d，期望 %[2]d", len(clean), 2*SM2CoordinateSize)
	}
	scalar, err := hex.DecodeString(clean)
	if err != nil {
		return nil, errs.Wrap(err, ErrInvalidPrivateKey)
	}
	return privateKeyFromScalar(scalar)
}

// PublicKeyFromHex parses a hex public key, accepting both the 130-character uncompressed point 04||X||Y and the 128-character X||Y without the 0x04 prefix. | PublicKeyFromHex 解析十六进制公钥：接受 130 字符的未压缩点 04||X||Y，也接受省略 0x04 前缀的 128 字符 X||Y。
// A leading "0x"/"0X" and surrounding whitespace are allowed. | 允许前置 "0x"/"0X" 与首尾空白。
//
// It verifies that the point lies on the SM2 curve and otherwise returns an error wrapping ErrInvalidPublicKey. | 解析后会校验点落在 SM2 曲线上，失败返回包装了 ErrInvalidPublicKey 的错误。
func PublicKeyFromHex(s string) (*PublicKey, error) {
	clean := strings.TrimSpace(s)
	clean = strings.TrimPrefix(strings.TrimPrefix(clean, "0x"), "0X")
	raw, err := hex.DecodeString(clean)
	if err != nil {
		return nil, errs.Wrap(err, ErrInvalidPublicKey)
	}
	switch len(raw) {
	case 1 + 2*SM2CoordinateSize:
		if raw[0] != 0x04 {
			return nil, errs.Detail(ErrInvalidPublicKey, "invalid uncompressed point marker 0x%[1]02x | 未压缩点标识 0x%[1]02x 非法", raw[0])
		}
		raw = raw[1:]
	case 2 * SM2CoordinateSize:
	default:
		return nil, errs.Detail(ErrInvalidPublicKey, "invalid hex length %[1]d (want 64 or 65 bytes) | 十六进制长度 %[1]d 非法（需要 64 或 65 字节）", len(raw))
	}
	return publicKeyFromBytes(raw)
}

// PublicKeyToHex returns the uncompressed point hex of the public key, 04||X||Y in 130 lowercase characters. | PublicKeyToHex 返回公钥的未压缩点十六进制表示：04||X||Y，共 130 字符小写。
// It returns an empty string for a nil key, invalid coordinates or a point off the curve. | 公钥为 nil、坐标非法或点不在曲线上时返回空串。
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

// privateKeyFromScalar builds a private key from a 1 to 32 byte big-endian scalar and derives its public key. | privateKeyFromScalar 由 1~32 字节的大端标量构造私钥，并推导出公钥。
func privateKeyFromScalar(scalar []byte) (*PrivateKey, error) {
	if len(scalar) == 0 || len(scalar) > SM2CoordinateSize {
		return nil, errs.Detail(ErrInvalidPrivateKey, "invalid scalar length %[1]d | 标量长度 %[1]d 非法", len(scalar))
	}
	curve := esm2.P256()
	d := new(big.Int).SetBytes(scalar)
	if d.Sign() <= 0 || d.Cmp(curve.Params().N) >= 0 {
		return nil, errs.Detail(ErrInvalidPrivateKey, "scalar D is out of range | 标量 D 越界")
	}
	raw, err := esm2.NewPrivateKeyFromInt(d)
	if err != nil {
		return nil, errs.WrapDetail(err, ErrInvalidPrivateKey, "private scalar rejected by gmsm | 私钥标量被 gmsm 拒绝")
	}
	return &PrivateKey{raw: raw}, nil
}

// publicKeyFromBytes builds a public key from a 64-byte X||Y and checks that the point lies on the SM2 curve. | publicKeyFromBytes 由 64 字节的 X||Y 构造公钥，并校验点落在 SM2 曲线上。
func publicKeyFromBytes(xy []byte) (*PublicKey, error) {
	point := make([]byte, 0, 1+len(xy))
	point = append(point, 0x04)
	point = append(point, xy...)
	raw, err := esm2.ParseUncompressedPublicKey(point)
	if err != nil {
		return nil, errs.WrapDetail(err, ErrInvalidPublicKey, "point is not on the SM2 curve | 点不在 SM2 曲线上")
	}
	return &PublicKey{raw: raw}, nil
}

// pointBytes encodes public key coordinates as the uncompressed point 04||X||Y of 65 bytes. | pointBytes 把公钥坐标编码为未压缩点 04||X||Y（65 字节）。
// A nil coordinate or one wider than 32 bytes returns ok=false. | 任一坐标超过 32 字节或为 nil 时返回 ok=false。
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
