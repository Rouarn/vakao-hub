<template>
  <div class="shell">
    <LauncherHeader
      :loading="loading"
      :update-count="updateCount"
      :updating-all="updatingAll"
      :sort-by="sortBy"
      :search="search"
      :grid-mode="gridMode"
      @refresh="load"
      @open-settings="openSettings"
      @update-all="updateAll"
      @update:sort-by="sortBy = $event"
      @update:search="search = $event"
      @update:grid-mode="gridMode = $event"
    />

    <main ref="contentRef" class="content">
      <div v-if="loading && !apps.length" class="empty">正在读取软件目录…</div>
      <div v-else-if="catalogError" class="empty">
        <p>{{ catalogError.message }}</p>
        <p class="hint">检查网络或分发服务地址后重试。</p>
        <button class="empty-retry" @click="load">重试</button>
      </div>
      <div v-else-if="!visibleApps.length" class="empty">
        <p>{{ search ? '没有匹配的软件。' : '目录还是空的。' }}</p>
        <p class="hint">试试清空搜索，或到管理后台发布版本。</p>
      </div>

      <div v-else class="app-sections">
        <section v-for="group in appGroups" :key="group.key" class="app-section">
          <div class="app-section-head">
            <span class="hud">{{ group.label }}</span>
            <span class="app-section-count">{{ group.apps.length }}</span>
          </div>

          <div class="app-grid" :class="{ 'list-mode': !gridMode }">
            <AppCard
              v-for="app in group.apps"
              :key="app.appKey"
              :app="app"
              :grid-mode="gridMode"
              :busy="busy(app.appKey)"
              :updating-all="updatingAll"
              :supports-os="supportsOs(app)"
              :menu-open="menuKey === app.appKey"
              :platform-label="platformLabel"
              :progress="progress[app.appKey]"
              :note="cardNotes[app.appKey]"
              @open-detail="openDetail"
              @primary-action="primaryAction"
              @toggle-menu="toggleMenu"
              @launch="launch"
              @reveal="reveal"
              @install="install"
              @bind-path="bindPath"
              @open-url="openUrl"
              @request-uninstall="requestUninstall"
              @cancel-install="cancelInstall"
              @retry-note="retryNote"
              @dismiss-note="dismissNote"
            />
          </div>
        </section>
      </div>
    </main>

    <AppDetailPanel
      v-if="selected"
      :selected="selected"
      :meta="selectedMeta"
      :theme-vars="themeVars"
      :menu-open="menuKey === selected.appKey"
      :busy="busy(selected.appKey)"
      :supports-os="supportsOs(selected)"
      :host-os-version="hostOsVersion"
      :platform-label="platformLabel"
      :progress="progress[selected.appKey]"
      :note="cardNotes[selected.appKey]"
      @close="closeDetail"
      @prev="prevApp"
      @next="nextApp"
      @primary-action="primaryAction"
      @toggle-menu="toggleMenu"
      @launch="launch"
      @reveal="reveal"
      @install="install"
      @bind-path="bindPath"
      @open-url="openUrl"
      @request-uninstall="requestUninstall"
      @cancel-install="cancelInstall"
      @retry-note="retryNote"
      @dismiss-note="dismissNote"
    />

    <StatusBar :last-checked-label="lastCheckedLabel" :service-online="serviceOnline" @open-url="openUrl" />

    <SettingsModal
      v-model:base-url="draftBaseUrl"
      v-model:install-dir="draftInstallDir"
      v-model:refresh-minutes="refreshMinutes"
      :visible="settingsVisible"
      :theme-mode="themeMode"
      :service-online="serviceOnline"
      :saving="saving"
      :loading="loading"
      :platform-label="platformLabel"
      :host-os-version="hostOsVersion"
      :arch="arch"
      :launcher-version="launcherVersion"
      :default-install-dir="defaultInstallDir"
      :install-dir-editable="installDirEditable"
      :settings-status="settingsStatus"
      :startup-notice="startupNotice"
      @close="settingsVisible = false"
      @save-base-url="saveBaseUrl"
      @save-install-dir="saveInstallDir"
      @reset="resetBaseUrl"
      @select-install-dir="chooseInstallDir"
      @reset-install-dir="resetInstallDir"
      @theme-mode="setThemeMode"
      @refresh-changed="saveRefreshInterval"
      @refresh-now="load"
      @open-url="openUrl"
      @open-log="openLog"
    />

    <RunningDialog
      :target="runningTarget"
      :message="runningMessage"
      :busy="quitting"
      @close="closeRunningDialog"
      @confirm="quitAndInstall"
    />

    <UninstallDialog
      v-model:delete-data="deleteUninstallData"
      :target="uninstallTarget"
      :platform-label="platformLabel"
      @close="uninstallTarget = null"
      @confirm="confirmUninstall"
    />

    <InstallChoiceDialog
      :target="installChoiceTarget"
      :message="installChoiceMessage"
      @cancel="cancelInstallChoice"
      @choose="chooseInstallMode"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import { Events } from '@wailsio/runtime';
