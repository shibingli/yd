# Vendored icon data | 随包图标数据

This directory holds the exact Iconify JSON used by `yd icons`. It is a build-time input only:
`yd icons sync` reads it and emits the much smaller `web/static/img/icons.svg`; the JSON itself is
never copied into an application binary or served at runtime. | 本目录保存 `yd icons` 使用的确切
Iconify JSON。它只是构建期输入：`yd icons sync` 读取它并生成小得多的
`web/static/img/icons.svg`；JSON 本身绝不会进入应用二进制，也不会在运行时提供。

| Item | Value |
|---|---|
| Icon set | Lucide |
| Iconify package | `@iconify-json/lucide` |
| Version | `1.2.146` |
| License (SPDX) | `ISC` |
| Source URL | https://cdn.jsdelivr.net/npm/@iconify-json/lucide@1.2.146/icons.json |
| SHA-256 | `fb5da55513386fdbbe12d7f265496c7770fa6b7b3c7578edcce587691bfadc57` |
| Icons / aliases | 1966 / 220 |

The version, license and checksum are also constants in `tooling/icons/icons.go`; replacing
`lucide.json` requires updating all three at once. `TestLucideSourceIsLocked` fails on drift. |
版本、许可与校验和同样是 `tooling/icons/icons.go` 中的常量；替换 `lucide.json` 时必须同时更新三者，
`TestLucideSourceIsLocked` 会拦截漂移。

## Update | 更新

```bash
curl -fsSL https://cdn.jsdelivr.net/npm/@iconify-json/lucide@<version>/icons.json \
  -o tooling/icons/data/lucide.json
sha256sum tooling/icons/data/lucide.json
```

Then update the constants and this table, run `yd icons sync`, `yd assets minify`, and the test
suite. | 然后更新常量与本表，运行 `yd icons sync`、`yd assets minify` 与测试套件。

Only icons referenced through `{{template "partials/icon" "…"}}` are emitted into the sprite, so
vendoring the full set does not increase the front-end payload. | 只有经
`{{template "partials/icon" "…"}}` 引用的图标才会进入 sprite，因此随包完整图标集不会增加前端载荷。
