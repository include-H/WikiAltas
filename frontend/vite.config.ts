import react from '@vitejs/plugin-react'
import semiTheming from '@douyinfe/semi-vite-plugin'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [
    react(),
    // DSM 官方主题：飞书品牌主题包（@semi-bot/semi-theme-feishu）。
    // 它是一套 --semi-* token 覆盖（主色 51,112,255 / 小圆角 4px / 中性色盘），
    // 由插件在构建期注入 Semi 的 CSS，界面观感对齐 VISUAL_SPEC 的母本。
    semiTheming({
      theme: '@semi-bot/semi-theme-feishu',
    }),
  ],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})
