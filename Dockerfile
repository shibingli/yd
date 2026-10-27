# yd 生产镜像：多阶段构建 + scratch 运行时（约十几 MB）
# 由 `yd docker` 自动调用；也可直接 `docker build -t <name>:<tag> .`

FROM golang:1.27-alpine AS builder

WORKDIR /src

# 依赖层缓存：先只复制模块描述文件
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# 静态资源（Htmx/Tailwind 产物）已由 `yd build` 通过 go:embed 嵌入二进制，
# 此处只需产出静态链接的单文件可执行程序。
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/app ./cmd/app

# UPX 压缩：yd docker 会把嵌入的 Linux UPX 5.2.1 放到 .yd/upx/upx（无需下载）；--no-upx 或
# --build-arg YD_INCLUDE_UPX=false 保留原文件。不经过 yd 的直接 docker build 不受影响。
# UPX: yd docker stages the embedded Linux UPX 5.2.1 binary at .yd/upx/upx (no download); --no-upx or
# --build-arg YD_INCLUDE_UPX=false keeps the original binary. A direct docker build without yd is unaffected.
ARG YD_INCLUDE_UPX=false
RUN if [ "$YD_INCLUDE_UPX" = "true" ] && [ -x /src/.yd/upx/upx ]; then \
        if [ "$(wc -c < /out/app)" -lt 1073741824 ]; then \
            /src/.yd/upx/upx --best --lzma /out/app || echo "yd: UPX failed; keeping the original binary | yd：UPX 失败，保留原文件" >&2; \
        else \
            echo "yd: binary exceeds the UPX size limit | yd：二进制超过 UPX 体积上限" >&2; \
        fi; \
    fi

FROM scratch

# RFC 8441: yd docker passes this build arg when server.http2.extended_connect is true.
# RFC 8441：server.http2.extended_connect 为 true 时由 yd docker 传入该构建参数。
ARG YD_GODEBUG=""
ENV GODEBUG=${YD_GODEBUG}

# 时区与根证书：scratch 镜像不含任何运行时依赖，需要显式复制
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

COPY --from=builder /out/app /app

EXPOSE 8080

ENTRYPOINT ["/app"]
