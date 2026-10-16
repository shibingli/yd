# Vendored front-end licenses | 前端 vendoring 第三方许可清单

Every file in this directory is a byte-for-byte copy of the upstream release tarball artifact, | 本目录内每个文件都与上游发布产物逐字节一致，
downloaded from jsDelivr and locked by `web/assets.lock.json` (SHA-256). | 下载自 jsDelivr 并由 `web/assets.lock.json` 以 SHA-256 锁定。
No file here may be edited by hand — re-run `yd assets update` instead. | 本目录文件禁止手工修改，请改用 `yd assets update` 重新拉取。

| Library | Version | License (SPDX) | Package | Source URL |
|---|---|---|---|---|
| htmx | 4.0.0 | `0BSD` (Zero-Clause BSD) | `htmx.org` | https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js |
| htmx `hx-sse` extension | 4.0.0 | `0BSD` (Zero-Clause BSD) | `htmx.org` | https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-sse.min.js |
| htmx `hx-ws` extension | 4.0.0 | `0BSD` (Zero-Clause BSD) | `htmx.org` | https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-ws.min.js |
| htmx `hx-preload` extension | 4.0.0 | `0BSD` (Zero-Clause BSD) | `htmx.org` | https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-preload.min.js |
| htmx `hx-targets` extension | 4.0.0 | `0BSD` (Zero-Clause BSD) | `htmx.org` | https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-targets.min.js |
| htmx `hx-upsert` extension | 4.0.0 | `0BSD` (Zero-Clause BSD) | `htmx.org` | https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-upsert.min.js |
| htmx `hx-head` extension | 4.0.0 | `0BSD` (Zero-Clause BSD) | `htmx.org` | https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-head.min.js |
| htmx `hx-pending` extension | 4.0.0 | `0BSD` (Zero-Clause BSD) | `htmx.org` | https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-pending.min.js |
| htmx `hx-multipart` extension | 4.0.0 | `0BSD` (Zero-Clause BSD) | `htmx.org` | https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-multipart.min.js |
| htmx `hx-download` extension | 4.0.0 | `0BSD` (Zero-Clause BSD) | `htmx.org` | https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-download.min.js |
| htmx `hx-alpine-compat` extension | 4.0.0 | `0BSD` (Zero-Clause BSD) | `htmx.org` | https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-alpine-compat.min.js |
| Alpine.js | 3.17.4 | `MIT` | `alpinejs` | https://cdn.jsdelivr.net/npm/alpinejs@3.17.4/dist/cdn.min.js |
| Idiomorph | 0.8.0 | `0BSD` (Zero-Clause BSD) | `idiomorph` | https://cdn.jsdelivr.net/npm/idiomorph@0.8.0/dist/idiomorph.min.js |

## License texts | 许可正文

### Zero-Clause BSD (0BSD) — htmx, htmx extensions, Idiomorph

```text
Zero-Clause BSD
=============

Permission to use, copy, modify, and/or distribute this software for
any purpose with or without fee is hereby granted.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL
WARRANTIES WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES
OF MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE
FOR ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY
DAMAGES WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN
AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT
OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
```

### MIT License — Alpine.js

Copyright (c) 2019-2025 Caleb Porzio and contributors

```text
Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Notes | 说明

- htmx and Idiomorph are recorded as `0BSD` (Zero-Clause BSD) because that is what the
  packaged `LICENSE` file and the npm `license` field of the locked versions declare.
  Earlier htmx 2.x releases were `BSD-2-Clause`; the plan document still carries that older
  value. `0BSD` is strictly more permissive, so the change only loosens obligations.
  | htmx 与 Idiomorph 记为 `0BSD`（Zero-Clause BSD），因为锁定版本随包发布的 `LICENSE`
  文件与 npm `license` 字段声明的就是该协议。htmx 2.x 时代为 `BSD-2-Clause`，实施计划中
  仍保留该旧值。`0BSD` 比 `BSD-2-Clause` 更宽松，变更只减少了义务。
- `hx-status:*`, `hx-validate`, `hx-encoding` and the `innerMorph` / `outerMorph` swap
  strategies are built into htmx 4 itself and therefore have no separate extension file here.
  | `hx-status:*`、`hx-validate`、`hx-encoding` 与 `innerMorph` / `outerMorph` 交换策略均
  为 htmx 4 内建能力，因此本目录没有对应的独立扩展文件。
