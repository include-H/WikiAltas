import { useCallback, useEffect, useState } from 'react'
import {
  AIChatDialogue,
  Button,
  Empty,
  Modal,
  Spin,
  Table,
  Tag,
  Toast,
  Typography,
} from '@douyinfe/semi-ui'
import type { Run } from '../types'
import { cancelRun, deleteRun, getRun, listRuns, resumeRun, streamRunEvents } from '../lib/api'
import { buildDialogueMessages } from '../lib/runProjection'
import type { DialogueStep } from '../lib/runProjection'
import type { RunEvent } from '../types'

const { Text, Title } = Typography

const STATUS: Record<string, { color: 'blue' | 'orange' | 'green' | 'red' | 'grey'; label: string }> = {
  running: { color: 'blue', label: '进行中' },
  interrupted: { color: 'orange', label: '已中断' },
  completed: { color: 'green', label: '已完成' },
  failed: { color: 'red', label: '失败' },
  expired: { color: 'grey', label: '已过期' },
}

export default function RunList() {
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState<Run | null>(null)
  const [events, setEvents] = useState<RunEvent[]>([])

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await listRuns()
      setRuns(res.runs ?? [])
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载失败')
      setRuns([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  // poll running runs lightly
  useEffect(() => {
    const hasRunning = runs.some((r) => r.status === 'running')
    if (!hasRunning) return
    const t = setInterval(() => void load(), 5000)
    return () => clearInterval(t)
  }, [runs, load])

  const openRun = useCallback(async (run: Run) => {
    setSelected(run)
    setEvents([])
    try {
      const detail = await getRun(run.id)
      setEvents(detail.events ?? [])
    } catch {
      setEvents([])
    }
    if (run.status === 'running') {
      const handle = streamRunEvents(run.id, (ev) => {
        setEvents((prev) => {
          if (prev.some((e) => e.id === ev.id && e.id)) return prev
          return [...prev, ev]
        })
        if (ev.type === 'run.completed' || ev.type === 'run.failed') void load()
      })
      // store cleanup via selected change is enough for demo; close on unmount
      return () => handle.close()
    }
    return undefined
  }, [load])

  useEffect(() => {
    let cleanup: (() => void) | undefined
    let cancelled = false
    if (selected && selected.status === 'running') {
      void (async () => {
        const c = await openRun(selected)
        if (cancelled) c?.()
        else cleanup = c
      })()
    }
    return () => {
      cancelled = true
      cleanup?.()
    }
  }, [selected?.id]) // eslint-disable-line react-hooks/exhaustive-deps

  const act = async (fn: () => Promise<unknown>, okMsg: string) => {
    try {
      await fn()
      Toast.success(okMsg)
      void load()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '操作失败')
    }
  }

  /** 手工删除：正在跑的工单先停下来再删，事件流一并删除（正文改动不受影响）。 */
  const remove = (run: Run) => {
    Modal.confirm({
      title: '删除这条工单？',
      content: `「${run.goal || run.intent}」的事件流会一并删除，正文改动不受影响。`,
      okText: '删除',
      okButtonProps: { type: 'danger', theme: 'solid' },
      cancelText: '取消',
      onOk: async () => {
        await deleteRun(run.id)
        Toast.success('已删除')
        setSelected((cur) => (cur?.id === run.id ? null : cur))
        await load()
      },
    })
  }

  return (
    <div className="run-list-view">
      <div className="run-list-header">
        <Title heading={4} style={{ margin: 0 }}>
          Altas 工单
        </Title>
        <Button onClick={() => void load()}>刷新</Button>
      </div>

      {loading && runs.length === 0 && <Spin style={{ display: 'block', margin: 48 }} />}
      {error && <Empty description={error} />}
      {!loading && !error && runs.length === 0 && <Empty description="暂无工单" />}

      {runs.length > 0 && (
        <Table
          rowKey="id"
          dataSource={runs}
          pagination={false}
          size="small"
          onRow={(r) => ({
            // 点行要真的把事件流拉回来：只 setSelected 的话详情永远是空投影（只剩"理解任务"）
            onClick: () => void openRun(r as Run),
            style: { cursor: 'pointer' },
          })}
          columns={[
            {
              title: '目标',
              dataIndex: 'goal',
              render: (v: string, r: Run) => (
                <div>
                  <div>{v || r.intent}</div>
                  <Text type="tertiary" size="small">
                    {r.intent}
                  </Text>
                </div>
              ),
            },
            {
              title: '状态',
              dataIndex: 'status',
              width: 100,
              render: (s: string) => {
                const st = STATUS[s] ?? { color: 'grey' as const, label: s }
                return <Tag color={st.color}>{st.label}</Tag>
              },
            },
            {
              title: '模型',
              dataIndex: 'model',
              width: 140,
              render: (m: string) => <Text type="tertiary" size="small">{m || '—'}</Text>,
            },
            {
              title: '操作',
              width: 220,
              render: (_: unknown, r: Run) => (
                <div onClick={(e) => e.stopPropagation()}>
                  {(r.status === 'interrupted' || r.status === 'failed') && (
                    <Button
                      size="small"
                      style={{ marginRight: 8 }}
                      onClick={() => void act(() => resumeRun(r.id), '已恢复')}
                    >
                      继续
                    </Button>
                  )}
                  {r.status === 'running' && (
                    <Button
                      size="small"
                      type="danger"
                      theme="borderless"
                      onClick={() => void act(() => cancelRun(r.id), '已取消')}
                    >
                      取消
                    </Button>
                  )}
                  <Button
                    size="small"
                    type="danger"
                    theme="borderless"
                    style={{ marginLeft: 8 }}
                    onClick={() => remove(r)}
                  >
                    删除
                  </Button>
                </div>
              ),
            },
          ]}
        />
      )}

      {selected && (
        <div className="run-detail">
          <div className="run-detail-header">
            <Title heading={5} style={{ margin: 0 }}>
              {selected.goal || selected.intent}
            </Title>
            <Button size="small" theme="borderless" onClick={() => setSelected(null)}>
              关闭
            </Button>
          </div>
          <AIChatDialogue
            chats={buildDialogueMessages(events, selected) as never}
            roleConfig={{ assistant: { name: 'Altas' } }}
            mode="noBubble"
            showReset={false}
            renderDialogueContentItem={
              {
                plan: (item: { content?: DialogueStep[] }) => (
                  <AIChatDialogue.Step steps={item.content ?? []} />
                ),
              } as never
            }
          />
        </div>
      )}
    </div>
  )
}
