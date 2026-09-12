import { Button, Toast } from '@douyinfe/semi-ui'
import { IconCopyStroked } from '@douyinfe/semi-icons'
import type { DialogueMessage } from '../../lib/responses'

// 消息操作条。Semi 的默认形态给一排按钮（复制/分享/编辑/点赞/重新生成/更多），
// 但我们只接了复制的语义——其余点了毫无反应（真实反馈："这些按钮都不起作用"）。
// 与其留一排装饰，不如只画一个真能用的复制：点击 → 剪贴板 → toast 回执。
//
// className 是 Semi 传进来的外壳类（含悬停显隐的 show/hidden 状态），
// 必须挂到我们的根节点上，否则悬停显示的 CSS 语义就丢了。

/** 把归约出来的消息抽成可复制的纯文本：助手是 content 里的 message 项，用户是纯字符串。 */
export function plainTextOf(m?: DialogueMessage): string {
  const c = m?.content
  if (typeof c === 'string') return c.trim()
  if (!Array.isArray(c)) return ''
  const out: string[] = []
  for (const it of c as Array<Record<string, unknown>>) {
    if (!it || it.type !== 'message') continue
    const parts = Array.isArray(it.content) ? (it.content as Array<Record<string, unknown>>) : []
    for (const p of parts) {
      if (typeof p?.text === 'string') out.push(p.text)
    }
  }
  return out.join('').trim()
}

/** 写剪贴板。navigator.clipboard 只在安全上下文（localhost/https）存在——
 *  从局域网 http://192.168.x.x 访问时它直接是 undefined，所以留一条
 *  execCommand 的老路（Semi 自带的 copy-text-to-clipboard 也是这么兜的）。 */
async function copyPlain(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    /* 权限被拒或非安全上下文：走下面的兜底 */
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}

export default function MessageActions({
  message,
  className,
}: {
  message?: DialogueMessage
  className?: string
}) {
  const text = plainTextOf(message)
  if (!text) return null
  return (
    <div className={className}>
      <Button
        theme="borderless"
        type="tertiary"
        size="small"
        icon={<IconCopyStroked />}
        aria-label="复制"
        className="semi-ai-chat-dialogue-action-btn msg-action-copy"
        onClick={() => {
          void copyPlain(text).then((ok) => {
            if (ok) Toast.success('已复制')
            else Toast.error('复制失败')
          })
        }}
      />
    </div>
  )
}
