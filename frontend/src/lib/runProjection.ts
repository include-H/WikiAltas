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
  /** 流式中（还没落最终文本）：渲染时带打字光标 */
  streaming?: boolean
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

/** 检索类工具 → 「检索资料」阶段。 */
const RESEARCH_TOOLS = new Set([
  'read_skill',
  'search_works',
  'read_work',
  'read_doc',
  'get_tree',
  'search_web',
  'fetch_url',
])

/** 写入类工具 → 「产出内容」阶段。 */
const WRITE_TOOLS = new Set([
  'write_content',
  'patch_section',
  'create_doc',
  'upsert_work',
  'upsert_relation',
  'attach_library_link',
])

/**
 * 阶段一律按"实际发生了什么"来分：模型给的 plan 常常只有一条、状态词也不统一，
 * 硬跟着它走就会出现"每条工单都停在理解任务"（真实观测）。
 * 计划里恰好有四段时，用它的四个标题当阶段名。
 */
function synthesizePhases(steps: Step[], allDone: boolean, planTitles?: string[] | null): Phase[] {
  const titles = [
    planTitles?.[0] ?? '理解任务',
    planTitles?.[1] ?? '检索资料',
    planTitles?.[2] ?? '产出内容',
    planTitles?.[3] ?? '写入并完成',
  ]
  const phases: Phase[] = [
    { id: 'auto-1', title: titles[0], status: 'in_progress', steps: [] },
    { id: 'auto-2', title: titles[1], status: 'pending', steps: [] },
    { id: 'auto-3', title: titles[2], status: 'pending', steps: [] },
    { id: 'auto-4', title: titles[3], status: 'pending', steps: [] },
  ]
  let wrote = false
  let committed = false
  for (const step of steps) {
    if (step.kind === 'tool' && WRITE_TOOLS.has(step.name)) wrote = true
    if (step.kind === 'system') committed = true
    let idx = 0
    if (step.kind === 'tool' && RESEARCH_TOOLS.has(step.name) && !wrote) idx = 1
    else if (step.kind === 'tool' && WRITE_TOOLS.has(step.name)) idx = 2
    else if (wrote) idx = 3
    phases[idx].steps.push(step)
  }
  // 空阶段不留：只保留有内容的那几段（顺序不变）
  const kept = phases.filter((ph, i) => ph.steps.length > 0 || i === 0)
  const lastIdx = Math.max(0, kept.length - 1)
  kept.forEach((ph, i) => {
    ph.status = allDone || i < lastIdx ? 'completed' : 'in_progress'
  })
  if (committed && kept.length > 1) kept[kept.length - 1].status = 'completed'
  return kept
}

/** Semi AIChatDialogue 的 Step 结构（见 widgets/contentItem/dialogueStep.tsx）。 */
export interface DialogueStep {
  summary: string
  status?: 'completed' | 'in_progress'
  actions?: { summary: string; description?: string }[]
}

/**
 * Semi AIChatDialogue 的 ContentItem。
 * 叙事句必须写成 `{type:'message', content:'文字'}`：
 * Semi 的 builtinRenderers 只认 message / reasoning / function_call / custom_tool_call，
 * 没有独立的 output_text 渲染器（写成 output_text 会静默渲染成空）。
 */
export type DialogueItem =
  | { type: 'message'; content: string }
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

/**
 * 从"半截 JSON"里宽松地抠出 text 字段（answer/narrative 工具的流式参数）。
 * 参数是逐字传的，随时可能是 `{"text": "已确认对` 这种未闭合状态，
 * 所以不能用 JSON.parse（dsh 的 plan-summary 也是同样思路：mid-stream 回退）。
 */
