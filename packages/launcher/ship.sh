#!/bin/bash
# 构建 VaKao 启动器 → macOS zip（默认）、Windows exe（--win32），或两端一起（--both）→ 上传并发布到软件中心。
#
# 用法: ship.sh [--win32 | --both] [--no-build] [--dry-run]
#                [--publish none|gray|full] [--gray-percent N]
#   --both         一次出两端：先各自构建并以草稿/下架状态上传（不发布），
#                  两端都成功后再统一发布一次并复核两个平台；任一端失败都不发布，可直接重跑
#                  （上传带 --replace，会替换同平台/架构/格式的旧产物）
#   --win32        只出 Windows x64 裸 exe（单平台流程保持原语义：上传后立即发布）
#   --macos        默认单平台，等价于不传；保留是为了脚本里显式声明平台
#   --no-build     跳过编译，直接用 bin/ 下已有产物；产物版本或归档与当前 .app 不一致时会被门禁拒绝
#   --dry-run      构建并执行全部门禁检查，只打印命令序列、不下载/下架/上传/发布
#   --publish      发布模式，只接受 none / gray / full（默认 full）；拼写错误直接退非零
#   --gray-percent 灰度百分比，仅在 --publish gray 时生效
#
# 版本与产品信息的唯一来源是 packages/launcher/build/config.yml 的 info 块，
# 通过 scripts/version.mjs 读取；构建前先同步 build 资产，版本经 APP_VERSION
# 传给 wails3 task，由 Taskfile 的 ldflags 注入 Go 二进制与 Info.plist。
set -euo pipefail