import {
  CatalogService,
  InstallService,
  LauncherService,
  SettingsService,
} from '../bindings/vakaohub/launcher/internal/service';
import type { AppView, Progress, Settings } from '../bindings/vakaohub/launcher/internal/service';
import { InstallProgress } from '../bindings/events';
import { SELF_APP_KEY } from './lib/constants';
import type { UserError } from './lib/errors';
import { toUserError } from './lib/errors';
import type { DetailMeta } from './lib/meta';
import type { CardNote } from './lib/notes';

import AppCard from './components/AppCard.vue';
import AppDetailPanel from './components/AppDetailPanel.vue';
import InstallChoiceDialog from './components/InstallChoiceDialog.vue';
import LauncherHeader from './components/LauncherHeader.vue';
import RunningDialog from './components/RunningDialog.vue';
import SettingsModal from './components/SettingsModal.vue';
import StatusBar from './components/StatusBar.vue';
import UninstallDialog from './components/UninstallDialog.vue';
import { useTheme } from './composables/useTheme';
import { appColor, hexToRgba, shadeColor } from './lib/theme';
import { compareVersion } from './lib/version';

// 设置弹窗里两张卡片各自的保存结果；field 决定提示显示在哪张卡片下。
interface SettingsStatus {
  field: 'baseUrl' | 'installDir';
  error: boolean;
  message: string;
}

const apps = ref<AppView[]>([]);
const platform = ref('');
const arch = ref('');
const launcherVersion = ref('dev');

function initialRefreshMinutes(): number {
  try {
    const value = Number(localStorage.getItem('vakaohub-refresh-minutes'));
    if (value === 0 || value === 15 || value === 30 || value === 60) return value;
  } catch {
    // localStorage 不可用时使用默认值。
  }
  return 30;
}

const hostOsVersion = ref('');
const contentRef = ref<HTMLElement | null>(null);
let initialScrollDone = false;
const { themeMode, setThemeMode, toggleTheme } = useTheme();
const refreshMinutes = ref<number>(initialRefreshMinutes());
const settings = ref<Settings>({ baseUrl: '', installDir: '' });
const loading = ref(false);
const saving = ref(false);
const updatingAll = ref(false);
// 目录加载失败只在列表为空时占位展示；已有内容时交给底部状态栏的「服务离线」。
const catalogError = ref<UserError | null>(null);
// 每个应用一条内联提示，互不覆盖；安装失败、运行状态未知、自更新结果都走这里。
const cardNotes = ref<Record<string, CardNote>>({});
// 每个在跑安装的可取消句柄：允许同时更新多个应用，各自取消。
const installCalls = ref<Record<string, () => void>>({});
const startupNotice = ref('');
const busyKeys = ref<Record<string, boolean>>({});
const progress = ref<Record<string, Progress>>({});
const settingsVisible = ref(false);
const settingsStatus = ref<SettingsStatus | null>(null);
const uninstallTarget = ref<AppView | null>(null);
const deleteUninstallData = ref(false);
const runningTarget = ref<AppView | null>(null);
const runningMessage = ref('');
const quitting = ref(false);
// 分类不确定时让用户选择「便携复制」还是「交给安装程序」，选择后继续同一次安装。
const installChoiceTarget = ref<AppView | null>(null);
const installChoiceMessage = ref('');
const draftBaseUrl = ref('');
const draftInstallDir = ref('');
const defaultInstallDir = ref('');
// macOS 与 Windows 都可以选：macOS 是应用安装目录，Windows 是便携应用目录。
const installDirEditable = computed(() => platform.value === 'darwin' || platform.value === 'win32');
const search = ref('');
const sortBy = ref<'recent' | 'name' | 'status'>('recent');
const gridMode = ref(true);
const menuKey = ref('');
const selectedKey = ref('');
const serviceOnline = ref(true);
const lastCheckedAt = ref(0);
const now = ref(Date.now());

