package router

import (
	"net"
	"net/netip"
	"strings"
)

// maxForwardedHops bounds the number of X-Forwarded-For entries accepted from a trusted peer.
// | maxForwardedHops 限制可信对端提供的 X-Forwarded-For 条目数。
//
// A chain longer than this is treated as malformed: real deployments sit far below the limit, and
// parsing an unbounded header on every request is a cheap denial-of-service vector. | 超过该长度的链
// 视为畸形：真实部署远低于该上限，而每请求解析无界头部是一个廉价的拒绝服务入口。
const maxForwardedHops = 64

// maxForwardedBytes bounds the raw X-Forwarded-For length. | maxForwardedBytes 限制 X-Forwarded-For 原始长度。
const maxForwardedBytes = 8 << 10

// ClientIP resolves the client address using an explicit trusted-proxy model. | ClientIP 使用显式的可信代理模型解析客户端地址。
//
// The rule is the only one that resists forgery: | 这条规则是唯一经得起伪造的：
//
//   - when the peer (RemoteAddr) is not inside trusted, proxy headers are ignored entirely and the
//     peer address is returned; | 对端（RemoteAddr）不在 trusted 内时，完全忽略代理头并返回对端地址；
//   - when the peer is trusted, X-Forwarded-For is walked from the right, skipping trusted proxies,
//     and the first untrusted hop is the client; the leftmost value is attacker-controlled and is
//     never used unless every hop is trusted; | 对端可信时，从右向左遍历 X-Forwarded-For 并跳过可信代理，
//     第一个不可信跳即客户端；最左值可被攻击者伪造，只有在所有跳都可信时才使用；
//   - a malformed or oversized chain falls back to the peer address; X-Real-Ip is consulted only
//     for a trusted peer and must parse as an address. | 畸形或超长的链回退到对端地址；X-Real-Ip
//     仅在可信对端时参考，且必须能解析为地址。
//
// With an empty trusted list the method degrades to the peer address, which is the safe default.
// RealIP remains available for deployments that terminate their own proxy, but it trusts headers
// unconditionally and must not be used to make a security decision. | trusted 为空时该方法退化为对端
// 地址，这是安全默认值。RealIP 仍可供自己终结代理的部署使用，但它无条件信任头部，不得用于安全判定。
func (c *Ctx) ClientIP(trusted []netip.Prefix) string {
	peer := remoteHost(c.Request.RemoteAddr)
	if len(trusted) == 0 {
		return peer
	}
	if !ipTrusted(peer, trusted) {
		return peer
	}
	// X-Forwarded-For may arrive as several header lines (a proxy appends its own line). Header.Get
	// returns only the first line, so walking it would both miss hops and let a client-controlled
	// first line win. Join every line in wire order before parsing. | X-Forwarded-For 可能以多行
	// 头部到达（代理会追加自己的一行）。Header.Get 只返回第一行，按它遍历既会漏掉跳数，又会
	// 让客户端控制的第一行胜出。解析前按线上顺序拼接所有行。
	if lines := c.Request.Header.Values("X-Forwarded-For"); len(lines) > 0 {
		raw := strings.Join(lines, ",")
		if raw == "" {
			return peer
		}
		hops, ok := parseForwarded(raw)
		if !ok {
			return peer
		}
		for i := len(hops) - 1; i >= 0; i-- {
			if !addrTrusted(hops[i], trusted) {
				return hops[i].String()
			}
		}
		return hops[0].String()
	}
	if raw := strings.TrimSpace(c.Request.Header.Get("X-Real-Ip")); raw != "" {
		if addr, err := netip.ParseAddr(raw); err == nil {
			return addr.Unmap().String()
		}
	}
	return peer
}

// remoteHost extracts the host from RemoteAddr, accepting both "host:port" and a bare address.
// | remoteHost 从 RemoteAddr 提取主机，同时接受 "host:port" 与裸地址。
func remoteHost(remoteAddr string) string {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return ""
	}
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	// Only the IPv4-mapped form needs canonicalization; parsing every peer would allocate on the
	// default (no trusted proxies) path for no benefit. | 只有 IPv4 映射形式需要归一；对默认
	// （无可信代理）路径上的每个对端都做解析只会白白分配。
	if strings.HasPrefix(host, "::ffff:") {
		if addr, err := netip.ParseAddr(host); err == nil {
			return addr.Unmap().String()
		}
	}
	return host
}

// parseForwarded parses an X-Forwarded-For value into addresses, rejecting any malformed hop.
// | parseForwarded 把 X-Forwarded-For 解析为地址列表，任一跳畸形即整体拒绝。
func parseForwarded(raw string) ([]netip.Addr, bool) {
	if len(raw) > maxForwardedBytes {
		return nil, false
	}
	parts := strings.Split(raw, ",")
	if len(parts) == 0 || len(parts) > maxForwardedHops {
		return nil, false
	}
	hops := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		addr, err := netip.ParseAddr(part)
		if err != nil {
			return nil, false
		}
		hops = append(hops, addr.Unmap())
	}
	return hops, true
}

// ipTrusted reports whether the peer string is inside any trusted prefix. | ipTrusted 报告对端字符串是否落在任一可信前缀内。
func ipTrusted(peer string, trusted []netip.Prefix) bool {
	addr, err := netip.ParseAddr(peer)
	if err != nil {
		return false // a non-address peer can never be a trusted proxy | 非地址对端绝不可能是可信代理
	}
	return addrTrusted(addr.Unmap(), trusted)
}

// addrTrusted reports whether addr is inside any trusted prefix. | addrTrusted 报告 addr 是否落在任一可信前缀内。
func addrTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	for _, prefix := range trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
