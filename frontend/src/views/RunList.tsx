import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  AIChatDialogue,
  Button,
  Empty,
  Input,
  Modal,
  SideSheet,
  Spin,
  Table,
  Tag,
  Toast,
  Typography,
} from '@douyinfe/semi-ui'
import { IconRefresh, IconSearch } from '@douyinfe/semi-icons'
import type { Run, RunEvent, RunStatus, Session } from '../types'
import {
  getDoc,
  getRun,
  listRuns,
  listSessions,
  renameSession,
  streamRunEvents,
} from '../lib/api'
import type { SseHandle } from '../lib/api'
import { mergeEvent, runMessages, usageOf, type DialogueMessage } from '../lib/responses'
import ToolCard, { type FunctionCallItem } from '../components/run/ToolCard'
import { useAppStore } from '../lib/store'
import { relativeTime } from '../lib/tree'
import { docPath } from '../lib/routes'
import { SESSION_POINTER } from '../lib/sessionPointer'

const { Text, Title } = Typography

const STATUS: Record<RunStatus, { color: 'blue' | 'orange' | 'green' | 'red' | 'grey'; label: string }> = {
  running: { color: 'blue', label: '进行中' },
  interrupted: { color: 'orange', label: '已中断' },
  completed: { color: 'green', label: '已完成' },
  failed: { color: 'red', label: '失败' },
  expired: { color: 'grey', label: '已过期' },
}

function sessionStatusTag(status: string) {
  const meta = STATUS[status as RunStatus] ?? { color: 'grey' as const, label: status || '—' }
  return (
    <Tag size="small" color={meta.color}>
      {meta.label}
    </Tag>
  )
}

/**
 * 这是「工单」列表——但**工单 = 一场对话**（等价于 /resume 的那个列表）。
 *
 * runs 是会话内部的执行记录（可取消/续跑/过期），不是用户看到的单位：
 * 你发一句"继续"只是在同一场对话里多接一段，不该在这里多出一行。
 * 所以列出的是 sessions，点进去看的是把该会话各次执行接起来的完整经过。
 *
 * 这一页**不发起新对话**：新对话只在 LLM 面板里开（那儿才知道要挂在哪个节点上）。
 * 这里只做两件事——看，和切到面板继续。
 */
function fmtTokens(n: number): string {
  return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n)
}

