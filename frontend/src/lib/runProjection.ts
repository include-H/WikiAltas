import type { Run, RunEvent, RunTask } from '../types'

// Run 事件 → Semi AIChatDialogue 的消息投影。
//
// 复用 Semi 原生能力（不再手搓折叠）：
//   · 阶段折叠 = AIChatDialogue.Step（steps[{summary,status,actions[]}]）
//   · 叙事句   = message / output_text（AIChatDialogue 自带 Markdown 渲染）
//   · 工具调用 = 折进对应阶段的 actions（summary + description）
//   · 失败     = message.status = failed（Semi 自带失败样式）

export interface ToolStep {
  kind: 'tool'
  id: string
  name: string
  label: string
  detail?: string
  artifact?: string[]
  artifactNote?: string
  durationMs?: number
  running: boolean
  failed: boolean
}

export interface NarrativeStep {
  kind: 'narrative'
  id: string
  text: string
}

export interface SystemStep {
  kind: 'system'
  id: string
  text: string
  tone: 'success' | 'danger'
}

export type Step = ToolStep | NarrativeStep | SystemStep

export interface Phase {
  id: string
  title: string
  status: string
  steps: Step[]
}

export interface RunProjection {
  phases: Phase[]
  done: boolean
  failed: boolean
}

/** Semi AIChatDialogue 的 Step 结构（见 widgets/contentItem/dialogueStep.tsx）。 */
export interface DialogueStep {
  summary: string
  status?: 'completed' | 'in_progress'
  actions?: { summary: string; description?: string }[]
}

/** Semi AIChatDialogue 的 ContentItem（只需我们用到的那几种类型）。 */
export type DialogueItem =
  | { type: 'message'; content: { type: 'output_text'; text: string }[] }
  | { type: 'plan'; content: DialogueStep[] }

/** Semi AIChatDialogue 的 Message。 */
export interface DialogueMessage {
  id: string
  role: 'assistant'
  status: 'in_progress' | 'completed' | 'failed' | 'cancelled'
  content: DialogueItem[]
}

/** 工具名 → 人话短语（飞书芯片语气）。 */
const TOOL_LABELS: Record<string, string> = {
  read_skill: '已读取 wiki-writing skill',
  search_works: '已检索站内条目',
  read_work: '已读取作品正文',
  read_doc: '已读取资料',
  get_tree: '已读取作品树',
  search_web: '已联网检索',
  fetch_url: '已抓取网页',
  write_content: '已写入正文',
  patch_section: '已改写章节',
  upsert_work: '已更新作品节点',
  upsert_relation: '已建立作品关系',
  create_doc: '已创建资料',
  attach_library_link: '已挂接媒体库链接',
  answer: '已作答',
}

function labelOf(name: string): string {
  return TOOL_LABELS[name] ?? `已执行 ${name}`
}

function payloadOf(ev: RunEvent): Record<string, unknown> {
  return (ev.payload ?? {}) as Record<string, unknown>
}

function str(v: unknown): string | undefined {
  return typeof v === 'string' && v.trim() ? v : undefined
}

function artifactOf(p: Record<string, unknown>): { lines?: string[]; note?: string } {
  const art = p.artifact as
    | { kind?: string; lines?: unknown; truncated?: boolean; totalLines?: number; note?: string }
    | undefined
  if (!art) return {}
  const lines = Array.isArray(art.lines) ? art.lines.filter((l): l is string => typeof l === 'string') : []
  const note = art.truncated
    ? `已截断 · 仅显示前 ${lines.length} 行${art.totalLines ? ` · 共 ${art.totalLines} 行` : ''}`
    : art.note
  return { lines: lines.length ? lines : undefined, note }
}

/**
 * 把事件流压成「阶段 → 步骤」。
 * plan.updated 驱动阶段；第一个 plan 之前的步骤放在"理解任务"阶段里。
 */
