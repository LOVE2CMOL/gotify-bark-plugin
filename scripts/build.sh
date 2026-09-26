#!/usr/bin/env bash
#
# 构建 Bark Forwarder 插件（linux/amd64、linux/arm64）。
#
# ── 为什么不能直接 `go build -buildmode=plugin`？──────────────────────────────
# Go 的 plugin 机制在加载时会逐个包比对「包指纹」（编译器导出数据的 sha256）。
# 官方 gotify 发布版是在 gotify/build 镜像里编译的，环境固定为：
#     GOROOT=/usr/local/go     GOMODCACHE=/go/pkg/mod
# 而 -buildmode=plugin 会强制所有包加 -dynlink，此时编译器会把每个包的
# **源码目录绝对路径**写进导出数据。只要我们的 GOROOT / GOMODCACHE 与官方
# 不同，指纹就对不上，加载时会报：
#     plugin was built with a different version of package github.com/gorilla/websocket
#
# 解决办法：用 -gcflags 把本机的临时路径重写成官方路径，使导出数据完全一致：
#   * 所有包    ：$GOMODCACHE  =>  /go/pkg/mod
#   * runtime/cgo：它是 GOROOT 内的包，编译器本来就隐藏 GOROOT 真实路径，
#                  额外加 -trimpath 反而会破坏它，所以单独用空参数覆盖掉。
# 另外不要使用 -trimpath：它会让 runtime/cgo 的导出数据再次变化。
#
# 注意：这样构建出来的 .so **只兼容官方发布版二进制**。如果你用的是自己从源码
# 编译的 gotify，请设置 MATCH_LOCAL=1（不做路径重写），并用同一套 Go 工具链、
# 同一个 GOROOT/GOMODCACHE 路径编译 gotify 本体。
#
# ── 用法 ─────────────────────────────────────────────────────────────────────
#   ./scripts/build.sh                 # 构建 amd64（有交叉编译器时顺带 arm64）
#   ./scripts/build.sh amd64           # 只构建 amd64
#   ./scripts/build.sh arm64           # 只构建 arm64
#   MATCH_LOCAL=1 ./scripts/build.sh   # 配合自编译的 gotify 使用
#
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
OUT_DIR="${OUT_DIR:-$ROOT/build}"
mkdir -p "$OUT_DIR"

# --- Go 工具链 ---------------------------------------------------------------
GO_BIN="${GO_BIN:-go}"
if ! command -v "$GO_BIN" >/dev/null 2>&1; then
  echo "找不到 go 命令，请先安装 Go（gotify v3.1.1 使用 go1.26.0）。" >&2
  exit 1
fi

# gotify v3.1.1 由 go1.26.0 编译；Go 版本不同，包指纹必然对不上。
WANT_GO="${WANT_GO:-go1.26.0}"
HAVE_GO="$("$GO_BIN" env GOVERSION 2>/dev/null || echo unknown)"
if [ "$HAVE_GO" != "$WANT_GO" ]; then
  echo "警告：当前工具链是 $HAVE_GO，官方 gotify v3.1.1 用的是 $WANT_GO。"
  echo "      版本不一致时插件将无法加载。可尝试 GOTOOLCHAIN=$WANT_GO 重新执行。"
fi

# --- 包指纹对齐 --------------------------------------------------------------
OFFICIAL_GOMODCACHE="/go/pkg/mod"
REAL_GOMODCACHE="$("$GO_BIN" env GOMODCACHE)"

if [ "${MATCH_LOCAL:-0}" = "1" ]; then
  echo "==> MATCH_LOCAL=1：不做路径重写（用于自己编译的 gotify）"
  GCFLAGS=()
else
  echo "==> 对齐官方发布版：GOMODCACHE $REAL_GOMODCACHE => $OFFICIAL_GOMODCACHE"
  GCFLAGS=(
    -gcflags="all=-trimpath=$REAL_GOMODCACHE=>$OFFICIAL_GOMODCACHE"
    -gcflags="runtime/cgo="
  )
fi

# --- 构建 --------------------------------------------------------------------
build_amd64() {
  echo "==> linux/amd64"
  CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
    "$GO_BIN" build -buildmode=plugin -ldflags="-s -w" ${GCFLAGS+"${GCFLAGS[@]}"} \
    -o "$OUT_DIR/bark-linux-amd64.so" .
  ls -lh "$OUT_DIR/bark-linux-amd64.so"
}

build_arm64() {
  local cc="${CC_ARM64:-aarch64-linux-gnu-gcc}"
  if ! command -v "$cc" >/dev/null 2>&1; then
    echo "跳过 arm64：找不到交叉编译器 $cc"
    echo "  Debian/Ubuntu: sudo apt install gcc-aarch64-linux-gnu"
    echo "  或改用官方镜像构建：docker build --build-arg GOARCH=arm64 -o build ."
    return 0
  fi
  echo "==> linux/arm64（CC=$cc）"
  CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC="$cc" \
    CGO_LDFLAGS="${CGO_LDFLAGS_ARM64:---sysroot=/usr/aarch64-linux-gnu}" \
    "$GO_BIN" build -buildmode=plugin -ldflags="-s -w" ${GCFLAGS+"${GCFLAGS[@]}"} \
    -o "$OUT_DIR/bark-linux-arm64.so" .
  ls -lh "$OUT_DIR/bark-linux-arm64.so"
}

target="${1:-all}"
case "$target" in
  amd64) build_amd64 ;;
  arm64) build_arm64 ;;
  all)   build_amd64; build_arm64 ;;
  *)     echo "未知目标：$target（可选 amd64 / arm64 / all）" >&2; exit 1 ;;
esac

cat <<'EOF'

构建完成。安装（把对应架构的 .so 放进 gotify 的插件目录并重启）：

  # Docker
  docker cp build/bark-linux-amd64.so gotify:/app/data/plugins/bark.so
  docker restart gotify

  # 原生安装（GOTIFY_PLUGINSDIR 默认 ./data/plugins）
  cp build/bark-linux-amd64.so "$GOTIFY_PLUGINSDIR/bark.so"

然后打开 gotify WebUI 的 Plugins 页面填写配置。详见 README.md。
EOF
