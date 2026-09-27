#!/usr/bin/env bash
#
# 发布新版本：打标签 → 建 Release → 把预编译产物挂到发布页面。
# GitHub 为主，自建 Gitea 镜像可选，两边可以一次同时发布。
#
# ── 用法 ─────────────────────────────────────────────────────────────────────
#   ./scripts/build.sh all
#   GITHUB_TOKEN=xxx ./scripts/release.sh v1.2.3
#
# ── 环境变量 ─────────────────────────────────────────────────────────────────
#   GITHUB_TOKEN   发布到 GitHub 时必填（细粒度令牌需 Contents: Read and write）
#   GITHUB_REPO    可选，默认 LOVE2CMOL/gotify-bark-plugin
#
#   GITEA_URL      自建 Gitea 的根地址，例如 https://gitea.example.com
#   GITEA_TOKEN    自建 Gitea 的访问令牌；与 GITEA_URL 同时给出才会启用
#   GITEA_OWNER    可选，默认 dsh
#   GITEA_REPO     可选，默认 gotify-bark-plugin
#
# ── 关于附件大小 ─────────────────────────────────────────────────────────────
# GitHub 的 Release 附件上限为 2 GB，无需任何配置。
# Gitea 的 Release 附件受 [attachment] MAX_SIZE 限制（默认只有 4 MB），而插件
# 产物约 30 MB，需要服务端先调大：app.ini 里写 [attachment] MAX_SIZE = 64，
# Docker 部署可加环境变量 GITEA__attachment__MAX_SIZE=64，重启后生效。
# 未调大时该平台会上传失败，脚本打印提示后继续处理另一个平台。
#
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  echo "用法: $0 <版本号，如 v1.2.3>" >&2
  exit 1
fi

GITHUB_REPO="${GITHUB_REPO:-LOVE2CMOL/gotify-bark-plugin}"
GITEA_OWNER="${GITEA_OWNER:-dsh}"
GITEA_REPO="${GITEA_REPO:-gotify-bark-plugin}"

want_github=0
[ -n "${GITHUB_TOKEN:-}" ] && want_github=1
want_gitea=0
if [ -n "${GITEA_URL:-}" ] && [ -n "${GITEA_TOKEN:-}" ]; then
  want_gitea=1
fi

if [ "$want_github" = 0 ] && [ "$want_gitea" = 0 ]; then
  cat >&2 <<'EOF'
没有可用的发布目标。
  * 发布到 GitHub：设置 GITHUB_TOKEN
  * 同时发布到自建 Gitea：再设置 GITEA_URL 与 GITEA_TOKEN
EOF
  exit 1
fi

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

# --- 2. 标签与推送 -----------------------------------------------------------
echo "==> 标签 $VERSION"
git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null || git tag -a "$VERSION" -m "release $VERSION"

push_to() {
  local url="$1" label="$2"
  git push -q "$url" HEAD:refs/heads/main 2>/dev/null ||
    echo "    ($label 的 main 分支未推送，可能已是最新或存在分歧)" >&2
  git push -q "$url" "$VERSION"
  echo "    $label 已推送"
}

if [ "$want_github" = 1 ]; then
  push_to "https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_REPO}.git" GitHub
fi
if [ "$want_gitea" = 1 ]; then
  gitea_host="${GITEA_URL#https://}"
  gitea_host="${gitea_host#http://}"
  gitea_host="${gitea_host%/}"
  push_to "https://oauth2:${GITEA_TOKEN}@${gitea_host}/${GITEA_OWNER}/${GITEA_REPO}.git" Gitea
fi

# --- 3. Release 说明（供两个平台复用）----------------------------------------
BODY_FILE="$(mktemp)"
JSON_FILE="$(mktemp)"
trap 'rm -f "$BODY_FILE" "$JSON_FILE"' EXIT

{
  echo "## 下载"
  echo
  echo "见本页下方 **Assets**：\`bark-linux-amd64.so\`（x86_64）与 \`bark-linux-arm64.so\`（ARM64）。"
  echo
  echo "sha256："
  echo
  echo '```'
  printf '%s\n' "$SHA_LINES"
  echo '```'
} >"$BODY_FILE"

python3 - "$VERSION" "$BODY_FILE" "$JSON_FILE" <<'PY'
import json, sys
version, body_path, out_path = sys.argv[1], sys.argv[2], sys.argv[3]
body = open(body_path, encoding='utf-8').read()
with open(out_path, 'w', encoding='utf-8') as fh:
    json.dump(
        {"tag_name": version, "name": version, "body": body, "draft": False, "prerelease": False},
        fh, ensure_ascii=False,
    )
PY

