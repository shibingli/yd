package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/shibingli/yd/core/errx"
)

// Sentinel errors of the development self-signed certificate path, all testable with errors.Is. | 开发自签证书路径的哨兵错误，均可通过 errors.Is 判定。
var (
	// ErrSelfSignedKeyGen reports a key pair that could not be generated. | ErrSelfSignedKeyGen 表示密钥对生成失败。
	ErrSelfSignedKeyGen = errx.New("server: failed to generate the self-signed key | server：生成自签密钥失败")

	// ErrSelfSignedSerial reports a serial number that could not be generated. | ErrSelfSignedSerial 表示序列号生成失败。
	ErrSelfSignedSerial = errx.New("server: failed to generate the serial number | server：生成序列号失败")

	// ErrSelfSignedSign reports a certificate that could not be signed. | ErrSelfSignedSign 表示自签证书签发失败。
	ErrSelfSignedSign = errx.New("server: failed to sign the self-signed certificate | server：签发自签证书失败")

	// ErrSelfSignedKeyMarshal reports a private key that could not be serialized. | ErrSelfSignedKeyMarshal 表示私钥序列化失败。
	ErrSelfSignedKeyMarshal = errx.New("server: failed to marshal the private key | server：序列化私钥失败")

	// ErrSelfSignedCertAssemble reports a certificate that could not be assembled into a tls.Certificate. | ErrSelfSignedCertAssemble 表示自签证书组装失败。
	ErrSelfSignedCertAssemble = errx.New("server: failed to assemble the self-signed certificate | server：组装自签证书失败")
)

// selfSignedOnce caches the development certificate for the process lifetime. | selfSignedOnce 在进程生命周期内缓存开发证书。
var (
	selfSignedOnce sync.Once
	selfSignedCert tls.Certificate
	selfSignedErr  error
)

// selfSignedCertForDev generates a one-off ECDSA P-256 certificate. | selfSignedCertForDev 生成一次性 ECDSA P-256 证书。
//
// QUIC mandates TLS 1.3, so HTTP/3 cannot run in cleartext. Instead of forcing
// every developer to provision certificates, the server mints an in-memory
// self-signed certificate valid for localhost and loopback addresses. | QUIC 强制
// TLS 1.3，因此 HTTP/3 无法明文运行。为避免强制开发者准备证书，服务器会在内存中
// 生成一张对 localhost 与回环地址有效的自签证书。
func selfSignedCertForDev() (tls.Certificate, error) {
	selfSignedOnce.Do(func() {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			selfSignedErr = errx.Wrap(err, ErrSelfSignedKeyGen)
			return
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			selfSignedErr = errx.Wrap(err, ErrSelfSignedSerial)
			return
		}
		tmpl := &x509.Certificate{
			SerialNumber:          serial,
			Subject:               pkix.Name{CommonName: "yd-dev-localhost"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(365 * 24 * time.Hour),
			KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: true,
			IsCA:                  true,
			DNSNames:              []string{"localhost"},
			IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			selfSignedErr = errx.Wrap(err, ErrSelfSignedSign)
			return
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			selfSignedErr = errx.Wrap(err, ErrSelfSignedKeyMarshal)
			return
		}
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			selfSignedErr = errx.Wrap(err, ErrSelfSignedCertAssemble)
			return
		}
		cert.Leaf, _ = x509.ParseCertificate(der)
		selfSignedCert = cert
	})
	return selfSignedCert, selfSignedErr
}
