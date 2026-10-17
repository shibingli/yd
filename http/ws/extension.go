package ws

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/shibingli/yd/core/errx"
)

// Window size bounds from RFC 7692 §7.1.2. | RFC 7692 §7.1.2 规定的窗口大小边界。
const (
	minWindowBits     = 8
	maxWindowBits     = 15
	defaultWindowBits = maxWindowBits
)

// compressionConfig is one negotiated RFC 7692 permessage-deflate configuration. | compressionConfig 是一份协商完成的 RFC 7692 permessage-deflate 配置。
//
// Parameters are named after the two protocol roles rather than the local process: | 参数按协议角色而非本进程命名：
// "client" always denotes the client endpoint and "server" the server endpoint, so the same | "client" 恒指客户端端点、"server" 恒指服务端端点，
// struct describes both the compressor and the decompressor of either side. | 因此同一结构同时描述任一侧的压缩器与解压器。
type compressionConfig struct {
	// clientNoContextTakeover means the client resets its compression context for every message. | clientNoContextTakeover 表示客户端每条消息都重置压缩上下文。
	clientNoContextTakeover bool
	// serverNoContextTakeover means the server resets its compression context for every message. | serverNoContextTakeover 表示服务端每条消息都重置压缩上下文。
	serverNoContextTakeover bool
	// clientWindowBits is the LZ77 window (log2) the client may use for compression. | clientWindowBits 是客户端压缩所用的 LZ77 窗口（以 2 为底的对数）。
	clientWindowBits int
	// serverWindowBits is the LZ77 window (log2) the server may use for compression. | serverWindowBits 是服务端压缩所用的 LZ77 窗口（以 2 为底的对数）。
	serverWindowBits int
}

// newCompressionConfig returns a configuration with the RFC 7692 defaults (32 KiB windows). | newCompressionConfig 返回使用 RFC 7692 默认值（32 KiB 窗口）的配置。
func newCompressionConfig() *compressionConfig {
	return &compressionConfig{clientWindowBits: defaultWindowBits, serverWindowBits: defaultWindowBits}
}

// extensionParam is one parsed extension parameter. | extensionParam 是一个解析后的扩展参数。
type extensionParam struct {
	// name is the lower-case parameter name. | name 为小写参数名。
	name string
	// value is the parameter value with any quoted-string framing removed. | value 为参数取值，已去掉引号包裹。
	value string
	// hasValue reports whether an "=value" part was present. | hasValue 报告是否出现了 "=value" 部分。
	hasValue bool
}

// extensionEntry is one parsed item of a Sec-WebSocket-Extensions header. | extensionEntry 是 Sec-WebSocket-Extensions 头部中的一项。
type extensionEntry struct {
	// name is the lower-case extension token. | name 为小写扩展 token。
	name string
	// params holds the parsed parameters in wire order. | params 按线上顺序保存解析后的参数。
	params []extensionParam
	// invalid marks malformed syntax; such an entry must be declined. | invalid 标记语法非法；这类项必须被拒绝。
	invalid bool
}

