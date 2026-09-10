import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button, Progress, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import { IconClose, IconRefresh } from '@douyinfe/semi-icons'
import type { Run, RunStatus } from '../../types'
import { cancelRun, getRuntime, listRuns } from '../../lib/api'
import { useAppStore } from '../../lib/store'
import { workPath } from '../../lib/routes'

const { Text } = Typography

const STATUS_META: Record<RunStatus, { color: 'blue' | 'green' | 'red' | 'orange' | 'grey'; label: string }> =
  {
    running: { color: 'blue', label: '进行中' },
    completed: { color: 'green', label: '已完成' },
    failed: { color: 'red', label: '失败' },
    interrupted: { color: 'orange', label: '已中断' },
    expired: { color: 'grey', label: '已过期' },
  }

/**
 * 批次卡：批量建档的进度与暂停/继续。
 * 批次 = 共享 workspace（batch:<id>）的一组工单；并发由后端 worker 池控制，
 * 所以这里的"进行中"里有一部分其实在排队，属预期。
 */
export default function BatchCard() {
  const { activeBatchId, setActiveBatchId } = useAppStore()
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(false)
  const [live, setLive] = useState<{ active: number; queued: number; limit: number }>({
    active: 0,
    queued: 0,
    limit: 2,
  })
  const nav = useNavigate()

  const load = useCallback(async () => {
    if (!activeBatchId) return
    setLoading(true)
    try {
      const res = await listRuns(undefined, 200, `batch:${activeBatchId}`)
      setRuns(res.runs ?? [])
      const rt = await getRuntime().catch(() => null)
      if (rt) setLive({ active: rt.activeRuns, queued: rt.queuedRuns, limit: rt.maxConcurrentRuns })
    } catch {
      setRuns([])
    } finally {
      setLoading(false)
    }
  }, [activeBatchId])

  useEffect(() => {
    void load()
  }, [load])

  const hasLive = useMemo(
    () => runs.some((r) => r.status === 'running' || r.status === 'interrupted'),
    [runs],
  )

  useEffect(() => {
    if (!hasLive) return
    const t = window.setInterval(() => void load(), 3000)
    return () => window.clearInterval(t)
  }, [hasLive, load])

  if (!activeBatchId) return null

  const total = runs.length
  const done = runs.filter((r) => r.status === 'completed').length
  const failed = runs.filter((r) => r.status === 'failed').length
  const queued = runs.filter((r) => r.status === 'running').length
  const percent = total ? Math.round((done / total) * 100) : 0

  const stopAll = async () => {
    const live = runs.filter((r) => r.status === 'running')
    for (const r of live) await cancelRun(r.id).catch(() => undefined)
    Toast.info('已停止排队中的工单，可稍后继续')
    void load()
  }

  return (
    <section className="batch-card">
      <header className="batch-card-head">
        <span className="batch-card-title">批量建档</span>
        <Text type="tertiary" size="small">
          {done}/{total} 完成{failed ? ` · ${failed} 失败` : ''}
        </Text>
        <div className="batch-card-actions">
          <Button
            theme="borderless"
            type="tertiary"
            size="small"
            icon={<IconRefresh />}
            loading={loading}
            onClick={() => void load()}
            aria-label="刷新批次"
          />
          <Button
            theme="borderless"
            type="tertiary"
            size="small"
            icon={<IconClose />}
            onClick={() => setActiveBatchId(null)}
            aria-label="收起批次卡"
          />
        </div>
      </header>

      <Progress percent={percent} showInfo={false} size="small" aria-label="批次进度" />

      <div className="batch-list">
        {runs.map((r) => {
          const meta = STATUS_META[r.status]
          const workId = r.context?.workId
          return (
            <div
              key={r.id}
              className="batch-row"
              role={workId ? 'button' : undefined}
              tabIndex={workId ? 0 : undefined}
              onClick={() => workId && nav(workPath(workId))}
              onKeyDown={(e) => {
                if (workId && e.key === 'Enter') nav(workPath(workId))
              }}
            >
              <span className="batch-row-title">{r.goal}</span>
              <Tag size="small" color={meta.color}>
                {meta.label}
              </Tag>
            </div>
          )
        })}
      </div>

      <footer className="batch-card-foot">
        <Text type="tertiary" size="small">
          执行中 {live.active}/{live.limit} · 排队 {live.queued} · 本批 待处理 {queued}
        </Text>
        {queued > 0 && (
          <Button size="small" theme="borderless" type="tertiary" onClick={() => void stopAll()}>
            暂停
          </Button>
        )}
      </footer>
    </section>
  )
}
