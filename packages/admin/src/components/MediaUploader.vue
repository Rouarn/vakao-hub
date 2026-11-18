<template>
  <div class="media-uploader">
    <div v-if="items.length" class="media-grid">
      <div v-for="(item, index) in items" :key="item" class="media-item">
        <img :src="item" alt="" />
        <div class="media-actions">
          <button v-if="multiple && index > 0" title="左移" @click="move(index, -1)">←</button>
          <button v-if="multiple && index < items.length - 1" title="右移" @click="move(index, 1)">→</button>
          <button title="移除" @click="removeAt(index)">✕</button>
        </div>
      </div>
    </div>

    <n-upload
      :show-file-list="false"
      :custom-request="upload"
      accept="image/png,image/jpeg,image/webp"
      :disabled="uploading || (!multiple && items.length >= 1)"
    >
      <n-button size="small" :loading="uploading">
        {{ multiple ? '上传截图' : items.length ? '替换图标' : '上传图标' }}
      </n-button>
    </n-upload>

    <n-input
      :value="manualUrl"
      size="small"
      placeholder="或直接填外链 https://..."
      style="margin-top: 8px"
      @update:value="onManualUrl"
    />
  </div>
</template>

<script setup lang="ts">
import { NButton, NInput, NUpload, useMessage } from 'naive-ui';
import type { UploadCustomRequestOptions } from 'naive-ui';
import { computed, ref } from 'vue';
import { api } from '../api';

const props = defineProps<{
  modelValue: string | string[] | null;
  scope: 'icon' | 'screens';
  appKey: string;
  multiple?: boolean;
}>();

const emit = defineEmits<{
  'update:modelValue': [value: string | string[] | null];
}>();

const message = useMessage();
const uploading = ref(false);

const items = computed<string[]>(() => {
  if (!props.modelValue) return [];
  return Array.isArray(props.modelValue) ? props.modelValue : [props.modelValue];
});

const manualUrl = ref('');

function commit(next: string[]): void {
  if (props.multiple) emit('update:modelValue', next);
  else emit('update:modelValue', next[0] ?? null);
}

function move(index: number, offset: number): void {
  const next = [...items.value];
  const [item] = next.splice(index, 1);
  next.splice(index + offset, 0, item);
  commit(next);
}

function removeAt(index: number): void {
  const next = [...items.value];
  next.splice(index, 1);
  commit(next);
}

function onManualUrl(value: string): void {
  manualUrl.value = value;
  if (!value.trim()) return;
  commit(props.multiple ? [...items.value, value.trim()] : [value.trim()]);
  manualUrl.value = '';
}

async function upload(options: UploadCustomRequestOptions): Promise<void> {
  const file = options.file.file;
  if (!file) return;
  if (!props.appKey) {
    message.warning('先填 appKey 再上传');
    return;
  }
  uploading.value = true;
  try {
    const result = await api.media.upload(file, props.scope, props.appKey);
    commit(props.multiple ? [...items.value, result.path] : [result.path]);
    message.success('已上传');
  } catch (error) {
    message.error((error as Error).message);
  } finally {
    uploading.value = false;
  }
}
</script>

<style scoped>
.media-uploader {
  width: 100%;
}
.media-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 8px;
}
.media-item {
  position: relative;
  border: 1px solid var(--line, #e5e5e5);
  border-radius: 6px;
  overflow: hidden;
  background: #fafafa;
}
.media-item img {
  display: block;
  width: 96px;
  height: 64px;
  object-fit: contain;
}
.media-actions {
  position: absolute;
  inset: auto 0 0 0;
  display: flex;
  justify-content: center;
  gap: 2px;
  background: rgba(0, 0, 0, 0.45);
}
.media-actions button {
  border: 0;
  background: transparent;
  color: #fff;
  font-size: 11px;
  padding: 1px 5px;
  cursor: pointer;
}
</style>
