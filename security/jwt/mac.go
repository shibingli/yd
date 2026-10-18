package jwt

import (
	"crypto/sha256"
	"crypto/sha512"
	"hash"
	"sync"
)

// This file pools the hash and HMAC engines of the verification path. | 本文件池化校验路径上的哈希与 HMAC 引擎。
//
// The standard library's hmac.New allocates a wrapper plus two hash states on every call, which is the
// single largest source of per-request allocations. A pooled engine reuses those states and is reset
// before every use; the key schedule is re-applied per call, so no key material is ever reused across
// requests. | 标准库的 hmac.New 每次调用都会分配一个包装对象与两个哈希状态，是每请求分配的最大来源。
// 池化引擎复用这些状态并在每次使用前重置；密钥调度每次调用都会重新执行，因此绝不存在跨请求复用
// 密钥材料的情况。

// hashPool recycles hash states of one algorithm. | hashPool 回收一种算法的哈希状态。
type hashPool struct {
	newHash func() hash.Hash
	pool    sync.Pool
}

// newHashPool builds a hash pool whose states are created lazily. | newHashPool 构造一个按需创建状态的哈希池。
func newHashPool(newHash func() hash.Hash) *hashPool {
	p := &hashPool{newHash: newHash}
	p.pool.New = func() any { return newHash() }
	return p
}

// sum returns the digest of input appended to dst, reusing a pooled state. | sum 返回 input 的摘要并追加到 dst，复用池化状态。
func (p *hashPool) sum(input, dst []byte) []byte {
	h := p.pool.Get().(hash.Hash)
	h.Reset()
	_, _ = h.Write(input)
	out := h.Sum(dst)
	// Reset before returning to the pool so no digest state survives between uses. | 归还前重置，
	// 使摘要状态不在两次使用之间残留。
	h.Reset()
	p.pool.Put(h)
	return out
}

var (
	sha256HashPool = newHashPool(sha256.New)
	sha384HashPool = newHashPool(sha512.New384)
	sha512HashPool = newHashPool(sha512.New)
)

// algHashPool returns the digest pool of alg. | algHashPool 返回 alg 对应的摘要池。
func algHashPool(alg Alg) *hashPool {
	switch alg {
	case HS256, RS256, PS256, ES256:
		return sha256HashPool
	case HS384, RS384, PS384, ES384:
		return sha384HashPool
	case HS512, RS512, PS512, ES512:
		return sha512HashPool
	}
	return nil
}

// macEngine is one pooled HMAC engine. It implements RFC 2104 with the pooled inner and outer hash
// states. All scratch space lives on the engine, so a steady-state MAC performs no allocation. | macEngine
// 是一个池化的 HMAC 引擎。它用池化的内、外层哈希状态实现 RFC 2104；全部暂存空间都在引擎上，
// 因此稳态 MAC 不产生任何分配。
type macEngine struct {
	inner     hash.Hash
	outer     hash.Hash
	block     [maxHashBlock]byte
	digest    [maxHashSize]byte
	blockSize int
}

// maxHashBlock and maxHashSize bound SHA-512, the largest hash used here. | maxHashBlock 与 maxHashSize
// 以本包使用的最大哈希 SHA-512 为上界。
const (
	maxHashBlock = 128
	maxHashSize  = 64
)

// macPool recycles macEngine values of one hash function. | macPool 回收一种哈希函数的 macEngine。
type macPool struct {
	newHash func() hash.Hash
	pool    sync.Pool
	block   int
}

// newMACPool builds an HMAC pool for newHash; block is its block size in bytes. | newMACPool 为 newHash
// 构造 HMAC 池；block 是它的块字节数。
func newMACPool(newHash func() hash.Hash, block int) *macPool {
	p := &macPool{newHash: newHash, block: block}
	p.pool.New = func() any {
		return &macEngine{
			inner:     newHash(),
			outer:     newHash(),
			blockSize: block,
		}
	}
	return p
}

var (
	hmacSHA256Pool = newMACPool(sha256.New, sha256.BlockSize)
	hmacSHA384Pool = newMACPool(sha512.New384, sha512.BlockSize)
	hmacSHA512Pool = newMACPool(sha512.New, sha512.BlockSize)
)

// algMACPool returns the HMAC pool of a symmetric algorithm. | algMACPool 返回对称算法对应的 HMAC 池。
func algMACPool(alg Alg) *macPool {
	switch alg {
	case HS256:
		return hmacSHA256Pool
	case HS384:
		return hmacSHA384Pool
	case HS512:
		return hmacSHA512Pool
	}
	return nil
}

// sum computes HMAC(key, msg) and appends it to dst. | sum 计算 HMAC(key, msg) 并追加到 dst。
func (p *macPool) sum(key, msg, dst []byte) []byte {
	m := p.pool.Get().(*macEngine)
	out := m.sum(key, msg, dst)
	p.pool.Put(m)
	return out
}

// sum implements RFC 2104 §2: | sum 实现 RFC 2104 §2：
//
//	K' = K padded to the block size (or H(K) padded when K is longer)
//	HMAC(K, m) = H((K' xor opad) || H((K' xor ipad) || m))
func (m *macEngine) sum(key, msg, dst []byte) []byte {
	k := m.block[:m.blockSize]
	clear(k)
	if len(key) > m.blockSize {
		// A key longer than the block is hashed first; its digest is shorter than the block
		// because every supported digest fits in maxHashSize. | 超长密钥先做哈希；所有受支持
		// 摘要都小于块长，因此摘要必然完整落入块内。
		m.inner.Reset()
		_, _ = m.inner.Write(key)
		digest := m.inner.Sum(m.digest[:0])
		copy(k, digest)
	} else {
		copy(k, key)
	}

	m.inner.Reset()
	for i := range k {
		k[i] ^= 0x36
	}
	_, _ = m.inner.Write(k)
	_, _ = m.inner.Write(msg)
	innerSum := m.inner.Sum(m.digest[:0])

	m.outer.Reset()
	// k currently holds the ipad block; turn it into the opad block. | k 当前是 ipad 块，
	// 把它转成 opad 块。
	for i := range k {
		// (key xor 0x36) xor (0x36 xor 0x5c) = key xor 0x5c | （key xor 0x36）xor（0x36 xor 0x5c）= key xor 0x5c
		k[i] ^= 0x36 ^ 0x5c
	}
	_, _ = m.outer.Write(k)
	_, _ = m.outer.Write(innerSum)
	out := m.outer.Sum(dst)

	// Do not retain key-derived pads or digest state in the pool. | 不在池中保留密钥派生的填充或摘要状态。
	clear(k)
	clear(m.digest[:])
	m.inner.Reset()
	m.outer.Reset()
	return out
}
