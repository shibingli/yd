package guomi

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/tjfoc/gmsm/sm2"
)

// mustKey generates a pair of SM2 test keys and fails the test on error. | mustKey 生成一对测试用 SM2 密钥，失败即终止测试。
func mustKey(t *testing.T) (*PrivateKey, *PublicKey) {
	t.Helper()
	priv, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() 返回错误: %v", err)
	}
	if priv == nil || priv.Raw() == nil || priv.Public() == nil {
		t.Fatal("GenerateKey() returned an empty key | GenerateKey() 返回了空密钥")
	}
	return priv, priv.Public()
}

// randBytes generates n bytes of cryptographically secure random data. | randBytes 生成 n 字节密码学安全随机数据。
func randBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("crypto/rand 读取失败: %v", err)
	}
	return b
}

// leftPad32 left-pads a big integer to its 32-byte big-endian form. | leftPad32 把大整数补足为 32 字节大端表示。
func leftPad32(x *big.Int) []byte {
	b := x.Bytes()
	if len(b) >= SM2CoordinateSize {
		return b
	}
	return append(make([]byte, SM2CoordinateSize-len(b)), b...)
}

// TestGenerateKeyIsValidAndRandom checks the value range, curve validity and randomness of generated keys. | TestGenerateKeyIsValidAndRandom 校验生成密钥的取值范围、曲线有效性与随机性。
func TestGenerateKeyIsValidAndRandom(t *testing.T) {
	priv, pub := mustKey(t)
	raw := priv.Raw()

	n := raw.Curve.Params().N
	if raw.D.Sign() <= 0 || raw.D.Cmp(n) >= 0 {
		t.Fatalf("私钥 D 越界: %v", raw.D)
	}
	if !raw.Curve.IsOnCurve(raw.X, raw.Y) {
		t.Fatal("the public key exported from the private key is not on the SM2 curve | 由私钥导出的公钥不在 SM2 曲线上")
	}
	if !pub.Raw().Curve.IsOnCurve(pub.Raw().X, pub.Raw().Y) {
		t.Fatal("PublicKey is not on the SM2 curve | PublicKey 不在 SM2 曲线上")
	}
	if got := len(priv.Hex()); got != 64 {
		t.Fatalf("私钥十六进制长度 = %d, want 64", got)
	}
	pubHex := pub.Hex()
	if len(pubHex) != 130 || !strings.HasPrefix(pubHex, "04") {
		t.Fatalf("公钥十六进制 = %d 字符 (%.6s...), want 130 字符且以 04 开头", len(pubHex), pubHex)
	}

	priv2, _ := mustKey(t)
	if priv2.Hex() == priv.Hex() {
		t.Fatal("two generated private keys must not be identical | 两次生成的私钥不应相同")
	}
	if got := PublicKeyToHex(pub); got != pubHex {
		t.Fatalf("PublicKeyToHex = %s, want %s", got, pubHex)
	}
	if got := priv.Public().Hex(); got != pubHex {
		t.Fatalf("priv.Public().Hex() = %s, want %s", got, pubHex)
	}
}

