# 生产部署（宝塔面板）

线上：下载页 <https://dist.woyuxiaji.cn>、管理端 <https://dist.woyuxiaji.cn/admin/>。

## 0. 架构总览

**一个 Node 进程同时提供**：下载页、管理端、`/api` 接口、安装包下载。
**Nginx 只做一件事**：把所有请求反代给这个 Node 进程。

```
浏览器 ──> Nginx (:80) ──proxy──> Node (:31888)
                                   ├─ /            下载页（web/site）
                                   ├─ /admin/      管理端（web/admin）
                                   ├─ /api/v1/…   接口
                                   └─ /api/v1/updates/download/:id  安装包（流式发送）
```

这样只有一个进程要管、一个服务要重启，前后台不会再出现「改了 Nginx 忘了改另一边」的问题。

## 1. 目录结构

```
/www/wwwroot/vakao-hub/
├── app/             服务端
│   ├── dist/         编译产物（node dist/main.js）
│   ├── node_modules/
│   ├── shared/       @vakao/hub-shared 构建产物（软链到 node_modules/@vakao/hub-shared）
│   ├── package.json
│   └── .env          运行配置
├── web/
│   ├── site/         下载页静态文件
│   └── admin/        管理端 SPA
├── storage/         安装包（按 appKey 分目录），70 MB 量级
└── logs/            预留日志目录
```

安装包在 `storage/<appKey>/v<versionCode>_<versionName>/`，例如
`storage/dockdeck/v100_1.0.0/darwin-arm64-dmg-DockDeck_1.0.0_arm64.dmg`。

## 2. 前置

- Node.js >= 22.18（线上为 v24.21.0）
- MySQL 8（本机或内网均可）
- Nginx（宝塔自带）
- 运行用户 `www`，需对 `app/`、`storage/` 有读写权限

## 3. 数据库

执行 [mysql-init.sql.example](mysql-init.sql.example) 建库与专用账号（不要用 root 跑应用）。

## 4. 构建（本地）

```bash
cd vakao-hub
pnpm install
pnpm --filter @vakao/hub-shared build
pnpm --filter @vakao/hub-server build
ADMIN_BASE=/admin/ pnpm --filter @vakao/hub-admin build
```

产物：`packages/server/dist`、`packages/shared/dist`、`packages/admin/dist`、`packages/website`（纯静态）。

## 5. 上传到服务器

```bash
rsync -az --delete packages/server/dist/   server:/www/wwwroot/vakao-hub/app/dist/
rsync -az --delete packages/shared/dist/   server:/www/wwwroot/vakao-hub/app/shared/dist/
rsync -az --delete packages/admin/dist/    server:/www/wwwroot/vakao-hub/web/admin/
rsync -az --delete packages/website/       server:/www/wwwroot/vakao-hub/web/site/
ssh server 'chown -R www:www /www/wwwroot/vakao-hub'
```

依赖只在首次部署或 `package.json` 变化时需要：把 `node_modules` 拷过去，或在 `app/` 下 `npm ci --omit=dev`。

## 6. 服务端配置（`app/.env`）

```bash
cp .env.production.example .env
chmod 600 .env
```

必改/必查：

| 键 | 说明 |
| --- | --- |
| `NODE_ENV` | `production` |
| `PORT` | `31888`（与宝塔 Node 项目端口一致） |
| `WEB_DIR` | `/www/wwwroot/vakao-hub/web` |
| `STORAGE_DIR` | `/www/wwwroot/vakao-hub/storage` |
| `DB_*` | 数据库连接，`DB_SYNCHRONIZE=false` |
| `JWT_SECRET` | ≥ 32 字符 |
| `ADMIN_PASSWORD` | ≥ 8 位 |
| `PUBLIC_BASE_URL` | 对外 HTTPS 地址，不带 `/api` |
| `CORS_ORIGINS` | 同域部署时可不填 |

生产环境会硬校验占位值，示例里的 `replace-with-…` 会导致启动失败。

## 7. 数据库迁移

```bash
cd /www/wwwroot/vakao-hub/app
node scripts/migrate.mjs
```

迁移按列/表检测是否已存在，对「synchronize 建过的开发库」和「全新库」都安全。

## 8. Nginx（宝塔站点 → 设置 → 配置文件）

整个站点只有两段：

```nginx
# API 不缓存：下载计数与实时数据都要求请求回到源站
location ^~ /api/ {
    proxy_pass http://127.0.0.1:31888;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_request_buffering off;
    proxy_buffering off;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
    proxy_hide_header Cache-Control;
    add_header Cache-Control "no-store" always;
}

location / {
    proxy_pass http://127.0.0.1:31888;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_request_buffering off;
    proxy_buffering off;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
}
```

站点根目录填 `/www/wwwroot/vakao-hub`（实际不用于静态托管，仅占位），上传上限 `client_max_body_size 2g`。

HTTPS 用宝塔「SSL」申请，再开「强制 HTTPS」；挡在前面的是 EdgeOne CDN。

## 9. 进程托管（宝塔 Node 项目）

宝塔 → 网站 → Node 项目 → 添加：

| 字段 | 值 |
| --- | --- |
| 项目名称 | `vakao-hub` |
| 项目目录 | `/www/wwwroot/vakao-hub/app` |
| 项目端口 | `31888` |
| 启动方式 | node → `dist/main.js` |
| Node 版本 | v24.21.0 |
| 运行用户 | `www` |
| 安装依赖 | 否（已就绪） |
| 守护进程 / 开机自启 | 勾上 |

日志：`/www/wwwlogs/nodejs/vakao-hub.log`（启动、迁移与运行时输出）。

更新代码后在面板点「重启」即可，不需要动 Nginx。

## 10. 备份

宝塔 → 计划任务 → Shell 脚本，每天 3:30：

```bash
/www/wwwroot/vakao-hub/app/../../deploy/backup.sh >> /var/log/vakaohub-backup.log 2>&1
```

脚本备份数据库与 `storage/`，默认保留 7 天，产物在 `/www/backup/vakaohub`。
（脚本里的 `VAKAO_HUB_ROOT` 需指向 `/www/wwwroot/vakao-hub`。）

## 11. 日常发布

- 发版：各项目的 `ship.sh` 直接调 `tools/hub-cli.mjs` 推产物，不经服务器
- 改服务端：按第 4、5 步构建上传，再在宝塔里重启 Node 项目
- 改前端：`ADMIN_BASE=/admin/` 重新构建 admin，或直接更新 `web/site`

## 12. 排障

| 现象 | 先看 |
| --- | --- |
| 全站 502 | Node 项目是否在运行、`/www/wwwlogs/nodejs/vakao-hub.log` |
| `/api` 正常但页面 404 | `web/site`、`web/admin` 是否上传到位 |
| 下载 404 | `STORAGE_DIR` 与实际目录是否一致、`storage/<appKey>/…` 是否存在 |
| 下载计数不涨 | 是否被 CDN 缓存（响应头应为 `Cache-Control: no-store`） |