const updateCount = computed(() => apps.value.filter((app) => app.hasUpdate).length);

const lastCheckedLabel = computed(() => {
  if (!lastCheckedAt.value) return '尚未检查';
  const seconds = Math.max(0, Math.floor((now.value - lastCheckedAt.value) / 1000));
  if (seconds < 60) return '刚刚';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  return new Date(lastCheckedAt.value).toLocaleDateString('zh-CN');
});
const platformLabel = computed(() => {
  const labels: Record<string, string> = {
    darwin: 'macOS',
    win32: 'Windows',
    android: 'Android',
    linux: 'Linux',
  };
  return labels[platform.value] ?? platform.value ?? 'DESKTOP';
});

const visibleApps = computed(() => {
  const keyword = search.value.trim().toLowerCase();
  const list = apps.value.filter((app) => {
    if (!keyword) return true;
    return (
      app.name.toLowerCase().includes(keyword) ||
      app.appKey.toLowerCase().includes(keyword) ||
      (app.description ?? '').toLowerCase().includes(keyword)
    );
  });
  return list.sort((left, right) => {
    if (sortBy.value === 'name') return left.name.localeCompare(right.name, 'zh-CN');
    if (sortBy.value === 'status') {
      return Number(right.installed) - Number(left.installed) || Number(right.hasUpdate) - Number(left.hasUpdate);
    }
    return right.latestVersionCode - left.latestVersionCode || left.name.localeCompare(right.name, 'zh-CN');
  });
});

const appGroups = computed(() => {
  const installed = visibleApps.value.filter((app) => app.installed);
  const uninstalled = visibleApps.value.filter((app) => !app.installed);
  return [
    { key: 'installed', label: '已安装', apps: installed },
    { key: 'uninstalled', label: '未安装', apps: uninstalled },
  ].filter((group) => group.apps.length > 0);
});

const themeVars = computed<Record<string, string>>(() => {
  const app = selected.value;
  const theme = app ? appColor(app) : '#46A6C6';
  const deep = app ? shadeColor(theme, 0.3) : theme;
  return {
    '--theme': theme,
    '--theme-deep': deep,
    '--theme-soft': hexToRgba(theme, 0.26),
    '--theme-wash': hexToRgba(theme, 0.09),
    '--theme-scroll': hexToRgba(theme, 0.6),
  };
});

const selected = computed(() => apps.value.find((app) => app.appKey === selectedKey.value) ?? null);
const selectedMeta = computed<DetailMeta>(() => {
  if (!selected.value) {
    return { tagline: '', features: [], screenshots: [] };
  }
  const app = selected.value;
  return {
    tagline: app.tagline || app.description || '暂无描述',
    features: app.features?.length
      ? app.features
      : [
          { title: '统一分发', description: '由 VaKao Hub 维护版本、产物与更新记录。' },
          { title: '完整性校验', description: '下载后校验 SHA-256，macOS 额外检查代码签名。' },
          { title: '持续更新', description: '启动器会检查新版本，并在本机完成安装。' },
        ],
    screenshots: app.screenshots ?? [],
  };
});

let refreshTimer: number | undefined;
let clockTimer: number | undefined;
let offProgress: (() => void) | undefined;

function setNote(appKey: string, note: CardNote): void {
  cardNotes.value = { ...cardNotes.value, [appKey]: note };
}

function clearNote(appKey: string): void {
  if (!cardNotes.value[appKey]) return;
  const next = { ...cardNotes.value };
  delete next[appKey];
  cardNotes.value = next;
}

function dismissNote(app: AppView): void {
  clearNote(app.appKey);
}

function retryNote(app: AppView): void {
  const retry = cardNotes.value[app.appKey]?.retry;
  clearNote(app.appKey);
  void retry?.();
}

// 错误一律就近展示：传了 appKey 就落在该应用卡片里，否则只有当列表为空时才占位显示。
function showError(cause: unknown, appKey?: string, retry?: () => Promise<void>): void {
  const info = toUserError(cause);
  if (info.kind === 'canceled') return;
  if (appKey) {
    setNote(appKey, {
      kind: 'error',
      message: info.message,
      retry: info.retryable && retry ? retry : undefined,
    });
    return;
  }
  if (!apps.value.length) catalogError.value = info;
}

