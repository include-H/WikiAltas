import type { RunEvent, RunTask } from '../types'

// Run 事件 → 折叠叙事流的投影规则（VISUAL_SPEC §4）：
// 阶段头来自 plan.updated 的任务；阶段内是叙事句 + 工具芯片；
// 芯片文案是人话，不是工具名；展开才是入参/产物摘要。

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

  const push = (step: Step) => current.steps.push(step)

  for (const ev of events) {
    const p = payloadOf(ev)
    switch (ev.type) {
      case 'plan.updated': {
        const tasks = p.tasks as RunTask[] | undefined
        if (!Array.isArray(tasks) || tasks.length === 0) break
        // 计划就是阶段：按任务 id 合并，已经落在某阶段的步骤留在原地
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
        push({ kind: 'narrative', id: ev.id, text: '开始写入正文…' })
        break
      }
      case 'content.committed': {
        const version = typeof p.version === 'number' ? p.version : undefined
        push({
          kind: 'system',
          id: ev.id,
          text: version ? `已写入正文 v${version}` : '已写入正文',
          tone: 'success',
        })
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
