/// <reference types="vite/client" />
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './index.css'
import { applyTheme, getStoredTheme } from './lib/theme'

applyTheme(getStoredTheme())

// 开发期降噪：Semi UI 2.x 内部的 ReactResizeObserver（Typography / Breadcrumb /
// Tooltip 都会用到）仍调用 ReactDOM.findDOMNode，React 18 会刷屏弃用警告。
// 这是组件库内部实现，我们不改它，只在 dev 过滤掉这一条，其它警告照常输出。
if (import.meta.env.DEV) {
  const originalError = console.error
  console.error = (...args: unknown[]) => {
    const first = args[0]
    if (typeof first === 'string' && first.includes('is deprecated in StrictMode')) return
    if (typeof first === 'string' && first.includes('findDOMNode is deprecated')) return
    originalError(...args)
  }
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
