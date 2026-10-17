package auth

import (
	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/security/guomi"
)

// secKeyAADSep separates the tenant id from the user id inside the additional
// authenticated data. | secKeyAADSep 在附加认证数据中分隔租户 ID 与用户 ID。
const secKeyAADSep = "\x00"

// sealSecKey encrypts a per-user JWT key with SM4-GCM. | sealSecKey 用 SM4-GCM 加密每用户 JWT 密钥。
//
// The output layout is nonce(12) || ciphertext || tag(16). The tenant id and the
// user id are authenticated as additional data, so a ciphertext moved onto another
// record is rejected by OpenGCM instead of being used as that user's key. | 输出布局为
// nonce(12) || 密文 || tag(16)。租户 ID 与用户 ID 作为附加认证数据参与认证，因此被搬到其它记录上的密文
// 会被 OpenGCM 拒绝，而不会被当作该用户的密钥使用。
func sealSecKey(masterKey [guomi.SM4KeySize]byte, tenantID, userID string, secKey []byte) ([]byte, error) {
	nonce, err := randomBytes(guomi.SM4GCMNonceSize)
	if err != nil {
		return nil, err
	}
	sealed, err := guomi.SealGCM(masterKey[:], nonce, secKey, secKeyAAD(tenantID, userID))
	if err != nil {
		// Unreachable with a 16-byte key and a 12-byte nonce; kept for honesty. | 在 16 字节密钥与 12 字节 nonce 下不可达，保留以如实上报。
		return nil, errx.Wrap(err, guomi.ErrCipherCreate)
	}
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	return append(out, sealed...), nil
}

// openSecKey decrypts what sealSecKey produced. | openSecKey 解密 sealSecKey 的输出。
//
// A truncated blob, a wrong master key or a blob that was moved to another tenant
// or user fails with an error wrapping ErrSecKeyMissing, and no key is returned. | 数据被截断、
// 主密钥不匹配，或密文被搬到其它租户/用户上时，返回包装了 ErrSecKeyMissing 的错误，且不返回任何密钥。
func openSecKey(masterKey [guomi.SM4KeySize]byte, tenantID, userID string, sealed []byte) ([]byte, error) {
	if len(sealed) <= guomi.SM4GCMNonceSize {
		return nil, errx.Detail(ErrSecKeyMissing, "sealed sec_key is truncated | 封装的 sec_key 被截断")
	}
	nonce := sealed[:guomi.SM4GCMNonceSize]
	body := sealed[guomi.SM4GCMNonceSize:]
	secKey, err := guomi.OpenGCM(masterKey[:], nonce, body, secKeyAAD(tenantID, userID))
	if err != nil {
		return nil, errx.Wrap(err, ErrSecKeyMissing)
	}
	return secKey, nil
}

// secKeyAAD builds the additional authenticated data of a sec_key blob. | secKeyAAD 构造 sec_key 密文的附加认证数据。
func secKeyAAD(tenantID, userID string) []byte {
	aad := make([]byte, 0, len(tenantID)+len(userID)+1)
	aad = append(aad, tenantID...)
	aad = append(aad, secKeyAADSep...)
	return append(aad, userID...)
}