// TestSM2EncryptDecryptRoundTrip covers both ciphertext formats, ASN.1 and raw, over empty, 1-byte, 1 KB and 1 MB payloads. | TestSM2EncryptDecryptRoundTrip 覆盖 ASN.1 与原始两种密文格式的往返（含空串、1 字节、1KB、1MB）。
func TestSM2EncryptDecryptRoundTrip(t *testing.T) {
	priv, pub := mustKey(t)

	sizes := []int{0, 1, 1024, 1 << 20}
	for _, n := range sizes {
		t.Run(fmt.Sprintf("len=%d", n), func(t *testing.T) {
			data := make([]byte, n)
			if n > 0 {
				data = randBytes(t, n)
			}

			ct, err := Encrypt(pub, data)
			if err != nil {
				t.Fatalf("Encrypt 返回错误: %v", err)
			}
			if len(ct) <= 96 {
				t.Fatalf("ASN.1 密文长度 = %d, want > 96", len(ct))
			}
			if ct[0] != 0x30 {
				t.Fatalf("ASN.1 密文首字节 = 0x%02x, want 0x30 (SEQUENCE)", ct[0])
			}
			got, err := Decrypt(priv, ct)
			if err != nil {
				t.Fatalf("Decrypt 返回错误: %v", err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("ASN.1 往返不一致: got %d 字节, want %d 字节", len(got), len(data))
			}

			rawCT, err := EncryptRaw(pub, data)
			if err != nil {
				t.Fatalf("EncryptRaw 返回错误: %v", err)
			}
			if want := 96 + n; len(rawCT) != want {
				t.Fatalf("原始密文长度 = %d, want %d (64 字节 C1 + 32 字节 C3 + 明文长度)", len(rawCT), want)
			}
			gotRaw, err := DecryptRaw(priv, rawCT)
			if err != nil {
				t.Fatalf("DecryptRaw 返回错误: %v", err)
			}
			if !bytes.Equal(gotRaw, data) {
				t.Fatalf("原始格式往返不一致: got %d 字节, want %d 字节", len(gotRaw), len(data))
			}

			// Encrypting the same plaintext twice must differ because of the random nonce. | 同一明文两次加密必须因随机数不同而不同。
			rawCT2, err := EncryptRaw(pub, data)
			if err != nil {
				t.Fatalf("第二次 EncryptRaw 返回错误: %v", err)
			}
			if bytes.Equal(rawCT, rawCT2) {
				t.Fatal("both encryptions produced the same ciphertext, the random nonce is reused | 两次加密的密文相同，随机数复用")
			}
		})
	}
}

// TestSM2EncryptRawLayout pins the byte layout of the raw ciphertext for the JavaScript front end to match. | TestSM2EncryptRawLayout 锁定原始密文的字节布局，供前端 JS 实现对齐。
//
// Layout: X1(32) || Y1(32) || C3(32) || C2(len(plaintext)), with no uncompressed point marker 0x04, and C3 = SM3(x2 || M || y2). | 布局：X1(32) || Y1(32) || C3(32) || C2(len(明文))，不含未压缩点标识 0x04，且 C3 = SM3(x2 || M || y2)。
func TestSM2EncryptRawLayout(t *testing.T) {
	priv, pub := mustKey(t)
	data := []byte("layout-probe|前端互操作|guomi")

	rawCT, err := EncryptRaw(pub, data)
	if err != nil {
		t.Fatalf("EncryptRaw 返回错误: %v", err)
	}
	if len(rawCT) != 96+len(data) {
		t.Fatalf("原始密文长度 = %d, want %d", len(rawCT), 96+len(data))
	}

	// Compare against gmsm's C1C3C2 ciphertext: gmsm adds one leading 0x04, the rest is identical. | 与 gmsm 的 C1C3C2 密文逐字节比对：gmsm 多出一个前导 0x04，其余完全一致。
	g, err := sm2.Encrypt(pub.Raw(), data, rand.Reader, sm2.C1C3C2)
	if err != nil {
		t.Fatalf("gmsm sm2.Encrypt 返回错误: %v", err)
	}
	if len(g) != 97+len(data) || g[0] != 0x04 {
		t.Fatalf("gmsm 原始密文 = %d 字节且首字节 0x%02x, want %d 字节且首字节 0x04", len(g), g[0], 97+len(data))
	}
	_ = g // The layout comparison is the deterministic check below; the two ciphertexts use different random nonces so only length and prefix can be compared. | 布局比对见下面的确定性检查（两次加密随机数不同，只能比长度与前缀）。

	// The first 64 bytes must be a point (X1, Y1) on the curve. | 前 64 字节必须是曲线上的点 (X1, Y1)。
	x1 := new(big.Int).SetBytes(rawCT[:32])
	y1 := new(big.Int).SetBytes(rawCT[32:64])
	if !pub.Raw().Curve.IsOnCurve(x1, y1) {
		t.Fatal("C1 of the raw ciphertext is not a point on the SM2 curve | 原始密文的 C1 不是 SM2 曲线上的点")
	}

	// C3 must equal SM3(x2 || M || y2), where (x2, y2) = d * C1. | C3 必须等于 SM3(x2 || M || y2)，其中 (x2, y2) = d * C1。
	x2, y2 := pub.Raw().Curve.ScalarMult(x1, y1, priv.Raw().D.Bytes())
	tm := make([]byte, 0, len(data)+64)
	tm = append(tm, leftPad32(x2)...)
	tm = append(tm, data...)
	tm = append(tm, leftPad32(y2)...)
	wantC3 := Sum(tm)
	if !bytes.Equal(rawCT[64:96], wantC3[:]) {
		t.Fatalf("C3 = %x, want SM3(x2||M||y2) = %x", rawCT[64:96], wantC3)
	}

	// gmsm's ciphertext without the 0x04 must decrypt here, and our ciphertext with 0x04 restored must decrypt in gmsm. | 剥掉 0x04 的 gmsm 密文必须能被本包解密；我们补回 0x04 后 gmsm 也必须能解密。
	gRaw := g[1:]
	back, err := DecryptRaw(priv, gRaw)
	if err != nil || !bytes.Equal(back, data) {
		t.Fatalf("DecryptRaw(gmsm 密文去掉 0x04) = (%d 字节, %v), want (%d 字节, nil)", len(back), err, len(data))
	}
	withPrefix := append([]byte{0x04}, rawCT...)
	gBack, err := sm2.Decrypt(priv.Raw(), withPrefix, sm2.C1C3C2)
	if err != nil || !bytes.Equal(gBack, data) {
		t.Fatalf("gmsm sm2.Decrypt(0x04 || 本包密文) = (%d 字节, %v), want (%d 字节, nil)", len(gBack), err, len(data))
	}
}

// TestSM2EncryptDecryptCrossCheckWithGmsm cross-checks ciphertext formats in both directions against gmsm. | TestSM2EncryptDecryptCrossCheckWithGmsm 与 gmsm 双向交叉校验密文格式。
func TestSM2EncryptDecryptCrossCheckWithGmsm(t *testing.T) {
	priv, pub := mustKey(t)
	data := []byte("cross-check|gmsm|SM2|国密")

	// Our ASN.1 ciphertext into gmsm. | 本包 ASN.1 密文 → gmsm 解密。
	ct, err := Encrypt(pub, data)
	if err != nil {
		t.Fatalf("Encrypt 返回错误: %v", err)
	}
	if got, err := sm2.DecryptAsn1(priv.Raw(), ct); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("gmsm DecryptAsn1(本包密文) = (%d 字节, %v), want (%d 字节, nil)", len(got), err, len(data))
	}

	// gmsm's ASN.1 ciphertext into us. | gmsm ASN.1 密文 → 本包解密。
	gCT, err := sm2.EncryptAsn1(pub.Raw(), data, rand.Reader)
	if err != nil {
		t.Fatalf("gmsm EncryptAsn1 返回错误: %v", err)
	}
	if got, err := Decrypt(priv, gCT); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("Decrypt(gmsm 密文) = (%d 字节, %v), want (%d 字节, nil)", len(got), err, len(data))
	}

	// gmsm's raw ciphertext with 0x04 into DecryptRaw without it. | gmsm 原始密文（含 0x04）→ 本包 DecryptRaw（去 0x04）。
	gRaw, err := sm2.Encrypt(pub.Raw(), data, rand.Reader, sm2.C1C3C2)
	if err != nil {
		t.Fatalf("gmsm Encrypt 返回错误: %v", err)
	}
	if got, err := DecryptRaw(priv, gRaw[1:]); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("DecryptRaw(gmsm 原始密文) = (%d 字节, %v), want (%d 字节, nil)", len(got), err, len(data))
	}

	// Our signature into gmsm and gmsm's signature into us. | 本包签名 → gmsm 验签；gmsm 签名 → 本包验签。
	sig, err := Sign(priv, data)
	if err != nil {
		t.Fatalf("Sign 返回错误: %v", err)
	}
	if !pub.Raw().Verify(data, sig) {
		t.Fatal("gmsm could not verify this package's signature | gmsm 未能验证本包签名")
	}
	gSig, err := priv.Raw().Sign(rand.Reader, data, nil)
	if err != nil {
		t.Fatalf("gmsm Sign 返回错误: %v", err)
	}
	if ok, err := Verify(pub, data, gSig); err != nil || !ok {
		t.Fatalf("Verify(gmsm 签名) = (%v, %v), want (true, nil)", ok, err)
	}
}