function showMessage(message: string, appKey?: string): void {
  if (appKey) {
    setNote(appKey, { kind: 'warning', message });
    return;
  }
  if (!apps.value.length) {
    catalogError.value = { code: 'E_INTERNAL', kind: 'internal', retryable: false, message };
  }
}

// 取消某个应用的安装：只影响这一张卡片，其它应用继续下载。
function cancelInstall(app: AppView): void {
  const cancel = installCalls.value[app.appKey];
  if (!cancel) return;
  const nextCalls = { ...installCalls.value };
  delete nextCalls[app.appKey];
  installCalls.value = nextCalls;
  const nextProgress = { ...progress.value };
  delete nextProgress[app.appKey];
  progress.value = nextProgress;
  cancel();
}

function openLog(): void {
  void LauncherService.OpenLog().catch(() => undefined);
}

async function load(): Promise<void> {
  loading.value = true;
  catalogError.value = null;
  try {
    launcherVersion.value = (await LauncherService.GetAppVersion()) || 'dev';
    settings.value = await SettingsService.GetSettings();
    if (!settingsVisible.value) draftBaseUrl.value = settings.value.baseUrl;
    const catalog = await CatalogService.GetCatalog();
    apps.value = catalog.apps ?? [];
    platform.value = catalog.platform;
    arch.value = catalog.arch;
    hostOsVersion.value = catalog.osVersion ?? '';
    serviceOnline.value = true;
  } catch (cause) {
    serviceOnline.value = false;
    // 后台自动刷新撞上安装/卸载时静默跳过，不打扰用户。
    if (toUserError(cause).kind !== 'busy') showError(cause);
  } finally {
    loading.value = false;
    lastCheckedAt.value = Date.now();
    now.value = Date.now();
    (window as Window & { __vakaoHideSplash?: () => void }).__vakaoHideSplash?.();
    if (!initialScrollDone) {
      initialScrollDone = true;
      void nextTick(() => {
        contentRef.value?.scrollTo({ top: 0 });
        window.setTimeout(() => contentRef.value?.scrollTo({ top: 0 }), 80);
      });
    }
  }
}

type InstallOutcome = 'done' | 'failed' | 'blocked' | 'canceled';

async function install(app: AppView, mode?: 'portable' | 'installer'): Promise<InstallOutcome> {
  // 启动器自身走自更新替换流程，允许在运行中执行；其它运行中的应用由主进程检测并返回 running。
  const selfUpdate = app.appKey === SELF_APP_KEY;
  if (!supportsOs(app)) {
    showMessage(`该版本需要 macOS ${app.minOs} 或更高版本，当前系统为 ${hostOsVersion.value}`, app.appKey);
    return 'failed';
  }
  menuKey.value = '';
  busyKeys.value = { ...busyKeys.value, [app.appKey]: true };
  clearNote(app.appKey);
  const call = mode
    ? InstallService.InstallWithMode(app.appKey, app.artifactID, mode)
    : InstallService.Install(app.appKey, app.artifactID);
  installCalls.value = { ...installCalls.value, [app.appKey]: () => call.cancel() };
  try {
    await call;
    // 安装成功后，之前「无法确认运行状态」之类的告警就不再成立，一并清掉。
    clearNote(app.appKey);
    if (selfUpdate) {
      restartForSelfUpdate();
      return 'done';
    }
    await load();
    return 'done';
  } catch (cause) {
    const info = toUserError(cause);
    if (info.kind === 'canceled') return 'canceled';
    if (info.kind === 'choice') {
      // 主进程已保留下载好的产物，用户选完直接继续，不会重新下载。
      installChoiceTarget.value = app;
      installChoiceMessage.value = info.message;
      return 'blocked';
    }
    if (info.kind === 'running' && !selfUpdate) {
      // 首次命中不结束进程，先让用户确认；确认后走 QuitAppsAndInstall 继续这次更新。
      runningTarget.value = app;
      runningMessage.value = info.message;
      return 'blocked';
    }
    setNote(app.appKey, {
      kind: 'error',
      message: info.message,
      retry: info.retryable ? () => install(app, mode) : undefined,
    });
    return 'failed';
  } finally {
    const nextCalls = { ...installCalls.value };
    delete nextCalls[app.appKey];
    installCalls.value = nextCalls;
    const nextBusy = { ...busyKeys.value };
    delete nextBusy[app.appKey];
    busyKeys.value = nextBusy;
    const nextProgress = { ...progress.value };
    delete nextProgress[app.appKey];
    progress.value = nextProgress;
  }
}

