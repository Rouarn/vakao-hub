#!/bin/bash
# 构建 VaKao Hub 启动器 → macOS zip（默认）或 Windows exe（--win32）→ 上传并发布到软件中心
# 用法: ship.sh [--win32] [--no-build] [--dry-run] [--no-publish]
#   --win32     改出 Windows x64 裸 exe；不加则保持原有 macOS zip 行为
#   --no-build  跳过构建，直接用已有产物上传
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
CLI="${HUB_CLI:-/Users/woyuxiaji/Rouarn/AliyunCloud/vakao-hub/tools/hub-cli.mjs}"

# wails.json 的版本号。macOS 用 python3，Windows（Git Bash）没有则回落 node。
read_version() {
  if command -v python3 >/dev/null 2>&1; then
    python3 -c "import json;print(json.load(open('$1'))['info']['productVersion'])"
  else
    node -p "require('$1').info.productVersion"
  fi
}

VERSION=$(read_version "$ROOT/wails.json")
CODE=$(echo "$VERSION" | awk -F. '{printf "%d", $1*100+$2*10+$3}')

PLATFORM="darwin"
BUILD=1
PASSTHRU=()
for arg in "$@"; do
  case "$arg" in
    --win32) PLATFORM="win32" ;;
    --macos) PLATFORM="darwin" ;;
    --no-build) BUILD=0 ;;
    *) PASSTHRU+=("$arg") ;;
  esac
done

if [ "$PLATFORM" = "win32" ]; then
  # Windows 产物为裸 exe（wails 默认不产安装器；需要 NSIS 时另行加 -nsis）。
  EXE="$ROOT/build/bin/VaKaoHub.exe"
  if [ "$BUILD" = 1 ]; then
    if command -v wails >/dev/null 2>&1; then
      ( cd "$ROOT" && wails build -clean -platform windows/amd64 -ldflags "-X vakaohub/launcher/internal/version.Version=$VERSION" )
    else
      ( cd "$ROOT" && "$(go env GOPATH)/bin/wails" build -clean -platform windows/amd64 -ldflags "-X vakaohub/launcher/internal/version.Version=$VERSION" )
    fi
  fi
  [ -f "$EXE" ] || { echo "找不到产物 $EXE" >&2; exit 1; }
  exec node "$CLI" release --app vakaohub --version "$VERSION" --code "$CODE" \
    --file "$EXE" --platform win32 --arch x64 --format exe --min-os 10.0 \
    --notes "VaKao Hub $VERSION" ${PASSTHRU[@]+"${PASSTHRU[@]}"}
fi

ZIP="/tmp/VaKaoHub-${VERSION}.zip"
if [ "$BUILD" = 1 ]; then
  export PATH="$HOME/go/bin:$PATH"
  ( cd "$ROOT" && wails build -clean -ldflags "-X vakaohub/launcher/internal/version.Version=$VERSION" )
fi
COPYFILE_DISABLE=1 ditto --norsrc --noextattr -c -k --keepParent "$ROOT/build/bin/VaKaoHub.app" "$ZIP"
exec node "$CLI" release --app vakaohub --version "$VERSION" --code "$CODE" \
  --file "$ZIP" --platform darwin --arch arm64 --format zip --min-os 12.0 \
  --notes "VaKao Hub $VERSION" ${PASSTHRU[@]+"${PASSTHRU[@]}"}
