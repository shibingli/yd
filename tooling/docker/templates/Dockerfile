# yd production image: multi-stage build with a scratch runtime stage. | yd 生产镜像：多阶段构建 + scratch 运行阶段。
# Invoked by `yd docker`; a project Dockerfile at the repository root always wins over this template. | 由 `yd docker` 调用；仓库根目录若存在项目 Dockerfile，则优先于本模板。

# Stage 1: build a fully static, dependency-free single binary. | 阶段一：编译出完全静态、无依赖的单文件可执行程序。
FROM golang:{{GO_MAJOR_MINOR}}-alpine AS builder

WORKDIR /src

# Dependency layer cache: copy only the module descriptors first. | 依赖层缓存：先只复制模块描述文件。
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The static assets (Htmx/Tailwind output) are already embedded into the binary by
# `yd build` through go:embed, so this stage only has to emit the executable. | 静态资产（Htmx/Tailwind 产物）已由
# `yd build` 通过 go:embed 嵌入二进制，此处只需产出可执行文件。
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/app ./cmd/app

# Stage 2: scratch carries no runtime dependency at all, which is why the CA
# certificates and the time zone database have to be copied explicitly. | 阶段二：scratch 不含任何运行时依赖，
# 因此必须显式复制根证书与时区库。
FROM scratch

# RFC 8441: yd docker passes this build arg when server.http2.extended_connect is true.
# RFC 8441：server.http2.extended_connect 为 true 时由 yd docker 传入该构建参数。
ARG YD_GODEBUG=""
ENV GODEBUG=${YD_GODEBUG}

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

COPY --from=builder /out/app /app

EXPOSE 8080

ENTRYPOINT ["/app"]
