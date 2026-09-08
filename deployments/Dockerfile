# Openspace OS Core 多阶段构建 Dockerfile
# 构建阶段
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/openspace-os-core ./cmd/openspace-os-core
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/openspace-os-cli ./cmd/openspace-os-cli

# 运行阶段
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/openspace-os-core /app/openspace-os-core
COPY --from=builder /out/openspace-os-cli /usr/local/bin/openspace-os-cli
COPY config.yaml /app/config.yaml
RUN mkdir -p /app/data /app/plugins
EXPOSE 8080 9090 7000
ENTRYPOINT ["/app/openspace-os-core"]
CMD ["run", "--config", "/app/config.yaml"]
