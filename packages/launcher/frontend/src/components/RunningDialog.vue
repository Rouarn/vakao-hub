<template>
  <div v-if="target" class="confirm-overlay" @click.self="!busy && $emit('close')">
    <div class="confirm-card" role="dialog" aria-modal="true">
      <p class="hud">RUNNING / {{ target.appKey }}</p>
      <h2>需要先退出 {{ target.name }}</h2>
      <p class="confirm-desc">{{ message }}</p>
      <p class="confirm-desc">退出后会直接继续这次更新，未保存的内容可能丢失。</p>
      <div class="confirm-actions">
        <button class="ghost-btn" :disabled="busy" @click="$emit('close')">取消</button>
        <button class="action" :disabled="busy" @click="$emit('confirm')">
          {{ busy ? '正在退出…' : '退出并更新' }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { AppView } from '../../bindings/vakaohub/launcher/internal/service';

defineProps<{
  target: AppView | null;
  message: string;
  busy: boolean;
}>();

defineEmits<{
  close: [];
  confirm: [];
}>();
</script>
