<template>
  <div class="media">
    <!-- 图标：单个方块，点击上传，悬停出操作 -->
    <template v-if="!multiple">
      <div class="icon-slot" :class="{ empty: !items.length }" @click="pick">
        <img v-if="items.length" :src="items[0]" alt="" />
        <span v-else class="hint">点击上传</span>
        <div v-if="items.length" class="slot-actions">
          <span @click.stop="pick">更换</span>
          <span class="sep">·</span>
          <span @click.stop="removeAt(0)">移除</span>
        </div>
      </div>
    </template>

    <!-- 截图：缩略图网格 + 加号卡片 -->
    <template v-else>
      <div class="shots">
        <div v-for="(item, index) in items" :key="item" class="shot">
          <img :src="item" alt="" />
          <div class="shot-actions">
            <button v-if="index > 0" title="左移" @click="move(index, -1)">‹</button>
            <button v-if="index < items.length - 1" title="右移" @click="move(index, 1)">›</button>
            <button title="移除" @click="removeAt(index)">✕</button>
          </div>
        </div>
        <button v-if="items.length < 12" class="shot add" :disabled="uploading" @click="pick">
          <span>{{ uploading ? '上传中' : '+' }}</span>
        </button>
      </div>
    </template>

    <input
      ref="input"
      type="file"
      :multiple="multiple"
      accept="image/png,image/jpeg,image/webp"
      hidden
      @change="onPick"
    />

    <div class="foot">
      <button class="link-toggle" type="button" @click="showUrl = !showUrl">
        {{ showUrl ? '收起外链' : '用外链' }}
      </button>
      <span class="tip">PNG / JPEG / WebP，单张 ≤ 5MB</span>
    </div>

    <n-input
      v-if="showUrl"
      size="small"
      placeholder="粘贴图片地址，回车添加"
      style="margin-top: 8px"
      @keyup.enter="addUrl"
    />
  </div>
</template>

<script setup lang="ts">
import { NInput, useMessage } from 'naive-ui';
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
const showUrl = ref(false);
const input = ref<HTMLInputElement | null>(null);

const items = computed<string[]>(() => {
  if (!props.modelValue) return [];
  return Array.isArray(props.modelValue) ? props.modelValue : [props.modelValue];
});

function commit(next: string[]): void {
  if (props.multiple) emit('update:modelValue', next);
  else emit('update:modelValue', next[0] ?? null);
}

function pick(): void {
  if (!props.appKey) {
    message.warning('先填 appKey 再上传');
    return;
  }
  input.value?.click();
}

async function onPick(event: Event): Promise<void> {
  const target = event.target as HTMLInputElement;
  const files = Array.from(target.files ?? []);
  target.value = '';
  if (!files.length) return;

  uploading.value = true;
  try {
    const uploaded: string[] = [];
    for (const file of props.multiple ? files : files.slice(0, 1)) {
      const result = await api.media.upload(file, props.scope, props.appKey);
      uploaded.push(result.path);
    }
    commit(props.multiple ? [...items.value, ...uploaded] : [uploaded[0]]);
  } catch (error) {
    message.error((error as Error).message);
  } finally {
    uploading.value = false;
  }
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

function addUrl(event: KeyboardEvent): void {
  const value = (event.target as HTMLInputElement).value.trim();
  if (!value) return;
  commit(props.multiple ? [...items.value, value] : [value]);
  (event.target as HTMLInputElement).value = '';
}
</script>

<style scoped>
.media {
  width: 100%;
}
.icon-slot {
  position: relative;
  width: 92px;
  height: 92px;
  border-radius: 14px;
  border: 1px solid var(--line);
  background: var(--line-soft);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  overflow: hidden;
  transition: border-color 0.15s ease;
}
.icon-slot:hover {
  border-color: var(--line-strong);
}
.icon-slot.empty {
  border-style: dashed;
}
.icon-slot img {
  width: 74px;
  height: 74px;
  object-fit: contain;
}
.hint {
  font-size: 12px;
  opacity: 0.45;
}
.slot-actions {
  position: absolute;
  inset: auto 0 0 0;
  display: flex;
  justify-content: center;
  gap: 4px;
  padding: 4px 0;
  font-size: 11px;
  color: var(--nav-text);
  /* 遮罩压在缩略图上，两套主题下都要保持深色，故用导航墨色加透明度 */
  background: color-mix(in srgb, var(--nav-bg) 62%, transparent);
  opacity: 0;
  transition: opacity 0.15s ease;
}
.slot-actions .sep {
  opacity: 0.5;
}
.icon-slot:hover .slot-actions {
  opacity: 1;
}

.shots {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.shot {
  position: relative;
  width: 104px;
  height: 66px;
  border-radius: 10px;
  border: 1px solid var(--line);
  background: var(--line-soft);
  overflow: hidden;
}
.shot img {
  width: 100%;
  height: 100%;
  object-fit: contain;
}
.shot-actions {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  background: color-mix(in srgb, var(--nav-bg) 42%, transparent);
  opacity: 0;
  transition: opacity 0.15s ease;
}
.shot:hover .shot-actions {
  opacity: 1;
}
.shot-actions button {
  border: 0;
  background: var(--nav-hover);
  color: var(--nav-text);
  width: 22px;
  height: 22px;
  border-radius: 50%;
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
}
.shot.add {
  display: flex;
  align-items: center;
  justify-content: center;
  border-style: dashed;
  color: var(--faint);
  font-size: 18px;
  cursor: pointer;
}
.shot.add:hover {
  border-color: var(--line-strong);
  color: var(--muted);
}
.foot {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 8px;
}
.link-toggle {
  border: 0;
  padding: 0;
  background: transparent;
  color: inherit;
  font-size: 12px;
  opacity: 0.55;
  text-decoration: underline;
  text-underline-offset: 2px;
  cursor: pointer;
}
.link-toggle:hover {
  opacity: 0.85;
}
.tip {
  font-size: 11px;
  opacity: 0.35;
}
</style>