// splitHTTPList splits s on sep while ignoring separators inside quoted strings. | splitHTTPList 按 sep 切分 s，忽略引号内的分隔符。
func splitHTTPList(s string, sep byte) []string {
	out := make([]string, 0, 4)
	start := 0
	quoted, escaped := false, false
	for i := 0; i < len(s); i++ {
		switch {
		case escaped:
			escaped = false
		case quoted && s[i] == '\\':
			escaped = true
		case s[i] == '"':
			quoted = !quoted
		case s[i] == sep && !quoted:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// isTokenChar reports whether c may appear in an RFC 7230 token. | isTokenChar 报告 c 是否可以出现在 RFC 7230 的 token 中。
func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

// parseParamValue validates and unquotes a parameter value (token or quoted-string). | parseParamValue 校验并去引号（token 或 quoted-string）。
func parseParamValue(v string) (string, bool) {
	if v == "" {
		return "", false
	}
	if v[0] != '"' {
		for i := 0; i < len(v); i++ {
			if !isTokenChar(v[i]) {
				return "", false
			}
		}
		return v, true
	}
	if len(v) < 2 || v[len(v)-1] != '"' {
		return "", false
	}
	var b strings.Builder
	b.Grow(len(v))
	for i := 1; i < len(v)-1; i++ {
		c := v[i]
		if c == '\\' {
			i++
			if i >= len(v)-1 {
				return "", false
			}
			b.WriteByte(v[i])
			continue
		}
		if c == '"' {
			return "", false
		}
		b.WriteByte(c)
	}
	return b.String(), true
}

// splitParam splits "name=value" at the first unquoted '='. | splitParam 在第一个引号外的 '=' 处切分 "name=value"。
func splitParam(s string) (name, value string, hasValue, ok bool) {
	quoted, escaped := false, false
	for i := 0; i < len(s); i++ {
		switch {
		case escaped:
			escaped = false
		case quoted && s[i] == '\\':
			escaped = true
		case s[i] == '"':
			quoted = !quoted
		case s[i] == '=' && !quoted:
			return strings.ToLower(strings.TrimSpace(s[:i])), strings.TrimSpace(s[i+1:]), true, true
		}
	}
	if quoted || escaped {
		return "", "", false, false
	}
	return strings.ToLower(strings.TrimSpace(s)), "", false, true
}

// parseExtensionHeader parses every Sec-WebSocket-Extensions value into entries. | parseExtensionHeader 把全部 Sec-WebSocket-Extensions 值解析为扩展项。
//
// Parsing never fails as a whole: a malformed parameter marks only its own entry invalid, so a later | 解析不会整体失败：非法参数只会把所在项标记为非法，
// entry in the same header remains usable. | 同一头部中后续的项仍然可用。
func parseExtensionHeader(values []string) []extensionEntry {
	var out []extensionEntry
	for _, v := range values {
		for _, raw := range splitHTTPList(v, ',') {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			parts := splitHTTPList(raw, ';')
			name := strings.ToLower(strings.TrimSpace(parts[0]))
			if name == "" {
				continue
			}
			e := extensionEntry{name: name}
			for _, p := range parts[1:] {
				p = strings.TrimSpace(p)
				if p == "" {
					e.invalid = true
					continue
				}
				pname, pvalue, hasValue, ok := splitParam(p)
				if !ok || pname == "" {
					e.invalid = true
					continue
				}
				if hasValue {
					pvalue, ok = parseParamValue(pvalue)
					if !ok {
						e.invalid = true
						continue
					}
				}
				e.params = append(e.params, extensionParam{name: pname, value: pvalue, hasValue: hasValue})
			}
			out = append(out, e)
		}
	}
	return out
}

// parseWindowBits parses a window-size value, which must be an integer from 8 to 15. | parseWindowBits 解析窗口大小取值，必须是 8 到 15 的整数。
func parseWindowBits(v string) (int, bool) {
	n, err := strconv.Atoi(v)
	if err != nil || n < minWindowBits || n > maxWindowBits {
		return 0, false
	}
	return n, true
}

// negotiateServerCompression picks the first permessage-deflate offer the server can honour. | negotiateServerCompression 选择服务端能够履行的第一个 permessage-deflate 提议。
//
// It returns the chosen configuration and the exact response-header value, or (nil, "") when no | 返回选中的配置与精确的响应头取值；没有任何可接受提议时返回 (nil, "")，
// offer is acceptable. An unknown parameter, a duplicate parameter or an out-of-range window size | 此时不启用扩展而不是让握手失败：未知参数、重复参数或越界窗口
// declines only that offer instead of failing the handshake (RFC 7692 §7.1). | 只会拒绝该项提议（RFC 7692 §7.1）。
func negotiateServerCompression(h http.Header) (*compressionConfig, string) {
	for _, e := range parseExtensionHeader(h.Values(headerSecWebSocketExtensions)) {
		if e.name != "permessage-deflate" || e.invalid {
			continue
		}
		if cfg, reply, ok := acceptDeflateOffer(e); ok {
			return cfg, reply
		}
	}
	return nil, ""
}

// acceptDeflateOffer validates one permessage-deflate offer and builds the matching response value. | acceptDeflateOffer 校验单个 permessage-deflate 提议并构造对应的响应取值。
//
// The server agrees to every no-context-takeover parameter the client offered and never introduces | 服务端接受客户端提议的全部 no-context-takeover 参数，且绝不引入客户端未提议的
// parameters the client did not offer (RFC 7692 §7.1.1.2 / §7.1.2.2). | 参数（RFC 7692 §7.1.1.2 / §7.1.2.2）。
func acceptDeflateOffer(e extensionEntry) (*compressionConfig, string, bool) {
	cfg := newCompressionConfig()
	clientBitsOffered, serverBitsOffered := false, false
	seen := make(map[string]struct{}, len(e.params))

	for _, p := range e.params {
		if _, dup := seen[p.name]; dup {
			return nil, "", false
		}
		seen[p.name] = struct{}{}

		switch p.name {
		case "client_max_window_bits":
			clientBitsOffered = true
			if !p.hasValue {
				// a bare parameter means "up to 15 bits", which is the default | 无值表示「至多 15 位」，即默认值
				continue
			}
			n, ok := parseWindowBits(p.value)
			if !ok {
				return nil, "", false
			}
			cfg.clientWindowBits = n
		case "server_max_window_bits":
			serverBitsOffered = true
			if !p.hasValue {
				// RFC 7692 §7.1.2.2 requires a value in an offer | RFC 7692 §7.1.2.2 要求提议中必须带值
				return nil, "", false
			}
			n, ok := parseWindowBits(p.value)
			if !ok {
				return nil, "", false
			}
			cfg.serverWindowBits = n
		case "client_no_context_takeover":
			if p.hasValue {
				return nil, "", false
			}
			cfg.clientNoContextTakeover = true
		case "server_no_context_takeover":
			if p.hasValue {
				return nil, "", false
			}
			cfg.serverNoContextTakeover = true
		default:
			// an unknown parameter makes the offer unacceptable | 未知参数使该提议不可接受
			return nil, "", false
		}
	}

	var sb strings.Builder
	sb.WriteString("permessage-deflate")
	if cfg.clientNoContextTakeover {
		sb.WriteString("; client_no_context_takeover")
	}
	if cfg.serverNoContextTakeover {
		sb.WriteString("; server_no_context_takeover")
	}
	if clientBitsOffered {
		sb.WriteString("; client_max_window_bits=")
		sb.WriteString(strconv.Itoa(cfg.clientWindowBits))
	}
	if serverBitsOffered {
		sb.WriteString("; server_max_window_bits=")
		sb.WriteString(strconv.Itoa(cfg.serverWindowBits))
	}
	return cfg, sb.String(), true
}

// negotiateClientCompression validates the server's extension response and returns the configuration to use. | negotiateClientCompression 校验服务端的扩展响应并返回要使用的配置。
//
// When the client did not offer the extension, any response that activates one is a handshake | 客户端未提议扩展时，响应中启用扩展即为握手错误（RFC 6455 §4.1）；
// error (RFC 6455 §4.1). When it did offer permessage-deflate, an unknown parameter or a malformed | 已提议 permessage-deflate 时，出现未知参数或非法取值同样必须失败：
// value also fails the handshake: the server has already activated compression, so silently ignoring | 服务端已经启用压缩，静默忽略会让两侧对后续帧的理解不一致。
// the parameter would leave the two sides disagreeing about every following frame.
func negotiateClientCompression(h http.Header, offered bool) (*compressionConfig, error) {
	entries := parseExtensionHeader(h.Values(headerSecWebSocketExtensions))

	if !offered {
		if len(entries) > 0 {
			return nil, errx.Wrap(
				errx.Detail(ErrUnrequestedExtension,
					"server sent %[1]q, which the client did not offer | 服务端发送了客户端未提议的 %[1]q",
					strings.Join(h.Values(headerSecWebSocketExtensions), ", ")),
				ErrHandshake)
		}
		return nil, nil
	}
	if len(entries) == 0 {
		// the server declined; the connection stays uncompressed | 服务端拒绝，连接保持不压缩
		return nil, nil
	}
	if len(entries) > 1 {
		return nil, errx.Wrap(
			errx.Detail(ErrUnrequestedExtension,
				"server sent %[1]d extension entries, at most one is allowed | 服务端发送了 %[1]d 个扩展项，最多允许一个", len(entries)),
			ErrHandshake)
	}
	e := entries[0]
	if e.name != "permessage-deflate" || e.invalid {
		return nil, errx.Wrap(
			errx.Detail(ErrUnrequestedExtension,
				"server activated unsupported extension %[1]q | 服务端启用了不支持的扩展 %[1]q", e.name),
			ErrHandshake)
	}

	cfg := newCompressionConfig()
	seen := make(map[string]struct{}, len(e.params))
	for _, p := range e.params {
		if _, dup := seen[p.name]; dup {
			return nil, errx.Detail(ErrHandshake,
				"duplicate permessage-deflate parameter %[1]q | permessage-deflate 参数 %[1]q 重复", p.name)
		}
		seen[p.name] = struct{}{}

		switch p.name {
		case "client_no_context_takeover":
			if p.hasValue {
				return nil, errx.Detail(ErrHandshake,
					"parameter %[1]q must not carry a value | 参数 %[1]q 不得带值", p.name)
			}
			cfg.clientNoContextTakeover = true
		case "server_no_context_takeover":
			if p.hasValue {
				return nil, errx.Detail(ErrHandshake,
					"parameter %[1]q must not carry a value | 参数 %[1]q 不得带值", p.name)
			}
			cfg.serverNoContextTakeover = true
		case "client_max_window_bits":
			if !p.hasValue {
				return nil, errx.Detail(ErrHandshake,
					"parameter %[1]q must carry a value in a response | 响应中的参数 %[1]q 必须带值", p.name)
			}
			n, ok := parseWindowBits(p.value)
			if !ok {
				return nil, errx.Detail(ErrHandshake,
					"invalid %[1]q value %[2]q | %[1]q 取值 %[2]q 非法", p.name, p.value)
			}
			cfg.clientWindowBits = n
		case "server_max_window_bits":
			if !p.hasValue {
				// some servers send this parameter bare even though RFC 7692 requires a value; | 某些服务端即便 RFC 7692 要求带值也会发无值形式；
				// treating it as the default window keeps interoperability without guessing a smaller one | 按默认窗口处理既保持互操作，也不会误猜更小的窗口
				continue
			}
			n, ok := parseWindowBits(p.value)
			if !ok {
				return nil, errx.Detail(ErrHandshake,
					"invalid %[1]q value %[2]q | %[1]q 取值 %[2]q 非法", p.name, p.value)
			}
			cfg.serverWindowBits = n
		default:
			return nil, errx.Detail(ErrHandshake,
				"unsupported permessage-deflate parameter %[1]q | 不支持的 permessage-deflate 参数 %[1]q", p.name)
		}
	}
	return cfg, nil
}
