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
      <div v-if="errorInfo && errorInfo.kind !== 'canceled'" class="banner">
        <span>{{ errorInfo.message }}</span>
        <button v-if="activeInstall" @click="cancelInstall">取消安装</button>
        <button v-else-if="canRetry" @click="retryLast">重试</button>
        <button @click="openLog">打开日志</button>
      </div>

      <div v-if="loading && !apps.length" class="empty">正在读取软件目录…</div>
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
              :progress="progress[app.appKey]"
              @open-detail="openDetail"
              @primary-action="primaryAction"
              @toggle-menu="toggleMenu"
              @launch="launch"
              @reveal="reveal"
              @install="install"
              @open-url="openUrl"
              @request-uninstall="requestUninstall"
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
      @close="closeDetail"
      @prev="prevApp"
      @next="nextApp"
      @primary-action="primaryAction"
      @toggle-menu="toggleMenu"
      @launch="launch"
      @reveal="reveal"
      @install="install"
      @open-url="openUrl"
      @request-uninstall="requestUninstall"
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
      :settings-error="settingsError"
      @close="settingsVisible = false"
      @save="saveSettings"
      @reset="resetBaseUrl"
      @select-install-dir="chooseInstallDir"
      @reset-install-dir="draftInstallDir = ''"
      @theme-mode="setThemeMode"
      @refresh-changed="saveRefreshInterval"
      @refresh-now="load"
      @open-url="openUrl"
    />

    <UninstallDialog
      v-model:delete-data="deleteUninstallData"
      :target="uninstallTarget"
      @close="uninstallTarget = null"
      @confirm="confirmUninstall"
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

import AppCard from './components/AppCard.vue';
import AppDetailPanel from './components/AppDetailPanel.vue';
import LauncherHeader from './components/LauncherHeader.vue';
import SettingsModal from './components/SettingsModal.vue';
import StatusBar from './components/StatusBar.vue';
import UninstallDialog from './components/UninstallDialog.vue';
import { useTheme } from './composables/useTheme';
import { appColor, hexToRgba, shadeColor } from './lib/theme';
import { compareVersion } from './lib/version';

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
const errorInfo = ref<UserError | null>(null);
const activeInstall = ref<{ appKey: string; cancel: () => void } | null>(null);
const canRetry = ref(false);
let retryAction: (() => Promise<void>) | null = null;
const busyKeys = ref<Record<string, boolean>>({});
const progress = ref<Record<string, Progress>>({});
const settingsVisible = ref(false);
const settingsError = ref('');
const uninstallTarget = ref<AppView | null>(null);
const deleteUninstallData = ref(false);
const draftBaseUrl = ref('');
const draftInstallDir = ref('');
const defaultInstallDir = ref('~/Applications');
// Windows 的安装位置由安装包决定，设置项只展示不可编辑。
const installDirEditable = computed(() => platform.value === 'darwin');
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

function clearError(): void {
  errorInfo.value = null;
  canRetry.value = false;
  retryAction = null;
}

function showError(cause: unknown, retry?: () => Promise<void>): void {
  const info = toUserError(cause);
  if (info.kind === 'canceled') return;
  errorInfo.value = info;
  retryAction = info.retryable && retry ? retry : null;
  canRetry.value = Boolean(retryAction);
}

function showMessage(message: string): void {
  errorInfo.value = { code: 'E_INTERNAL', kind: 'internal', retryable: false, message };
  canRetry.value = false;
  retryAction = null;
}

function retryLast(): void {
  void retryAction?.();
}

function cancelInstall(): void {
  const current = activeInstall.value;
  activeInstall.value = null;
  current?.cancel();
}

function openLog(): void {
  void LauncherService.OpenLog().catch(() => undefined);
}

async function load(): Promise<void> {
  loading.value = true;
  clearError();
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
    if (toUserError(cause).kind !== 'busy') showError(cause, load);
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

async function install(app: AppView): Promise<void> {
  // 其它应用运行中不能覆盖安装；启动器自身走自更新替换流程，允许在运行中执行。
  const selfUpdate = app.appKey === SELF_APP_KEY;
  if (app.running && !selfUpdate) return;
  if (!supportsOs(app)) {
    showMessage(`该版本需要 macOS ${app.minOs} 或更高版本，当前系统为 ${hostOsVersion.value}`);
    return;
  }
  menuKey.value = '';
  busyKeys.value = { ...busyKeys.value, [app.appKey]: true };
  clearError();
  const call = InstallService.Install(app.appKey, app.artifactID);
  activeInstall.value = { appKey: app.appKey, cancel: () => call.cancel() };
  try {
    await call;
    if (selfUpdate) {
      restartForSelfUpdate();
      return;
    }
    await load();
  } catch (cause) {
    showError(cause, () => install(app));
  } finally {
    if (activeInstall.value?.appKey === app.appKey) activeInstall.value = null;
    const next = { ...busyKeys.value };
    delete next[app.appKey];
    busyKeys.value = next;
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
    .filter((app) => app.hasUpdate && (!app.running || app.appKey === SELF_APP_KEY))
    .sort((a, b) => Number(a.appKey === SELF_APP_KEY) - Number(b.appKey === SELF_APP_KEY));
  for (const app of targets) {
    await install(app);
  }
  updatingAll.value = false;
}

function supportsOs(app: AppView): boolean {
  if (!app.minOs || !hostOsVersion.value) return true;
  return compareVersion(hostOsVersion.value, app.minOs) >= 0;
}

async function primaryAction(app: AppView): Promise<void> {
  if (!supportsOs(app)) {
    showMessage(`该版本需要 macOS ${app.minOs} 或更高版本，当前系统为 ${hostOsVersion.value}`);
    return;
  }
  // 已在运行的应用不再执行打开；启动器自身只在「有更新」时走自更新替换。
  if (app.running) {
    if (app.appKey === SELF_APP_KEY && app.hasUpdate) {
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
  clearError();
  try {
    await InstallService.Uninstall(app.appKey, deleteData);
    const next = { ...progress.value };
    delete next[app.appKey];
    progress.value = next;
    await load();
  } catch (cause) {
    showError(cause, () => uninstall(app, deleteData));
  }
}

async function launch(app: AppView): Promise<void> {
  menuKey.value = '';
  clearError();
  try {
    await CatalogService.Launch(app.appKey);
  } catch (cause) {
    showError(cause, () => launch(app));
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
  settingsError.value = '';
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
  settingsError.value = '';
  try {
    const dir = await InstallService.SelectInstallDir();
    if (dir) draftInstallDir.value = dir;
  } catch (cause) {
    const info = toUserError(cause);
    settingsError.value = info.kind === 'canceled' ? '' : info.message;
  }
}

async function saveSettings(): Promise<void> {
  saving.value = true;
  clearError();
  settingsError.value = '';
  try {
    settings.value = await SettingsService.SaveSettings(draftBaseUrl.value, draftInstallDir.value);
    settingsVisible.value = false;
    await load();
  } catch (cause) {
    settingsError.value = toUserError(cause).message;
  } finally {
    saving.value = false;
  }
}

function reveal(app: AppView): Promise<void> {
  menuKey.value = '';
  clearError();
  return CatalogService.Reveal(app.appKey).catch((cause: unknown) => {
    showError(cause, () => reveal(app));
  });
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
  if (uninstallTarget.value) {
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

onMounted(() => {
  void load();
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
