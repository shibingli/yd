// This file parses the image references out of ko's output. | 本文件从 ko 的输出中解析镜像引用。

package ko

import "strings"

// digestPrefix is the only digest algorithm ko publishes with. | digestPrefix 是 ko 发布时使用的唯一摘要算法前缀。
const digestPrefix = "sha256:"

// digestHexLen is the length of a sha256 digest in hex digits. | digestHexLen 是 sha256 摘要的十六进制位数。
const digestHexLen = 64

// parseImages returns every distinct image reference ko printed, in order. | parseImages 按顺序返回 ko 打印的全部不重复镜像引用。
//
// ko writes one reference per image to stdout while its progress log goes to stderr; because the
// default runner merges both streams, a line counts as a reference only when it cannot be a log
// line: it must be a single token, must not be a URL, and must either carry a valid sha256 digest or
// have both a repository slash and a tag. That shape covers every form ko prints - a digest, a tag
// and tag@digest - while a progress message such as "Using base
// cgr.dev/chainguard/static:latest@sha256:..." is rejected by its own whitespace. | ko
// 在 stdout 上按镜像逐行写引用，进度日志走 stderr；由于默认执行器会合并两路输出，只有不可能是日志行的行才算引用：
// 它必须是单个词、不是 URL，且要么带合法的 sha256 摘要，要么同时具备仓库斜杠与标签。这一形态覆盖 ko 打印的全部形式
// ——摘要、标签与 tag@digest——而 "Using base cgr.dev/chainguard/static:latest@sha256:..." 这类进度消息
// 会因自身包含空白而被排除。
func parseImages(logs []string) []string {
	var images []string
	seen := make(map[string]struct{}, len(logs))
	for _, line := range logs {
		if !isImageReference(line) {
			continue
		}
		if _, dup := seen[line]; dup {
			continue
		}
		seen[line] = struct{}{}
		images = append(images, line)
	}
	return images
}

// digestOf returns the sha256 digest of an image reference, or "" when it carries none. | digestOf
// 返回镜像引用的 sha256 摘要，未携带时返回 ""。
func digestOf(reference string) string {
	idx := strings.Index(reference, "@")
	if idx < 0 {
		return ""
	}
	digest := reference[idx+1:]
	if !validDigest(digest) {
		return ""
	}
	return digest
}

// isImageReference reports whether a line has the shape of one image reference. | isImageReference
// 判断一行是否具备单个镜像引用的形态。
func isImageReference(line string) bool {
	if line == "" || strings.ContainsAny(line, " \t") || strings.Contains(line, "://") {
		return false
	}
	if idx := strings.Index(line, "@"); idx >= 0 {
		return line[:idx] != "" && validDigest(line[idx+1:])
	}
	// Without a digest it | 无摘要时必须同时具备仓库斜杠与标签，例如 registry.example.com/team/app:v1。
	// needs both a repository slash and a tag, e.g. registry.example.com/team/app:v1.
	if !strings.Contains(line, "/") {
		return false
	}
	colon := strings.LastIndex(line, ":")
	return colon > 0 && colon < len(line)-1 && !strings.Contains(line[colon+1:], "/")
}

// validDigest reports whether s is exactly "sha256:" plus 64 lowercase hex digits. | validDigest
// 判断 s 是否恰好是 "sha256:" 加 64 位小写十六进制数字。
func validDigest(s string) bool {
	hex, ok := strings.CutPrefix(s, digestPrefix)
	if !ok || len(hex) != digestHexLen {
		return false
	}
	for i := 0; i < len(hex); i++ {
		c := hex[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
