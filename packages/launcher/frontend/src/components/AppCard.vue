<template>
  <article
    class="app-card"
    :class="{ 'is-list': !gridMode, 'is-installed': app.installed }"
    :style="{ '--app-color': appColor(app) }"
    @click="$emit('open-detail', app)"
  >
    <div class="card-top">
      <span class="version">
        <template v-if="app.installed && app.hasUpdate">
          {{ app.installedVersion }} <b>→</b> {{ app.latestVersion }}
        </template>
        <template v-else>v{{ app.latestVersion }}</template>
      </span>
      <a
        v-if="app.repoUrl"
        class="repo"
        :href="app.repoUrl"
        rel="noreferrer"
        @click.stop.prevent="$emit('open-url', app.repoUrl)"
      >
        GITHUB ↗
      </a>
    </div>

    <div class="card-head">
      <div
        class="app-icon"
        :class="{ 'with-image': app.iconUrl }"
        :style="app.iconUrl ? {} : { background: appColor(app) }"
      >
        <img v-if="app.iconUrl" :src="app.iconUrl" :alt="app.name" />
        <span v-else>{{ initial(app.name) }}</span>
      </div>
      <div class="app-title">
        <div class="title-line">
          <h2>{{ app.name }}</h2>
          <span class="provider-badge">由 VAKAO 提供</span>
        </div>
      </div>
      <span v-if="app.hasUpdate" class="state update">有可用更新</span>
      <span v-else-if="app.installPending" class="state update">等待安装完成</span>
      <span v-else-if="app.running" class="state running">当前运行</span>
      <span v-else-if="app.installed" class="state installed">已安装</span>
      <span v-else class="state available">可安装</span>
    </div>

    <p class="desc">{{ app.description || '暂无描述' }}</p>

    <div class="card-meta">
      <span>最后更新：{{ formatDate(app.latestPublishedAt) }}</span>
      <span class="format">{{ app.artifactFormat.toUpperCase() }} · {{ formatBytes(app.artifactSize) }}</span>
    </div>

    <div v-if="progress && busy" class="progress">
      <div class="bar"><span :style="{ width: percentOf(progress.percent) }"></span></div>
      <div class="progress-foot">
        <span class="hint">{{ progress.message }}</span>
        <button class="progress-cancel" @click.stop="$emit('cancel-install', app)">取消</button>
      </div>
    </div>

    <div v-if="note" class="card-note" :class="note.kind">
      <span class="note-text">{{ note.message }}</span>
      <button v-if="note.retry" class="note-action" @click.stop="$emit('retry-note', app)">重试</button>
      <button v-else class="note-action" title="知道了" @click.stop="$emit('dismiss-note', app)">×</button>
    </div>

    <p v-if="app.duplicates?.length" class="notice">检测到 {{ app.duplicates.length + 1 }} 份安装，请保留一份。</p>

    <div class="card-bottom">
      <button
        class="action"
        :class="{ 'is-update': app.hasUpdate }"
        :disabled="busy || updatingAll || !supportsOs || app.installPending || (app.running && !app.hasUpdate)"
        @click.stop="$emit('primary-action', app)"
      >
        <template v-if="app.hasUpdate">更新至 {{ app.latestVersion }}</template>
        <template v-else-if="app.installPending">等待安装完成</template>
        <template v-else-if="app.running">当前运行</template>
        <template v-else-if="!app.installed">安装</template>
        <template v-else>打开</template>
      </button>
      <button class="more" title="更多" @click.stop="$emit('toggle-menu', app)">⋯</button>
    </div>

    <div v-if="menuOpen" class="card-menu" @click.stop>
      <button @click="$emit('open-detail', app)">查看详情</button>
      <button v-if="app.installed && !app.running" @click="$emit('launch', app)">打开</button>
      <button v-if="app.installed" :disabled="!app.installPathKnown" @click="$emit('reveal', app)">
        {{ isWindows ? '在资源管理器中显示' : '在访达中显示' }}
      </button>
      <button v-if="isWindows && (!app.installed || !app.installPathKnown)" @click="$emit('bind-path', app)">
        手动指定程序位置…
      </button>
      <button v-if="app.installed && !app.running" @click="$emit('install', app)">重新安装</button>
      <button v-if="app.homepage" @click="$emit('open-url', app.homepage)">打开主页</button>
      <button v-if="app.repoUrl" @click="$emit('open-url', app.repoUrl)">查看源码</button>
      <div v-if="app.installed && !app.running" class="menu-divider"></div>
      <button v-if="app.installed && !app.running" class="danger" @click="$emit('request-uninstall', app)">
        卸载…
      </button>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { AppView, Progress } from '../../bindings/vakaohub/launcher/internal/service';
import { appColor } from '../lib/theme';
import { formatBytes, formatDate, initial, percentOf } from '../lib/format';
import type { CardNote } from '../lib/notes';

const props = defineProps<{
  app: AppView;
  gridMode: boolean;
  busy: boolean;
  updatingAll: boolean;
  supportsOs: boolean;
  menuOpen: boolean;
  platformLabel: string;
  progress?: Progress;
  note?: CardNote;
}>();

const isWindows = computed(() => props.platformLabel === 'Windows');

defineEmits<{
  'open-detail': [app: AppView];
  'primary-action': [app: AppView];
  'toggle-menu': [app: AppView];
  launch: [app: AppView];
  reveal: [app: AppView];
  install: [app: AppView];
  'bind-path': [app: AppView];
  'open-url': [url: string];
  'request-uninstall': [app: AppView];
  'cancel-install': [app: AppView];
  'retry-note': [app: AppView];
  'dismiss-note': [app: AppView];
}>();
</script>
