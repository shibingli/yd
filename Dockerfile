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

FROM scratch

# 时区与根证书：scratch 镜像不含任何运行时依赖，需要显式复制
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

COPY --from=builder /out/app /app

EXPOSE 8080

ENTRYPOINT ["/app"]
