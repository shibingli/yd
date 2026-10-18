package jwt

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"testing"
)

// TestPooledHMACMatchesStdlib differentially checks the pooled HMAC engine
// against crypto/hmac across key lengths around the block boundary and across
// message lengths, including pool reuse. | TestPooledHMACMatchesStdlib 用
// crypto/hmac 对池化 HMAC 引擎做差分校验，覆盖块长附近的密钥长度与多种消息长度，
// 并包含池复用。
func TestPooledHMACMatchesStdlib(t *testing.T) {
	cases := []struct {
		alg     Alg
		newHash func() hash.Hash
	}{
		{alg: HS256, newHash: sha256.New},
		{alg: HS384, newHash: sha512.New384},
		{alg: HS512, newHash: sha512.New},
	}
	keyLens := []int{0, 1, 15, 16, 31, 63, 64, 65, 127, 128, 129, 200}
	msgLens := []int{0, 1, 31, 64, 127, 128, 129, 1024}

	for _, tc := range cases {
		pool := algMACPool(tc.alg)
		for _, kl := range keyLens {
			key := make([]byte, kl)
			for i := range key {
				key[i] = byte(i*7 + kl)
			}
			for _, ml := range msgLens {
				msg := make([]byte, ml)
				for i := range msg {
					msg[i] = byte(i*13 + ml)
				}
				mac := hmac.New(tc.newHash, key)
				_, _ = mac.Write(msg)
				want := mac.Sum(nil)

				if got := pool.sum(key, msg, nil); !hmac.Equal(got, want) {
					t.Fatalf("%s key=%d msg=%d: got %x want %x | 结果不一致", tc.alg, kl, ml, got, want)
				}
				// Reuse the pool and append to a non-empty destination. | 复用池并向非空目标追加。
				prefix := []byte("prefix:")
				got := pool.sum(key, msg, prefix)
				if !bytes.Equal(got[:len(prefix)], prefix) || !hmac.Equal(got[len(prefix):], want) {
					t.Fatalf("%s key=%d msg=%d: append result mismatch | 追加结果不一致", tc.alg, kl, ml)
				}
			}
		}
	}
}

// TestPooledHMACRFC4231Vectors pins the published RFC 4231 test vectors. | TestPooledHMACRFC4231Vectors
// 固定 RFC 4231 的公开测试向量。
func TestPooledHMACRFC4231Vectors(t *testing.T) {
	key := bytes.Repeat([]byte{0x0b}, 20)
	msg := []byte("Hi There")
	cases := []struct {
		alg  Alg
		want string
	}{
		{alg: HS256, want: "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7"},
		{alg: HS384, want: "afd03944d84895626b0825f4ab46907f15f9dadbe4101ec682aa034c7cebc59cfaea9ea9076ede7f4af152e8b2fa9cb6"},
		{alg: HS512, want: "87aa7cdea5ef619d4ff0b4241a1d6cb02379f4e2ce4ec2787ad0b30545e17cdedaa833b7d6b8a702038b274eaea3f4e4be9d914eeb61f1702e696c203a126854"},
	}
	for _, tc := range cases {
		want, err := hex.DecodeString(tc.want)
		if err != nil {
			t.Fatalf("bad test vector | 测试向量非法: %v", err)
		}
		if got := algMACPool(tc.alg).sum(key, msg, nil); !hmac.Equal(got, want) {
			t.Fatalf("%s: got %x want %x | 结果不一致", tc.alg, got, want)
		}
	}
}
