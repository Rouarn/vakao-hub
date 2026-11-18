<template>
  <n-modal
    :show="show"
    preset="card"
    :title="editing ? '编辑应用' : '新建应用'"
    style="width: 780px; max-height: 82vh"
    :bordered="false"
    @update:show="$emit('update:show', $event)"
  >
    <template #header-extra>
      <span class="modal-hint">{{ editing ? editing.appKey : '新建后 appKey 不可修改' }}</span>
    </template>

    <n-tabs v-model:value="tab" type="line" animated pane-style="padding-top: 14px">
      <n-tab-pane name="basic" tab="基础信息">
        <div class="field-grid">
          <n-form-item label="appKey">
            <n-input v-model:value="form.appKey" :disabled="Boolean(editing)" placeholder="wiredeck" />
          </n-form-item>
          <n-form-item label="名称">
            <n-input v-model:value="form.name" placeholder="WireDeck" />
          </n-form-item>

          <n-form-item label="分类">
            <n-input v-model:value="form.category" placeholder="desktop / mobile / tool" />
          </n-form-item>
          <n-form-item label="主题色">
            <div class="theme-row">
              <n-color-picker
                :value="form.themeColor"
                :show-alpha="false"
                :modes="['hex']"
                :swatches="themeSwatches"
                style="flex: 1; min-width: 0"
                @update:value="onThemeColorChange"
              />
              <n-button size="tiny" :type="themeAuto ? 'primary' : 'default'" :disabled="themeAuto" @click="useAutoTheme">
                {{ themeAuto ? '自动' : '手动' }}
              </n-button>
            </div>
          </n-form-item>

          <n-form-item label="仓库">
            <n-input v-model:value="form.repoUrl" placeholder="https://github.com/..." />
          </n-form-item>
          <n-form-item label="主页">
            <n-input v-model:value="form.homepage" placeholder="https://..." />
          </n-form-item>

          <n-form-item label="帮助文档">
            <n-input v-model:value="form.docsUrl" placeholder="https://.../docs" />
          </n-form-item>
          <n-form-item label="版本发布">
            <n-input v-model:value="form.releaseUrl" placeholder="https://.../releases" />
          </n-form-item>

          <n-form-item label="图标" class="span-2">
            <MediaUploader v-model="form.iconUrl" scope="icon" :app-key="form.appKey" />
          </n-form-item>

          <n-form-item label="描述" class="span-2">
            <n-input
              v-model:value="form.description"
              type="textarea"
              :autosize="{ minRows: 2, maxRows: 3 }"
              placeholder="一句话说明这个软件是什么"
            />
          </n-form-item>
        </div>
      </n-tab-pane>

      <n-tab-pane name="detail" tab="详情展示">
        <div class="field-stack">
          <n-form-item label="一句话定位">
            <n-input v-model:value="form.tagline" :maxlength="160" placeholder="显示在详情页顶部，留空则用描述" />
          </n-form-item>

          <n-form-item label="功能点">
            <div class="feature-list">
              <div v-for="(item, index) in form.features" :key="index" class="feature-item">
                <n-input v-model:value="item.title" size="small" placeholder="标题" style="width: 140px; flex: none" />
                <n-input v-model:value="item.description" size="small" placeholder="描述" />
                <n-button size="tiny" quaternary @click="removeFeature(index)">✕</n-button>
              </div>
              <n-button size="small" dashed :disabled="form.features.length >= 8" @click="addFeature">
                + 添加功能点（{{ form.features.length }}/8）
              </n-button>
            </div>
          </n-form-item>

          <n-form-item label="详情截图">
            <MediaUploader v-model="form.screenshots" scope="screens" :app-key="form.appKey" multiple />
          </n-form-item>
        </div>
      </n-tab-pane>
    </n-tabs>

    <template #footer>
      <div class="modal-footer">
        <n-button @click="$emit('update:show', false)">取消</n-button>
        <n-button type="primary" :loading="saving" @click="save">保存</n-button>
      </div>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import {
  NButton,
  NColorPicker,
  NFormItem,
  NInput,
  NModal,
  NTabPane,
  NTabs,
  useMessage,
} from 'naive-ui';
import { reactive, ref, watch } from 'vue';
import { api } from '@/api';
import type { AppRow } from '@/api/types';
import MediaUploader from './MediaUploader.vue';

const props = defineProps<{
  show: boolean;
  editing: AppRow | null;
}>();

