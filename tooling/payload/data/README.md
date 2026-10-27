# Embedded tool payloads | 嵌入工具 payload

These gzip files are embedded into `yd` and materialized into `.yd/tools` on demand. They are pinned build
inputs, not application code. | 这些 gzip 文件嵌入 `yd`，在需要时物化到 `.yd/tools`；它们是固定版本的构建输入，
不是应用代码。

| Tool | Version | Source | License |
|---|---|---|---|
| `ko` | v0.19.1 | <https://github.com/ko-build/ko/releases/tag/v0.19.1> | Apache-2.0 (`licenses/ko-LICENSE`) |
| `minify` | v2.24.24 | <https://github.com/tdewolff/minify> | MIT (`licenses/minify-LICENSE`) |
| `betteralign` | v0.15.4 | <https://github.com/dkorunic/betteralign> | BSD-3-Clause (`licenses/betteralign-LICENSE`) |

The set holds 18 binaries: three tools for each of `linux/{amd64,arm64}`, `windows/{amd64,arm64}` and
`darwin/{amd64,arm64}`. The SHA-256 of every compressed file is a constant in `payload_<os>_<arch>.go`
and is enforced by `TestHostPayloadsMatchChecksums`; a drifting payload fails the test instead of silently
shipping. | 共 18 个二进制：三种工具 × `linux/{amd64,arm64}`、`windows/{amd64,arm64}` 与
`darwin/{amd64,arm64}` 六个平台。每个压缩文件的 SHA-256 是 `payload_<os>_<arch>.go` 中的常量，由
`TestHostPayloadsMatchChecksums` 强制校验；payload 漂移会让测试失败，而不是悄悄发布。

**Updating**: replace the six `.gz` files for the tool, update the version constants in `payload.go` and the
checksums in the six platform files, refresh the license file, then run `go test ./tooling/payload/`.
| 更新：替换该工具的 6 个 `.gz` 文件，同步 `payload.go` 的版本常量与六个平台文件的校验和，更新许可证文件，
然后运行 `go test ./tooling/payload/`。

**Extraction cache**: materialization goes through a content-addressed cache at
`<user cache dir>/yd/payloads/<sha256>/` (override with `YD_TOOLS_CACHE_DIR`). The same payload is decompressed
once per machine and later projects hard-link the cached file (a copy is used across volumes). A sidecar
records the extracted SHA-256, size and mtime; a damaged entry is re-extracted automatically. Deleting the
cache directory is safe. An existing `.yd/tools/<tool>` always wins; after a `yd` upgrade, delete that file
(or the whole `.yd/tools`) and rerun `yd tools install` to switch to the newly embedded version. | **解压缓存**：
物化经 `<用户缓存目录>/yd/payloads/<sha256>/` 的内容寻址缓存（可用 `YD_TOOLS_CACHE_DIR` 重定位）。同一
payload 每台机器只解压一次，后续项目硬链接缓存文件（跨卷时复制）。sidecar 记录解压后的 SHA-256、大小与
mtime；损坏条目会自动重新解压。删除缓存目录是安全的。已存在的 `.yd/tools/<工具>` 始终优先；`yd` 升级后
删除该文件（或整个 `.yd/tools`）再运行 `yd tools install` 即可切换到新的嵌入版本。