function cancelInstallChoice(): void {
  installChoiceTarget.value = null;
  installChoiceMessage.value = '';
}

// 用户选完安装方式后继续同一次安装；mode 会一路传到主进程的安装器分支。
function chooseInstallMode(mode: 'portable' | 'installer'): void {
  const target = installChoiceTarget.value;
  cancelInstallChoice();
  if (target) void install(target, mode);
}

function closeRunningDialog(): void {
  if (quitting.value) return;
  runningTarget.value = null;
  runningMessage.value = '';
}

// 用户确认「退出并更新」：主进程先请求正常退出，再自动继续同一次安装。
async function quitAndInstall(): Promise<void> {
  const app = runningTarget.value;
  if (!app || quitting.value) return;
  quitting.value = true;
  clearNote(app.appKey);
  busyKeys.value = { ...busyKeys.value, [app.appKey]: true };
  const call = InstallService.QuitAppsAndInstall(app.appKey, app.artifactID);
  installCalls.value = { ...installCalls.value, [app.appKey]: () => call.cancel() };
  try {
    await call;
    runningTarget.value = null;
    runningMessage.value = '';
    await load();
  } catch (cause) {
    // 退出失败时保留弹窗，把具体原因放在原处，用户可以直接重试或取消。
    runningMessage.value = toUserError(cause).message;
  } finally {
    quitting.value = false;
    const nextCalls = { ...installCalls.value };
    delete nextCalls[app.appKey];
    installCalls.value = nextCalls;
    const nextBusy = { ...busyKeys.value };
    delete nextBusy[app.appKey];
    busyKeys.value = nextBusy;
  }
}

// 自更新：新包已落到缓存，后台脚本要等本进程退出才替换 bundle，因此主动重启。
function restartForSelfUpdate(): void {
  setTimeout(() => {
    try {
      LauncherService.Quit();
    } catch {
      // 开发模式（浏览器）没有 Wails 运行时，保持窗口即可。
    }
  }, 900);
}

async function updateAll(): Promise<void> {
  if (updatingAll.value) return;
  updatingAll.value = true;
  // 启动器自身永远处于运行中，只有它允许被「全部更新」带进替换流程；
  // 排到最后，避免自身重启打断其它应用的更新。
  const targets = apps.value
    .filter((app) => app.hasUpdate && !busy(app.appKey) && (!app.running || app.appKey === SELF_APP_KEY))
    .sort((a, b) => Number(a.appKey === SELF_APP_KEY) - Number(b.appKey === SELF_APP_KEY));
  try {
    for (const app of targets) {
      const outcome = await install(app);
      // 需要用户确认（退出运行中的应用 / 选择安装方式）时先停下，避免连续弹窗把队列卡住。
      if (outcome === 'blocked') break;
    }
  } finally {
    updatingAll.value = false;
  }
}

function supportsOs(app: AppView): boolean {
  if (!app.minOs || !hostOsVersion.value) return true;
  return compareVersion(hostOsVersion.value, app.minOs) >= 0;
}

async function primaryAction(app: AppView): Promise<void> {
  if (!supportsOs(app)) {
    showMessage(`该版本需要 macOS ${app.minOs} 或更高版本，当前系统为 ${hostOsVersion.value}`, app.appKey);
    return;
  }
  if (app.installPending) {
    showMessage('安装程序已启动，正在等待系统登记；完成安装向导后点刷新即可', app.appKey);
    return;
  }
  // 已在运行的应用不执行打开；有更新时进入安装流程，由主进程决定是否需要先退出。
  if (app.running) {
    if (app.hasUpdate) {
      await install(app);
    }
    return;
  }
  if (!app.installed || app.hasUpdate) {
    await install(app);
  } else {
    await launch(app);
  }
}

function requestUninstall(app: AppView): void {
  if (app.running) return;
  menuKey.value = '';
  deleteUninstallData.value = false;
  uninstallTarget.value = app;
}

async function confirmUninstall(): Promise<void> {
  const app = uninstallTarget.value;
  uninstallTarget.value = null;
  if (!app) return;
  await uninstall(app, deleteUninstallData.value);
}

