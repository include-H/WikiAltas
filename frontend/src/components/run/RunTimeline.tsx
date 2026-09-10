import { useEffect, useMemo, useState } from 'react'
import { Typography } from '@douyinfe/semi-ui'
import { IconChevronRight } from '@douyinfe/semi-icons'
import type { RunEvent } from '../../types'
import { dedupeEvents, projectRun } from '../../lib/runProjection'
import NarrativeLine from './NarrativeLine'
import ToolChip from './ToolChip'

const { Text } = Typography

function formatDuration(ms: number): string {
  if (ms < 1000) return '不到 1 秒'
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s} 秒`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} 分 ${s % 60} 秒`
  return `${Math.floor(m / 60)} 小时 ${m % 60} 分`
}

/**
 * 折叠叙事流：阶段头（模型的计划步骤）+ 叙事句 + 工具芯片。
 * 已完成/失败时整段收成一条摘要，点开才看过程。
 */
export default function RunTimeline({
  events,
  startedAt,
}: {
  events: RunEvent[]
  startedAt?: string
}) {
  const [collapsed, setCollapsed] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  const { phases, done, failed } = useMemo(() => projectRun(dedupeEvents(events)), [events])

  // 进行中时让"已处理 X"持续走字
  useEffect(() => {
    if (done || failed) return
    const t = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(t)
  }, [done, failed])

  const elapsed = useMemo(() => {
    if (!startedAt) return null
    const start = new Date(startedAt).getTime()
    if (Number.isNaN(start)) return null
    return Math.max(0, now - start)
  }, [startedAt, now])

  const finished = done || failed

  return (
    <div className="run-timeline">
      <button
        type="button"
        className="run-summary"
        onClick={() => finished && setCollapsed((v) => !v)}
        aria-expanded={!collapsed}
      >
        {finished ? (
          <IconChevronRight size="small" className={`run-summary-caret${collapsed ? '' : ' open'}`} />
        ) : (
          <span className="tool-dot running" aria-hidden />
        )}
        <span className="run-summary-title">
          {failed ? '工单失败' : done ? '已处理完成' : '馆员处理中…'}
        </span>
        {elapsed !== null && (
          <Text type="tertiary" size="small">
            {done || failed ? `用时 ${formatDuration(elapsed)}` : `已处理 ${formatDuration(elapsed)}`}
          </Text>
        )}
      </button>

      {(!finished || !collapsed) && (
        <div className="run-phases">
          {phases.map((phase) => (
            <section className="run-phase" key={phase.id}>
              <header className={`run-phase-head${phase.status === 'in_progress' ? ' is-active' : ''}`}>
                <span className="run-phase-title">{phase.title}</span>
                {phase.status === 'completed' && <span className="run-phase-state">✓</span>}
                {phase.status === 'failed' && <span className="run-phase-state danger">✗</span>}
              </header>
              <div className="run-phase-steps">
                {phase.steps.map((step) => {
                  if (step.kind === 'narrative') {
                    return <NarrativeLine key={step.id} text={step.text} />
                  }
                  if (step.kind === 'tool') {
                    return <ToolChip key={step.id} step={step} />
                  }
                  return (
                    <div
                      key={step.id}
                      className={`run-system-line${step.tone === 'danger' ? ' danger' : ''}`}
                    >
                      <Text type={step.tone === 'danger' ? 'danger' : 'success'} size="small">
                        {step.text}
                      </Text>
                    </div>
                  )
                })}
              </div>
            </section>
          ))}
        </div>
      )}
    </div>
  )
}
