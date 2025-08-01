# ========= 构建阶段 =========
FROM golang:1.23 AS builder

# 启用 Go Modules
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

# 复制源码
COPY . .

# 构建二进制文件
RUN go build -o excel-replacer main.go

# ========= 运行阶段 =========
FROM alpine:3.19

# 设置工作目录
WORKDIR /app

# 拷贝编译好的二进制和静态文件
COPY --from=builder /app/excel-replacer /app/excel-replacer
COPY static ./static

# 暴露端口
EXPOSE 8080

# 启动服务
CMD ["./excel-replacer"]
