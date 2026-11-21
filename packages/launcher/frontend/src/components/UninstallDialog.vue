<template>
  <div v-if="target" class="confirm-overlay" @click.self="$emit('close')">
    <div class="confirm-card" role="dialog" aria-modal="true">
      <p class="hud">UNINSTALL / {{ target.appKey }}</p>
      <h2>卸载 {{ target.name }}？</h2>
      <p v-if="isInstaller" class="confirm-desc">将打开系统卸载程序，卸载位置和卸载内容由它自己决定。</p>
      <p v-else class="confirm-desc">
        将删除 <code>{{ target.installPath }}</code>。
      </p>
      <label v-if="showDeleteData" class="confirm-option">
        <input v-model="deleteData" type="checkbox" />
        <span>同时删除应用数据与设置（不可恢复）</span>
      </label>
      <div class="confirm-actions">
        <button class="ghost-btn" @click="$emit('close')">取消</button>
        <button class="action confirm-danger" @click="$emit('confirm')">卸载</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { AppView } from '../../bindings/vakaohub/launcher/internal/service';

const props = defineProps<{
  target: AppView | null;
  platformLabel: string;
}>();

const deleteData = defineModel<boolean>('deleteData', { required: true });

// 安装器类由系统卸载程序接管；Windows 也不支持删除应用数据，勾选项只会让人误解。
const isInstaller = computed(() => props.target?.installKind === 'installer');
const showDeleteData = computed(() => !isInstaller.value && props.platformLabel !== 'Windows');

defineEmits<{
  close: [];
  confirm: [];
}>();
</script>