# 通过软链调用时按真实路径定位仓库根，避免 ROOT 指向软链所在目录。
SOURCE="${BASH_SOURCE[0]}"
while [ -L "$SOURCE" ]; do
  DIR="$(cd -P "$(dirname "$SOURCE")" && pwd)"
  SOURCE="$(readlink "$SOURCE")"
  case "$SOURCE" in
    /*) ;;
    *) SOURCE="$DIR/$SOURCE" ;;
  esac
done
ROOT="$(cd -P "$(dirname "$SOURCE")" && pwd)"
REPO="$(cd "$ROOT/../.." && pwd)"
CLI="${HUB_CLI:-$REPO/tools/hub-cli.mjs}"
APP_KEY="vakaohub"
export PATH="$HOME/go/bin:$PATH"

command -v node >/dev/null 2>&1 || { echo "找不到 node" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "找不到 python3（归档与版本门禁依赖）" >&2; exit 1; }

VERSION="$(node "$REPO/scripts/version.mjs")"
CODE="$(node "$REPO/scripts/version.mjs" --code)"
PRODUCT="$(node "$REPO/scripts/version.mjs" productName)"
BUNDLE_ID="$(node "$REPO/scripts/version.mjs" productIdentifier)"

PLATFORM="darwin"
BOTH=0
BUILD=1
DRY_RUN=0
PUBLISH="full"
PASSTHRU=()
GRAY_PERCENT=""
while [ $# -gt 0 ]; do
  case "$1" in
    --both)
      BOTH=1
      shift
      ;;
    --win32)
      PLATFORM="win32"
      shift
      ;;
    --macos)
      PLATFORM="darwin"
      shift
      ;;
    --no-build)
      BUILD=0
      shift
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --publish)
      if [ $# -lt 2 ]; then
        echo "--publish 需要一个值：none / gray / full" >&2
        exit 2
      fi
      case "$2" in
        none|gray|full)
          PUBLISH="$2"
          PASSTHRU+=(--publish "$2")
          shift 2
          ;;
        *)
          echo "--publish 只接受 none / gray / full，收到: $2" >&2
          exit 2
          ;;
      esac
      ;;
    --publish=*)
      echo "不支持等号写法 $1，请用 --publish full" >&2
      exit 2
      ;;
    --gray-percent)
      if [ $# -lt 2 ] || ! [[ "$2" =~ ^[0-9]+$ ]]; then
        echo "--gray-percent 需要一个整数" >&2
        exit 2
      fi
      PASSTHRU+=(--gray-percent "$2")
      GRAY_PERCENT="$2"
      shift 2
      ;;
    *)
      echo "未知参数: $1" >&2
      exit 2
      ;;
  esac
done

if [ "$BOTH" = 1 ] && [ "$PLATFORM" = "win32" ]; then
  echo "--both 不能与 --win32 同时使用" >&2
  exit 2
fi

# 构建前先预检凭据，避免整轮编译跑完才报缺 token；dry-run 不上传，跳过。
if [ "$DRY_RUN" = 0 ]; then
  node "$CLI" auth-check
fi

# 发布门禁：所有 workflow 只允许 workflow_dispatch。
# 顺序放在构建之前，是为了先花一秒排除触发方式违规，避免白跑一轮编译。
# 只允许手动触发是私有仓库 Actions 额度约束的守门，不是遗留 bug，后续不要改回 push/pull_request。
node "$REPO/scripts/check-workflows.mjs"

assert_equal() {
  local label="$1" expected="$2" actual="$3"
  if [ "$expected" != "$actual" ]; then
    echo "门禁失败：$label = '$actual'，期望 '$expected'" >&2
    exit 1
  fi
}

# 先执行命令再比较；命令本身失败时显式退非零，不会被 set -e 吞掉。
assert_cmd_equal() {
  local label="$1" expected="$2"
  shift 2
  local actual
  if ! actual=$("$@"); then
    echo "门禁失败：$label 取值命令失败：$*" >&2
    exit 1
  fi
  assert_equal "$label" "$expected" "$actual"
}

# build/config.yml → Info.plist / info.json；必须在注入 APP_VERSION 之前完成。
sync_build_assets() {
  ( cd "$ROOT" && wails3 task common:update:build-assets )
}

# 发布端包形态门禁：与安装端共用 install.ValidatePackage，
# zip 顶层结构、缺主程序、路径穿越等问题在上传前就被挡住。
check_package() {
  ( cd "$ROOT" && go run ./cmd/artifact-check --platform "$1" --format "$2" --path "$3" )
}

# 自更新组件门禁：Go 的 embed 会把 helper 路径作为字面量写进二进制。
# 缺这个字符串说明更新组件没被打进产物，Windows 端会直接报「更新组件缺失，请重新安装」。
check_helper_embed() {
  local file="$1" expected="$2"
  grep -a -q -F -- "$expected" "$file" || {
    echo "门禁失败：$file 未内嵌自更新组件 $expected（安装端无法自更新）" >&2
    exit 1
  }
  echo "自更新组件校验通过：$expected"
}

# 归档结构、版本与 .app 一致性统一交给独立脚本，便于单测。
check_archive() {
  local archive="$1"
  python3 "$REPO/scripts/check-archive.py" \
    --archive "$archive" \
    --version "$VERSION" \
    --product "$PRODUCT" \
    --app "$2"
}

# 按实际产物取架构，避免在 Intel 机器上被写死成 arm64。
build_arch() {
  local archs
  archs="$(lipo -archs "$BIN" 2>/dev/null || true)"
  case "$archs" in
    "arm64 x86_64"|"x86_64 arm64") echo "universal" ;;
    *arm64*) echo "arm64" ;;
    *x86_64*) echo "x64" ;;
    *)
      local goarch
      goarch="$( cd "$ROOT" && go env GOARCH )"
      case "$goarch" in
        amd64) echo "x64" ;;
        arm64) echo "arm64" ;;
        *)
          echo "无法识别架构: $goarch" >&2
          return 1
          ;;
      esac
      ;;
  esac
}

# 从 Windows PE 的版本资源里读取版本号；读不到就显式失败，不静默跳过。
check_pe_version() {
  python3 - "$1" "$2" <<'PY'
import struct
import sys

path, expected = sys.argv[1], sys.argv[2]
data = open(path, 'rb').read()


def fail(message):
    print("门禁失败：" + message, file=sys.stderr)
    sys.exit(1)


if len(data) < 0x40:
    fail(f"{path} 不是有效的 PE 文件")
pe_offset = struct.unpack_from('<I', data, 0x3C)[0]
if data[pe_offset:pe_offset + 4] != b'PE\0\0':
    fail(f"{path} 缺少 PE 签名")

coff = pe_offset + 4
section_count = struct.unpack_from('<H', data, coff + 2)[0]
optional_size = struct.unpack_from('<H', data, coff + 16)[0]
optional = coff + 20
magic = struct.unpack_from('<H', data, optional)[0]
if magic == 0x10B:
    data_dirs = optional + 96
elif magic == 0x20B:
    data_dirs = optional + 112
else:
    fail(f"未知 PE 可选头 magic 0x{magic:x}")

resource_rva, resource_size = struct.unpack_from('<II', data, data_dirs + 2 * 8)
if not resource_rva or not resource_size:
    fail("exe 没有资源段，无法从产物中读取版本；请在上传前于 Windows 上运行 --version 补验")

sections = []
section_table = optional + optional_size
for index in range(section_count):
    offset = section_table + index * 40
    virtual_size, virtual_address, raw_size, raw_pointer = struct.unpack_from('<IIII', data, offset + 8)
    sections.append((virtual_address, max(virtual_size, raw_size), raw_pointer))


def rva_to_offset(rva):
    for virtual_address, size, raw_pointer in sections:
        if virtual_address <= rva < virtual_address + size:
            return raw_pointer + (rva - virtual_address)
    return None


resource_base = rva_to_offset(resource_rva)
if resource_base is None:
    fail("资源段无法映射到文件偏移")

found = []


def walk(directory_offset, depth):
    if depth > 4:
        return
    named, ids = struct.unpack_from('<HH', data, directory_offset + 12)
    for index in range(named + ids):
        entry = directory_offset + 16 + index * 8
        name, target = struct.unpack_from('<II', data, entry)
        if target & 0x80000000:
            walk(resource_base + (target & 0x7FFFFFFF), depth + 1)
            continue
        rva, size = struct.unpack_from('<II', data, resource_base + target)
        blob_offset = rva_to_offset(rva)
        if blob_offset is None:
            continue
        found.append(data[blob_offset:blob_offset + size])


walk(resource_base, 0)

signature = b'\xbd\x04\xef\xfe'
for blob in found:
    position = blob.find(signature)
    if position == -1:
        continue
    file_version_ms, file_version_ls = struct.unpack_from('<II', blob, position + 8)
    actual = (
        file_version_ms >> 16,
        file_version_ms & 0xFFFF,
        file_version_ls >> 16,
    )
    expected_tuple = tuple(int(part) for part in expected.split('.')[:3])
    if actual != expected_tuple:
        fail(f"exe 内版本资源为 {'.'.join(map(str, actual))}，期望 {expected}")
    print(f"exe 版本资源校验通过：{'.'.join(map(str, actual))}")
    break
else:
    fail("exe 内找不到版本资源；请在上传前于 Windows 上运行 --version 补验")
PY
}

# 发布成功后只保留最近 3 份同产品归档，避免 /tmp 无限堆积。
cleanup_old_zips() {
  local keep=3
  # shellcheck disable=SC2012 # 产品名固定为字母数字，不存在特殊文件名风险
  ls -1t /tmp/"$PRODUCT"-*.zip 2>/dev/null | tail -n +$((keep + 1)) | while IFS= read -r old; do
    rm -f -- "$old"
    echo "清理旧归档: $old"
  done
}

build_darwin() {
  APP="$ROOT/bin/$PRODUCT.app"
  BIN="$APP/Contents/MacOS/$PRODUCT"
  ZIP="/tmp/$PRODUCT-$VERSION.zip"
  if [ "$BUILD" = 1 ]; then
    command -v wails3 >/dev/null 2>&1 || {
      echo "找不到 wails3（go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0）" >&2
      exit 1
    }
    sync_build_assets
    ( cd "$ROOT" && wails3 task darwin:package APP_VERSION="$VERSION" )
  fi
  [ -d "$APP" ] || { echo "找不到产物 $APP" >&2; exit 1; }

  assert_cmd_equal "--version" "$VERSION" "$BIN" --version
  assert_cmd_equal "Info.plist 版本" "$VERSION" \
    plutil -extract CFBundleShortVersionString raw -o - "$APP/Contents/Info.plist"
  assert_cmd_equal "Bundle ID" "$BUNDLE_ID" \
    plutil -extract CFBundleIdentifier raw -o - "$APP/Contents/Info.plist"
  codesign --verify --deep --strict "$APP"
  ARCH_NAME="$(build_arch)"

  if [ "$BUILD" = 1 ] || [ ! -f "$ZIP" ]; then
    rm -f "$ZIP"
    COPYFILE_DISABLE=1 ditto --norsrc --noextattr -c -k --keepParent "$APP" "$ZIP"
  else
    echo "复用已有归档 ${ZIP}（--no-build），先与当前 .app 对比"
  fi
  check_archive "$ZIP" "$APP"
  check_package darwin zip "$ZIP"
  check_helper_embed "$BIN" "bin/darwin_$(cd "$ROOT" && go env GOARCH)/vakaohub-updater"
}

build_win32() {
  EXE="$ROOT/bin/$PRODUCT.exe"
  if [ "$BUILD" = 1 ]; then
    command -v wails3 >/dev/null 2>&1 || {
      echo "找不到 wails3（go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0）" >&2
      exit 1
    }
    sync_build_assets
    ( cd "$ROOT" && wails3 task windows:build APP_VERSION="$VERSION" ARCH=amd64 )
  fi
  [ -f "$EXE" ] || { echo "找不到产物 $EXE" >&2; exit 1; }

  assert_cmd_equal "windows/info.json 版本" "$VERSION" \
    env INFO_JSON="$ROOT/build/windows/info.json" \
    node -p "require(process.env.INFO_JSON).info['0409'].ProductVersion"
  check_pe_version "$EXE" "$VERSION"
  check_package win32 exe "$EXE"
  check_helper_embed "$EXE" "bin/windows_amd64/vakaohub-updater.exe"
}

# --both：两端都构建、都以草稿/下架状态上传（--publish none），全部成功后才统一发布一次。
# 任一端门禁或上传失败都会直接退出，不发布；--replace 让重跑可以覆盖已上传的旧产物。
if [ "$BOTH" = 1 ]; then
  echo "[1/6] 构建 macOS"
  build_darwin
  echo "[2/6] 构建 Windows"
  build_win32

  UPLOAD_MAC=(node "$CLI" release --app "$APP_KEY" --version "$VERSION" --code "$CODE" \
    --file "$ZIP" --platform darwin --arch "$ARCH_NAME" --format zip --min-os 12.0 \
    --notes "VaKao Hub $VERSION" --publish none --replace)
  UPLOAD_WIN=(node "$CLI" release --app "$APP_KEY" --version "$VERSION" --code "$CODE" \
    --file "$EXE" --platform win32 --arch x64 --format exe --min-os 10.0 \
    --notes "VaKao Hub $VERSION" --publish none --replace)

  PUBLISH_ARGS=(--publish "$PUBLISH")
  if [ -n "$GRAY_PERCENT" ]; then
    PUBLISH_ARGS+=(--gray-percent "$GRAY_PERCENT")
  fi
  PUBLISH_CMD=(node "$CLI" publish --app "$APP_KEY" --version "$VERSION" --code "$CODE" \
    "${PUBLISH_ARGS[@]}")
  VERIFY_CMD=(node "$CLI" verify --app "$APP_KEY" --version "$VERSION" --code "$CODE" \
    --expect "$PUBLISH" --platforms darwin,win32)

  echo "版本: $VERSION (code $CODE)"
  echo "macOS: $ZIP ($ARCH_NAME)"
  echo "Windows: $EXE (x64)"
  echo "发布: $PUBLISH"

  if [ "$DRY_RUN" = 1 ]; then
    echo "dry-run：两端只构建与门禁，不访问线上状态"
    echo "步骤 1: 准备可上传版本（若已全量/灰度会先临时下架）"
    printf '  %q' node "$CLI" stage --app "$APP_KEY" --version "$VERSION" --code "$CODE"
    printf '\n步骤 2: 上传 macOS（不发布）\n  '
    printf ' %q' "${UPLOAD_MAC[@]}"
    printf '\n步骤 3: 上传 Windows（不发布）\n  '
    printf ' %q' "${UPLOAD_WIN[@]}"
    if [ "$PUBLISH" = "none" ]; then
      printf '\n步骤 4: --publish none，跳过统一发布\n'
    else
      printf '\n步骤 4: 统一发布\n  '
      printf ' %q' "${PUBLISH_CMD[@]}"
      printf '\n步骤 5: 发布后复核\n  '
      printf ' %q' "${VERIFY_CMD[@]}"
      printf '\n'
    fi
    exit 0
  fi

  echo "[3/6] 准备可上传版本"
  node "$CLI" stage --app "$APP_KEY" --version "$VERSION" --code "$CODE"
  echo "[4/6] 上传 macOS（不发布）"
  "${UPLOAD_MAC[@]}"
  echo "[5/6] 上传 Windows（不发布）"
  "${UPLOAD_WIN[@]}"

  if [ "$PUBLISH" = "none" ]; then
    echo "两端产物已上传，按 --publish none 未发布"
  else
    echo "[6/6] 统一发布（${PUBLISH}）"
    "${PUBLISH_CMD[@]}"
    echo "发布后复核"
    "${VERIFY_CMD[@]}"
  fi
  cleanup_old_zips
  exit 0
fi

if [ "$PLATFORM" = "win32" ]; then
  build_win32
  UPLOAD=(node "$CLI" release --app "$APP_KEY" --version "$VERSION" --code "$CODE" \
    --file "$EXE" --platform win32 --arch x64 --format exe --min-os 10.0 \
    --notes "VaKao Hub $VERSION" ${PASSTHRU[@]+"${PASSTHRU[@]}"})
else
  build_darwin
  UPLOAD=(node "$CLI" release --app "$APP_KEY" --version "$VERSION" --code "$CODE" \
    --file "$ZIP" --platform darwin --arch "$ARCH_NAME" --format zip --min-os 12.0 \
    --notes "VaKao Hub $VERSION" ${PASSTHRU[@]+"${PASSTHRU[@]}"})
fi

echo "版本: $VERSION (code $CODE)"
echo "平台: $PLATFORM"
if [ "$PLATFORM" = "win32" ]; then
  echo "产物: $EXE"
  echo "架构: x64"
else
  echo "产物: $ZIP"
  echo "架构: $ARCH_NAME"
fi
echo "发布: $PUBLISH"

if [ "$DRY_RUN" = 1 ]; then
  echo "dry-run：跳过上传与发布后复核"
  printf '上传命令:'
  printf ' %q' "${UPLOAD[@]}"
  printf '\n'
  exit 0
fi

"${UPLOAD[@]}"

# 发布后复核：拿接口确认目标版本确实处于全量/灰度状态，避免“CLI 打印成功但实际没发出去”。
if [ "$PUBLISH" != "none" ]; then
  node "$CLI" status --app "$APP_KEY" --version "$VERSION" --code "$CODE" --expect "$PUBLISH"
  echo "发布复核通过：$VERSION 处于 $PUBLISH 状态"
fi

if [ "$PLATFORM" != "win32" ]; then
  cleanup_old_zips
fi
