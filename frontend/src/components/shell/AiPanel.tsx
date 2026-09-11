import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import {
  AIChatDialogue,
  AIChatInput,
  Button,
  Popover,
  Tag,
  Toast,
  Typography,
} from '@douyinfe/semi-ui'
import { IconAIFilledLevel1, IconClose, IconRefresh } from '@douyinfe/semi-icons'
import type { Run, RunEvent, RunIntent } from '../../types'
import {
  cancelRun,
  createRun,
  getRun,
  listRuns,
  resumeRun,
  streamRunEvents,
  type SseHandle,
} from '../../lib/api'
import { useAppStore } from '../../lib/store'
import { workPath } from '../../lib/routes'
import BatchCard from '../run/BatchCard'
import RunSteps from '../run/RunSteps'
import { buildDialogueMessages, dedupeEvents } from '../../lib/runProjection'
import type { DialogueStep } from '../../lib/runProjection'

const { Text, Title } = Typography

const RUN_KEY = 'wikiatlas.run.'

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
  } = useAppStore()
  const nav = useNavigate()
  const loc = useLocation()
  const [run, setRun] = useState<Run | null>(null)
  const [events, setEvents] = useState<RunEvent[]>([])
  const [busy, setBusy] = useState(false)
  const [restoring, setRestoring] = useState(false)
  const sseRef = useRef<SseHandle | null>(null)
  const panelRef = useRef<HTMLDivElement | null>(null)
  const dialogueRef = useRef<InstanceType<typeof AIChatDialogue> | null>(null)
  /** 用户往上翻看历史时不要抢滚动：只有贴着底部才自动跟 */
  const stickToBottom = useRef(true)
  const lastSeq = useRef(0)
  // 用户在正文里的选区存在 store 里：点进面板会丢掉 DOM 选区，正文工具栏也要用同一份
  const selection = docSelection
  // @ 引用的上下文（对齐飞书「@ 添加资料」）：作品/资料列表 + 已选中的引用
  const [mentions, setMentions] = useState<Mention[]>([])
  const [mentionOpen, setMentionOpen] = useState(false)
  const composerRef = useRef<InstanceType<typeof AIChatInput> | null>(null)

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

  // 会话键：同一篇文档/首页共用一段对话，刷新或切文章后按它召回
  const workspace = useMemo(
    () => (docId ? `doc:${docId}` : workId ? `work:${workId}` : 'home'),
    [docId, workId],
  )

  const handleEvent = useCallback(
    (ev: RunEvent) => {
      setEvents((prev) => {
        if (prev.some((e) => e.id === ev.id)) return prev
        lastSeq.current = Math.max(lastSeq.current, ev.seq)
        return [...prev, ev]
      })
      if (ev.type === 'content.staging') {
        const p = ev.payload as { targetId?: string; targetType?: string }
        if (p.targetId) setStaging({ targetId: p.targetId, targetType: p.targetType ?? 'work' })
      }
      if (ev.type === 'content.committed') {
        const p = ev.payload as { targetId?: string; targetType?: string; version?: number }
        if (p.targetId) {
          notifyContentCommitted(p.targetId, p.version ?? 0)
          setStaging(null)
          // 只在"这条正文确实存在"时才跳过去，避免收藏的旧工单指向已删除节点
          const exists =
            p.targetType === 'work' && nodes.some((n) => n.id === p.targetId)
          if (exists && !loc.pathname.startsWith(`/w/${p.targetId}`)) {
            nav(workPath(p.targetId as string))
          }
        }
      }
      if (ev.type === 'tree.updated') void refreshTree()
      if (ev.type === 'run.completed' || ev.type === 'run.failed') {
        setStaging(null)
        void refreshRuns()
      }
    },
    [loc.pathname, nav, nodes, notifyContentCommitted, refreshRuns, refreshTree, setStaging],
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

  useEffect(() => {
    const onSelectionChange = () => {
      const sel = window.getSelection()
      const text = sel?.toString().trim() ?? ''
      if (text.length < 2) return
      const anchor = sel?.anchorNode
      const el = anchor instanceof Element ? anchor : anchor?.parentElement ?? null
      // 只认正文区的选区，避免把侧栏/面板里的文字当成修订范围
      if (el?.closest('.work-view')) setDocSelection(text.slice(0, 2000))
    }
    document.addEventListener('selectionchange', onSelectionChange)
    return () => document.removeEventListener('selectionchange', onSelectionChange)
  }, [setDocSelection])

  // 面板打开/切换文档时，把这段会话最近一个工单召回来（这就是"刷新即丢"的解药）
  useEffect(() => {
    let cancelled = false
    setRestoring(true)
    void (async () => {
      try {
        const saved = localStorage.getItem(RUN_KEY + workspace)
        // 以"这段会话最新的工单"为准：localStorage 里的可能是很早以前那次，
        // 之前优先读它，导致面板一直挂着最初的会话（例如已被取消的旧工单）。
        const res = await listRuns(undefined, 5, workspace).catch(() => null)
        const newest = res?.runs?.[0]?.id ?? null
        const runId = newest ?? saved
        if (!runId || cancelled) return
        const detail = await getRun(runId)
        if (cancelled || !detail?.run) return
        setRun(detail.run)
        const history = dedupeEvents(detail.events ?? [])
        setEvents(history)
        lastSeq.current = history.length ? history[history.length - 1].seq : 0
        if (detail.run.status === 'running') subscribe(runId)
        localStorage.setItem(RUN_KEY + workspace, runId)
      } catch {
        // 没有历史工单是正常状态
      } finally {
        if (!cancelled) setRestoring(false)
      }
    })()
    return () => {
      cancelled = true
      sseRef.current?.close()
      sseRef.current = null
    }
  }, [workspace, subscribe])

  const send = async (text: string, forceIntent?: RunIntent) => {
    // 输入框里结尾的 "@" 只是唤起引用列表用的，别带进工单目标
    const goal = text.trim().replace(/@$/, '').trim()
    if (!goal) return
    // 意图由上下文推导，界面上没有"功能按钮菜单"（VISUAL_SPEC §4）。
    // 例外：只读模式、以及正文工具栏的"一次性输出"动作（翻译/解释）一律走 answer —— 只回话不改文件。
    const intent: RunIntent =
      forceIntent ?? (docMode === 'read' ? 'answer' : docId ? 'write_doc' : workId ? 'continue_wiki' : 'create_wiki')
    setBusy(true)
    try {
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
        workspace,
        context: {
          ...context,
          docMode: docMode === 'revision' ? 'revision' : docMode === 'read' ? 'read' : 'edit',
        },
      })
      const runId = (res as { runId?: string }).runId ?? (res as Run).id
      if (!runId) throw new Error('未返回 runId')
      localStorage.setItem(RUN_KEY + workspace, runId)
      lastSeq.current = 0
      setEvents([])
      const detail = await getRun(runId).catch(() => null)
      if (detail?.run) setRun(detail.run)
      subscribe(runId)
      void refreshRuns()
      setMentions([])
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
      Toast.info('已停止，工单可稍后继续')
      void refreshRuns()
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '停止失败')
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

  // 事件流 → Semi 消息（阶段折叠交给 AIChatDialogue.Step，不再手搓）
  const chats = useMemo(() => {
    if (!run) return []
    return buildDialogueMessages(events, run)
  }, [events, run])

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

  const dialogueRenderers = useMemo(
    () => ({
      plan: (item: { content?: DialogueStep[] }) => (
        <RunSteps steps={item.content ?? []} />
      ),
    }),
    [],
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
        <Button
          theme="borderless"
          type="tertiary"
          icon={<IconClose />}
          size="small"
          aria-label="收起 Altas"
          onClick={() => setAiPanelOpen(false)}
        />
      </div>

      <div className="ai-panel-body">
        <BatchCard />
        {events.length === 0 && !run ? (
          <div className="ai-empty">
            <div className="ai-empty-icon">
              <IconAIFilledLevel1 />
            </div>
            <div className="ai-empty-title">今天有什么工作需要处理？</div>
          </div>
        ) : restoring ? (
          <div className="ai-empty">
            <Text type="tertiary" size="small">
              正在召回这段会话…
            </Text>
          </div>
        ) : (
          <>
            <AIChatDialogue
              ref={dialogueRef}
              chats={chats as never}
              roleConfig={{ assistant: { name: 'Altas' } }}
              mode="noBubble"
              showReset={false}
              renderDialogueContentItem={dialogueRenderers as never}
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
          keepSkillAfterSend={false}
          showUploadButton={false}
          showTemplateButton={false}
          skills={SKILLS}
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
    </div>
  )
}
