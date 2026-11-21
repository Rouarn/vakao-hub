<template>
  <div v-if="target" class="confirm-overlay" @click.self="$emit('cancel')">
    <div class="confirm-card" role="dialog" aria-modal="true">
      <p class="hud">INSTALL / {{ target.appKey }}</p>
      <h2>选择安装方式</h2>
      <p class="confirm-desc">{{ message }}</p>
      <p class="confirm-desc">
        不确定时建议选「交给安装程序」：它不会把文件复制到便携应用目录，卸载也会走系统卸载程序。
      </p>
      <div class="confirm-actions">
        <button class="ghost-btn" @click="$emit('cancel')">取消</button>
        <button class="ghost-btn" @click="$emit('choose', 'installer')">交给安装程序</button>
        <button class="action" @click="$emit('choose', 'portable')">作为便携应用安装</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { AppView } from '../../bindings/vakaohub/launcher/internal/service';

defineProps<{
  target: AppView | null;
  message: string;
}>();

defineEmits<{
  cancel: [];
  choose: [mode: 'portable' | 'installer'];
}>();
</script>