function partialTextArg(raw: string): string {
  const m = /"text"\s*:\s*"((?:[^"\\]|\\.)*)/.exec(raw)
  if (!m) return ''
  const body = m[1]
  try {
    return JSON.parse(`"${body}"`) as string
  } catch {
    return body.replace(/\\n/g, '\n').replace(/\\"/g, '"').replace(/\\\\/g, '\\')
  }
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
 * 把事件流压成「阶段 → 步骤」：先按发生顺序收步骤，最后按步骤类型切四段。
 */
export function projectRun(events: RunEvent[]): RunProjection {
  const steps: Step[] = []
  const pendingTools = new Map<string, ToolStep>()
  /** 流式中的那一段助手文本（模型还在吐字时先占位，权威文本到达后收尾） */
  let streamingText: NarrativeStep | null = null
  /** tool.delta 先于 tool.started 到达时的运行中芯片（key = 流式 index） */
  const streamingTools = new Map<number, ToolStep>()
  /** answer / narrative 工具的流式文本占位（key = 流式 index） */
  const answerStreams = new Map<number, NarrativeStep>()
  let done = false
  let failed = false
  /** 计划恰好四段时，用它的标题当阶段名（状态词模型各写各的，不采信）。 */
  let planTitles: string[] | null = null

  const push = (step: Step) => steps.push(step)

  for (const ev of events) {
    const p = payloadOf(ev)
    switch (ev.type) {
      case 'plan.updated': {
        const tasks = p.tasks as RunTask[] | undefined
        if (Array.isArray(tasks) && tasks.length === 4) {
          const titles = tasks.map((t) => (t.title ?? '').trim()).filter(Boolean)
          if (titles.length === 4) planTitles = titles
        }
        break
      }
      case 'narrative': {
        const text = str(p.text)
        if (!text) break
        // 权威文本到达：所有"答案流"占位（answer/narrative 工具的半截参数）作废
        for (const [index, step] of answerStreams) {
          const at = steps.indexOf(step)
          if (at >= 0) steps.splice(at, 1)
          answerStreams.delete(index)
        }
        if (streamingText) {
          // 权威文本到达：用它收尾（比累积的更长就用权威的），并关掉打字光标
          streamingText.text = text.length > streamingText.text.length ? text : streamingText.text
          streamingText.streaming = false
          streamingText = null
          break
        }
        push({ kind: 'narrative', id: ev.id, text })
        break
      }
      case 'narrative.delta': {
        if (p.final === true) {
          if (streamingText) streamingText.streaming = false
          streamingText = null
          break
        }
        const text = str(p.text) ?? ''
        if (!text) break
        if (!streamingText) {
          streamingText = { kind: 'narrative', id: `stream-${ev.seq}`, text: '', streaming: true }
          push(streamingText)
        }
        streamingText.text += text
        break
      }
      case 'tool.delta': {
        const index = typeof p.index === 'number' ? p.index : 0
        const name = str(p.name) ?? 'tool'
        const args = str(p.args) ?? ''
        // answer / narrative 工具的参数就是"要说的话"：边传参边把它当叙事打出来，
        // 这样面板里的回答是逐字长出来的（等 tool.done 后由权威 narrative 接管）。
        if (name === 'answer' || name === 'narrative') {
          const partial = partialTextArg(args)
          if (partial) {
            let slot = answerStreams.get(index)
            if (!slot) {
              slot = { kind: 'narrative', id: `stream-tool-${index}`, text: '', streaming: true }
              answerStreams.set(index, slot)
              push(slot)
            }
            slot.text = partial
            break
          }
        }
        const existing = streamingTools.get(index)
        if (existing) {
          existing.detail = args
          break
        }
        const chip: ToolStep = {
          kind: 'tool',
          id: `stream-tool-${ev.seq}`,
          name,
          label: labelOf(name),
          detail: args,
          running: true,
          failed: false,
        }
        streamingTools.set(index, chip)
        push(chip)
        break
      }
      case 'tool.started': {
        const name = str(p.name) ?? 'tool'
        // 这一轮已经用 tool.delta 画过运行中芯片：让权威芯片接管，避免一行两个
        for (const [index, chip] of streamingTools) {
          if (chip.name === name) {
            const at = steps.indexOf(chip)
            if (at >= 0) steps.splice(at, 1)
            streamingTools.delete(index)
            break
          }
        }
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
        // 播报/回答类工具的流式占位到此为止（真正的文本由随后的 narrative 事件给出）
        if (name === 'answer' || name === 'narrative') {
          for (const [index, step] of answerStreams) {
            const at = steps.indexOf(step)
            if (at >= 0) steps.splice(at, 1)
            answerStreams.delete(index)
          }
        }
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
        // 仅用于正文区的"Altas 正在写入"标记（store.setStaging），不进叙事流：
        // 每次落库都占一行会变成刷屏（观测中出现了 23 行"开始写入正文…"）。
        break
      }
      case 'content.committed': {
        const version = typeof p.version === 'number' ? p.version : undefined
        // 写入回执不单独占行：同阶段通常已有 write_content / patch_section 芯片；
        // 没有工具芯片时（如 mock 路径）才补一枚，避免刷屏。
        const hasWriteChip = steps.some(
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

  return { phases: synthesizePhases(steps, done, planTitles), done, failed }
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
      // 只给一行摘要：产物全文（artifact）留在 run_events 里，
      // 直接塞进面板会让阶段默认展开时刷出一屏原文（飞书的芯片也只是一行）。
      description: compactDetail(t.name, t.detail),
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
 * 工具描述压成一行、只留最有信息量的那点（侧栏很窄，芯片一行放不下就换行/出滚动条）。
 * 联网检索给「关键词 · 参考 N 篇」，抓网页给域名，写入类给 summary。
 */
function compactDetail(name: string, detail?: string): string | undefined {
  if (!detail) return undefined
  const flat = detail
    .replace(/\s+/g, ' ')
    // 正文类字段是产物，不该出现在一行摘要里（芯片只留"做了什么"）
    .replace(/"(contentMd|newMarkdown|previewMd)"\s*:\s*"(?:[^"\\]|\\.)*"\s*,?/g, '')
    .replace(/[{}]/g, '')
    .replace(/"([^"]*)"\s*:\s*/g, '$1: ')
    .replace(/"([^"]*)"/g, '$1')
    .replace(/\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi, '…')
    .trim()
  if (!flat) return undefined

  const cut = (s: string, n: number) => (s.length > n ? `${s.slice(0, n)}…` : s)

  if (name === 'fetch_url') {
    const url = /url:\s*(\S+)|(https?:\/\/\S+)/.exec(flat)
    const raw = url?.[1] ?? url?.[2]
    if (raw) {
      try {
        const host = new URL(raw).host.replace(/^www\./, '')
        const failed = /http 403|http 4\d\d|http 5\d\d/i.test(flat)
        return failed ? `${host} · 抓取失败` : host
      } catch {
        return cut(flat, 32)
      }
    }
  }

  if (name === 'search_web' || name === 'search_works') {
    const q = /q:\s*(.+?)(?:·|参考|count=|$)/.exec(flat)?.[1]?.trim()
    const count = /参考\s*(\d+)/.exec(flat)?.[1]
    const parts = [q ? cut(q, 22) : '', count ? `参考 ${count} 篇` : ''].filter(Boolean)
    if (parts.length) return parts.join(' · ')
  }

  if (WRITE_TOOLS.has(name)) {
    const summary = /summary:\s*(.+?)(?:,|$)/.exec(flat)?.[1]?.trim()
    if (summary) return cut(summary, 26)
  }

  return cut(flat, 36)
}

/**
 * 把一次 Run 的事件流投影成一条 Semi 消息：
 * 阶段（plan.updated）→ 可折叠的 Step，阶段内的工具调用 → 该 Step 的 actions；
 * 叙事句与写入回执 → 顺序插入的 output_text。
 */
export function buildDialogueMessages(
  events: RunEvent[],
  run: Pick<Run, 'id' | 'status'>,
): DialogueMessage[] {
  const { phases } = projectRun(dedupeEvents(events))
  const content: DialogueItem[] = []
  let lastNarrative = ''

  for (const phase of phases) {
    // 阶段内的叙事句先按原文顺序输出（连续重复的只留一句）
    for (const step of phase.steps) {
      if (step.kind === 'narrative') {
        if (step.text === lastNarrative) continue
        lastNarrative = step.text
        // 流式中追加一个光标，读起来就是"正在打字"
        content.push({ type: 'message', content: step.streaming ? `${step.text} ▍` : step.text })
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
      content.push({ type: 'message', content: s.text })
    }
  }

  // 一条消息只放一个内容项：Semi 的自定义渲染节点用「消息下标」当 key，
  // 同一条消息里塞多个 plan 会被 React 当成重复 key，只剩第一个能渲染出来。
  // 同角色的连续消息会被 Semi 自动折叠成一组（continueSend），观感不变。
  return content.map((item, index) => ({
    id: `${run.id}-${index}`,
    role: 'assistant',
    status: index === content.length - 1 ? messageStatus(run) : 'completed',
    content: [item],
  }))
}

/** 兼容旧调用：只要一条消息时取第一条。 */
export function buildDialogueMessage(events: RunEvent[], run: Pick<Run, 'id' | 'status'>): DialogueMessage {
  const messages = buildDialogueMessages(events, run)
  return messages[messages.length - 1] ?? { id: run.id, role: 'assistant', status: messageStatus(run), content: [] }
}
