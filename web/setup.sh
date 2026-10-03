#!/usr/bin/env bash
# setup.sh —— 拉取构建所需的第三方资产（htmx 运行时库）。
# 版本与 SHA256 锁定；这是 clone 后、go build 前的唯一必做步骤
# （htmx 经 go:embed 编进二进制）。
#
# 仓库自身不提交任何第三方库：现成的东西按需拉取（见 README「前端」）。
set -euo pipefail

cd "$(dirname "$0")/.."

HTMX_VERSION="2.0.6"
HTMX_SHA256="b6768eed4f3af85b73a75054701bd60e17cac718aef2b7f6b254e5e0e2045616"
HTMX_URL="https://unpkg.com/htmx.org@${HTMX_VERSION}/dist/htmx.min.js"
DEST="internal/shared/ui/assets/htmx.min.js"

mkdir -p "$(dirname "$DEST")"

if [[ -f "$DEST" ]] && shasum -a 256 --check --status <<< "${HTMX_SHA256}  ${DEST}"; then
  echo "htmx ${HTMX_VERSION} 已就绪：${DEST}"
  exit 0
fi

echo "拉取 htmx ${HTMX_VERSION} …"
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
curl -fsSL "$HTMX_URL" -o "$TMP"
echo "${HTMX_SHA256}  ${TMP}" | shasum -a 256 --check --status || {
  echo "校验失败：$HTMX_URL" >&2
  exit 1
}
mv "$TMP" "$DEST"
trap - EXIT
echo "htmx ${HTMX_VERSION} → ${DEST}"
