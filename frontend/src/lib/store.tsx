import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Toast } from '@douyinfe/semi-ui'
import type { Me, Run, WorkSummary } from '../types'
import { getMe, getTree, listRuns } from '../lib/api'
import { MAX_PINS, readPins, writePins } from '../lib/pins'

export type DocMode = 'edit' | 'revision' | 'read'

/** Altas 悬浮窗的位置与尺寸（视口坐标，px）。 */
export interface ChatRect {
  x: number
  y: number
  w: number
  h: number
}

const CHAT_RECT_KEY = 'wikiatlas.chatRect'

/** 读回上次的窗口位置；读不出/字段不全都当"没定过"，由窗口按视口算默认值。 */
function readChatRect(): ChatRect | null {
  try {
    const raw = localStorage.getItem(CHAT_RECT_KEY)
    if (!raw) return null
    const r = JSON.parse(raw) as ChatRect
    if (![r?.x, r?.y, r?.w, r?.h].every((n) => Number.isFinite(n))) return null
    return r
  } catch {
    return null
  }
}

interface AppStore {
  nodes: WorkSummary[]
  treeLoading: boolean
  treeError: string | null
  refreshTree: () => Promise<void>
  /** 简易用户系统：访客=false，只能读公开文档 */
  me: Me
  /** /api/auth/me 是否已经回来过（用于避免"还没设密码"横幅一闪） */
  meLoaded: boolean
  refreshMe: () => Promise<void>
  aiPanelOpen: boolean
  setAiPanelOpen: (open: boolean) => void
  /** AI 侧栏宽度（贴右侧、可拖拽；localStorage 记忆） */
  /** Altas 悬浮窗的位置与尺寸（视口坐标 px）；null = 还没定过，由窗口按视口算默认值 */
  chatRect: ChatRect | null
  setChatRect: (r: ChatRect | null) => void
  activeRuns: Run[]
  refreshRuns: () => Promise<void>
  /** bump when AI committed content so views can refresh */
  contentStamp: number
  notifyContentCommitted: (targetId: string, version: number) => void
  lastCommitted: { targetId: string; version: number } | null
  /** Altas 正在写入的正文（wikiatlas.content.staging 带过来的，用于"正在写入"标记） */
  staging: { targetId: string; targetType: string } | null
  setStaging: (v: { targetId: string; targetType: string } | null) => void
  /**
   * 文档模式（对齐飞书顶栏「编辑 ▾」下拉）：
   * edit = 所见即所得编辑；revision = Markdown 修订；read = 只读。
   * 编辑器内部不再放模式按钮。
   */
  docMode: DocMode
  setDocMode: (mode: DocMode) => void
  /** 置顶的节点 id（首期本地存储，见 lib/pins.ts） */
  pinnedIds: string[]
  togglePin: (id: string) => void
  /** 正文选区（正文工具栏与 AI 面板共用：Altas 要知道"改的是哪一段"） */
  docSelection: string
  setDocSelection: (text: string) => void
  /**
   * 正文工具栏发起的一次提问，由 AI 面板消费：
   * prompt 为空 = 只把选段交给面板等用户提问；autoSend = 直接发出去（如「翻译这段」）。
   * intent = 'answer' 时走问答工单：不落正文、只回话（翻译/解释这类一次性输出）。
   */
  ask: { prompt: string; autoSend: boolean; intent?: 'answer' } | null
  askSelection: (opts: {
    selection: string
    prompt?: string
    autoSend?: boolean
    intent?: 'answer'
  }) => void
  clearAsk: () => void
  sidebarCollapsed: boolean
  setSidebarCollapsed: (v: boolean) => void
  /** 当前正在看的批次（批量建档）；面板顶部显示批次卡 */
  activeBatchId: string | null
  setActiveBatchId: (id: string | null) => void
}

const Ctx = createContext<AppStore | null>(null)

