import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  AIChatDialogue,
  Button,
  Dropdown,
  Empty,
  Input,
  Modal,
  SideSheet,
  Spin,
  Table,
  Tabs,
  Tag,
  Toast,
  Typography,
} from '@douyinfe/semi-ui'
import { IconMore, IconRefresh, IconSearch } from '@douyinfe/semi-icons'
import type { Run, RunEvent, RunStatus } from '../types'
import { cancelRun, deleteRun, getRun, listRuns, resumeRun, streamRunEvents } from '../lib/api'
import type { SseHandle } from '../lib/api'
import { buildDialogueMessages } from '../lib/runProjection'
import type { DialogueStep } from '../lib/runProjection'
import RunSteps from '../components/run/RunSteps'
import { relativeTime } from '../lib/tree'

const { Text, Title } = Typography

const STATUS: Record<
  RunStatus,
  { color: 'blue' | 'orange' | 'green' | 'red' | 'grey'; label: string }
> = {
  running: { color: 'blue', label: '进行中' },
  interrupted: { color: 'orange', label: '已中断' },
  completed: { color: 'green', label: '已完成' },
  failed: { color: 'red', label: '失败' },
  expired: { color: 'grey', label: '已过期' },
}

function statusTag(run: Run) {
  const meta = STATUS[run.status] ?? { color: 'grey' as const, label: run.status }
  return (
    <Tag size="small" color={meta.color}>
      {meta.label}
    </Tag>
  )
}

/** 工单详情：叙事流与 AI 面板共用同一套投影，避免两处两种读法。 */
function RunDetail({ run, events }: { run: Run; events: RunEvent[] }) {
  // 一次工单 = 一段叙事：投影出来的是「一个内容项一条消息」，直接渲染会出现
  // 一排重复的「Altas」头像。这里把它们并回同一条消息的 content[]，
  // 面板侧（B 维护）合并后这里会自动变成恒等变换。
  const chats = useMemo(() => {
    const msgs = buildDialogueMessages(events, run)
    if (msgs.length <= 1) return msgs
    return [{ ...msgs[msgs.length - 1], content: msgs.flatMap((m) => m.content) }]
  }, [events, run])
  return (
    <div className="run-detail-body">
      <div className="run-detail-meta">
        {statusTag(run)}
        <Tag size="small" color="grey">
          {run.intent}
        </Tag>
        <Text type="tertiary" size="small">
          {run.model || '未指定模型'} · 开始于 {relativeTime(run.startedAt)}
        </Text>
      </div>
      <Text type="secondary" className="run-detail-goal">
        {run.goal}
      </Text>
      {chats.length === 0 ? (
        <Empty description="这条工单还没有事件流" style={{ padding: 24 }} />
      ) : (
        <AIChatDialogue
          chats={chats as never}
          roleConfig={{ assistant: { name: 'Altas' } }}
          mode="noBubble"
          showReset={false}
          renderDialogueContentItem={
            {
              // 与 AI 面板共用同一枚折叠芯片（B 维护的 components/run/RunSteps）
              plan: (item: { content?: DialogueStep[] }) => <RunSteps steps={item.content ?? []} />,
            } as never
          }
        />
      )}
    </div>
  )
}

