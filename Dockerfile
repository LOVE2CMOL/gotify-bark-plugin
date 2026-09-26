# syntax=docker/dockerfile:1
#
# 用官方 gotify/build 镜像构建插件，保证与 gotify/server 发布版包指纹一致。
#
#   docker build --build-arg GOARCH=amd64 -o build .
#   docker build --build-arg GOARCH=arm64 -o build .
#
# 官方 gotify 发布版的构建环境（见 gotify/server 的 Makefile 与 docker/Dockerfile）：
#     gotify/build:<GO_VERSION>-linux-<arch> 镜像
#     GOROOT=/usr/local/go   GOMODCACHE=/go/pkg/mod
#     go build -mod=readonly -a -installsuffix cgo -ldflags "-w -s ..."
# 本 Dockerfile 复刻了其中的路径条件：容器内 GOMODCACHE 天然就是 /go/pkg/mod，
# 所以不需要额外的 -gcflags 重写，只要不用 -trimpath 即可。
#
# GO_VERSION 取自 https://raw.githubusercontent.com/gotify/server/<tag>/GO_VERSION
ARG GO_VERSION=1.26.0

FROM --platform=linux/amd64 gotify/build:${GO_VERSION}-linux-amd64 AS builder

ARG GOOS=linux
ARG GOARCH=amd64
ARG GOARM=

WORKDIR /src
ENV GOOS=${GOOS} GOARCH=${GOARCH} GOARM=${GOARM} CGO_ENABLED=1

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# 不要加 -trimpath：它会让 runtime/cgo 的导出数据与官方二进制不一致。
RUN go build -buildmode=plugin -ldflags "-s -w" \
      -o /out/bark-${GOOS}-${GOARCH}${GOARM:+-${GOARM}}.so .

FROM scratch AS export
COPY --from=builder /out/ /
