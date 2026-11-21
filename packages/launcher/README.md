# VaKao Hub Launcher（桌面启动器）

VaKao 软件中心的桌面客户端：读取分发服务的应用目录，下载产物、校验 SHA-256、安装并启动。应用卡片可进入详情页，查看功能特性、版本信息与更新日志。

## 技术栈

- Wails v3.1 + Go 1.26
- Vue 3.5 + Vite 8 + TypeScript 6
- 无第三方 UI 库，界面样式集中在 `frontend/src/style.css`

## 开发与构建

```bash
# 开发（热更新，Wails 内置 Vite）
wails3 task dev

# 生产构建：版本唯一来源是 build/config.yml 的 info.version，
# Taskfile 通过 ../../scripts/version.mjs 读取并注入 Go 代码，
# 构建前如需同步 Info.plist / info.json，先执行：
wails3 task common:update:build-assets

VERSION=$(node ../../scripts/version.mjs)
wails3 task darwin:package APP_VERSION="$VERSION"

# 产物
bin/VaKaoHub.app

# 发布（含版本一致、签名、归档结构、包形态与发布后复核门禁；--dry-run 只检查不上传）
./ship.sh --dry-run

# 复用 bin/ 里已有产物时用 --no-build；归档与当前 .app 不一致会被门禁拒绝
./ship.sh --no-build

# Windows 产物走同一套版本、PE 版本与包形态门禁
./ship.sh --win32 --dry-run

# 两端一起发：两端都先上传（不发布），全部成功后再统一发布一次并复核两个平台
./ship.sh --both --dry-run
./ship.sh --both
```

单平台（`./ship.sh`、`./ship.sh --win32`）保持「上传后立即发布」的原有语义。同一版本要同时发布两端时用 `--both`：
它会先构建两端并通过门禁，再显式把已全量/灰度的同版本临时下架（终端会打印），两端都以 `--publish none` 上传成功后
才发布一次，最后用 `verify` 核对状态、versionCode 和两个平台的产物。中途任一端失败都不会发布，可直接重跑
（上传带 `--replace`，会替换同平台/架构/格式的旧产物）。

`build/darwin/Info.plist`、`build/windows/info.json` 等属于生成物，不要手改；改版本只改 `build/config.yml`，再运行 `common:update:build-assets`。

默认连接生产服务 `https://dist.woyuxiaji.cn/api/v1`；本地开发在应用「设置」中改为 `http://127.0.0.1:3000/api/v1`。

## 功能

- 应用目录：按当前操作系统与 CPU 架构过滤，展示最新版本、已安装版本与可更新状态；
- 安装/更新：下载（带进度事件）→ SHA-256 校验 → 平台安装；
- 打开：调用系统默认方式启动已安装应用；
- 自检测：启动器识别当前运行的 `.app`，不会把自己当成未安装而再次安装；
- 手动安装识别：扫描 `~/Applications` 与 `/Applications`，手动拖入的应用也会显示为已安装；
- 重复安装提示：同一应用存在多份时列出路径，更新时替换已有路径而不是再复制一份；
- 自动刷新：启动即加载，之后每 30 分钟静默刷新；顶部显示可更新数量；
- 断点续传：半包保存在 `~/Library/Caches/vakaohub/downloads/<artifactID>.part`，下次继续；
- 强制更新：未更新到最新版时「打开」被禁用并提示；
- 安装校验：SHA-256 + `codesign --verify --deep --strict`（未签名或签名损坏会被拒绝）；
- 卸载：便携类删除应用容器目录；安装器类调起注册表里登记的系统卸载程序；
- 已安装记录：`~/Library/Application Support/vakaohub/installed.json`（Windows 为 `%AppData%\vakaohub`）；
- 设置：分发服务地址与安装目录持久化到同目录 `settings.json`。

## 平台安装策略

| 平台    | 支持格式      | 安装位置                                                                                                                                                     |
| ------- | ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| macOS   | dmg、zip      | `~/Applications/<App>.app`（DMG 挂载后 `ditto` 复制），可在设置中修改                                                                                        |
| Windows | zip、msi、exe | 便携类默认 `%LOCALAPPDATA%\Programs\VaKaoHub\<appKey>`（可在设置中修改，升级留在原位置）；msi 静默安装；exe 命中安装器标记时调起向导，无法判定时由用户二选一 |
| Linux   | zip、appimage | `~/.local/share/vakaohub/apps/<appKey>` 或 `~/Applications`                                                                                                  |

