import { useState } from 'react'
import { Tag, Typography } from '@douyinfe/semi-ui'
import { IconAlertCircle, IconChevronDown, IconChevronUp, IconInfoCircle, IconTickCircle } from '@douyinfe/semi-icons'
import type { Notice } from '../../lib/responses'

// 提醒条：宿主播报与护栏提醒（wikiatlas.notice）。
//
// 为什么单独一条通道、而不是混进对话：这些话**不是模型说的**——是 harness
// 在说"skill 没加载出来""模型接口不稳，正在重试""上下文被压缩了"，
// 以及两条不变量报警（前缀被改写 / 日志与上下文不一致）。
//
// 以前它们和模型输出挤在同一条叙事流里，于是要靠"这句话以「收到工单：」开头"
// 这种前缀规则再滤掉；现在是两条通道，各归各的。
//
// 挂载点与任务面板同一处（输入框的原生顶部槽）：它们都是"现在这个工单的状态"，
// 跟着输入框走才始终在视线里。

const TONE_STYLE: Record<Notice['tone'], { color: 'blue' | 'orange' | 'red'; icon: JSX.Element }> = {
  info: { color: 'blue', icon: <IconInfoCircle /> },
  warn: { color: 'orange', icon: <IconAlertCircle /> },
  error: { color: 'red', icon: <IconAlertCircle /> },
}

/**
 * 语气 → Semi Typography 的类型。走**组件 props**，不反写 `.semi-typography`
 * （VISUAL_SPEC §7.2：禁止反向覆盖 .semi-* 内部类）。
 */
const TONE_TEXT: Record<Notice['tone'], 'tertiary' | 'warning' | 'danger'> = {
  info: 'tertiary',
  warn: 'warning',
  error: 'danger',
}

export default function NoticeDock({ notices }: { notices: Notice[] }) {
  const [collapsed, setCollapsed] = useState(true)
  if (!notices.length) return null

  // 最近的一条常驻显示（它才是"此刻发生了什么"），其余折起来。
  const latest = notices[notices.length - 1]
  const tone = TONE_STYLE[latest.tone] ?? TONE_STYLE.info
  const errors = notices.filter((n) => n.tone === 'error').length

  return (
    <section className="notice-dock">
      <button
        type="button"
        className="notice-head"
        aria-expanded={!collapsed}
        onClick={() => setCollapsed((v) => !v)}
      >
        <span className={`notice-lead is-${latest.tone}`} aria-hidden>
          {latest.tone === 'error' ? <IconAlertCircle /> : latest.tone === 'info' ? <IconTickCircle /> : tone.icon}
        </span>
        <span className="notice-text">{latest.text}</span>
        {notices.length > 1 && (
          <Tag size="small" color={errors ? 'red' : 'grey'}>
            {notices.length} 条
          </Tag>
        )}
        <span className="notice-chevron" aria-hidden>
          {collapsed ? <IconChevronUp size="small" /> : <IconChevronDown size="small" />}
        </span>
      </button>
      {!collapsed && (
        <ul className="notice-list">
          {notices.map((n) => (
            <li key={n.id} className="notice-item">
              <Typography.Text type={TONE_TEXT[n.tone]} size="small">
                {n.text}
              </Typography.Text>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
