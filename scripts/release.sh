#!/usr/bin/env bash
#
# 发布新版本：打标签 → 建 Gitea Release → 把预编译产物挂到发布页面。
#
# ── 用法 ─────────────────────────────────────────────────────────────────────
#   ./scripts/build.sh all                     # 先生成 build/bark-linux-*.so
#   GITEA_TOKEN=xxx ./scripts/release.sh v1.2.2
#
# ── 环境变量 ─────────────────────────────────────────────────────────────────
#   GITEA_TOKEN   必填。Gitea 访问令牌，需有仓库写权限。
#   GITEA_URL     可选，默认 https://gitea.example.com
#   GITEA_OWNER   可选，默认 dsh
#   GITEA_REPO    可选，默认 gotify-bark-plugin
#
# ── 关于附件大小 ─────────────────────────────────────────────────────────────
# Gitea 的 Release 附件受 [attachment] MAX_SIZE 限制（默认 4 MB），而插件产物
# 约 30 MB。服务端没调大该值时上传会返回 HTTP 413 —— 脚本会打印处理办法并以
# 非零状态退出；在调大之前，产物仍可从 generic Package Registry 下载。
#
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  echo "用法: $0 <版本号，如 v1.2.2>" >&2
  exit 1
fi
if [ -z "${GITEA_TOKEN:-}" ]; then
  echo "请先设置 GITEA_TOKEN（Gitea 访问令牌）" >&2
  exit 1
fi

GITEA_URL="${GITEA_URL:-https://gitea.example.com}"
OWNER="${GITEA_OWNER:-dsh}"
REPO="${GITEA_REPO:-gotify-bark-plugin}"
API="$GITEA_URL/api/v1/repos/$OWNER/$REPO"
AUTH="Authorization: token $GITEA_TOKEN"

ARTIFACTS=(build/bark-linux-amd64.so build/bark-linux-arm64.so)
for f in "${ARTIFACTS[@]}"; do
  if [ ! -f "$f" ]; then
    echo "缺少产物 $f —— 请先执行 ./scripts/build.sh all" >&2
    exit 1
  fi
done

# --- 1. 校验和 ---------------------------------------------------------------
echo "==> 产物校验和"
SHA_LINES="$(sha256sum "${ARTIFACTS[@]}")"
printf '%s\n' "$SHA_LINES"

# --- 2. 标签 -----------------------------------------------------------------
echo "==> 标签 $VERSION"
if git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null; then
  echo "    已存在，跳过创建"
else
  git tag -a "$VERSION" -m "release $VERSION"
fi
git push origin "$VERSION"

# --- 3. Release --------------------------------------------------------------
echo "==> Release"
if [ "$(curl -sS -o /dev/null -w '%{http_code}' -H "$AUTH" "$API/releases/tags/$VERSION")" = "200" ]; then
  echo "    已存在，复用"
else
  TMP_BODY="$(mktemp)"
  TMP_JSON="$(mktemp)"
  {
    echo "## 下载"
    echo
    echo '```bash'
    echo "# x86_64 服务器 / NAS / 云主机"
    echo "curl -LO $GITEA_URL/$OWNER/$REPO/releases/download/$VERSION/bark-linux-amd64.so"
    echo
    echo "# ARM64（树莓派 4/5、甲骨文 ARM）"
    echo "curl -LO $GITEA_URL/$OWNER/$REPO/releases/download/$VERSION/bark-linux-arm64.so"
    echo '```'
    echo
    echo "sha256："
    echo
    echo '```'
    printf '%s\n' "$SHA_LINES"
    echo '```'
  } >"$TMP_BODY"

  python3 - "$VERSION" "$TMP_BODY" "$TMP_JSON" <<'PY'
import json, sys
version, body_path, out_path = sys.argv[1], sys.argv[2], sys.argv[3]
body = open(body_path, encoding='utf-8').read()
with open(out_path, 'w', encoding='utf-8') as fh:
    json.dump({"tag_name": version, "name": version, "body": body}, fh, ensure_ascii=False)
PY

  code="$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H "$AUTH" \
    -H 'Content-Type: application/json' --data-binary "@$TMP_JSON" "$API/releases")"
  rm -f "$TMP_BODY" "$TMP_JSON"
  if [ "$code" != "201" ]; then
    echo "    创建失败（HTTP $code）" >&2
    exit 1
  fi
  echo "    已创建"
fi

RID="$(curl -sS -H "$AUTH" "$API/releases/tags/$VERSION" |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"

# --- 4. 附件 -----------------------------------------------------------------
echo "==> 上传附件到发布页面"
failed=0
for f in "${ARTIFACTS[@]}"; do
  name="$(basename "$f")"
  size="$(stat -c%s "$f")"
  resp="$(mktemp)"
  code="$(curl -sS -m 900 -o "$resp" -w '%{http_code}' -X POST -H "$AUTH" \
    -F "attachment=@$f" "$API/releases/$RID/assets?name=$name")"
  if [ "$code" = "201" ]; then
    echo "    ✅ $name（$size 字节）"
  else
    echo "    ⚠️  $name 上传失败（HTTP $code）：$(head -c 200 "$resp")" >&2
    failed=1
  fi
  rm -f "$resp"
done

if [ "$failed" != "0" ]; then
  cat >&2 <<EOF

附件没能全部挂上。最常见的原因是 Gitea 的附件上限：

    [attachment]
    MAX_SIZE = 64      # 单位 MB，默认只有 4
    MAX_FILES = 20

Docker 部署可以直接加环境变量 GITEA__attachment__MAX_SIZE=64，改完重启 Gitea 生效
（反向代理若有 client_max_body_size，也要放行 100m 以上）。

在此之前，产物仍可从包管理页面下载：
    $GITEA_URL/$OWNER/-/packages/generic/$REPO/$VERSION/
EOF
  exit 1
fi

echo
echo "完成：$GITEA_URL/$OWNER/$REPO/releases/tag/$VERSION"
