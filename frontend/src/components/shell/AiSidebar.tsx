import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'

interface Props {
  width: number
  onWidthChange: (width: number) => void
  children: ReactNode
}

/**
 * 贴右侧的 AI 侧栏（可拖左边缘改宽度）。
 *
 * Semi 的官方 AI 组件只有 AIChatDialogue / AIChatInput，没有 AI 侧栏；
 * SideSheet 是带遮罩的抽屉、SideBar 是导航栏，都不适合常驻对话，
 * 所以这里用布局 + 一个拖拽手柄实现，颜色全走 Semi token。
 */
export default function AiSidebar({ width, onWidthChange, children }: Props) {
  const [dragging, setDragging] = useState(false)
  const frame = useRef(0)

  const onMouseDown = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    e.preventDefault()
    setDragging(true)
  }, [])

  // 拖拽期间监听 window：鼠标移出这 6px 手柄也照样跟手
  useEffect(() => {
    if (!dragging) return
    const onMove = (e: MouseEvent) => {
      const next = window.innerWidth - e.clientX
      if (frame.current) cancelAnimationFrame(frame.current)
      frame.current = requestAnimationFrame(() => onWidthChange(next))
    }
    const onUp = () => {
      if (frame.current) cancelAnimationFrame(frame.current)
      frame.current = 0
      setDragging(false)
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    return () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }
  }, [dragging, onWidthChange])

  // 拖拽期间禁掉选中与光标闪烁，避免拖出蓝色选区
  useEffect(() => {
    if (!dragging) return
    const prev = document.body.style.userSelect
    document.body.style.userSelect = 'none'
    return () => {
      document.body.style.userSelect = prev
    }
  }, [dragging])

  return (
    <aside
      className={`ai-side${dragging ? ' is-dragging' : ''}`}
      style={{ width }}
      aria-label="Altas 侧栏"
    >
      <div
        className="ai-side-handle"
        role="separator"
        aria-orientation="vertical"
        aria-label="拖拽调整宽度"
        onMouseDown={onMouseDown}
        onDoubleClick={() => onWidthChange(460)}
      />
      {children}
    </aside>
  )
}
