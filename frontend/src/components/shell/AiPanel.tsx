import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import {
  AIChatDialogue,
  AIChatInput,
  Avatar,
  Button,
  Input,
  Modal,
  Popover,
  Tag,
  Toast,
  Typography,
} from '@douyinfe/semi-ui'
import {
  IconAIFilledLevel1,
  IconClose,
  IconDelete,
  IconEdit,
  IconHistory,
  IconPlus,
  IconRefresh,
} from '@douyinfe/semi-icons'
import type { Run, RunEvent, RunIntent, Session } from '../../types'
import {
  cancelRun,
  createRun,
  createSession,
  deleteSession,
  getRun,
  listRuns,
  listSessions,
  renameSession,
  resumeRun,
  streamRunEvents,
  type SseHandle,
} from '../../lib/api'
import { useAppStore } from '../../lib/store'
import { useSmoothedChats } from '../../lib/smoothing'
import { workPath } from '../../lib/routes'
import BatchCard from '../run/BatchCard'
import ContextMeter from '../run/ContextMeter'
import MessageActions from '../run/MessageActions'
import NoticeDock from '../run/NoticeDock'
import ToolCard, { type FunctionCallItem } from '../run/ToolCard'
import TodoPanel from '../run/TodoPanel'
import { inferIntent } from '../../lib/intent'
import { SESSION_POINTER } from '../../lib/sessionPointer'
import {
  dedupeEvents,
  latestTasks,
  mergeEvent,
  runMessages,
  splitBoundaryNotices,
  type DialogueMessage,
} from '../../lib/responses'

const { Text, Title } = Typography
const { Configure } = AIChatInput

/** 每个页面"当前选中的会话"（localStorage，值 = 会话 id）。 */
/** 聊天框里选的思考等级（localStorage；跟设置页同一套 off|low|medium|high|xhigh|max）。 */
const EFFORT_KEY = 'wikiatlas.effort'
/** 档位：自动=不指定（用设置页的值）；其余原样进 Responses 的 `reasoning.effort`。 */
const EFFORT_LEVELS: { value: string; label: string }[] = [
  { value: 'auto', label: '自动' },
  { value: 'off', label: '关闭' },
  { value: 'low', label: '低' },
  { value: 'medium', label: '中' },
  { value: 'high', label: '高' },
  { value: 'xhigh', label: '超高' },
  { value: 'max', label: '最高' },
]
/** 面板里一次最多回放多少单；更早的留在工单页。 */
const SESSION_RUN_LIMIT = 12
/** 用户角色的显示名：roleConfig 与头像渲染必须用同一个，否则头像挑不中角色。 */
const USER_ROLE_NAME = '我'

interface Props {
  workId?: string | null
  workTitle?: string
  docId?: string | null
}

interface SendContent {
  text?: string
  content?: unknown
}

interface Mention {
  type: 'work' | 'doc'
  id: string
  title: string
}

/** AIChatInput 的富文本内容 → 纯文本工单目标。 */
function extractText(contents?: SendContent[]): string {
  return (contents ?? [])
    .map((item) => item.text ?? (typeof item.content === 'string' ? item.content : ''))
    .join('\n')
    .trim()
}

function fmtSessionTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

const SKILLS = [
  {
    value: 'wiki-writing',
    label: 'Wiki Writing',
    description: '按 9 章骨架撰写中文百科条目',
    hasTemplate: false,
  },
]

