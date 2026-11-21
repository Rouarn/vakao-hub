# hub-cli：把构建产物直接推到软件中心

一条命令完成「建版本 → 上传产物 → 发布」，不用再开后台点。

## 认证

优先级从高到低：

```bash
# 1) 直接给 token（后台登录后可拿）
VAKAO_HUB_TOKEN=... node tools/hub-cli.mjs apps

# 2) 给账号密码，自动登录并缓存 6 小时到 ~/.cache/vakao-hub/token.json
VAKAO_HUB_USER=admin VAKAO_HUB_PASS=*** node tools/hub-cli.mjs apps

# 3) 也可以指定文件
node tools/hub-cli.mjs apps --token-file ~/Documents/GlobalConfig/hub.token
```

服务地址默认 `https://dist.woyuxiaji.cn/api/v1`，用 `VAKAO_HUB_URL` 可覆盖。

## 发布一个版本

```bash
node tools/hub-cli.mjs release \
  --app dockdeck \
  --version 1.0.0 \
  --code 100 \
  --file ./DockDeck_1.0.0_arm64.dmg \
  --platform darwin --arch arm64 --format dmg --min-os 14.0 \
  --notes "1.0.0 首发"
```

- `--file` 可重复，每个 `--file` 后面跟它自己的 `--platform/--arch/--format/--min-os`
- 版本已存在时**复用**，不会重复建；产物会重新上传
- 默认全量发布；`--publish gray --gray-percent 10` 灰度；`--no-publish` 只上传不发布
- `--dry-run` 只打印计划，不写任何东西

## 接入到项目里

以 `dockdeck` 为例，`Scripts/ship.sh`：

```bash
Scripts/ship.sh            # 构建 + 打 dmg + 上传 + 全量发布
Scripts/ship.sh --dry-run  # 只看计划
```

其它项目照抄这个模式：先构建出安装包，再 `exec node "$CLI" release ...`。
CLI 路径可用 `HUB_CLI` 环境变量覆盖。

## 各项目入口

| 项目               | 命令                        | 产物                        |
| ------------------ | --------------------------- | --------------------------- |
| dockdeck           | `Scripts/ship.sh`           | dmg（Xcode 构建 + hdiutil） |
| lanlink            | `scripts/ship.sh`           | dmg（Tauri）                |
| wiredeck           | `Scripts/ship.sh`           | dmg（Wails + package.sh）   |
| vakaohub（启动器） | `packages/launcher/ship.sh` | zip（Wails）                |
| skipa              | `Scripts/ship.sh`           | apk（Gradle）               |
| DianMo             | `Scripts/ship.sh`           | apk（Gradle）               |
| 小绿记账           | `scripts/ship.sh`           | apk（HBuilderX 打好后上传） |

统一支持 `--no-build`（跳过构建只上传）、`--dry-run`（只看计划）、`--no-publish`（只传不发）。
