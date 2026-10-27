# Third-party notices | 第三方声明

`yd` itself is licensed under Apache-2.0 (see `LICENSE`). The following third-party binaries are
embedded into the `yd` executable and distributed under their own licenses. | `yd` 本体使用 Apache-2.0
（见 `LICENSE`）。下列第三方二进制被嵌入 `yd` 可执行文件，按其各自的许可证分发。

## Embedded tool payloads | 嵌入工具 payload

The following pinned tools are embedded into the `yd` executable as gzip payloads and materialized into
`.yd/tools` on demand. | 下列固定版本工具以 gzip payload 形式嵌入 `yd`，在需要时物化到 `.yd/tools`。

| Tool | Version | License | License text | Source |
|---|---|---|---|---|
| `ko` | v0.19.1 | Apache-2.0 | `tooling/payload/data/licenses/ko-LICENSE` | <https://github.com/ko-build/ko/releases/tag/v0.19.1> |
| `minify` | v2.24.24 | MIT | `tooling/payload/data/licenses/minify-LICENSE` | <https://github.com/tdewolff/minify> |
| `betteralign` | v0.15.4 | BSD-3-Clause | `tooling/payload/data/licenses/betteralign-LICENSE` | <https://github.com/dkorunic/betteralign> |

Every compressed payload has a SHA-256 constant in `tooling/payload/payload_<os>_<arch>.go`, enforced by
`TestHostPayloadsMatchChecksums`; see `tooling/payload/data/README.md` for the update procedure.
| 每个压缩 payload 的 SHA-256 常量位于 `tooling/payload/payload_<os>_<arch>.go`，由
`TestHostPayloadsMatchChecksums` 强制校验；更新流程见 `tooling/payload/data/README.md`。

## UPX 5.2.1

- Files: `tooling/upx/data/upx-linux-amd64`, `tooling/upx/data/upx-linux-arm64`,
  `tooling/upx/data/upx-windows-amd64`.
- Copyright: the UPX authors; see `tooling/upx/data/LICENSE` and `tooling/upx/data/COPYING`.
- License: GNU General Public License version 2 or later, with the UPX special exception for compressed
  executables. The exception allows UPX to compress programs under any license; it does not relicense
  the UPX binaries themselves. `yd` redistributes these binaries unmodified; their complete
  corresponding source is the upstream release named below. Redistributions that include these files
  must keep this notice together with the GPL text and preserve access to that source. | 许可证：
  GNU GPL v2 或更高版本，并附带 UPX 对「被压缩可执行程序」的特殊例外。该例外允许 UPX 压缩任意许可证的程序，
  但不改变 UPX 二进制自身的许可；`yd` 再分发的是未修改的二进制，其完整对应源码即下列上游发行版。包含这些
  文件的再分发必须保留本声明与 GPL 文本，并保持该源码的可获取性。
- Source: <https://github.com/upx/upx/releases/tag/v5.2.1>
- SHA-256:
  - `upx-linux-amd64`: `287b3dffe9dcafd8e366e162ac4ab41e5cf45a3c6768970256af0869288d84a1`
  - `upx-linux-arm64`: `871d65c543cfda923d8502b92a58d4934c434aead8746d7b4a7f6dad66eb3dad`
  - `upx-windows-amd64`: `d20ebe0b7b22b6be968c8c34be61f94ddea12cb11462e2cec27f548ef9574df8`