const emit = defineEmits<{
  'update:show': [value: boolean];
  saved: [];
}>();

const message = useMessage();
const saving = ref(false);
const tab = ref<'basic' | 'detail'>('basic');
const themeAuto = ref(true);
const themeSwatches = ['#5FBEDC', '#F0995F', '#A27FC2', '#EDDF55', '#D5B84A', '#171512'];
const autoPalette = ['#5FBEDC', '#F0995F', '#A27FC2', '#EDDF55'];

const form = reactive({
  appKey: '',
  name: '',
  category: '',
  iconUrl: '',
  repoUrl: '',
  homepage: '',
  docsUrl: '',
  releaseUrl: '',
  themeColor: '',
  description: '',
  tagline: '',
  features: [] as Array<{ title: string; description: string }>,
  screenshots: [] as string[],
});

function addFeature(): void {
  if (form.features.length >= 8) return;
  form.features.push({ title: '', description: '' });
}

function removeFeature(index: number): void {
  form.features.splice(index, 1);
}

function autoColorForKey(key: string): string {
  let hash = 0;
  for (const char of key) hash = (hash * 31 + char.charCodeAt(0)) >>> 0;
  return autoPalette[hash % autoPalette.length];
}

function onThemeColorChange(value: string | null): void {
  if (!value) {
    useAutoTheme();
    return;
  }
  themeAuto.value = false;
  form.themeColor = value;
}

function useAutoTheme(): void {
  themeAuto.value = true;
  form.themeColor = autoColorForKey(form.appKey);
}

function fill(row: AppRow | null): void {
  form.appKey = row?.appKey ?? '';
  form.name = row?.name ?? '';
  form.category = row?.category ?? '';
  form.iconUrl = row?.iconUrl ?? '';
  form.repoUrl = row?.repoUrl ?? '';
  form.homepage = row?.homepage ?? '';
  form.docsUrl = row?.docsUrl ?? '';
  form.releaseUrl = row?.releaseUrl ?? '';
  themeAuto.value = !row?.themeColor;
  form.themeColor = row?.themeColor ?? autoColorForKey(row?.appKey ?? '');
  form.description = row?.description ?? '';
  form.tagline = row?.tagline ?? '';
  form.features = (row?.features ?? []).map((item) => ({ ...item }));
  form.screenshots = [...(row?.screenshots ?? [])];
  tab.value = 'basic';
}

watch(
  () => [props.show, props.editing] as const,
  ([visible]) => {
    if (visible) fill(props.editing);
  },
  { immediate: true },
);

watch(
  () => form.appKey,
  (key: string) => {
    if (themeAuto.value) form.themeColor = autoColorForKey(key);
  },
);

async function save(): Promise<void> {
  if (!form.appKey || !form.name) {
    message.warning('appKey 与名称必填');
    return;
  }
  saving.value = true;
  try {
    const payload = {
      appKey: form.appKey,
      name: form.name,
      // 可选字段留空时发送 null（而不是 undefined），否则 PATCH 会当作“不修改”而保留旧值
      category: form.category.trim() || null,
      iconUrl: form.iconUrl.trim() || null,
      repoUrl: form.repoUrl.trim() || null,
      homepage: form.homepage.trim() || null,
      docsUrl: form.docsUrl.trim() || null,
      releaseUrl: form.releaseUrl.trim() || null,
      themeColor: themeAuto.value ? null : form.themeColor || null,
      description: form.description.trim() || null,
      tagline: form.tagline.trim() || null,
      features: form.features
        .filter((item) => item.title.trim())
        .map((item) => ({
          title: item.title.trim(),
          description: item.description.trim(),
        })),
      screenshots: form.screenshots,
    };
    if (props.editing) {
      await api.apps.update(props.editing.id, payload);
      message.success('已更新');
    } else {
      await api.apps.create(payload);
      message.success('已创建');
    }
    emit('update:show', false);
    emit('saved');
  } catch (error) {
    message.error((error as Error).message);
  } finally {
    saving.value = false;
  }
}
</script>

<style scoped>
.modal-hint {
  font-size: 12px;
  opacity: 0.55;
}
.field-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  column-gap: 18px;
}
.field-grid :deep(.n-form-item) {
  margin-bottom: 14px;
}
.span-2 {
  grid-column: span 2;
}
.field-stack :deep(.n-form-item) {
  margin-bottom: 14px;
}
.theme-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}
.feature-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}
.feature-item {
  display: flex;
  gap: 8px;
  align-items: center;
}
.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