# --- 4. GitHub ---------------------------------------------------------------
if [ "$want_github" = 1 ]; then
  echo "==> GitHub Release"
  api="https://api.github.com/repos/${GITHUB_REPO}"
  rid="$(curl -sS -H "Authorization: token $GITHUB_TOKEN" "$api/releases/tags/$VERSION" |
    python3 -c 'import json,sys
try: print(json.load(sys.stdin).get("id",""))
except Exception: print("")')"
  if [ -z "$rid" ]; then
    rid="$(curl -sS -X POST -H "Authorization: token $GITHUB_TOKEN" \
      -H 'Accept: application/vnd.github+json' -H 'Content-Type: application/json' \
      --data-binary "@$JSON_FILE" "$api/releases" |
      python3 -c 'import json,sys;print(json.load(sys.stdin).get("id",""))')"
    if [ -n "$rid" ]; then
      echo "    已创建 (id=$rid)"
    else
      echo "    ⚠️  创建失败" >&2
    fi
  else
    echo "    已存在，复用 (id=$rid)"
  fi
  if [ -n "$rid" ]; then
    for f in "${ARTIFACTS[@]}"; do
      name="$(basename "$f")"
      code="$(curl -sS -m 900 -o /dev/null -w '%{http_code}' -X POST \
        -H "Authorization: token $GITHUB_TOKEN" \
        -H 'Content-Type: application/octet-stream' --data-binary "@$f" \
        "https://uploads.github.com/repos/${GITHUB_REPO}/releases/$rid/assets?name=$name")"
      case "$code" in
        200 | 201) echo "    ✅ $name" ;;
        422) echo "    = $name 已存在，跳过" ;;
        *) echo "    ⚠️  $name 上传失败（HTTP $code）" >&2 ;;
      esac
    done
  fi
fi

# --- 5. Gitea（可选）---------------------------------------------------------
if [ "$want_gitea" = 1 ]; then
  echo "==> Gitea Release"
  base="${GITEA_URL%/}/api/v1/repos/${GITEA_OWNER}/${GITEA_REPO}"
  rid="$(curl -sS -H "Authorization: token $GITEA_TOKEN" "$base/releases/tags/$VERSION" |
    python3 -c 'import json,sys
try: print(json.load(sys.stdin).get("id",""))
except Exception: print("")')"
  if [ -z "$rid" ]; then
    rid="$(curl -sS -X POST -H "Authorization: token $GITEA_TOKEN" \
      -H 'Content-Type: application/json' --data-binary "@$JSON_FILE" "$base/releases" |
      python3 -c 'import json,sys;print(json.load(sys.stdin).get("id",""))')"
    if [ -n "$rid" ]; then
      echo "    已创建 (id=$rid)"
    else
      echo "    ⚠️  创建失败" >&2
    fi
  else
    echo "    已存在，复用 (id=$rid)"
  fi
  if [ -n "$rid" ]; then
    for f in "${ARTIFACTS[@]}"; do
      name="$(basename "$f")"
      resp="$(mktemp)"
      code="$(curl -sS -m 900 -o "$resp" -w '%{http_code}' -X POST -H "Authorization: token $GITEA_TOKEN" \
        -F "attachment=@$f" "$base/releases/$rid/assets?name=$name")"
      case "$code" in
        200 | 201) echo "    ✅ $name" ;;
        *) echo "    ⚠️  $name 上传失败（HTTP $code）：$(head -c 160 "$resp")" >&2 ;;
      esac
      rm -f "$resp"
    done

    echo "==> 同步到 Gitea 包管理页面（免登录下载）"
    for f in "${ARTIFACTS[@]}"; do
      name="$(basename "$f")"
      code="$(curl -sS -m 900 -o /dev/null -w '%{http_code}' -X PUT -H "Authorization: token $GITEA_TOKEN" \
        -H 'Content-Type: application/octet-stream' --data-binary "@$f" \
        "${GITEA_URL%/}/api/packages/${GITEA_OWNER}/generic/${GITEA_REPO}/$VERSION/$name")"
      case "$code" in
        200 | 201) echo "    ✅ $name" ;;
        *) echo "    ⚠️  $name 上传失败（HTTP $code）" >&2 ;;
      esac
    done
  fi
fi

echo
echo "发布完成：$VERSION"
if [ "$want_github" = 1 ]; then
  echo "  GitHub : https://github.com/${GITHUB_REPO}/releases/tag/$VERSION"
fi
if [ "$want_gitea" = 1 ]; then
  echo "  Gitea  : ${GITEA_URL%/}/${GITEA_OWNER}/${GITEA_REPO}/releases/tag/$VERSION"
fi
