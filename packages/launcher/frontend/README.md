# VaKao Hub Launcher Frontend

技术栈：Vue 3 + TypeScript + Vite。界面采用自定义设计系统，未引入通用组件库，以保持品牌视觉一致。

## 结构

```text
src/
├── App.vue                 # 页面编排、目录状态与业务动作
├── components/
│   ├── AppCard.vue         # 应用卡片与卡片菜单
│   ├── AppDetailPanel.vue  # 应用详情、截图、更新日志、自定义滚动条
│   ├── LauncherHeader.vue  # 品牌头、刷新、搜索、排序、视图切换
│   ├── SettingsModal.vue   # 外观、更新频率、分发服务、关于
│   ├── StatusBar.vue       # 上次检查、来源、服务状态、外链
│   └── UninstallDialog.vue # 卸载确认与数据删除选项
├── composables/
│   ├── useTheme.ts         # 明暗/跟随系统切换与持久化
│   └── useWindowDrag.ts    # 自定义顶栏拖拽窗口
├── lib/
│   ├── constants.ts        # 启动器自身 appKey 等常量
│   ├── errors.ts           # 后端错误到用户可读文案的映射
│   ├── format.ts           # 日期、大小、首字母、进度格式化
│   ├── meta.ts             # 详情类型
│   ├── theme.ts            # 主题色、哈希色、色值工具
│   └── version.ts          # 版本比较
└── bindings/               # wails3 生成的 Go 绑定（不要手改）
```

## 约束

- `App.vue` 不直接写大段页面结构，只负责组合组件和业务状态。
- 新 UI 片段优先放入 `components/`，公共逻辑放入 `composables/` 或 `lib/`。
- 修改后执行 `pnpm build`（含 `vue-tsc` 类型检查）；发布前用 `wails3 task darwin:package` 或 `./ship.sh`。
