import react from '@vitejs/plugin-react'
import semiTheming from '@douyinfe/semi-vite-plugin'
import { defineConfig } from 'vite'

// 项目没装 @types/node，这里只需要 process.env 一处
declare const process: { env: Record<string, string | undefined> }

// 联调环境用：WIKIATLAS_DEV_PORT / WIKIATLAS_API_TARGET 可起第二套前端指向测试后端；
// 不设置时行为和主实例一致（5173 → 8080）。
const devPort = Number(process.env.WIKIATLAS_DEV_PORT) || 5173
const apiTarget = process.env.WIKIATLAS_API_TARGET || 'http://localhost:8080'

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
    port: devPort,
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
})
