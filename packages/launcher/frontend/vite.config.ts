import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import wails from '@wailsio/runtime/plugins/vite';

// 配置参考：https://vitejs.dev/config/
export default defineConfig({
  plugins: [vue(), wails('./bindings')],
});
