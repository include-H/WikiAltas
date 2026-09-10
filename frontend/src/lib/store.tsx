import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import type { Me, Run, WorkSummary } from '../types'
import { getMe, getTree, listRuns } from '../lib/api'
import { readPins, writePins } from '../lib/pins'

export type DocMode = 'edit' | 'revision' | 'read'

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
  activeRuns: Run[]
  refreshRuns: () => Promise<void>
  /** bump when AI committed content so views can refresh */
  contentStamp: number
  notifyContentCommitted: (targetId: string, version: number) => void
  lastCommitted: { targetId: string; version: number } | null
  /** Altas 正在写入的正文（content.staging 投影，用于"正在写入"标记） */
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
   */
  ask: { prompt: string; autoSend: boolean } | null
  askSelection: (opts: { selection: string; prompt?: string; autoSend?: boolean }) => void
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
  const [activeRuns, setActiveRuns] = useState<Run[]>([])
  const [contentStamp, setContentStamp] = useState(0)
  const [lastCommitted, setLastCommitted] = useState<{ targetId: string; version: number } | null>(
    null,
  )
  const [staging, setStaging] = useState<{ targetId: string; targetType: string } | null>(null)
  const [docMode, setDocMode] = useState<DocMode>('edit')
  const [pinnedIds, setPinnedIds] = useState<string[]>(() => readPins())
  const [docSelection, setDocSelection] = useState('')
  const [ask, setAsk] = useState<{ prompt: string; autoSend: boolean } | null>(null)
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

  const togglePin = useCallback((id: string) => {
    setPinnedIds((prev) => {
      const next = prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]
      writePins(next)
      return next
    })
  }, [])

  const askSelection = useCallback(
    ({ selection, prompt = '', autoSend = false }: { selection: string; prompt?: string; autoSend?: boolean }) => {
      setDocSelection(selection)
      setAsk({ prompt, autoSend })
      setAiPanelOpen(true)
    },
    [],
  )

  const clearAsk = useCallback(() => setAsk(null), [])

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
