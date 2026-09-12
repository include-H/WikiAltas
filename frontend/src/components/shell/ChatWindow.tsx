import { useCallback, useEffect, useRef } from 'react'
import type { ReactNode } from 'react'
import { DragMove, Resizable } from '@douyinfe/semi-ui'
import { useAppStore } from '../../lib/store'
import type { ChatRect } from '../../lib/store'

/**
 * Altas 悬浮窗：浮在页面之上、可拖可缩，像桌面应用的窗口。
 *
 * 为什么不再用"贴右侧侧栏"：Semi 的 AIChat 系列是按**整页聊天**设计的
 * （官方 demo 是 `height: calc(100vh - 32px)` 的全宽容器），组件里那些
 * `nowrap` + `min-width: auto` 在 460px 侧栏里会集中爆发——我们为此打了
 * 好几轮 CSS 补丁。悬浮窗让宽度不再受布局挤占，那一整类问题自然消失。
 *
 * 两块积木都是 Semi 原生的（不手搓鼠标事件）：
 *   - `DragMove` 负责拖：`handler` 指定把手、`constrainer` 限制不许拖出屏；
 *   - `Resizable` 是官方移植的 re-resizable，八个方向的把手全给。
 *
 * 取舍要说清：悬浮窗**会挡住正文**，读的时候得挪开。侧栏的好处是永不遮挡，
 * 这是换一种工作方式，不是纯升级。
 */

const MIN_W = 420
const MIN_H = 320
/** 四周留的边距，让窗口不至于贴死在屏幕边上。 */
const GAP = 16

/** 按视口算一个默认位置：右下角，720 宽、78% 高。 */
function defaultRect(): ChatRect {
  const vw = window.innerWidth
  const vh = window.innerHeight
  const w = Math.min(720, vw - GAP * 2)
  const h = Math.min(Math.round(vh * 0.78), vh - GAP * 2)
  return { x: vw - w - GAP, y: vh - h - GAP, w, h }
}

/** 夹回视口内：换过显示器、缩过窗口之后，窗口不许留在屏幕外找不回来。 */
function clampRect(r: ChatRect): ChatRect {
  const vw = window.innerWidth
  const vh = window.innerHeight
  const w = Math.max(MIN_W, Math.min(r.w, vw - GAP * 2))
  const h = Math.max(MIN_H, Math.min(r.h, vh - GAP * 2))
  return {
    w,
    h,
    x: Math.max(GAP - w + 80, Math.min(r.x, vw - GAP - 80)),
    y: Math.max(0, Math.min(r.y, vh - GAP - 40)),
  }
}

export default function ChatWindow({ children }: { children: ReactNode }) {
  const { chatRect, setChatRect } = useAppStore()
  const layerRef = useRef<HTMLDivElement>(null)
  const winRef = useRef<HTMLDivElement>(null)

  // 首次打开算默认位置；之后夹回视口（存的值可能是别的屏幕尺寸下留的）
  const rect = clampRect(chatRect ?? defaultRect())

  // DragMove 拖拽期间直接改元素 style（不给每一像素都走一遍 React），
  // 松手才把最终位置收回 state——返回的有值，下次打开就还在原地。
  const customMove = useCallback((el: HTMLElement, top: number, left: number) => {
    el.style.left = `${left}px`
    el.style.top = `${top}px`
  }, [])

  const commitPos = useCallback(() => {
    const layer = layerRef.current
    const win = winRef.current
    if (!layer || !win) return
    const lr = layer.getBoundingClientRect()
    const wr = win.getBoundingClientRect()
    setChatRect({ ...rect, x: Math.round(wr.left - lr.left), y: Math.round(wr.top - lr.top) })
  }, [rect, setChatRect])

  // 视口变了（缩放/转屏）就把窗口夹回来，否则它会留在屏幕外
  useEffect(() => {
    const onResize = () => {
      const next = clampRect(chatRect ?? defaultRect())
      if (!chatRect || next.x !== chatRect.x || next.y !== chatRect.y || next.w !== chatRect.w || next.h !== chatRect.h) {
        setChatRect(next)
      }
    }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [chatRect, setChatRect])

  return (
    <div className="ai-window-layer" ref={layerRef}>
      <DragMove
        constrainer={() => layerRef.current as HTMLElement}
        // 把手就是 AiPanel 自己的标题栏——不再多长一条 chrome 出来。
        // 点标题栏上的按钮不会误拖：没有位移就不算拖动。
        handler={() => winRef.current?.querySelector('.ai-panel-header') as HTMLElement}
        customMove={customMove}
        onMouseUp={commitPos}
        onTouchEnd={commitPos}
      >
        <div
          ref={winRef}
          className="ai-window"
          style={{ left: rect.x, top: rect.y }}
        >
          <Resizable
            defaultSize={{ width: rect.w, height: rect.h }}
            minWidth={MIN_W}
            minHeight={MIN_H}
            maxWidth="96vw"
            maxHeight="94vh"
            enable={{ top: true, right: true, bottom: true, left: true, topRight: true, bottomRight: true, bottomLeft: true, topLeft: true }}
            // 只在松手时写盘：拖动过程中每一像素都落 localStorage 没必要。
            // 注意回调第一个参数是 size（不是 event）——Semi 的 ResizeCallback
            // 签名是 (size, event, direction)。
            onResizeEnd={(size) => {
              const w = Math.round(Number(size.width))
              const h = Math.round(Number(size.height))
              if (w && h && (w !== rect.w || h !== rect.h)) setChatRect({ ...rect, w, h })
            }}
          >
            {children}
          </Resizable>
        </div>
      </DragMove>
    </div>
  )
}