async function uninstall(app: AppView, deleteData: boolean): Promise<void> {
  if (app.running) return;
  menuKey.value = '';
  clearNote(app.appKey);
  try {
    await InstallService.Uninstall(app.appKey, deleteData);
    const next = { ...progress.value };
    delete next[app.appKey];
    progress.value = next;
    await load();
  } catch (cause) {
    showError(cause, app.appKey, () => uninstall(app, deleteData));
  }
}

async function launch(app: AppView): Promise<void> {
  menuKey.value = '';
  clearNote(app.appKey);
  try {
    await CatalogService.Launch(app.appKey);
  } catch (cause) {
    showError(cause, app.appKey, () => launch(app));
  }
}

// 手动指定主程序：用于安装器写注册表的名字对不上、或绿色软件放在别处的情况。
async function bindPath(app: AppView): Promise<void> {
  menuKey.value = '';
  clearNote(app.appKey);
  try {
    await InstallService.BindInstalledPath(app.appKey, app.name);
    await load();
  } catch (cause) {
    if (toUserError(cause).kind === 'canceled') return;
    showError(cause, app.appKey, () => bindPath(app));
  }
}

function openDetail(app: AppView): void {
  menuKey.value = '';
  selectedKey.value = app.appKey;
}

function closeDetail(): void {
  selectedKey.value = '';
  menuKey.value = '';
}

function moveDetail(offset: number): void {
  if (!selected.value) return;
  const list = visibleApps.value;
  const index = list.findIndex((app) => app.appKey === selected.value?.appKey);
  if (index < 0) return;
  const next = (index + offset + list.length) % list.length;
  selectedKey.value = list[next].appKey;
}

function prevApp(): void {
  moveDetail(-1);
}

function nextApp(): void {
  moveDetail(1);
}

function toggleMenu(app: AppView): void {
  menuKey.value = menuKey.value === app.appKey ? '' : app.appKey;
}

function closeMenu(): void {
  menuKey.value = '';
}

function openSettings(): void {
  draftBaseUrl.value = settings.value.baseUrl;
  draftInstallDir.value = settings.value.installDir ?? '';
  settingsStatus.value = null;
  settingsVisible.value = true;
  // 默认目录由主进程给，避免前端再写一份 ~/Applications。
  InstallService.GetDefaultInstallDir()
    .then((value) => {
      if (value) defaultInstallDir.value = value;
    })
    .catch(() => undefined);
}

// 目录选择器返回空串代表用户取消；选择阶段就做可写校验，错误留在弹窗里展示。
async function chooseInstallDir(): Promise<void> {
  settingsStatus.value = null;
  try {
    const dir = await InstallService.SelectInstallDir();
    if (dir) draftInstallDir.value = dir;
  } catch (cause) {
    const info = toUserError(cause);
    settingsStatus.value = {
      field: 'installDir',
      error: true,
      message: info.kind === 'canceled' ? '' : info.message,
    };
  }
}

// 保存分发服务地址：只提交这张卡片的字段，另一项沿用已保存值。
async function saveBaseUrl(): Promise<void> {
  saving.value = true;
  settingsStatus.value = null;
  try {
    const saved = await SettingsService.SaveSettings(draftBaseUrl.value, settings.value.installDir ?? '');
    settings.value = saved;
    draftBaseUrl.value = saved.baseUrl;
    settingsStatus.value = { field: 'baseUrl', error: false, message: '保存成功' };
    await load();
  } catch (cause) {
    settingsStatus.value = { field: 'baseUrl', error: true, message: toUserError(cause).message };
  } finally {
    saving.value = false;
  }
}

// 保存安装位置：同样只提交本卡片字段，成功后留在弹窗里，关闭动作仍丢弃草稿。
async function saveInstallDir(): Promise<void> {
  saving.value = true;
  settingsStatus.value = null;
  try {
    const saved = await SettingsService.SaveSettings(settings.value.baseUrl, draftInstallDir.value);
    settings.value = saved;
    draftInstallDir.value = saved.installDir ?? '';
    settingsStatus.value = { field: 'installDir', error: false, message: '保存成功' };
    await load();
  } catch (cause) {
    settingsStatus.value = { field: 'installDir', error: true, message: toUserError(cause).message };
  } finally {
    saving.value = false;
  }
}

