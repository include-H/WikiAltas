import { useEffect, useMemo, useState } from 'react'
import { Progress, Tooltip } from '@douyinfe/semi-ui'
import type { RunEvent } from '../../types'
import { getRuntime } from '../../lib/api'

// ContextMeter 是输入框右下角那圈"上下文用量"。
//
// Semi 没有现成的上下文用量组件，用 Progress 的环形拼一个：
// 分子 = 最后一轮 wikiatlas.usage 的 promptTokens（此刻窗口里装了多少，
// 不是累计——累计值只说明花了多少钱，不说明还剩多少地方）；
// 分母 = 模型窗口，来自 /api/runtime。
//
// 没有用量数据时不渲染（网关不回 usage 时就不会有这圈，而不是画一个 0%）。

const FALLBACK_WINDOW = 262144

function fmtTokens(n: number): string {
  return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n)
}

export default function ContextMeter({ events }: { events: RunEvent[] }) {
  const used = useMemo(() => {
    let last = 0
    for (const ev of events) {
      if (ev.type !== 'wikiatlas.usage') continue
      const v = ev.payload?.promptTokens
      if (typeof v === 'number' && v > 0) last = v
    }
    return last
  }, [events])

  const [window, setWindow] = useState(FALLBACK_WINDOW)
  useEffect(() => {
    let alive = true
    getRuntime()
      .then((r) => {
        if (alive && typeof r.contextWindow === 'number' && r.contextWindow > 0) setWindow(r.contextWindow)
      })
      .catch(() => {
        /* 取不到就用默认窗口，不影响主流程 */
      })
    return () => {
      alive = false
    }
  }, [])

  if (used <= 0) return null

  const percent = Math.min(100, (used / window) * 100)
  const stroke =
    percent >= 80
      ? 'var(--semi-color-danger)'
      : percent >= 60
        ? 'var(--semi-color-warning)'
        : 'var(--semi-color-text-2)'

  return (
    <Tooltip
      content={`上下文 ${fmtTokens(used)} / ${fmtTokens(window)}（${percent.toFixed(1)}%）`}
      position="topRight"
    >
      <span className="ctx-meter" role="img" aria-label={`上下文占用 ${percent.toFixed(0)}%`}>
        <Progress
          type="circle"
          percent={percent}
          size="small"
          width={22}
          strokeWidth={3}
          stroke={stroke}
          showInfo={false}
        />
      </span>
    </Tooltip>
  )
}
