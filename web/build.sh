#!/usr/bin/env bash
# build.sh —— 构建样式：internal/shared/ui/assets/app.css（提交入库的产物）。
#
# 工具链按需拉取、版本与 SHA256 锁定，不入库（与 templ generate 的
# “按需编译器 + 产物提交”同一哲学）：
#   - daisyUI 插件（npm tarball，264KB 压缩）→ web/.cache/daisyui/
#   - Tailwind v4 standalone CLI（免 Node）   → web/.cache/tailwindcss
#
# 仅修改样式时需要本脚本；日常 Go 开发只需 web/setup.sh。
set -euo pipefail

cd "$(dirname "$0")"

DAISYUI_VERSION="5.7.47"
DAISYUI_SHA256="9d66a336c61db879425b02fa0b9a2946efd5c28579307f6b241b4df4ad3b684b"
DAISYUI_URL="https://registry.npmjs.org/daisyui/-/daisyui-${DAISYUI_VERSION}.tgz"

TAILWIND_VERSION="v4.3.3"
# Tailwind standalone CLI 的官方校验和（github releases/sha256sums.txt）。
declare -A TAILWIND_SHA256=(
  [macos-arm64]="cdf646702987a743464dff4d9c60fd4480d1c1e73dd819a9a67f1078815dce9d"
  [macos-x64]="7922e0953f2110c05976e3bf58f14e643d90427575e766b7d433f5f80cbee7e1"
  [linux-arm64]="55fd0b241214eff3de1e8ee4f22796662f2d2e7a49bcfca7477cfd0bac398195"
  [linux-x64]="dc61b3ac6b8c9ca874c0cc4c57b2409791a64c5540404ca5f5367360babc313a"
)

CACHE=".cache"
mkdir -p "$CACHE"

# ---- 平台探测 ----
OS="$(uname -s)"
ARCH="$(uname -m)"
case "${OS}-${ARCH}" in
  Darwin-arm64)  PLATFORM="macos-arm64" ;;
  Darwin-x86_64) PLATFORM="macos-x64" ;;
  Linux-aarch64) PLATFORM="linux-arm64" ;;
  Linux-x86_64)  PLATFORM="linux-x64" ;;
  *) echo "不支持的平台：${OS}-${ARCH}（自行下载 tailwindcss ${TAILWIND_VERSION} 放到 ${CACHE}/tailwindcss）" >&2; exit 1 ;;
esac

# ---- 1. htmx（embed 依赖）----
./setup.sh

# ---- 2. daisyUI 插件 ----
if [[ ! -f "$CACHE/daisyui/index.js" ]]; then
  echo "拉取 daisyUI ${DAISYUI_VERSION} …"
  TARBALL="$CACHE/daisyui.tgz"
  curl -fsSL "$DAISYUI_URL" -o "$TARBALL"
  echo "${DAISYUI_SHA256}  ${TARBALL}" | shasum -a 256 --check --status || {
    echo "校验失败：$DAISYUI_URL" >&2; exit 1
  }
  rm -rf "$CACHE/daisyui" && mkdir -p "$CACHE/daisyui"
  tar -xzf "$TARBALL" -C "$CACHE/daisyui" --strip-components=1
  rm -f "$TARBALL"
  echo "daisyUI ${DAISYUI_VERSION} → $CACHE/daisyui/"
fi

# ---- 3. Tailwind standalone CLI ----
if [[ ! -x "$CACHE/tailwindcss" ]]; then
  echo "拉取 tailwindcss ${TAILWIND_VERSION}（${PLATFORM}）…"
  BIN="$CACHE/tailwindcss.download"
  curl -fsSL "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-${PLATFORM}" -o "$BIN"
  echo "${TAILWIND_SHA256[$PLATFORM]}  ${BIN}" | shasum -a 256 --check --status || {
    echo "校验失败：tailwindcss-${PLATFORM}" >&2; exit 1
  }
  chmod +x "$BIN" && mv "$BIN" "$CACHE/tailwindcss"
  echo "tailwindcss ${TAILWIND_VERSION} → $CACHE/tailwindcss"
fi

# ---- 4. 构建（产物提交入库）----
./"$CACHE/tailwindcss" -i app.css -o ../internal/shared/ui/assets/app.css --minify
echo "已构建 internal/shared/ui/assets/app.css"
