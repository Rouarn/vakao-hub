#!/bin/bash
# 构建 VaKao Hub 启动器 → macOS zip（默认）或 Windows exe（--win32）→ 上传并发布到软件中心
# 用法: ship.sh [--win32] [--no-build] [--dry-run] [--no-publish]
#   --win32       改出 Windows x64 裸 exe；不加则保持原有 macOS zip 行为
#   --no-build    跳过构建，直接用已有产物上传
#   --dry-run     构建并执行全部门禁检查，只打印上传命令、不真正上传
#
# 版本唯一来源是 packages/launcher/build/config.yml 的 info.version，
# 经 scripts/version.mjs 读取；构建前先同步 build 资产，再注入 APP_VERSION。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$ROOT/../.." && pwd)"
CLI="${HUB_CLI:-$REPO/tools/hub-cli.mjs}"
LAUNCHER="$ROOT"
export PATH="$HOME/go/bin:$PATH"

command -v node >/dev/null 2>&1 || { echo "找不到 node" >&2; exit 1; }
command -v wails3 >/dev/null 2>&1 || { echo "找不到 wails3（go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0）" >&2; exit 1; }

VERSION=$(node "$REPO/scripts/version.mjs")
CODE=$(echo "$VERSION" | awk -F. '{printf "%d", $1*100+$2*10+$3}')

PLATFORM="darwin"
BUILD=1
DRY_RUN=0
PASSTHRU=()
for arg in "$@"; do
  case "$arg" in
    --win32) PLATFORM="win32" ;;
    --macos) PLATFORM="darwin" ;;
    --no-build) BUILD=0 ;;
    --dry-run) DRY_RUN=1 ;;
    *) PASSTHRU+=("$arg") ;;
  esac
done

# 发布门禁 8：所有 workflow 只允许 workflow_dispatch。
node "$REPO/scripts/check-workflows.mjs"

assert_equal() {
  local label="$1" expected="$2" actual="$3"
  if [ "$expected" != "$actual" ]; then
    echo "门禁失败：$label = '$actual'，期望 '$expected'" >&2
    exit 1
  fi
}

# build/config.yml → Info.plist / info.json；必须在注入 APP_VERSION 之前完成。
sync_build_assets() {
  ( cd "$LAUNCHER" && wails3 task common:update:build-assets )
}

check_archive() {
  local archive="$1"
  python3 - "$archive" <<'PY'
import sys, zipfile
bad = []
with zipfile.ZipFile(sys.argv[1]) as zf:
    for name in zf.namelist():
        base = name.rsplit('/', 1)[-1]
        if base == '.DS_Store' or base.startswith('._'):
            bad.append(name)
if bad:
    print("归档包含 AppleDouble/DS_Store: " + ", ".join(bad), file=sys.stderr)
    sys.exit(1)
print("归档成员检查通过")
PY
}

if [ "$PLATFORM" = "win32" ]; then
  EXE="$LAUNCHER/bin/VaKaoHub.exe"
  if [ "$BUILD" = 1 ]; then
    sync_build_assets
    ( cd "$LAUNCHER" && wails3 task windows:build APP_VERSION="$VERSION" ARCH=amd64 )
  fi
  [ -f "$EXE" ] || { echo "找不到产物 $EXE" >&2; exit 1; }
  assert_equal "windows/info.json 版本" "$VERSION" \
    "$(node -p "require('$LAUNCHER/build/windows/info.json').info['0409'].ProductVersion")"
  case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
      assert_equal "--version" "$VERSION" "$("$EXE" --version)"
      ;;
    *)
      echo "当前主机无法运行 Windows 产物，跳过 --version 断言（请在上传前于 Windows 复核）"
      ;;
  esac

  UPLOAD=(node "$CLI" release --app vakaohub --version "$VERSION" --code "$CODE" \
    --file "$EXE" --platform win32 --arch x64 --format exe --min-os 10.0 \
    --notes "VaKao Hub $VERSION" ${PASSTHRU[@]+"${PASSTHRU[@]}"})
else
  APP="$LAUNCHER/bin/VaKaoHub.app"
  BIN="$APP/Contents/MacOS/VaKaoHub"
  ZIP="/tmp/VaKaoHub-${VERSION}.zip"
  if [ "$BUILD" = 1 ]; then
    sync_build_assets
    ( cd "$LAUNCHER" && wails3 task darwin:package APP_VERSION="$VERSION" )
  fi
  [ -d "$APP" ] || { echo "找不到产物 $APP" >&2; exit 1; }

  assert_equal "--version" "$VERSION" "$("$BIN" --version)"
  assert_equal "Info.plist 版本" "$VERSION" \
    "$(plutil -extract CFBundleShortVersionString raw -o - "$APP/Contents/Info.plist")"
  assert_equal "Bundle ID" "com.wails.vakaohub" \
    "$(plutil -extract CFBundleIdentifier raw -o - "$APP/Contents/Info.plist")"
  codesign --verify --deep --strict "$APP"

  if [ "$BUILD" = 1 ] || [ ! -f "$ZIP" ]; then
    rm -f "$ZIP"
    COPYFILE_DISABLE=1 ditto --norsrc --noextattr -c -k --keepParent "$APP" "$ZIP"
  fi
  check_archive "$ZIP"

  UPLOAD=(node "$CLI" release --app vakaohub --version "$VERSION" --code "$CODE" \
    --file "$ZIP" --platform darwin --arch arm64 --format zip --min-os 12.0 \
    --notes "VaKao Hub $VERSION" ${PASSTHRU[@]+"${PASSTHRU[@]}"})
fi

echo "版本: $VERSION (code $CODE)"
echo "平台: $PLATFORM"
if [ "$PLATFORM" = "win32" ]; then
  echo "产物: $EXE"
else
  echo "产物: $ZIP"
fi

if [ "$DRY_RUN" = 1 ]; then
  echo "dry-run：跳过上传"
  printf '上传命令:'
  printf ' %q' "${UPLOAD[@]}"
  printf '\n'
  exit 0
fi

exec "${UPLOAD[@]}"