export function AppProvider({ children }: { children: ReactNode }) {
  const [nodes, setNodes] = useState<WorkSummary[]>([])
  const [me, setMe] = useState<Me>({
    authed: false,
    username: '',
    adminPasswordConfigured: false,
  })
  const [meLoaded, setMeLoaded] = useState(false)
  const [treeLoading, setTreeLoading] = useState(true)
  const [treeError, setTreeError] = useState<string | null>(null)
  const [aiPanelOpen, setAiPanelOpen] = useState(false)
  const [chatRect, setChatRectState] = useState<ChatRect | null>(readChatRect)
  const [activeRuns, setActiveRuns] = useState<Run[]>([])
  const [contentStamp, setContentStamp] = useState(0)
  const [lastCommitted, setLastCommitted] = useState<{ targetId: string; version: number } | null>(
    null,
  )
  const [staging, setStaging] = useState<{ targetId: string; targetType: string } | null>(null)
  const [docMode, setDocMode] = useState<DocMode>('edit')
  const [pinnedIds, setPinnedIds] = useState<string[]>(() => readPins())
  const [docSelection, setDocSelection] = useState('')
  const [ask, setAsk] = useState<{ prompt: string; autoSend: boolean; intent?: 'answer' } | null>(
    null,
  )
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [activeBatchId, setActiveBatchIdState] = useState<string | null>(() =>
    localStorage.getItem('wikiatlas.batch'),
  )

  const setActiveBatchId = useCallback((id: string | null) => {
    setActiveBatchIdState(id)
    try {
      if (id) localStorage.setItem('wikiatlas.batch', id)
      else localStorage.removeItem('wikiatlas.batch')
    } catch {
      // storage disabled — 批次卡降级为会话内状态
    }
  }, [])

  const togglePin = useCallback(
    (id: string) => {
      const isPinned = pinnedIds.includes(id)
      if (!isPinned && pinnedIds.length >= MAX_PINS) {
        Toast.warning(`最多置顶 ${MAX_PINS} 篇——先取消一个再置顶`)
        return
      }
      setPinnedIds((prev) => {
        const next = prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]
        writePins(next)
        return next
      })
    },
    [pinnedIds],
  )

  const askSelection = useCallback(
    ({
      selection,
      prompt = '',
      autoSend = false,
      intent,
    }: {
      selection: string
      prompt?: string
      autoSend?: boolean
      intent?: 'answer'
    }) => {
      setDocSelection(selection)
      setAsk({ prompt, autoSend, intent })
      setAiPanelOpen(true)
    },
    [],
  )

  const clearAsk = useCallback(() => setAsk(null), [])

  const setChatRect = useCallback((r: ChatRect | null) => {
    setChatRectState(r)
    try {
      if (r) localStorage.setItem(CHAT_RECT_KEY, JSON.stringify(r))
    } catch {
      // 存不了就只在本次会话生效
    }
  }, [])

  const refreshTree = useCallback(async () => {
    setTreeLoading(true)
    setTreeError(null)
    try {
      const res = await getTree()
      setNodes(res.nodes ?? [])
    } catch (e) {
      setTreeError(e instanceof Error ? e.message : '加载树失败')
      setNodes([])
    } finally {
      setTreeLoading(false)
    }
  }, [])

  const refreshRuns = useCallback(async () => {
    try {
      const res = await listRuns()
      const runs = res.runs ?? []
      setActiveRuns(runs.filter((r) => r.status === 'running' || r.status === 'interrupted'))
    } catch {
      setActiveRuns([])
    }
  }, [])

  const refreshMe = useCallback(async () => {
    try {
      setMe(await getMe())
    } catch {
      setMe({ authed: false, username: '', adminPasswordConfigured: false })
    } finally {
      setMeLoaded(true)
    }
  }, [])

  const notifyContentCommitted = useCallback((targetId: string, version: number) => {
    setLastCommitted({ targetId, version })
    setContentStamp((n) => n + 1)
    void refreshTree()
  }, [refreshTree])

  useEffect(() => {
    void refreshMe()
    void refreshTree()
  }, [refreshMe, refreshTree])

  // 工单是管理态专属：访客不请求 /api/runs（否则每轮轮询都吃 401）
  useEffect(() => {
    if (me.authed) void refreshRuns()
    else setActiveRuns([])
  }, [me.authed, refreshRuns])

  const value = useMemo<AppStore>(
    () => ({
      nodes,
      treeLoading,
      treeError,
      refreshTree,
      me,
      meLoaded,
      refreshMe,
      aiPanelOpen,
      setAiPanelOpen,
      chatRect,
      setChatRect,
      activeRuns,
      refreshRuns,
      contentStamp,
      notifyContentCommitted,
      lastCommitted,
      staging,
      setStaging,
      docMode,
      setDocMode,
      pinnedIds,
      togglePin,
      docSelection,
      setDocSelection,
      ask,
      askSelection,
      clearAsk,
      sidebarCollapsed,
      setSidebarCollapsed,
      activeBatchId,
      setActiveBatchId,
    }),
    [
      nodes,
      treeLoading,
      treeError,
      refreshTree,
      me,
      meLoaded,
      refreshMe,
      aiPanelOpen,
      chatRect,
      setChatRect,
      activeRuns,
      refreshRuns,
      contentStamp,
      notifyContentCommitted,
      lastCommitted,
      staging,
      docMode,
      pinnedIds,
      togglePin,
      docSelection,
      ask,
      askSelection,
      clearAsk,
      sidebarCollapsed,
      activeBatchId,
      setActiveBatchId,
    ],
  )

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useAppStore(): AppStore {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useAppStore outside AppProvider')
  return ctx
}
