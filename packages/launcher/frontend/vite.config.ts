import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import wails from '@wailsio/runtime/plugins/vite';

// 配置参考：https://vitejs.dev/config/
export default defineConfig({
  // Wails v3 的开发代理固定用 IPv4 回连 127.0.0.1，而 Node 26 会把 localhost 解析成
  // IPv6 ::1（只监听 [::1]:9245），导致代理连接被拒、窗口整片白屏。开发服务器必须绑 IPv4。
  server: { host: '127.0.0.1' },
  plugins: [vue(), wails('./bindings')],
});
