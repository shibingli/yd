// Package cryptobench compares the national cryptography suite (SM2/SM3/SM4)
// and the standard algorithms (SHA-256, AES-GCM, ChaCha20-Poly1305, ECDSA,
// Ed25519) across the framework wrappers, the maintained emmansun/gmsm
// implementation, the previous tjfoc/gmsm backend, and the standard library.
// | Package cryptobench 对比国密套件（SM2/SM3/SM4）与常密算法（SHA-256、AES-GCM、
// ChaCha20-Poly1305、ECDSA、Ed25519）在框架封装、维护版 emmansun/gmsm、旧版
// tjfoc/gmsm 后端与标准库四种实现下的性能。
package cryptobench