export function projectRun(events: RunEvent[]): RunProjection {
  const phases: Phase[] = []
  let current: Phase = { id: 'phase-0', title: '理解任务', status: 'in_progress', steps: [] }
  phases.push(current)
  const pendingTools = new Map<string, ToolStep>()
  let done = false
  let failed = false
  // 阶段骨架只认第一份计划：执行器收尾时会把计划覆盖成固定四步（t1–t4），
  // 如果按 id 重建，之前收集的叙述与工具芯片会全部丢失（真实观测：四个阶段全空、点不开）。
  let planLocked = false

  const push = (step: Step) => current.steps.push(step)

  for (const ev of events) {
    const p = payloadOf(ev)
    switch (ev.type) {
      case 'plan.updated': {
        const tasks = p.tasks as RunTask[] | undefined
        if (!Array.isArray(tasks) || tasks.length === 0) break
        if (!planLocked) {
          planLocked = true
          const previous = new Map(phases.map((ph) => [ph.id, ph]))
          const next: Phase[] = tasks.map((t) => ({
            id: t.id,
            title: t.title,
            status: t.status,
            steps: previous.get(t.id)?.steps ?? [],
          }))
          // 计划出现之前的步骤（默认阶段）归到第一阶段
          const leftover = previous.get('phase-0')?.steps ?? []
          if (leftover.length && next.length) next[0].steps = [...leftover, ...next[0].steps]
          phases.length = 0
          phases.push(...next)
          current = next.find((ph) => ph.status === 'in_progress') ?? next[next.length - 1]
        } else {
          // 后续计划只更新状态（按 id 匹配），阶段标题与已收集的步骤保持不变
          for (const t of tasks) {
            const ph = phases.find((item) => item.id === t.id)
            if (ph) ph.status = t.status
          }
          const inProgress = tasks.find((t) => t.status === 'in_progress')
          if (inProgress) {
            current = phases.find((item) => item.id === inProgress.id) ?? current
          }
          // 计划没有点名"进行中"时保持当前阶段不变：
          // 否则一次收尾计划就会把后续所有芯片都塞进最后一段（观测中出现过 13 项挤在"自检收尾"）。
        }
        break
      }
      case 'narrative': {
        const text = str(p.text)
        if (text) push({ kind: 'narrative', id: ev.id, text })
        break
      }
      case 'tool.started': {
        const name = str(p.name) ?? 'tool'
        const step: ToolStep = {
          kind: 'tool',
          id: ev.id,
          name,
          label: labelOf(name),
          detail: str(p.inputSummary),
          running: true,
          failed: false,
        }
        pendingTools.set(ev.id, step)
        push(step)
        break
      }
      case 'tool.done': {
        const name = str(p.name) ?? 'tool'
        const outputSummary = str(p.outputSummary)
        const art = artifactOf(p)
        const durationMs = typeof p.durationMs === 'number' ? p.durationMs : undefined
        // 与最近的同名、进行中的芯片配对
        let paired: ToolStep | undefined
        for (const step of pendingTools.values()) {
          if (step.name === name && step.running) {
            paired = step
            break
          }
        }
        if (paired) {
          paired.running = false
          paired.durationMs = durationMs
          if (outputSummary) {
            paired.detail = [paired.detail, outputSummary].filter(Boolean).join('\n')
          }
          if (art.lines) paired.artifact = art.lines
          if (art.note) paired.artifactNote = art.note
          pendingTools.delete(paired.id)
        } else {
          push({
            kind: 'tool',
            id: ev.id,
            name,
            label: labelOf(name),
            detail: outputSummary,
            artifact: art.lines,
            artifactNote: art.note,
            durationMs,
            running: false,
            failed: false,
          })
        }
        break
      }
      case 'content.staging': {
        // 仅用于正文区的"馆员正在写入"标记（store.setStaging），不进叙事流：
        // 每次落库都占一行会变成刷屏（观测中出现了 23 行"开始写入正文…"）。
        break
      }
      case 'content.committed': {
        const version = typeof p.version === 'number' ? p.version : undefined
        // 写入回执不单独占行：同阶段通常已有 write_content / patch_section 芯片；
        // 没有工具芯片时（如 mock 路径）才补一枚，避免刷屏。
        const hasWriteChip = current.steps.some(
          (s) => s.kind === 'tool' && (s.name === 'write_content' || s.name === 'patch_section'),
        )
        if (!hasWriteChip) {
          push({
            kind: 'tool',
            id: ev.id,
            name: 'write_content',
            label: version ? `已写入正文 v${version}` : '已写入正文',
            running: false,
            failed: false,
          })
        }
        break
      }
      case 'tree.updated':
        break
      case 'run.completed': {
        done = true
        push({ kind: 'system', id: ev.id, text: '✓ 已完成', tone: 'success' })
        break
      }
      case 'run.failed': {
        failed = true
        const err = p.error as { message?: string } | string | undefined
        const msg = typeof err === 'string' ? err : err?.message
        push({ kind: 'system', id: ev.id, text: `✗ 失败：${msg ?? '未知错误'}`, tone: 'danger' })
        break
      }
      default:
        break
    }
  }

  // 未开始且没有内容的阶段不必占位置
  const cleaned = phases.filter((ph) => ph.steps.length > 0 || ph.status !== 'pending')
  return { phases: cleaned.length ? cleaned : phases, done, failed }
}

