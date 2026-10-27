# UPX payloads | UPX payload

These binaries are embedded into `yd` and used by `yd docker` (Linux builder) and `yd ko` (host-side
compression). They are pinned build inputs, not application code. | 这些二进制嵌入 `yd`，供 `yd docker`
（Linux 构建器）与 `yd ko`（宿主压缩）使用；它们是固定版本的构建输入，不是应用代码。

| File | Platform | Version | SHA-256 |
|---|---|---|---|
| `upx-linux-amd64` | linux/amd64 | 5.2.1 | `287b3dffe9dcafd8e366e162ac4ab41e5cf45a3c6768970256af0869288d84a1` |
| `upx-linux-arm64` | linux/arm64 | 5.2.1 | `871d65c543cfda923d8502b92a58d4934c434aead8746d7b4a7f6dad66eb3dad` |
| `upx-windows-amd64` | windows/amd64 | 5.2.1 | `d20ebe0b7b22b6be968c8c34be61f94ddea12cb11462e2cec27f548ef9574df8` |

- **Source**: <https://github.com/upx/upx/releases/tag/v5.2.1>
  (`upx-5.2.1-amd64_linux.tar.xz`, `upx-5.2.1-arm64_linux.tar.xz`, `upx-5.2.1-win64.zip`).
- **License**: GNU GPL v2 or later with the UPX special exception; the full texts are next to the payloads
  (`LICENSE`, `COPYING`). The exception covers programs packed by UPX, not redistribution of UPX itself, so
  every yd distribution that contains these files must also ship this notice and the license texts.
  | 许可证：GNU GPL v2 或更高版本，并附带 UPX 特殊例外；完整文本在同目录的 `LICENSE` 与 `COPYING` 中。
  例外只覆盖被 UPX 压缩的程序，不覆盖 UPX 本身的再分发，因此任何包含这些文件的 yd 发行包都必须同时附带
  本说明与许可证文本。
- **No `.exe` suffix**: the Windows payload is deliberately stored as `upx-windows-amd64` because the
  repository `.gitignore` ignores `*.exe`; it is renamed to `upx.exe` when materialized into `.yd/tools`.
  | Windows payload 刻意存为无 `.exe` 后缀的名字，因为仓库 `.gitignore` 忽略 `*.exe`；物化到 `.yd/tools`
  时会改名为 `upx.exe`。
- **macOS and Windows/arm64 hosts**: UPX has no official Darwin release, and its Windows release is
  amd64-only, so there is no embedded host payload for those hosts; `yd ko` uses an external `upx` from
  PATH (for example `brew install upx` on macOS) and skips compression with a warning when none exists.
  `yd docker` still compresses with the embedded Linux payloads, which are architecture-matched to the
  image being built. | macOS 与 Windows/arm64 宿主：UPX 没有官方 Darwin 发行版，其 Windows 发行版也只有
  amd64，因此这些宿主没有嵌入的宿主 payload；`yd ko` 使用 PATH 上的外部 `upx`（macOS 例如
  `brew install upx`），没有时告警并跳过压缩。`yd docker` 仍使用嵌入的 Linux payload（按目标镜像架构选择）
  完成压缩。
- **Extraction cache**: materialization shares the content-addressed cache described in
  `tooling/payload/data/README.md` (`YD_TOOLS_CACHE_DIR` relocates it); the same UPX payload is
  decompressed once per machine. | **解压缓存**：物化共用 `tooling/payload/data/README.md` 描述的内容寻址缓存
  （`YD_TOOLS_CACHE_DIR` 可重定位）；同一 UPX payload 每台机器只解压一次。
- **Updating**: download the pinned release, verify the SHA-256 above, replace the files, update the
  checksums in `tooling/upx/embedded.go` and this table, then run `go test ./tooling/upx/`.
  | 更新：下载固定版本、核对 SHA-256、替换文件、同步 `tooling/upx/embedded.go` 与本表的校验和，然后运行
  `go test ./tooling/upx/`。