### 应用包形态约定

安装端在下载校验后、安装前会检查包结构（`internal/install/package.go`）；发布端 `ship.sh` 通过
`go run ./cmd/artifact-check` 复用同一份规则，不合规的包在上传前就会被拒绝：

| 平台   | 格式 | 要求                                                                                                                                                           |
| ------ | ---- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| win32  | exe  | 必须是有效的 PE（GUI/Console 均可）；含 NSIS/Inno/Wix/InstallShield/Squirrel/7-Zip SFX 等标记或 `.ndata`/`.wixburn` 节时运行安装向导，否则按单文件便携应用安装 |
| win32  | msi  | 必须是 OLE 复合文档（微软安装包头）                                                                                                                            |
| win32  | zip  | 至少一个 `.exe`；顶层有 exe 时以最大者为主程序，否则所有 exe 必须位于同一个顶层目录；解压后主程序若被识别为安装器，则交给安装器运行而非复制托管                |
| darwin | zip  | 必须包含 `*.app/Contents/Info.plist` 与 `*.app/Contents/MacOS/` 下的可执行文件                                                                                 |

所有格式都禁止绝对路径与 `../` 路径穿越。便携应用推荐打成 zip；裸 exe 依赖安装器标记自动识别，
完全没有标记、文件名也不像安装程序的 exe 会在安装前让用户选择「作为便携应用安装」或「交给安装程序」，不会擅自复制。
安装器类的应用位置以 Windows 卸载注册表为准；暂时识别不到时显示「已安装 · 位置未知」，可在详情里手动指定主程序。

打新包后可先在本地校验，再走上传：

```bash
go run ./cmd/artifact-check --platform win32 --format zip --path /path/to/app.zip
```

手工在后台直接上传的包不会经过 `ship.sh`，仍需先执行上面的检查。

macOS 使用内置 `hdiutil`/`ditto`，不修改系统安全设置；未公证的应用首次启动仍可能被 Gatekeeper 拦截，启动失败时应用会提示「右键 → 打开」或到系统设置放行。要彻底消除提示，需要 Apple Developer Program（Developer ID 签名 + 公证）。

## Windows 构建

Windows 需要 Windows 机器或 CI。仓库提供 [`.github/workflows/launcher-windows.yml`](../../.github/workflows/launcher-windows.yml)，在 GitHub Actions 的 `windows-latest` 上安装 wails3、编译并上传 `VaKaoHub.exe`。所有 workflow 只允许 `workflow_dispatch` 手动触发，不使用 `push` / `pull_request` / `schedule`。

## 托盘

暂未实现系统托盘：Wails v3 与常见 Go 托盘库都要求占用主线程，两者同时启动会冲突；后续用原生 `NSStatusItem` 再补。

## 冒烟测试

需要本地服务已启动，并已在后台创建 `wiredeck` 应用与 macOS 产物：

```bash
VAKAO_HUB_SMOKE=1 go test -run TestSMOKECatalogAndInstall -v ./internal/service
```

测试会真实下载 DMG、校验 SHA-256、安装到 `~/Applications`；`TestSMOKEUninstall` 会卸载并校验记录被清除。半包续传可通过预先放置 `~/Library/Caches/vakaohub/downloads/<artifactID>.part` 验证。

## 目录

```
launcher/
├─ main.go                # Wails v3 入口与绑定注册
├─ ship.sh                # 构建、门禁与发布
├─ internal/api/          # 分发服务 HTTP 客户端与类型
├─ internal/service/      # 绑定层：目录、安装、启动、设置、事件
├─ internal/install/      # 分平台安装、检测、启动与卸载
├─ internal/atomicfs/     # 原子换入、回滚与暂存清理
├─ internal/contract/     # 跨层共享的契约常量与类型
├─ internal/store/        # 已安装应用记录
├─ internal/version/      # 构建版本注入
├─ internal/apperr/       # 统一错误类型与展示文案
├─ internal/logging/      # 日志与 panic 落盘
├─ internal/selfupdate/   # 自更新暂存、校验与替换
├─ internal/updater/      # 独立更新 helper
└─ frontend/              # Vue 3 界面
```