export default function AiPanel({ workId, workTitle, docId }: Props) {
  const {
    setAiPanelOpen,
    notifyContentCommitted,
    refreshRuns,
    refreshTree,
    setStaging,
    nodes,
    docMode,
    docSelection,
    setDocSelection,
    ask,
    clearAsk,
    me,
  } = useAppStore()
  const nav = useNavigate()
  const loc = useLocation()
  const [run, setRun] = useState<Run | null>(null)
  const [events, setEvents] = useState<RunEvent[]>([])
  /** 本会话更早几单冻结下来的消息（含用户提问），接在当前 run 之前渲染。 */
  const [history, setHistory] = useState<DialogueMessage[]>([])
  const [busy, setBusy] = useState(false)
  const [restoring, setRestoring] = useState(false)
  // 会话状态
  const [sessionId, setSessionId] = useState<string | null>(null)
  const [sessions, setSessions] = useState<Session[]>([])
  const [sessionsOpen, setSessionsOpen] = useState(false)
  const [renaming, setRenaming] = useState<Session | null>(null)
  const [renameValue, setRenameValue] = useState('')
  // 思考等级：聊天框里随手切；存本地，下一条工单生效
  const [effort, setEffortState] = useState(() => localStorage.getItem(EFFORT_KEY) || 'auto')
  const setEffort = (v: string) => {
    setEffortState(v)
    localStorage.setItem(EFFORT_KEY, v)
  }
  const sseRef = useRef<SseHandle | null>(null)
  const panelRef = useRef<HTMLDivElement | null>(null)
  const dialogueRef = useRef<InstanceType<typeof AIChatDialogue> | null>(null)
  /** 用户往上翻看历史时不要抢滚动：只有贴着底部才自动跟 */
  const stickToBottom = useRef(true)
  const lastSeq = useRef(0)
  /** 刚在同一条会话里开始新单：跳过重载，别把面板里正在长的对话清掉。 */
  const skipLoad = useRef<string | null>(null)
  /** handleEvent 通过它读最新的树与路由：避免它随 nodes 变化重建（重建会重挂 SSE）。 */
  const envRef = useRef({ nodes, pathname: loc.pathname, nav })
  envRef.current = { nodes, pathname: loc.pathname, nav }
  // 用户在正文里的选区存在 store 里：点进面板会丢掉 DOM 选区，正文工具栏也要用同一份
  const selection = docSelection
  // @ 引用的上下文（对齐飞书「@ 添加资料」）：作品/资料列表 + 已选中的引用
  const [mentions, setMentions] = useState<Mention[]>([])
  const [mentionOpen, setMentionOpen] = useState(false)
  const composerRef = useRef<InstanceType<typeof AIChatInput> | null>(null)

  /** 当前任务清单（最后一次 wikiatlas.todo 的全量列表），给输入框上方的任务面板用 */
  const todoTasks = useMemo(() => latestTasks(events), [events])
  /** 宿主的播报与护栏提醒（wikiatlas.notice）：不是模型说的话，所以不进对话。
   *  只收**过程里**的提醒（预算、护栏）——对话前/后的事实已经作为边界行
   *  贴进对话流（splitBoundaryNotices），不在浮层里重复显示。 */
  const notices = useMemo(() => splitBoundaryNotices(events).mid, [events])

  const mentionCandidates = useMemo<Mention[]>(() => {
    const list: Mention[] = []
    if (workId && workTitle) list.push({ type: 'work', id: workId, title: workTitle })
    for (const n of nodes) {
      if (n.id === workId) continue
      list.push({ type: 'work', id: n.id, title: n.title })
      if (list.length >= 8) break
    }
    return list
  }, [nodes, workId, workTitle])

  /** 选中一条引用：挂上芯片，并把输入框里那个 @ 删掉（飞书是 @ 变成芯片）。 */
  const addMention = (m: Mention) => {
    setMentions((prev) => (prev.some((x) => x.id === m.id) ? prev : [...prev, m]))
    setMentionOpen(false)
    const editor = composerRef.current?.getEditor?.() as
      | { state: { selection: { from: number } }; chain: () => any }
      | undefined
    try {
      const from = editor?.state?.selection?.from
      if (editor && typeof from === 'number' && from > 0) {
        editor
          .chain()
          .focus()
          .deleteRange({ from: from - 1, to: from })
          .run()
      }
    } catch {
      // 删不掉也无妨：发送时会把结尾的 @ 去掉
    }
  }

  // 会话归属的页面：同一篇文档/首页共用一批会话，切换页面换一批。
  const targetKey = useMemo(
    () => (docId ? `doc:${docId}` : workId ? `work:${workId}` : 'home'),
    [docId, workId],
  )
  const currentSession = useMemo(
    () => sessions.find((s) => s.id === sessionId) ?? null,
    [sessions, sessionId],
  )

  const refreshSessions = useCallback(async (target: string): Promise<Session[]> => {
    try {
      const res = await listSessions(target)
      return res.sessions ?? []
    } catch {
      return []
    }
  }, [])

  /** 把面板清到空态（切页面/切会话/没有会话时）。 */
  const clearConversation = useCallback(() => {
    setRun(null)
    setEvents([])
    setHistory([])
    lastSeq.current = 0
    sseRef.current?.close()
    sseRef.current = null
  }, [])

  // 切换页面：先清干净旧对话，再选出该页面"当前会话"
  // （本地记着的会话不在了就退到最近一段；一段都没有就空着，发消息时现开）。
  useEffect(() => {
    let cancelled = false
    clearConversation()
    void (async () => {
      const list = await refreshSessions(targetKey)
      if (cancelled) return
      setSessions(list)
      const saved = localStorage.getItem(SESSION_POINTER + targetKey)
      const pick = (saved && list.some((s) => s.id === saved) ? saved : list[0]?.id) ?? null
      setSessionId(pick)
    })()
    return () => {
      cancelled = true
    }
  }, [targetKey, refreshSessions, clearConversation])

  const handleEvent = useCallback(
    (ev: RunEvent) => {
      setEvents((prev) => {
        lastSeq.current = Math.max(lastSeq.current, ev.seq)
        return mergeEvent(prev, ev)
      })
      // 对话本体（response.*）由 lib/responses 的归约器处理，这里只管旁路事件
      // 的副作用：正文写入回执、作品树刷新、终态。
      if (ev.type === 'wikiatlas.content.staging') {
        const p = ev.payload as { targetId?: string; targetType?: string }
        if (p.targetId) setStaging({ targetId: p.targetId, targetType: p.targetType ?? 'work' })
      }
      if (ev.type === 'wikiatlas.content.committed') {
        const p = ev.payload as { targetId?: string; targetType?: string; version?: number }
        if (p.targetId) {
          notifyContentCommitted(p.targetId, p.version ?? 0)
          setStaging(null)
          // 只在"这条正文确实存在"时才跳过去，避免收藏的旧工单指向已删除节点
          const { nodes: curNodes, pathname, nav: curNav } = envRef.current
          const exists = p.targetType === 'work' && curNodes.some((n) => n.id === p.targetId)
          if (exists && !pathname.startsWith(`/w/${p.targetId}`)) {
            curNav(workPath(p.targetId as string))
          }
        }
      }
      if (ev.type === 'wikiatlas.tree') void refreshTree()
      if (ev.type === 'response.completed' || ev.type === 'response.failed') {
        setStaging(null)
        // 本地状态跟上终态：否则 generating 一直是 true，输入框永远显示「停止」，
        // 下一条消息发不出去（必须刷新页面才能继续——真实踩到）。
        // completedAt 也要补上：本地这份 run 是建单时的旧对象，缺了它末尾计时会瞬间归零。
        const status: Run['status'] = ev.type === 'response.completed' ? 'completed' : 'failed'
        setRun((prev) =>
          prev && prev.id === ev.runId ? { ...prev, status, completedAt: new Date().toISOString() } : prev,
        )
        void refreshRuns()
      }
    },
    [notifyContentCommitted, refreshRuns, refreshTree, setStaging],
  )

  const subscribe = useCallback(
    (runId: string) => {
      sseRef.current?.close()
      sseRef.current = streamRunEvents(runId, handleEvent, {
        lastEventId: lastSeq.current > 0 ? String(lastSeq.current) : undefined,
      })
    },
    [handleEvent],
  )

  // 切会话（或刚开新会话）时重建整段对话：更早的单冻结成历史，
  // 最新一单接到 SSE 上接着长（还在跑的单刷新后也能继续看）。
  useEffect(() => {
    if (sessionId && skipLoad.current === sessionId) {
      skipLoad.current = null
      return
    }
    clearConversation()
    if (!sessionId) return
    let cancelled = false
    setRestoring(true)
    void (async () => {
      try {
        const res = await listRuns(undefined, SESSION_RUN_LIMIT, sessionId).catch(() => null)
        const runs = (res?.runs ?? []).slice().reverse() // 倒序 → 时间顺序
        if (cancelled || runs.length === 0) return
        const frozen: DialogueMessage[] = []
        const newestID = runs[runs.length - 1].id
        for (const r of runs) {
          const detail = await getRun(r.id).catch(() => null)
          if (cancelled) return
          if (!detail?.run) continue
          if (r.id === newestID) {
            const evs = dedupeEvents(detail.events ?? [])
            setRun(detail.run)
            setEvents(evs)
            lastSeq.current = evs.length ? evs[evs.length - 1].seq : 0
            if (detail.run.status === 'running') subscribe(r.id)
            continue
          }
          frozen.push(...runMessages(r, detail.events ?? []))
        }
        if (!cancelled) setHistory(frozen)
      } finally {
        if (!cancelled) setRestoring(false)
      }
    })()
    return () => {
      cancelled = true
      sseRef.current?.close()
      sseRef.current = null
    }
  }, [sessionId, subscribe, clearConversation])

  // 这里曾经挂过一个全局 selectionchange：正文区选中 ≥2 个字就立刻 setDocSelection，
  // 理由是"点进面板会丢掉 DOM 选区"。真实体验是灾难：选中一段想**复制**，松手选段
  // 就进了聊天框（等于松手即 @），而且会一直挂着、跟着后面每条消息走。
  // 现在选段引用只走**显式动作**：正文工具栏的「问 Altas / 翻译 / 解释」。

  const send = async (text: string, forceIntent?: RunIntent) => {
    // 输入框里结尾的 "@" 只是唤起引用列表用的，别带进工单目标
    const goal = text.trim().replace(/@$/, '').trim()
    if (!goal) return
    const intent: RunIntent = forceIntent ?? inferIntent(goal, { docMode, workId, docId })
    setBusy(true)
    try {
      // 页面下还没有任何会话时现开一段（第一段会话的 id 就是页面本身）。
      let sid = sessionId
      if (!sid) {
        const created = await createSession({ target: targetKey })
        sid = created.session.id
        skipLoad.current = sid
        setSessionId(sid)
      }
      // 上一单的这段（提问 + 回答）先扣在手里，等新单就位时**同一批**换过去。
      // 不能就地 setHistory：那之后到 setRun 之间隔着 await，中间那次渲染里旧单会
      // 同时出现在 history 与当前 run 里，消息键（q-/a-<runId>）于是重复——真实观测到
      // React 报 "Encountered two children with the same key"。
      const frozenPrev = run ? runMessages(run, events) : []
      const context: Record<string, string> = {}
      if (workId) context.workId = workId
      if (docId) context.docId = docId
      // 修订模式的作用域：用户在正文里选中的那段（对齐飞书「选定内容」）
      if (selection) context.selection = selection
      // @ 引用的条目：让 Altas 知道"这条消息说的是哪几篇"
      if (mentions.length) {
        context.mentioned = mentions.map((m) => `${m.title}(${m.type}:${m.id})`).join('、')
      }
      // 把用户当前所处的模式发给 Altas：只读=只分析、编辑=直接改、修订=定点改+给理由
      const res = await createRun({
        intent,
        goal,
        workspace: sid,
        reasoningEffort: effort === 'auto' ? undefined : effort,
        context: {
          ...context,
          docMode: docMode === 'revision' ? 'revision' : docMode === 'read' ? 'read' : 'edit',
        },
      })
      const runId = (res as { runId?: string }).runId ?? (res as Run).id
      if (!runId) throw new Error('未返回 runId')
      localStorage.setItem(SESSION_POINTER + targetKey, sid)
      lastSeq.current = 0
      // 一次性交接：旧单进历史、当前单清空，同一批提交——旧单不会同时出现在两边。
      setHistory((prev) => [...prev, ...frozenPrev])
      setEvents([])
      setRun(null)
      subscribe(runId)
      const detail = await getRun(runId).catch(() => null)
      if (detail?.run) setRun(detail.run)
      void refreshRuns()
      void refreshSessions(targetKey).then(setSessions)
      setMentions([])
      // 选段引用也是一次性的：它已经跟着这条工单发出去了，别留在输入框里
      // 跟着后面每条消息走（以前只有点 X 才清）。
      setDocSelection('')
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '创建工单失败')
    } finally {
      setBusy(false)
    }
  }

  const resume = async () => {
    if (!run) return
    setBusy(true)
    try {
      await resumeRun(run.id)
      setRun((prev) => (prev ? { ...prev, status: 'running', completedAt: null } : prev))
      subscribe(run.id)
      void refreshRuns()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '继续工单失败')
    } finally {
      setBusy(false)
    }
  }

  const stop = async () => {
    if (!run) return
    try {
      await cancelRun(run.id)
      setRun((prev) =>
        prev ? { ...prev, status: 'interrupted', completedAt: new Date().toISOString() } : prev,
      )
      Toast.info('已停止，工单可稍后继续')
      void refreshRuns()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '停止失败')
    }
  }

  // --- 会话操作 ---

  /** 开一段新对话（复用"开出来还没说过话"的空白会话，不反复堆）。 */
  const startNewChat = async () => {
    if (busy) return
    if (currentSession && currentSession.runCount === 0) {
      setSessionsOpen(false)
      return
    }
    try {
      if (run?.status === 'running') {
        Toast.info('旧工单会继续在后台执行，可在工单页查看')
      }
      const created = await createSession({ target: targetKey })
      localStorage.setItem(SESSION_POINTER + targetKey, created.session.id)
      setSessionId(created.session.id)
      void refreshSessions(targetKey).then(setSessions)
      setSessionsOpen(false)
      window.requestAnimationFrame(() => {
        document.querySelector<HTMLElement>('.ai-panel .ProseMirror')?.focus()
      })
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '创建会话失败')
    }
  }

  const switchSession = (id: string) => {
    setSessionsOpen(false)
    if (id === sessionId) return
    localStorage.setItem(SESSION_POINTER + targetKey, id)
    setSessionId(id)
  }

  const removeSession = (sess: Session) => {
    Modal.confirm({
      title: '删除这段会话？',
      content: `「${sess.title || '新对话'}」及其 ${sess.runCount} 条工单会被一起删除，不可恢复。`,
      okText: '删除',
      cancelText: '取消',
      okButtonProps: { type: 'danger', theme: 'solid' },
      onOk: async () => {
        try {
          await deleteSession(sess.id)
          const list = await refreshSessions(targetKey)
          setSessions(list)
          if (sess.id === sessionId) {
            const next = list[0]?.id ?? null
            if (next) localStorage.setItem(SESSION_POINTER + targetKey, next)
            else localStorage.removeItem(SESSION_POINTER + targetKey)
            setSessionId(next)
          }
          Toast.success('已删除会话')
        } catch (e) {
          Toast.error(e instanceof Error ? e.message : '删除失败')
        }
      },
    })
  }

  const submitRename = async () => {
    if (!renaming) return
    const title = renameValue.trim()
    if (!title) {
      Toast.warning('会话名称不能为空')
      return
    }
    try {
      await renameSession(renaming.id, title)
      setRenaming(null)
      void refreshSessions(targetKey).then(setSessions)
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '重命名失败')
    }
  }

  const canResume = !!run && (run.status === 'interrupted' || run.status === 'failed')
  const running = run?.status === 'running' || busy

  // 正文工具栏的提问：问 Altas = 挂上选段等用户打字；翻译/解释 = 直接发出去
  const sendRef = useRef(send)
  sendRef.current = send
  useEffect(() => {
    if (!ask) return
    const { prompt, autoSend, intent } = ask
    clearAsk()
    if (autoSend && prompt) {
      void sendRef.current(prompt, intent)
      return
    }
    window.requestAnimationFrame(() => {
      const input = document.querySelector<HTMLElement>('.ai-panel .ProseMirror')
      input?.focus()
    })
  }, [ask, clearAsk])

  // 工具卡：function_call 输出项的自定义渲染（Semi 原生的扩展点）。
  // 一个函数就覆盖了所有工具——卡片内部按工具名换图标与措辞。
  // wikiatlas.turn 是我们注入的 run 边界行（对话前/对话后的事实，见 responses.ts
  // 的 splitBoundaryNotices）——Semi 的内容项渲染先查自定义键，所以这个自定义
  // 类型不会被内置渲染器吞掉。
  const contentItemRenderers = useMemo(
    () => ({
      function_call: (item: FunctionCallItem) => <ToolCard item={item} />,
      'wikiatlas.turn': (item: { phase?: string; text?: string; tone?: string }) => (
        <div className={`ai-turn-row is-${item.tone || 'info'}`}>
          <span className="ai-turn-dot" />
          <span className="ai-turn-text">{item.text}</span>
        </div>
      ),
    }),
    [],
  )

  // 头像：不给 avatar 时 Semi 只画一个空灰圆——那是每屏都看得见的"没做完"。
  // AI 用 Altas 标（VISUAL_SPEC §2：紫只准出现在 AI 元素上），
  // 用户用设置页里管理员标识的首字（仅显示用，不代表任何权限）。
  const dialogueRenderConfig = useMemo(
    () => ({
      renderDialogueAvatar: (props: { role?: { name?: string } }) => {
        if (props.role?.name === USER_ROLE_NAME) {
          return (
            <Avatar size="extra-small" className="ai-avatar-user">
              {(me.username || '我').trim().slice(0, 1)}
            </Avatar>
          )
        }
        return (
          <Avatar size="extra-small" className="ai-avatar-altas">
            <IconAIFilledLevel1 />
          </Avatar>
        )
      },
      // 操作条只留真能用的那个（复制）——默认的一排里分享/编辑/点赞/删除
      // 我们一个回调都没接，点了没反应（真实反馈"这些按钮都不起作用"）。
      renderDialogueAction: (props: { message?: DialogueMessage; className?: string }) => (
        <MessageActions message={props.message} className={props.className} />
      ),
    }),
    [me.username],
  )

  // 事件流 → Semi 消息：整段会话 = 早先几单冻结的历史 + 当前一单的实况。
  // 当前单的提问从 run.goal 派生（这样刷新召回后也不会丢"你说的话"）。
  const chats = useMemo(
    () => [
      ...history,
      ...(run ? runMessages(run, events) : []),
    ],
    [history, events, run],
  )

  // 打字机回放：网关一次砸来 430 字的整段时，让它按节奏吐出来（归约层不动）。
  const chatsShown = useSmoothedChats(chats, !!run && run.status === 'running')

  // 输入框顶部槽：任务面板 + 提醒条。它们都是"这个工单现在的状态"，
  // 不是对话内容——所以跟输入框走，而不是混进对话流。
  // 用量行**不在这里**：它已经被输入框右下角的上下文环取代，轮数/累计 token/
  // 缓存命中率属于工单统计，在工单页的对话详情里看。
  const topSlot = (
    <>
      <TodoPanel tasks={todoTasks} />
      <NoticeDock notices={notices} />
    </>
  )

  // 流式追加时保持贴底（用户翻历史就不抢）
  useEffect(() => {
    const box = panelRef.current?.querySelector('.semi-ai-chat-dialogue-list') as HTMLElement | null
    if (!box) return
    const onScroll = () => {
      stickToBottom.current = box.scrollHeight - box.scrollTop - box.clientHeight < 80
    }
    box.addEventListener('scroll', onScroll)
    return () => box.removeEventListener('scroll', onScroll)
  }, [run])

  useEffect(() => {
    if (run && stickToBottom.current) dialogueRef.current?.scrollToBottom(false)
  }, [events, run])

  const sessionList = (
    <div className="ai-session-list">
      <div className="ai-session-list-head">
        <Text strong size="small">
          本页面的会话
        </Text>
        <Text type="tertiary" size="small">
          {sessions.length} 段
        </Text>
      </div>
      {sessions.length === 0 ? (
        <div className="ai-session-empty">还没有会话，发一句话就会开一段</div>
      ) : (
        sessions.map((s) => (
          <div
            key={s.id}
            className={`ai-session-item${s.id === sessionId ? ' is-active' : ''}`}
            role="button"
            tabIndex={0}
            onClick={() => switchSession(s.id)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') switchSession(s.id)
            }}
          >
            <span className={`ai-session-dot is-${s.status || 'blank'}`} />
            <span className="ai-session-main">
              <span className="ai-session-title">{s.title || '新对话'}</span>
              <span className="ai-session-meta">
                {fmtSessionTime(s.updatedAt)} · {s.runCount > 0 ? `${s.runCount} 单` : '空白'}
              </span>
            </span>
            <span className="ai-session-ops">
              <Button
                theme="borderless"
                type="tertiary"
                size="small"
                icon={<IconEdit />}
                aria-label="重命名会话"
                onClick={(e) => {
                  e.stopPropagation()
                  setRenaming(s)
                  setRenameValue(s.title)
                }}
              />
              <Button
                theme="borderless"
                type="tertiary"
                size="small"
                icon={<IconDelete />}
                aria-label="删除会话"
                onClick={(e) => {
                  e.stopPropagation()
                  removeSession(s)
                }}
              />
            </span>
          </div>
        ))
      )}
    </div>
  )

  return (
    <div className="ai-panel" ref={panelRef}>
      <div className="ai-panel-header">
        <div className="ai-panel-title">
          <span className="ai-panel-mark">
            <IconAIFilledLevel1 />
          </span>
          <Title heading={6} style={{ margin: 0 }}>
            Altas
          </Title>
          {workTitle && (
            <Text type="tertiary" size="small" ellipsis={{ showTooltip: true }}>
              {workTitle}
            </Text>
          )}
        </div>
        <div className="ai-panel-actions">
          <Button
            theme="borderless"
            type="tertiary"
            size="small"
            icon={<IconPlus />}
            aria-label="开始新对话"
            onClick={() => void startNewChat()}
          >
            新对话
          </Button>
          <Popover
            visible={sessionsOpen}
            trigger="custom"
            position="bottomRight"
            onClickOutSide={() => setSessionsOpen(false)}
            content={sessionList}
          >
            <Button
              theme="borderless"
              type="tertiary"
              size="small"
              icon={<IconHistory />}
              aria-label="会话列表"
              onClick={() => {
                // 打开时顺手刷新：状态点/标题别停在旧值
                if (!sessionsOpen) void refreshSessions(targetKey).then(setSessions)
                setSessionsOpen((v) => !v)
              }}
            />
          </Popover>
          <Button
            theme="borderless"
            type="tertiary"
            icon={<IconClose />}
            size="small"
            aria-label="收起 Altas"
            onClick={() => setAiPanelOpen(false)}
          />
        </div>
      </div>

      <div className="ai-panel-body">
        <BatchCard />
        {restoring ? (
          <div className="ai-empty">
            <Text type="tertiary" size="small">
              正在召回这段会话…
            </Text>
          </div>
        ) : chats.length === 0 ? (
          <div className="ai-empty">
            <div className="ai-empty-icon">
              <IconAIFilledLevel1 />
            </div>
            <div className="ai-empty-title">今天有什么工作需要处理？</div>
          </div>
        ) : (
          <>
            {currentSession && currentSession.runCount > SESSION_RUN_LIMIT && (
              <div className="ai-thread-more">
                <Text type="tertiary" size="small">
                  本会话共 {currentSession.runCount} 单，这里显示最近 {SESSION_RUN_LIMIT} 单（更早的在工单页）
                </Text>
              </div>
            )}
            <AIChatDialogue
              ref={dialogueRef}
              chats={chatsShown as never}
              roleConfig={{ assistant: { name: 'Altas' }, user: { name: USER_ROLE_NAME } }}
              // userBubble 是 Semi 原生的模式：用户消息出气泡、助手留时间线。
              mode="userBubble"
              showReset={false}
              markdownRenderProps={{ className: 'ai-md' }}
              renderDialogueContentItem={contentItemRenderers as never}
              dialogueRenderConfig={dialogueRenderConfig as never}
            />
            {canResume && (
              <div className="ai-resume-row">
                <Tag size="small" color="orange">
                  {run?.status === 'failed' ? '工单失败' : '工单已中断'}
                </Tag>
                <Button size="small" icon={<IconRefresh />} loading={busy} onClick={() => void resume()}>
                  继续这个工单
                </Button>
              </div>
            )}
          </>
        )}
      </div>

      <Popover
        visible={mentionOpen}
        trigger="custom"
        position="topLeft"
        onClickOutSide={() => setMentionOpen(false)}
        content={
          <div className="ai-mention-list">
            {mentionCandidates.length === 0 ? (
              <div className="ai-mention-empty">暂无可引用的条目</div>
            ) : (
              mentionCandidates.map((m) => (
                <button
                  key={`${m.type}:${m.id}`}
                  type="button"
                  className="ai-mention-item"
                  onClick={() => addMention(m)}
                >
                  <span className="ai-mention-title">{m.title}</span>
                  <span className="ai-mention-kind">{m.type === 'work' ? '作品' : '资料'}</span>
                </button>
              ))
            )}
          </div>
        }
      >
      <div className="ai-panel-input">
        {selection && (
          <div className="ai-selection-chip">
            <Tag size="small" color={docMode === 'revision' ? 'orange' : 'grey'}>
              已引用选段 {selection.length} 字
            </Tag>
            <Text type="tertiary" size="small" ellipsis={{ showTooltip: true }}>
              {selection.replace(/\s+/g, ' ').slice(0, 40)}
            </Text>
            <Button
              theme="borderless"
              type="tertiary"
              size="small"
              icon={<IconClose />}
              aria-label="取消引用"
              onClick={() => setDocSelection('')}
            />
          </div>
        )}
        <AIChatInput
          ref={composerRef}
          // 高度上限：Semi 的 aiChatInput CSS 里**没有 max-height**，编辑器会跟着内容
          // 无限长高，把上面的对话挤没（实测塞 12 行 = 430px 且还在长）。
          // 走 style prop（它落在根节点 .semi-aiChatInput 上，根是 flex column），
          // 内部 .semi-aiChatInput-editor-content 本来就是 flex:1 + min-height:0 + overflow:auto，
          // 所以一给上限就自己滚——不需要反写任何 .semi-* 类。
          style={{ maxHeight: 'min(40vh, 320px)' }}
          // 任务面板 / 提醒条 / 用量行挂在输入框**原生的顶部槽**（不是旁边塞一个 div）：
          // renderTopSlot 是叠加的——源码里它和引用、附件按 topSlotPosition 拼在一起，
          // 所以 @ 芯片照常显示。默认收起、点开向上展开（topSlotPosition 默认 'top'）。
          renderTopSlot={() => topSlot}
          keepSkillAfterSend={false}
          showUploadButton={false}
          showTemplateButton={false}
          skills={SKILLS}
          renderConfigureArea={() => (
            <Configure.Select optionList={EFFORT_LEVELS} field="effort" initValue={effort} />
          )}
          // renderActionArea 是**替换**不是追加：它把默认的操作区交给你，
          // 收到的是 { menuItem, className }，menuItem 里就是发送按钮。
          // 只渲染自己的东西会连带把发送按钮吃掉——必须把 menuItem 放回去。
          renderActionArea={({ menuItem }) => (
            // footer 是 space-between 的横排：环若作为独立孩子会落在**整行正中间**
            // （真实现象：环飘在行中央、离发送键半个面板远）。包成一个 flex 组，
            // footer 只剩"配置 | [环+发送]"两个孩子 → 环紧挨发送键。
            <div className="ai-input-actions">
              <ContextMeter events={events} />
              {menuItem}
            </div>
          )}
          onConfigureChange={(value: Record<string, unknown>) => {
            const v = value?.effort
            if (typeof v === 'string' && v) setEffort(v)
          }}
          placeholder="发消息或创建任务... 使用技能 @ 添加资料"
          canSend={!busy}
          generating={running}
          references={mentions.map((m) => ({ type: m.type, id: m.id, name: m.title }))}
          renderReference={(ref) => (
            <span className="ai-mention-chip">
              <IconClose
                size="small"
                onClick={() => setMentions((prev) => prev.filter((m) => m.id !== ref.id))}
              />
              {String(ref.name ?? ref.id)}
            </span>
          )}
          onContentChange={(contents) => {
            const text = extractText(contents as SendContent[])
            if (text.endsWith('@')) setMentionOpen(true)
            else if (mentionOpen) setMentionOpen(false)
          }}
          onMessageSend={(msg) => void send(extractText(msg.inputContents as SendContent[]))}
          onStopGenerate={() => void stop()}
        />
      </div>
      </Popover>

      <Modal
        title="重命名会话"
        visible={!!renaming}
        onCancel={() => setRenaming(null)}
        onOk={async () => {
          await submitRename()
        }}
        okText="保存"
        cancelText="取消"
        width={380}
      >
        <Input
          value={renameValue}
          maxLength={60}
          placeholder="会话名称"
          onChange={(v: string) => setRenameValue(v)}
          onEnterPress={() => void submitRename()}
        />
      </Modal>
    </div>
  )
}