export default function RunList() {
  const nav = useNavigate()
  const { nodes } = useAppStore()
  const [sessions, setSessions] = useState<Session[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState('')
  const [selected, setSelected] = useState<Session | null>(null)
  const [conv, setConv] = useState<Run[]>([])
  const [eventsByRun, setEventsByRun] = useState<Map<string, RunEvent[]>>(new Map())
  const [detailLoading, setDetailLoading] = useState(false)
  const [renaming, setRenaming] = useState<Session | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const sseRef = useRef<SseHandle | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await listSessions()
      setSessions(res.sessions ?? [])
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载失败')
      setSessions([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  // 只在有正在跑的对话时轮询，空闲不空转
  useEffect(() => {
    if (!sessions.some((s) => s.status === 'running')) return
    const t = setInterval(() => void load(), 5000)
    return () => clearInterval(t)
  }, [sessions, load])

  const closeSse = useCallback(() => {
    sseRef.current?.close()
    sseRef.current = null
  }, [])
  useEffect(() => closeSse, [closeSse])

  /** 打开一场对话：取它各次执行的事件，按时间接成一条时间线。 */
  const open = useCallback(
    async (sess: Session) => {
      setSelected(sess)
      setConv([])
      setEventsByRun(new Map())
      setDetailLoading(true)
      closeSse()
      try {
        const { runs } = await listRuns(undefined, 100, sess.id)
        const ordered = [...(runs ?? [])].sort((a, b) => a.startedAt.localeCompare(b.startedAt))
        const map = new Map<string, RunEvent[]>()
        for (const r of ordered) {
          const detail = await getRun(r.id)
          map.set(r.id, detail.events ?? [])
        }
        setConv(ordered)
        setEventsByRun(map)
        const running = ordered.find((r) => r.status === 'running')
        if (running) {
          sseRef.current = streamRunEvents(running.id, (ev) =>
            setEventsByRun((prev) => {
              const next = new Map(prev)
              next.set(running.id, mergeEvent(next.get(running.id) ?? [], ev))
              return next
            }),
          )
        }
      } catch {
        setConv([])
      } finally {
        setDetailLoading(false)
      }
    },
    [closeSse],
  )

  /** 「继续」= 到这场对话所属的页面，把面板切到这段会话（等价于 /resume）。 */
  const resume = useCallback(
    async (sess: Session) => {
      const t = sess.target
      try {
        let path = '/'
        if (t.startsWith('work:')) {
          path = `/w/${t.slice(5)}`
        } else if (t.startsWith('doc:')) {
          // 资料页的路由要 workId，只有 docId 时先查一下它挂在谁名下
          const { doc } = await getDoc(t.slice(4))
          path = docPath(doc.folderOf, t.slice(4))
        } else if (t !== 'home') {
          Toast.info('这段会话不属于某个页面，请到 AI 面板里继续')
          return
        }
        localStorage.setItem(SESSION_POINTER + t, sess.id)
        nav(`${path}?ai=1`.replace('//', '/'))
      } catch (e) {
        Toast.error(e instanceof Error ? e.message : '打开失败')
      }
    },
    [nav],
  )

  const targetLabel = useCallback(
    (s: Session) => {
      const t = s.target
      // 'default' 是后端在"没给工作区"时落的内部值（脚本/外部建的工单），
      // 直接渲染等于把内部键吐给用户；它语义上就是首页那条全局会话。
      // 兜底也不回原值——内部键不该出现在界面上。
      if (!t || t === 'home' || t === 'default') return '首页'
      if (t.startsWith('work:')) return nodes.find((n) => n.id === t.slice(5))?.title ?? '作品'
      if (t.startsWith('doc:')) return '资料'
      if (t.startsWith('batch:')) return '批量建档'
      return '其他'
    },
    [nodes],
  )

  const filtered = useMemo(() => {
    // 空会话不进列表：面板点「新对话」会先建一段空白会话，没说话就搁下了——
    // 那样的东西不是工单。（面板自己的会话下拉仍会列出它，那儿是"可回到的会话"。）
    const real = sessions.filter((s) => s.runCount > 0)
    const kw = q.trim().toLowerCase()
    if (!kw) return real
    return real.filter((s) =>
      `${s.title} ${s.lastGoal} ${s.target} ${targetLabel(s)}`.toLowerCase().includes(kw),
    )
  }, [sessions, q, targetLabel])

  return (
    <div className="run-list-view">
      <div className="run-list-header">
        {/* 标题与说明是一组：三个子元素配 space-between 会把说明甩到页面正中，
            看着像无主的浮字。收成一块，留给右侧工具栏的才是真间距。 */}
        <div className="run-list-heading">
          <Title heading={4} style={{ margin: 0 }}>
            工单
          </Title>
          <Text type="tertiary" size="small">
            一场对话 = 一张工单。点开看完整经过，或接着往下说。
          </Text>
        </div>
        <div className="run-list-tools">
          <Input
            prefix={<IconSearch />}
            placeholder="搜索对话"
            value={q}
            onChange={setQ}
            showClear
            style={{ width: 200 }}
          />
          <Button icon={<IconRefresh />} onClick={() => void load()} />
        </div>
      </div>

      {error && (
        <Text type="danger" size="small">
          {error}
        </Text>
      )}

      <Table<Session>
        dataSource={filtered}
        loading={loading}
        rowKey="id"
        pagination={filtered.length > 20 ? { pageSize: 20 } : false}
        onRow={(row) => ({
          onClick: () => {
            if (row) void open(row)
          },
          style: { cursor: 'pointer' },
        })}
        columns={[
          {
            title: '对话',
            dataIndex: 'title',
            render: (_v: string, s: Session) => (
              <div className="run-cell-goal">
                <span className="run-cell-title">{s.title || '（未命名对话）'}</span>
                {s.lastGoal && s.lastGoal !== s.title && (
                  <Text type="tertiary" size="small" className="run-cell-sub">
                    最近：{s.lastGoal}
                  </Text>
                )}
              </div>
            ),
          },
          { title: '归属', dataIndex: 'target', width: 160, render: (_v: string, s: Session) => targetLabel(s) },
          { title: '状态', dataIndex: 'status', width: 96, render: (v: string) => sessionStatusTag(v) },
          { title: '轮次', dataIndex: 'runCount', width: 72 },
          {
            title: '最近活跃',
            dataIndex: 'updatedAt',
            width: 120,
            render: (v: string) => relativeTime(v),
          },
        ]}
      />

      <SideSheet
        visible={!!selected}
        onCancel={() => {
          setSelected(null)
          closeSse()
        }}
        width={720}
        title={selected?.title || '对话'}
        footer={
          selected && (
            <div className="run-detail-foot">
              <Button
                onClick={() => {
                  setRenaming(selected)
                  setRenameValue(selected.title)
                }}
              >
                重命名
              </Button>
              <Button theme="solid" type="primary" onClick={() => void resume(selected)}>
                继续这段对话
              </Button>
            </div>
          )
        }
      >
        {detailLoading ? (
          <Spin style={{ display: 'block', margin: '48px auto' }} />
        ) : (
          <ConversationDetail
            session={selected}
            runs={conv}
            eventsByRun={eventsByRun}
          />
        )}
      </SideSheet>

      <Modal
        title="重命名对话"
        visible={!!renaming}
        onCancel={() => setRenaming(null)}
        onOk={async () => {
          if (!renaming) return
          try {
            await renameSession(renaming.id, renameValue.trim())
            Toast.success('已重命名')
            setRenaming(null)
            setSelected((cur) => (cur?.id === renaming.id ? { ...cur, title: renameValue.trim() } : cur))
            await load()
          } catch (e) {
            Toast.error(e instanceof Error ? e.message : '重命名失败')
          }
        }}
      >
        <Input value={renameValue} onChange={setRenameValue} autoFocus />
      </Modal>
    </div>
  )
}

/** 一场对话的完整经过：把各次执行接成一条时间线，与 AI 面板同一套读法。 */
function ConversationDetail({
  session,
  runs,
  eventsByRun,
}: {
  session: Session | null
  runs: Run[]
  eventsByRun: Map<string, RunEvent[]>
}) {
  // 每个 run 一轮对话（用户那句 + 归约出来的助手消息），按时间接起来
  const chats = useMemo<DialogueMessage[]>(
    () => runs.flatMap((r) => runMessages(r, eventsByRun.get(r.id) ?? [])),
    [runs, eventsByRun],
  )
  // 整场对话的用量与缓存命中。以前这条在 AI 面板输入框上方，后来被上下文环
  // 取代——轮数/累计 token/缓存命中率是**工单统计**，归这里。
  const usage = useMemo(
    () => usageOf(runs.flatMap((r) => eventsByRun.get(r.id) ?? [])),
    [runs, eventsByRun],
  )
  const model = runs.length ? runs[runs.length - 1].model : ''
  const renderers = useMemo(
    () => ({ function_call: (item: FunctionCallItem) => <ToolCard item={item} /> }),
    [],
  )
  if (!session) return null
  return (
    <div className="run-detail-body">
      <div className="run-detail-meta">
        {sessionStatusTag(session.status)}
        <Text type="tertiary" size="small">
          共 {runs.length} 次执行 · 开始于 {relativeTime(session.createdAt)}
          {model ? ` · 模型 ${model}` : ''}
          {usage.steps > 0 && (
            <>
              {' · 输入 '}
              {fmtTokens(usage.prompt)}
              {usage.cacheReported
                ? `（缓存命中 ${fmtTokens(usage.cacheRead)}，${Math.round((usage.cacheRead / Math.max(1, usage.prompt)) * 100)}%）`
                : '（该网关未回缓存用量）'}
              {usage.cacheDropped ? ' · 缓存命中掉到 0，前缀可能被改写' : ''}
              {' · 输出 '}
              {fmtTokens(usage.completion)}
            </>
          )}
        </Text>
      </div>
      {chats.length === 0 ? (
        <Empty description="这段对话还没有内容" style={{ padding: 24 }} />
      ) : (
        <AIChatDialogue
          chats={chats as never}
          // user 也要给：细节里把用户提问一起接进来了（整场对话），
          // 缺一个角色 Semi 的 DialogueAvatar 会直接抛（Cannot destructure 'avatar'）。
          roleConfig={{ assistant: { name: 'Altas' }, user: { name: '我' } }}
          mode="userBubble"
          showReset={false}
          markdownRenderProps={{ className: 'ai-md' }}
          renderDialogueContentItem={renderers as never}
        />
      )}
    </div>
  )
}
