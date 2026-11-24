<template>
  <div v-if="visible" class="settings-overlay" @click.self="$emit('close')">
    <section class="settings-window" role="dialog" aria-modal="true" aria-label="设置">
      <header class="settings-head">
        <img :src="starUrl" class="settings-icon" alt="" />
        <div class="settings-title">
          <h2>设置</h2>
          <p class="hud">VAKAO HUB · LAUNCHER {{ launcherVersion }}</p>
        </div>
        <button class="icon-btn settings-close" title="关闭" @click="$emit('close')">×</button>
      </header>

      <div class="settings-body">
        <div class="settings-col">
          <section class="settings-card">
            <p class="hud">外观</p>
            <div class="segmented">
              <button :class="{ active: themeMode === 'system' }" @click="$emit('theme-mode', 'system')">
                跟随系统
              </button>
              <button :class="{ active: themeMode === 'dark' }" @click="$emit('theme-mode', 'dark')">深色</button>
              <button :class="{ active: themeMode === 'light' }" @click="$emit('theme-mode', 'light')">浅色</button>
            </div>
            <p class="settings-note">跟随系统时会随 macOS 外观自动切换。</p>
          </section>

          <section class="settings-card">
            <p class="hud">更新</p>
            <div class="settings-row">
              <div>
                <strong>检查更新</strong>
                <span>自动检查软件目录的频率</span>
              </div>
              <select v-model.number="refreshMinutes" class="settings-select" @change="$emit('refresh-changed')">
                <option :value="0">手动</option>
                <option :value="15">每 15 分钟</option>
                <option :value="30">每 30 分钟</option>
                <option :value="60">每 1 小时</option>
              </select>
            </div>
            <div class="settings-actions">
              <button class="settings-secondary" :disabled="loading" @click="$emit('refresh-now')">立即检查</button>
              <span class="service-state" :class="{ offline: !serviceOnline }">
                <i></i>{{ serviceOnline ? '分发服务在线' : '分发服务离线' }}
              </span>
            </div>
          </section>

          <section class="settings-card">
            <p class="hud">分发服务</p>
            <div class="settings-row settings-row-start">
              <div>
                <strong>服务地址</strong>
                <span>安装、更新与校验都从这里读取</span>
              </div>
            </div>
            <input
              v-model="baseUrl"
              class="settings-input"
              spellcheck="false"
              placeholder="https://dist.woyuxiaji.cn/api/v1"
            />
            <p v-if="statusFor('baseUrl')" class="settings-status" :class="{ error: statusFor('baseUrl')?.error }">
              {{ statusFor('baseUrl')?.message }}
            </p>
            <div class="settings-actions">
              <button class="settings-secondary" @click="$emit('reset')">恢复默认</button>
              <button class="settings-primary" :disabled="saving" @click="$emit('save-base-url')">保存</button>
            </div>
          </section>
        </div>

        <div class="settings-col">
          <section class="settings-card">
            <p class="hud">安装位置</p>
            <div class="settings-row settings-row-start">
              <div>
                <strong>{{ isWindows ? '便携应用目录' : '应用安装目录' }}</strong>
                <span v-if="isWindows">没有自带安装程序的应用（单文件 exe、zip 便携包）会安装到这里</span>
                <span v-else>新安装的应用会放在这里，已安装的应用不会移动</span>
              </div>
            </div>
            <div class="settings-path-row">
              <input
                v-model="installDir"
                class="settings-input settings-path-input"
                spellcheck="false"
                :disabled="!installDirEditable"
                :placeholder="defaultInstallDir"
              />
              <button class="settings-secondary" :disabled="!installDirEditable" @click="$emit('select-install-dir')">
                选择…
              </button>
            </div>
            <p
              v-if="statusFor('installDir')"
              class="settings-status"
              :class="{ error: statusFor('installDir')?.error }"
            >
              {{ statusFor('installDir')?.message }}
            </p>
            <p v-else-if="startupNotice" class="settings-status error">{{ startupNotice }}</p>
            <div v-if="installDirEditable" class="settings-actions">
              <button class="settings-secondary" @click="$emit('reset-install-dir')">恢复默认</button>
              <button class="settings-primary" :disabled="saving" @click="$emit('save-install-dir')">保存</button>
            </div>
            <p v-if="!installDirEditable" class="settings-note">当前平台不支持修改安装目录。</p>
            <p v-else-if="isWindows" class="settings-note">
              msi 和带安装向导的 exe 由它们自己的安装程序决定位置，不受此项影响；已安装的应用不会自动移动。
            </p>
            <p v-else class="settings-note">
              可选用户主目录内或 /Applications 下的目录；桌面、文稿等受保护目录会触发系统授权。
            </p>
          </section>

          <section class="settings-card">
            <p class="hud">关于</p>
            <div class="about-mark">
              <strong>VaKao Hub</strong>
              <span>软件中心启动器</span>
            </div>
            <div class="about-grid">
              <div>
                <span class="hud">版本</span><strong>{{ launcherVersion }}</strong>
              </div>
              <div>
                <span class="hud">系统</span><strong>{{ platformLabel }} {{ hostOsVersion }}</strong>
              </div>
              <div>
                <span class="hud">架构</span><strong>{{ arch }}</strong>
              </div>
            </div>
            <div class="settings-links">
              <button @click="$emit('open-log')">打开日志</button>
              <button @click="$emit('open-url', 'https://github.com/Rouarn/vakao-hub')">GitHub ↗</button>
              <button @click="$emit('open-url', 'https://github.com/Rouarn/vakao-hub/releases')">版本发布 ↗</button>
              <button @click="$emit('open-url', 'https://github.com/Rouarn/vakao-hub#readme')">帮助文档 ↗</button>
            </div>
          </section>
        </div>
      </div>

      <footer class="settings-foot">
        <span class="hud">VAKAO HUB / {{ launcherVersion }}</span>
        <button class="settings-primary" @click="$emit('close')">完成</button>
      </footer>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import starUrl from '../assets/star.png';
import type { ThemeMode } from '../composables/useTheme';

interface SettingsStatus {
  field: 'baseUrl' | 'installDir';
  error: boolean;
  message: string;
}

const props = defineProps<{
  visible: boolean;
  themeMode: ThemeMode;
  serviceOnline: boolean;
  saving: boolean;
  loading: boolean;
  platformLabel: string;
  hostOsVersion: string;
  arch: string;
  launcherVersion: string;
  defaultInstallDir: string;
  installDirEditable: boolean;
  settingsStatus: SettingsStatus | null;
  startupNotice: string;
}>();

// 只显示属于当前卡片的保存结果，两张卡片的反馈互不干扰。
function statusFor(field: SettingsStatus['field']): SettingsStatus | null {
  return props.settingsStatus?.field === field ? props.settingsStatus : null;
}

const isWindows = computed(() => props.platformLabel === 'Windows');

const baseUrl = defineModel<string>('baseUrl', { required: true });
const installDir = defineModel<string>('installDir', { required: true });
const refreshMinutes = defineModel<number>('refreshMinutes', { required: true });

defineEmits<{
  close: [];
  'save-base-url': [];
  'save-install-dir': [];
  reset: [];
  'select-install-dir': [];
  'reset-install-dir': [];
  'theme-mode': [mode: ThemeMode];
  'refresh-changed': [];
  'refresh-now': [];
  'open-url': [url: string];
  'open-log': [];
}>();
</script>
