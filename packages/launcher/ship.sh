#!/bin/bash
# 构建 VaKao Hub 启动器 → zip → 上传并发布到软件中心
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
CLI="${HUB_CLI:-/Users/woyuxiaji/Rouarn/AliyunCloud/vakao-hub/tools/hub-cli.mjs}"
VERSION=$(python3 -c "import json;print(json.load(open('$ROOT/wails.json'))['info']['productVersion'])")
CODE=$(echo "$VERSION" | awk -F. '{printf "%d", $1*100+$2*10+$3}')
ZIP="/tmp/VaKaoHub-${VERSION}.zip"
if [ "${1:-}" != "--no-build" ]; then
  export PATH="$HOME/go/bin:$PATH"
  ( cd "$ROOT" && wails build -clean -ldflags "-X vakaohub/launcher/internal/version.Version=$VERSION" )
fi
COPYFILE_DISABLE=1 ditto --norsrc --noextattr -c -k --keepParent "$ROOT/build/bin/VaKaoHub.app" "$ZIP"
exec node "$CLI" release --app vakaohub --version "$VERSION" --code "$CODE" \
  --file "$ZIP" --platform darwin --arch arm64 --format zip --min-os 12.0 \
  --notes "VaKao Hub $VERSION" "$@"