async function reveal(app: AppView): Promise<void> {
  menuKey.value = '';
  clearNote(app.appKey);
  try {
    await CatalogService.Reveal(app.appKey);
  } catch (cause) {
    showError(cause, app.appKey, () => reveal(app));
  }
}

function openUrl(url: string): void {
  menuKey.value = '';
  LauncherService.OpenURL(url);
}

function busy(appKey: string): boolean {
  return Boolean(busyKeys.value[appKey]);
}

function saveRefreshInterval(): void {
  try {
    localStorage.setItem('vakaohub-refresh-minutes', String(refreshMinutes.value));
  } catch {
    // localStorage 不可用时只保留本次会话的频率。
  }
  restartRefreshTimer();
}

async function resetBaseUrl(): Promise<void> {
  draftBaseUrl.value = await SettingsService.GetDefaultBaseURL();
  settingsStatus.value = null;
}

// 恢复默认只改草稿，仍要点「保存」才写盘。
function resetInstallDir(): void {
  draftInstallDir.value = '';
  settingsStatus.value = null;
}

function onKeydown(event: KeyboardEvent): void {
  if ((event.metaKey || event.ctrlKey) && event.key === ',') {
    event.preventDefault();
    openSettings();
    return;
  }
  if ((event.metaKey || event.ctrlKey) && event.shiftKey && event.key.toLowerCase() === 'd') {
    event.preventDefault();
    toggleTheme();
    return;
  }
  if (event.key !== 'Escape') return;
  if (runningTarget.value) {
    closeRunningDialog();
  } else if (installChoiceTarget.value) {
    cancelInstallChoice();
  } else if (uninstallTarget.value) {
    uninstallTarget.value = null;
  } else if (settingsVisible.value) {
    settingsVisible.value = false;
  } else if (selectedKey.value) {
    closeDetail();
  }
}

function onProgress(payload: Progress): void {
  // 错误以 Promise 拒绝为准，这里只更新进度，避免 canceled 被当成错误弹窗。
  progress.value = { ...progress.value, [payload.appKey]: payload };
  // 无法确认运行状态时主进程仍会继续安装；告警挂在对应应用卡片上，不和失败提示互相覆盖。
  if (payload.phase === 'warning' && payload.message && cardNotes.value[payload.appKey]?.kind !== 'error') {
    setNote(payload.appKey, { kind: 'warning', message: payload.message });
  }
}

function restartRefreshTimer(): void {
  if (refreshTimer) window.clearInterval(refreshTimer);
  refreshTimer = undefined;
  if (refreshMinutes.value <= 0) return;
  refreshTimer = window.setInterval(
    () => {
      if (!document.hidden && !loading.value) {
        void load();
      }
    },
    refreshMinutes.value * 60 * 1000,
  );
}

// 上次自更新的结果只提示一次：读取后主进程会删除结果文件。
// 它属于启动器这个应用，落在自己的卡片里，不再占列表顶部的横幅。
async function loadUpdateNotice(): Promise<void> {
  try {
    const notice = await LauncherService.ConsumeUpdateNotice();
    if (!notice?.message) return;
    setNote(SELF_APP_KEY, {
      kind: notice.status === 'success' ? 'info' : 'warning',
      message: notice.message,
    });
  } catch {
    // 开发模式（浏览器）没有 Wails 运行时，忽略即可。
  }
}

// 启动时安装目录失效的提示只出现一次：放在设置页的安装位置卡片里，避免启动即弹条。
async function loadStartupNotice(): Promise<void> {
  try {
    startupNotice.value = await SettingsService.GetStartupNotice();
  } catch {
    // 开发模式（浏览器）没有 Wails 运行时，忽略即可。
  }
}

onMounted(() => {
  void load();
  void loadUpdateNotice().then(loadStartupNotice);
  window.addEventListener('click', closeMenu);
  window.addEventListener('keydown', onKeydown);
  offProgress = Events.On(InstallProgress, (event) => onProgress(event.data as Progress));

  restartRefreshTimer();
  clockTimer = window.setInterval(() => {
    now.value = Date.now();
  }, 30 * 1000);
});

onUnmounted(() => {
  if (refreshTimer) window.clearInterval(refreshTimer);
  if (clockTimer) window.clearInterval(clockTimer);
  window.removeEventListener('click', closeMenu);
  window.removeEventListener('keydown', onKeydown);
  offProgress?.();
});
</script>
