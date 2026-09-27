#!/usr/bin/env bash
#
# 把当前仓库同步到 GitHub。
#
# 做法：先把仓库复制到临时目录，在副本里把**提交邮箱**统一改写成 GitHub 的
# noreply 形式，再强推 main，并为远端尚未存在的标签补推标签（已存在的同名标签
# 一律跳过，避免 GitHub 把该标签下的 Release 资源一并删掉）。
# 本地仓库和其它远端（例如自建 Gitea）完全不受影响 —— 于是 Gitea 上保留原始
# 邮箱，GitHub 上只有 noreply。
#
# 注意：提交邮箱是提交内容的一部分，所以两边改写后 commit hash 必然不同，
# 这是预期行为，GitHub 只作为公开镜像使用即可。
#
# ── 用法 ─────────────────────────────────────────────────────────────────────
#   GITHUB_TOKEN=xxx ./scripts/sync-github.sh
#
# ── 环境变量 ─────────────────────────────────────────────────────────────────
#   GITHUB_TOKEN   必填（细粒度令牌需 Contents: Read and write）
#   GITHUB_REPO    可选，默认 LOVE2CMOL/gotify-bark-plugin
#   GITHUB_EMAIL   可选，默认 LOVE2CMOL@users.noreply.github.com
#
set -euo pipefail

cd "$(dirname "$0")/.."
SRC="$(pwd)"

if [ -z "${GITHUB_TOKEN:-}" ]; then
  echo "请先设置 GITHUB_TOKEN（GitHub 访问令牌）" >&2
  exit 1
fi

GITHUB_REPO="${GITHUB_REPO:-LOVE2CMOL/gotify-bark-plugin}"
GITHUB_EMAIL="${GITHUB_EMAIL:-LOVE2CMOL@users.noreply.github.com}"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "==> 复制仓库到临时目录（本地仓库不会被改动）"
if ! git clone -q --no-local "$SRC" "$WORK/repo" 2>/dev/null; then
  git clone -q "$SRC" "$WORK/repo"
fi
cd "$WORK/repo"

echo "==> 把提交邮箱改写为 $GITHUB_EMAIL"
FILTER_BRANCH_SQUELCH_WARNING=1 git filter-branch -f --env-filter "
  export GIT_AUTHOR_EMAIL='$GITHUB_EMAIL'
  export GIT_COMMITTER_EMAIL='$GITHUB_EMAIL'
" --tag-name-filter cat -- --all >/dev/null

rm -rf .git/refs/original
git reflog expire --expire=now --all
git gc --prune=now -q

REMOTE="https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_REPO}.git"
echo "==> 推送 main"
git push -q -f "$REMOTE" HEAD:refs/heads/main

# 标签：只推远端还没有的，**绝不**强推已存在的同名标签。
# 原因：GitHub 的 Release 挂在 tag 对象上，一旦用别的对象覆盖同名 tag，
# 该 Release 名下的全部资源会被一并删除（本项目历史上丢过 5 个 Release）。
# 新版本用的都是新版本号，所以「只增不覆盖」不会影响正常发布。
echo "==> 同步标签（只新增，不覆盖）"
remote_tags="$(git ls-remote --tags --refs "$REMOTE" | awk '{print $2}' | sed 's#^refs/tags/##')"
while read -r t; do
  [ -n "$t" ] || continue
  if printf '%s\n' "$remote_tags" | grep -qxF "$t"; then
    echo "  跳过 $t（远端已存在，改动它会让同名 Release 的资源被删除）"
  else
    git push -q "$REMOTE" "refs/tags/$t"
    echo "  新增 $t"
  fi
done < <(git tag)

echo
echo "同步完成：https://github.com/${GITHUB_REPO}"
git log -1 --format='  最新提交：%h  %an <%ae>'
