#!/usr/bin/env bash
#
# 发布新版本：打标签 → 建 Release → 把预编译产物挂到发布页面。
#
# 默认**只发布到自建 Gitea**（地址自动从 origin 远端推断）。
# GitHub 是公开镜像，必须显式加 --github 才会触碰，避免误推。
#
# ── 用法 ─────────────────────────────────────────────────────────────────────
#   ./scripts/build.sh all
#   GITEA_TOKEN=xxx ./scripts/release.sh v1.2.3             # 只发 Gitea（默认）
#   GITEA_TOKEN=xxx ./scripts/release.sh --github v1.2.3    # Gitea + GitHub
#
#   ./scripts/release.sh --help
#
# ── 环境变量 ─────────────────────────────────────────────────────────────────
#   GITEA_TOKEN    发布到 Gitea 时必填（仓库写权限）
#   GITEA_URL      可选，默认从 origin 远端推断，例如 https://gitea.example.com
#   GITEA_OWNER    可选，默认从 origin 远端推断
#   GITEA_REPO     可选，默认从 origin 远端推断
#
#   GITHUB_TOKEN   仅在使用 --github 时必填（Contents: Read and write）
#   GITHUB_REPO    可选，默认 LOVE2CMOL/gotify-bark-plugin
#   PUBLISH_GITHUB 置 1 等价于传 --github
#
# ── 关于附件大小 ─────────────────────────────────────────────────────────────
# Gitea 的 Release 附件受 [attachment] MAX_SIZE 限制（默认只有 4 MB），而插件
# 产物约 30 MB，需要服务端先调大：app.ini 里写 [attachment] MAX_SIZE = 64，
# Docker 部署可加环境变量 GITEA__attachment__MAX_SIZE=64，重启后生效。
# GitHub 的附件上限为 2 GB，无需配置。
#
set -euo pipefail

cd "$(dirname "$0")/.."

usage() {
  cat <<'EOF'
用法: release.sh [--github] <版本号>

  --github       同时发布到 GitHub（默认不触碰 GitHub）

示例:
  GITEA_TOKEN=xxx ./scripts/release.sh v1.2.3
  GITEA_TOKEN=xxx GITHUB_TOKEN=yyy ./scripts/release.sh --github v1.2.3
EOF
}

want_github=0
if [ "${PUBLISH_GITHUB:-0}" = "1" ]; then
  want_github=1
fi
VERSION=""
for arg in "$@"; do
  case "$arg" in
    --github) want_github=1 ;;
    -h | --help)
      usage
      exit 0
      ;;
    -*) echo "未知参数：$arg" >&2; usage >&2; exit 1 ;;
    *) VERSION="$arg" ;;
  esac
done

if [ -z "$VERSION" ]; then
  usage >&2
  exit 1
fi

# --- 解析 Gitea 地址（优先环境变量，其次 origin 远端）-------------------------
derive_origin() {
  local url rest host path owner repo scheme
  url="$(git remote get-url origin 2>/dev/null || true)"
  case "$url" in
    http://* | https://*) ;;
    *) return 1 ;;
  esac
  scheme="${url%%://*}"
  rest="${url#*://}"
  rest="${rest#*@}"
  host="${rest%%/*}"
  path="${rest#*/}"
  path="${path%.git}"
  owner="${path%%/*}"
  repo="${path##*/}"
  [ -n "$host" ] && [ -n "$owner" ] && [ -n "$repo" ] || return 1
  printf '%s://%s|%s|%s\n' "$scheme" "$host" "$owner" "$repo"
}

if [ -z "${GITEA_URL:-}" ] || [ -z "${GITEA_OWNER:-}" ] || [ -z "${GITEA_REPO:-}" ]; then
  if derived="$(derive_origin)"; then
    IFS='|' read -r d_url d_owner d_repo <<<"$derived"
    GITEA_URL="${GITEA_URL:-$d_url}"
    GITEA_OWNER="${GITEA_OWNER:-$d_owner}"
    GITEA_REPO="${GITEA_REPO:-$d_repo}"
  fi
fi

GITHUB_REPO="${GITHUB_REPO:-LOVE2CMOL/gotify-bark-plugin}"

want_gitea=0
if [ -n "${GITEA_TOKEN:-}" ] && [ -n "${GITEA_URL:-}" ] && [ -n "${GITEA_OWNER:-}" ]; then
  want_gitea=1
fi

if [ "$want_github" = 1 ] && [ -z "${GITHUB_TOKEN:-}" ]; then
  echo "使用了 --github，但没有设置 GITHUB_TOKEN。" >&2
  exit 1
fi

if [ "$want_gitea" = 0 ] && [ "$want_github" = 0 ]; then
  cat >&2 <<'EOF'
没有可用的发布目标。
  * 发布到自建 Gitea：设置 GITEA_TOKEN（地址默认从 origin 远端推断）
  * 同时发布到 GitHub：再加 --github 并设置 GITHUB_TOKEN
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

echo "==> 发布目标"
if [ "$want_gitea" = 1 ]; then
  echo "    Gitea  : ${GITEA_URL}/${GITEA_OWNER}/${GITEA_REPO}"
fi
if [ "$want_github" = 1 ]; then
  echo "    GitHub : https://github.com/${GITHUB_REPO}（已显式确认）"
else
  echo "    GitHub : 跳过（需要时加 --github）"
fi

echo "==> 产物校验和"
SHA_LINES="$(sha256sum "${ARTIFACTS[@]}")"
printf '%s\n' "$SHA_LINES"

# --- 标签 --------------------------------------------------------------------
echo "==> 标签 $VERSION"
git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null || git tag -a "$VERSION" -m "release $VERSION"

if [ "$want_gitea" = 1 ]; then
  echo "==> 推送到 Gitea（保留原始提交信息）"
  git push -q origin HEAD:refs/heads/main 2>/dev/null ||
    echo "    (main 分支未推送，可能已是最新或存在分歧)" >&2
  git push -q origin "$VERSION"
  echo "    已推送"
fi

if [ "$want_github" = 1 ]; then
  echo "==> 同步代码到 GitHub（提交邮箱改写为 noreply）"
  "$(dirname "$0")/sync-github.sh"
fi

# --- Release 说明（两个平台共用）---------------------------------------------
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

# --- Gitea Release -----------------------------------------------------------
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

# --- GitHub Release（仅在显式 --github 时）-----------------------------------
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

echo
echo "发布完成：$VERSION"
if [ "$want_gitea" = 1 ]; then
  echo "  Gitea  : ${GITEA_URL%/}/${GITEA_OWNER}/${GITEA_REPO}/releases/tag/$VERSION"
fi
if [ "$want_github" = 1 ]; then
  echo "  GitHub : https://github.com/${GITHUB_REPO}/releases/tag/$VERSION"
fi