export default function RunList() {
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState<string>('all')
  const [q, setQ] = useState('')
  const [selected, setSelected] = useState<Run | null>(null)
  const [events, setEvents] = useState<RunEvent[]>([])
  const [detailLoading, setDetailLoading] = useState(false)
  const sseRef = useRef<SseHandle | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await listRuns(undefined, 100)
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

  // 只在有进行中的工单时轮询，空闲不空转
  useEffect(() => {
    const hasRunning = runs.some((r) => r.status === 'running')
    if (!hasRunning) return
    const t = setInterval(() => void load(), 5000)
    return () => clearInterval(t)
  }, [runs, load])

  const closeSse = useCallback(() => {
    sseRef.current?.close()
    sseRef.current = null
  }, [])

  useEffect(() => closeSse, [closeSse])

  const openRun = useCallback(
    async (run: Run) => {
      setSelected(run)
      setEvents([])
      setDetailLoading(true)
      closeSse()
      try {
        const detail = await getRun(run.id)
        setEvents(detail.events ?? [])
        if (detail.run) setSelected(detail.run)
      } catch {
        setEvents([])
      } finally {
        setDetailLoading(false)
      }
      if (run.status === 'running') {
        sseRef.current = streamRunEvents(run.id, (ev) => {
          setEvents((prev) => (prev.some((e) => e.id && e.id === ev.id) ? prev : [...prev, ev]))
          if (ev.type === 'run.completed' || ev.type === 'run.failed') void load()
        })
      }
    },
    [closeSse, load],
  )

  const act = async (fn: () => Promise<unknown>, okMsg: string) => {
    try {
      await fn()
      Toast.success(okMsg)
      await load()
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

  const filtered = useMemo(() => {
    const kw = q.trim().toLowerCase()
    return runs.filter((r) => {
      if (status !== 'all' && r.status !== status) return false
      if (!kw) return true
      return `${r.goal ?? ''} ${r.intent} ${r.model ?? ''}`.toLowerCase().includes(kw)
    })
  }, [runs, status, q])

  const countOf = useCallback(
    (key: string) => (key === 'all' ? runs.length : runs.filter((r) => r.status === key).length),
    [runs],
  )

  const tabList = useMemo(
    () =>
      (['all', 'running', 'interrupted', 'failed', 'completed'] as const)
        .map((key) => ({
          itemKey: key,
          tab:
            key === 'all'
              ? `全部 ${countOf('all')}`
              : `${STATUS[key as RunStatus].label} ${countOf(key)}`,
        }))
        .filter((t) => t.itemKey === 'all' || countOf(t.itemKey) > 0),
    [countOf],
  )

  const actionsMenu = (r: Run) => (
    <Dropdown
      trigger="click"
      position="bottomRight"
      render={
        <Dropdown.Menu>
          <Dropdown.Item onClick={() => void openRun(r)}>查看详情</Dropdown.Item>
          {(r.status === 'interrupted' || r.status === 'failed') && (
            <Dropdown.Item onClick={() => void act(() => resumeRun(r.id), '已恢复')}>
              继续这个工单
            </Dropdown.Item>
          )}
          {r.status === 'running' && (
            <Dropdown.Item onClick={() => void act(() => cancelRun(r.id), '已取消')}>
              停止
            </Dropdown.Item>
          )}
          <Dropdown.Divider />
          <Dropdown.Item type="danger" onClick={() => remove(r)}>
            删除
          </Dropdown.Item>
        </Dropdown.Menu>
      }
    >
      <Button
        theme="borderless"
        type="tertiary"
        size="small"
        icon={<IconMore />}
        aria-label="更多操作"
      />
    </Dropdown>
  )

  return (
    <div className="run-list-view">
      <div className="run-list-header">
        <Title heading={4} style={{ margin: 0 }}>
          Altas 工单
        </Title>
      </div>

      <Tabs
        type="line"
        size="small"
        activeKey={status}
        onChange={(k) => setStatus(String(k))}
        tabList={tabList}
        className="run-list-tabs"
        tabBarExtraContent={
          <div className="run-list-tools">
            <Input
              size="small"
              prefix={<IconSearch />}
              placeholder="搜索目标 / 意图"
              value={q}
              onChange={setQ}
              showClear
              style={{ width: 220 }}
            />
            <Button
              size="small"
              theme="borderless"
              type="tertiary"
              icon={<IconRefresh />}
              loading={loading}
              onClick={() => void load()}
              aria-label="刷新"
            />
          </div>
        }
      />

      {loading && runs.length === 0 && <Spin style={{ display: 'block', margin: 48 }} />}
      {error && <Empty description={error} style={{ padding: 32 }} />}
      {!loading && !error && filtered.length === 0 && (
        <Empty
          description={runs.length === 0 ? '还没有工单' : '没有符合条件的工单'}
          style={{ padding: 32 }}
        />
      )}

      {filtered.length > 0 && (
        <Table
          rowKey="id"
          dataSource={filtered}
          size="small"
          pagination={{ pageSize: 20, showTotal: true, hideOnSinglePage: true }}
          onRow={(r) => ({
            onClick: () => void openRun(r as Run),
            style: { cursor: 'pointer' },
          })}
          columns={[
            {
              title: '目标',
              dataIndex: 'goal',
              width: 640,
              render: (v: string, r: Run) => (
                <div className="run-cell-goal">
                  <span className="run-cell-title">{v || r.intent}</span>
                  <Text type="tertiary" size="small">
                    {r.intent}
                  </Text>
                </div>
              ),
            },
            {
              title: '状态',
              dataIndex: 'status',
              width: 110,
              render: (_: unknown, r: Run) => statusTag(r),
            },
            {
              title: '模型',
              dataIndex: 'model',
              width: 150,
              render: (m: string) => (
                <Text type="tertiary" size="small">
                  {m || '—'}
                </Text>
              ),
            },
            {
              title: '开始',
              dataIndex: 'startedAt',
              width: 110,
              render: (v: string) => (
                <Text type="tertiary" size="small">
                  {relativeTime(v)}
                </Text>
              ),
            },
            {
              title: '',
              width: 56,
              align: 'right' as const,
              render: (_: unknown, r: Run) => (
                <div onClick={(e) => e.stopPropagation()}>{actionsMenu(r)}</div>
              ),
            },
          ]}
        />
      )}

      <SideSheet
        title={selected ? selected.goal || selected.intent : ''}
        visible={!!selected}
        onCancel={() => setSelected(null)}
        width={560}
        className="run-detail-sheet"
        footer={
          selected ? (
            <div className="run-detail-actions">
              {(selected.status === 'interrupted' || selected.status === 'failed') && (
                <Button
                  theme="solid"
                  type="primary"
                  onClick={() => void act(() => resumeRun(selected.id), '已恢复')}
                >
                  继续这个工单
                </Button>
              )}
              {selected.status === 'running' && (
                <Button onClick={() => void act(() => cancelRun(selected.id), '已取消')}>
                  停止
                </Button>
              )}
              <Button theme="borderless" type="danger" onClick={() => remove(selected)}>
                删除
              </Button>
            </div>
          ) : null
        }
      >
        {detailLoading ? (
          <Spin style={{ display: 'block', margin: '48px auto' }} />
        ) : selected ? (
          <RunDetail run={selected} events={events} />
        ) : null}
      </SideSheet>
    </div>
  )
}
