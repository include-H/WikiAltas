import { useEffect } from 'react'

/** 用户此刻是否正在正文里打字（打字时不抢内容，等下一轮再刷）。 */
function isEditing(): boolean {
  const el = document.activeElement
  if (!el || !(el instanceof HTMLElement)) return false
  return !!el.closest('.block-editor-inner, .md-editor, .dn-editor')
}

/**
 * Altas 写正文时，前端要能看见"写到哪儿了"。
 *
 * staging 期间按间隔静默重取正文（不闪 loading、不覆盖用户正在打的字）；
 * 提交（content.committed）之后由调用方自己的逻辑做最终刷新。
 */
export function useLiveRefresh(
  active: boolean,
  reload: () => void | Promise<void>,
  intervalMs = 2000,
) {
  useEffect(() => {
    if (!active) return
    const timer = window.setInterval(() => {
      if (!isEditing()) void reload()
    }, intervalMs)
    return () => window.clearInterval(timer)
  }, [active, reload, intervalMs])
}