// TestSM2SignVerifyAndTamperFails checks the signature semantics: a correct signature verifies, and any single-byte change must fail. | TestSM2SignVerifyAndTamperFails 校验签名语义：正确验签通过，任何一字节改动必须失败。
func TestSM2SignVerifyAndTamperFails(t *testing.T) {
	priv, pub := mustKey(t)
	_, otherPub := mustKey(t)
	data := []byte("待签名消息|SM2|guomi|2026")

	sig, err := Sign(priv, data)
	if err != nil {
		t.Fatalf("Sign 返回错误: %v", err)
	}
	if len(sig) < 8 || sig[0] != 0x30 {
		t.Fatalf("ASN.1 签名 = %x, want SEQUENCE 开头且长度 >= 8", sig)
	}
	ok, err := Verify(pub, data, sig)
	if err != nil || !ok {
		t.Fatalf("Verify(正确签名) = (%v, %v), want (true, nil)", ok, err)
	}

	// Change one message byte. | 改一个字节的消息。
	for _, i := range []int{0, len(data) / 2, len(data) - 1} {
		tampered := append([]byte{}, data...)
		tampered[i] ^= 0x01
		ok, err := Verify(pub, tampered, sig)
		if err != nil {
			t.Fatalf("Verify(篡改消息) 返回错误: %v", err)
		}
		if ok {
			t.Fatalf("Verify(篡改消息第 %d 字节) = true, want false", i)
		}
	}

	// Change one signature byte. | 改一个字节的签名。
	for _, i := range []int{1, len(sig) / 2, len(sig) - 1} {
		tampered := append([]byte{}, sig...)
		tampered[i] ^= 0x01
		ok, err := Verify(pub, data, tampered)
		if err != nil {
			t.Fatalf("Verify(篡改签名) 返回错误: %v", err)
		}
		if ok {
			t.Fatalf("Verify(篡改签名第 %d 字节) = true, want false", i)
		}
	}

	// Another public key must fail to verify, and so must empty or garbage signatures. | 另一把公钥验签必须失败；空签名/垃圾签名也必须失败。
	if ok, _ := Verify(otherPub, data, sig); ok {
		t.Fatal("verification still passed with another public key | 换用其他公钥验签仍通过")
	}
	for _, bad := range [][]byte{nil, {}, {0x30, 0x03, 0x02, 0x01, 0x01}, []byte("not-a-der-signature")} {
		if ok, err := Verify(pub, data, bad); err != nil || ok {
			t.Fatalf("Verify(%x) = (%v, %v), want (false, nil)", bad, ok, err)
		}
	}

	// The hex signature API. | 十六进制签名 API。
	sigHex, err := SignHex(priv, data)
	if err != nil {
		t.Fatalf("SignHex 返回错误: %v", err)
	}
	if _, err := hex.DecodeString(sigHex); err != nil {
		t.Fatalf("SignHex 输出不是合法十六进制: %v", err)
	}
	if ok, err := VerifyHex(pub, data, sigHex); err != nil || !ok {
		t.Fatalf("VerifyHex(正确签名) = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, _ := VerifyHex(pub, []byte("other"), sigHex); ok {
		t.Fatal("VerifyHex(tampered message) = true, want false | VerifyHex(篡改消息) = true, want false")
	}
	last := sigHex[len(sigHex)-1]
	tamperedHex := sigHex[:len(sigHex)-1] + map[bool]string{true: "0", false: "1"}[last == '0']
	if ok, _ := VerifyHex(pub, data, tamperedHex); ok {
		t.Fatal("VerifyHex(tampered signature) = true, want false | VerifyHex(篡改签名) = true, want false")
	}
	if ok, err := VerifyHex(pub, data, "zz"); err == nil || ok {
		t.Fatalf("VerifyHex(非法十六进制) = (%v, %v), want (false, err)", ok, err)
	}

	// Nil receivers must return an error rather than panic. | 空指针必须返回错误而不是 panic。
	if _, err := Sign(nil, data); err == nil {
		t.Fatal("Sign(nil) did not return an error | Sign(nil) 未返回错误")
	}
	if _, err := Encrypt(nil, data); err == nil {
		t.Fatal("Encrypt(nil) did not return an error | Encrypt(nil) 未返回错误")
	}
	if _, err := Decrypt(nil, []byte{0x30}); err == nil {
		t.Fatal("Decrypt(nil) did not return an error | Decrypt(nil) 未返回错误")
	}
	if ok, err := Verify(nil, data, sig); err == nil || ok {
		t.Fatalf("Verify(nil) = (%v, %v), want (false, err)", ok, err)
	}
}

// TestSM2PEMAndHexRoundTrip checks PEM and hex key encoding round trips and the rejection of invalid input. | TestSM2PEMAndHexRoundTrip 校验 PEM/十六进制密钥编码的往返与非法输入拒绝。
func TestSM2PEMAndHexRoundTrip(t *testing.T) {
	priv, pub := mustKey(t)

	privPEM, err := MarshalPrivateKeyPEM(priv)
	if err != nil {
		t.Fatalf("MarshalPrivateKeyPEM 返回错误: %v", err)
	}
	block, rest := pem.Decode(privPEM)
	if block == nil {
		t.Fatal("the private key PEM could not be parsed | 私钥 PEM 无法解析")
	}
	if block.Type != "PRIVATE KEY" {
		t.Fatalf("私钥 PEM 类型 = %q, want %q", block.Type, "PRIVATE KEY")
	}
	if len(rest) != 0 {
		t.Fatalf("私钥 PEM 存在多余数据: %d 字节", len(rest))
	}
	// The DER encoding of the SM2 curve OID 1.2.156.10197.1.301 must appear in the PKCS#8 body. | SM2 曲线 OID 1.2.156.10197.1.301 的 DER 编码必须出现在 PKCS#8 中。
	if !bytes.Contains(block.Bytes, []byte{0x06, 0x08, 0x2a, 0x81, 0x1c, 0xcf, 0x55, 0x01, 0x82, 0x2d}) {
		t.Fatal("the private key PKCS#8 lacks the SM2 curve OID | 私钥 PKCS#8 中缺少 SM2 曲线 OID")
	}

	priv2, err := ParsePrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatalf("ParsePrivateKeyPEM 返回错误: %v", err)
	}
	if priv2.Raw().D.Cmp(priv.Raw().D) != 0 {
		t.Fatal("D differs after the private key PEM round-trip | 私钥 PEM 往返后 D 不一致")
	}

	pubPEM, err := MarshalPublicKeyPEM(pub)
	if err != nil {
		t.Fatalf("MarshalPublicKeyPEM 返回错误: %v", err)
	}
	pubBlock, _ := pem.Decode(pubPEM)
	if pubBlock == nil || pubBlock.Type != "PUBLIC KEY" {
		t.Fatalf("公钥 PEM 块 = %v, want 类型 PUBLIC KEY", pubBlock)
	}
	pub2, err := ParsePublicKeyPEM(pubPEM)
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM 返回错误: %v", err)
	}
	if pub2.Raw().X.Cmp(pub.Raw().X) != 0 || pub2.Raw().Y.Cmp(pub.Raw().Y) != 0 {
		t.Fatal("the coordinates differ after the public key PEM round-trip | 公钥 PEM 往返后坐标不一致")
	}

	// Hex: the private key is 64 lowercase characters, the public key is 04||X||Y in 130 lowercase characters. | 十六进制：私钥 64 字符小写；公钥 04||X||Y 共 130 字符小写。
	privHex := priv.Hex()
	if privHex != strings.ToLower(privHex) {
		t.Fatalf("私钥十六进制应为小写: %s", privHex)
	}
	priv3, err := PrivateKeyFromHex(privHex)
	if err != nil {
		t.Fatalf("PrivateKeyFromHex 返回错误: %v", err)
	}
	if priv3.Raw().D.Cmp(priv.Raw().D) != 0 {
		t.Fatal("D differs after the private key hex round-trip | 私钥十六进制往返后 D 不一致")
	}
	if priv4, err := PrivateKeyFromHex("0x" + privHex); err != nil || priv4.Raw().D.Cmp(priv.Raw().D) != 0 {
		t.Fatalf("PrivateKeyFromHex(带 0x 前缀) = (nil, %v), want 相同私钥", err)
	}
	if priv5, err := PrivateKeyFromHex("  " + strings.ToUpper(privHex) + "\n"); err != nil || priv5.Raw().D.Cmp(priv.Raw().D) != 0 {
		t.Fatalf("PrivateKeyFromHex(大写+空白) = (nil, %v), want 相同私钥", err)
	}

	pubHex := PublicKeyToHex(pub)
	if len(pubHex) != 130 || !strings.HasPrefix(pubHex, "04") {
		t.Fatalf("PublicKeyToHex = %d 字符, want 130 且以 04 开头", len(pubHex))
	}
	if pubHex != strings.ToLower(pubHex) {
		t.Fatalf("公钥十六进制应为小写: %s", pubHex)
	}
	for name, in := range map[string]string{
		"带 04 前缀": pubHex,
		"无 04 前缀": pubHex[2:],
		"含空白":     " " + pubHex + "\n",
	} {
		p, err := PublicKeyFromHex(in)
		if err != nil {
			t.Fatalf("PublicKeyFromHex(%s) 返回错误: %v", name, err)
		}
		if p.Raw().X.Cmp(pub.Raw().X) != 0 || p.Raw().Y.Cmp(pub.Raw().Y) != 0 {
			t.Fatalf("PublicKeyFromHex(%s) 往返后坐标不一致", name)
		}
	}
	if got := PublicKeyToHex(priv.Public()); got != pubHex {
		t.Fatalf("PublicKeyToHex(priv.Public()) = %s, want %s", got, pubHex)
	}

	// Invalid input. | 非法输入。
	if _, err := PrivateKeyFromHex("zz" + "00"); err == nil {
		t.Fatal("PrivateKeyFromHex(invalid hex) did not return an error | PrivateKeyFromHex(非法十六进制) 未返回错误")
	}
	if _, err := PrivateKeyFromHex(strings.Repeat("0", 64)); err == nil {
		t.Fatal("PrivateKeyFromHex(D=0) did not return an error | PrivateKeyFromHex(D=0) 未返回错误")
	}
	if _, err := PrivateKeyFromHex(privHex[:62]); err == nil {
		t.Fatal("PrivateKeyFromHex(too short) did not return an error | PrivateKeyFromHex(长度不足) 未返回错误")
	}
	if _, err := PublicKeyFromHex("04" + strings.Repeat("00", 64)); err == nil {
		t.Fatal("PublicKeyFromHex(point off the curve) did not return an error | PublicKeyFromHex(不在曲线上的点) 未返回错误")
	}
	if _, err := PublicKeyFromHex("abcd" + strings.Repeat("00", 64)); err == nil {
		t.Fatal("PublicKeyFromHex(invalid prefix length) did not return an error | PublicKeyFromHex(非法前缀长度) 未返回错误")
	}
	if _, err := ParsePrivateKeyPEM([]byte("not a pem")); err == nil {
		t.Fatal("ParsePrivateKeyPEM(garbage) did not return an error | ParsePrivateKeyPEM(垃圾数据) 未返回错误")
	}
	if _, err := ParsePublicKeyPEM([]byte("not a pem")); err == nil {
		t.Fatal("ParsePublicKeyPEM(garbage) did not return an error | ParsePublicKeyPEM(垃圾数据) 未返回错误")
	}
	// Type mismatch: parse a public key PEM as a private key. | 类型错配：把公钥 PEM 当私钥解析。
	if _, err := ParsePrivateKeyPEM(pubPEM); err == nil {
		t.Fatal("ParsePrivateKeyPEM(public key PEM) did not return an error | ParsePrivateKeyPEM(公钥 PEM) 未返回错误")
	}
	if _, err := MarshalPrivateKeyPEM(nil); err == nil {
		t.Fatal("MarshalPrivateKeyPEM(nil) did not return an error | MarshalPrivateKeyPEM(nil) 未返回错误")
	}
	if _, err := MarshalPublicKeyPEM(nil); err == nil {
		t.Fatal("MarshalPublicKeyPEM(nil) did not return an error | MarshalPublicKeyPEM(nil) 未返回错误")
	}
}

