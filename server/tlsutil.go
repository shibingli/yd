package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"
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
			selfSignedErr = fmt.Errorf("server: 生成自签密钥失败: %w", err)
			return
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			selfSignedErr = fmt.Errorf("server: 生成序列号失败: %w", err)
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
			selfSignedErr = fmt.Errorf("server: 签发自签证书失败: %w", err)
			return
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			selfSignedErr = fmt.Errorf("server: 序列化私钥失败: %w", err)
			return
		}
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			selfSignedErr = fmt.Errorf("server: 组装自签证书失败: %w", err)
			return
		}
		cert.Leaf, _ = x509.ParseCertificate(der)
		selfSignedCert = cert
	})
	return selfSignedCert, selfSignedErr
}