/** SSE 重连时可能收到同一 seq 的重复事件，按 id 去重。 */
export function dedupeEvents(events: RunEvent[]): RunEvent[] {
  const seen = new Set<string>()
  const out: RunEvent[] = []
  for (const ev of events) {
    const key = ev.id || `${ev.seq}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push(ev)
  }
  return out
}

function messageStatus(run: Pick<Run, 'status'>): DialogueMessage['status'] {
  switch (run.status) {
    case 'completed':
      return 'completed'
    case 'failed':
      return 'failed'
    case 'interrupted':
    case 'expired':
      return 'cancelled'
    default:
      return 'in_progress'
  }
}

function toolActions(steps: Step[]): { summary: string; description?: string }[] {
  const raw = steps
    .filter((s): s is ToolStep => s.kind === 'tool')
    .map((t) => ({
      summary: t.label,
      description: [t.detail, t.artifact?.join('\n')].filter(Boolean).join('\n\n') || undefined,
    }))
  // 连续同类调用合并成一行（飞书是「已搜索 2 次 · 参考 14 篇」的写法）
  const merged: { summary: string; description?: string; count: number }[] = []
  for (const a of raw) {
    const last = merged[merged.length - 1]
    if (last && last.summary === a.summary) {
      last.count += 1
      if (a.description) last.description = a.description
      continue
    }
    merged.push({ ...a, count: 1 })
  }
  return merged.map(({ summary, description, count }) => ({
    summary: count > 1 ? `${summary} ×${count}` : summary,
    description,
  }))
}

/**
 * 把一次 Run 的事件流投影成一条 Semi 消息：
 * 阶段（plan.updated）→ 可折叠的 Step，阶段内的工具调用 → 该 Step 的 actions；
 * 叙事句与写入回执 → 顺序插入的 output_text。
 */
export function buildDialogueMessage(events: RunEvent[], run: Pick<Run, 'id' | 'status'>): DialogueMessage {
  const { phases } = projectRun(dedupeEvents(events))
  const content: DialogueItem[] = []
  let lastNarrative = ''

  for (const phase of phases) {
    // 阶段内的叙事句先按原文顺序输出（连续重复的只留一句）
    for (const step of phase.steps) {
      if (step.kind === 'narrative') {
        if (step.text === lastNarrative) continue
        lastNarrative = step.text
        content.push({ type: 'message', content: [{ type: 'output_text', text: step.text }] })
      }
    }
    const actions = toolActions(phase.steps)
    const step: DialogueStep = {
      summary: phase.title,
      status: phase.status === 'completed' ? 'completed' : 'in_progress',
      actions: actions.length ? actions : undefined,
    }
    content.push({ type: 'plan', content: [step] })
  }

  // 收尾一行（Semi 的消息状态另有 loading / failed 图标）
  const tail = phases[phases.length - 1]?.steps ?? []
  for (const s of tail) {
    if (s.kind === 'system') {
      content.push({ type: 'message', content: [{ type: 'output_text', text: s.text }] })
    }
  }

  return {
    id: run.id,
    role: 'assistant',
    status: messageStatus(run),
    content,
  }
}