// TestSM2DecryptRejectsInvalidInput checks decryption robustness against malformed ciphertext, which must never panic. | TestSM2DecryptRejectsInvalidInput 校验解密对非法密文的健壮性（绝不 panic）。
func TestSM2DecryptRejectsInvalidInput(t *testing.T) {
	priv, pub := mustKey(t)
	data := []byte("tamper-probe-国密")

	rawCT, err := EncryptRaw(pub, data)
	if err != nil {
		t.Fatalf("EncryptRaw 返回错误: %v", err)
	}

	if _, err := DecryptRaw(priv, make([]byte, 95)); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("DecryptRaw(95 字节) 的错误 = %v, want ErrInvalidCiphertext", err)
	}
	// Replacing C1 with a point off the curve must return an error rather than panic. | C1 被替换为不在曲线上的点：必须返回错误而不是 panic。
	badPoint := append([]byte{}, rawCT...)
	copy(badPoint[:64], make([]byte, 64))
	if _, err := DecryptRaw(priv, badPoint); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("DecryptRaw(无效 C1) 的错误 = %v, want ErrInvalidCiphertext", err)
	}
	// Tamper with C3. | 篡改 C3。
	tamperC3 := append([]byte{}, rawCT...)
	tamperC3[70] ^= 0x01
	if _, err := DecryptRaw(priv, tamperC3); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("DecryptRaw(篡改 C3) 的错误 = %v, want ErrDecrypt", err)
	}
	// Tamper with C2. | 篡改 C2。
	tamperC2 := append([]byte{}, rawCT...)
	tamperC2[len(tamperC2)-1] ^= 0x01
	if _, err := DecryptRaw(priv, tamperC2); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("DecryptRaw(篡改 C2) 的错误 = %v, want ErrDecrypt", err)
	}
	// Garbage ASN.1. | ASN.1 垃圾数据。
	for _, bad := range [][]byte{nil, {}, {0x30, 0x03, 0x02, 0x01, 0x01}, []byte("garbage")} {
		if _, err := Decrypt(priv, bad); !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatalf("Decrypt(%x) 的错误 = %v, want ErrInvalidCiphertext", bad, err)
		}
	}
	// A well-formed ASN.1 structure whose C1 is off the curve. | ASN.1 结构正确但 C1 不在曲线上。
	asn1CT, err := Encrypt(pub, data)
	if err != nil {
		t.Fatalf("Encrypt 返回错误: %v", err)
	}
	asn1CT[len(asn1CT)-1] ^= 0x01
	if _, err := Decrypt(priv, asn1CT); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("Decrypt(篡改 ASN.1 密文) 的错误 = %v, want ErrDecrypt", err)
	}
}
