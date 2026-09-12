import { streamingResponseToMessage } from '@douyinfe/semi-ui'
import type { RunEvent, RunTaskInfo, RunEventType } from '../types'

// ── Responses 协议层 ────────────────────────────────────────────────────────
//
// 这一层只做三件事，**不做投影**：
//
//   1. 把 run 的事件流按命名空间分开：`response.*` 归对话，`wikiatlas.*` 归宿主部件；
//   2. 把 `response.*` 交回 Semi 原生的流式归约器（`streamingResponseToMessage`），
//      由它折出那条助手消息——协议怎么拼、乱序怎么缓冲、缺块怎么容错，都不归我们管；
//   3. 给宿主部件读 `wikiatlas.*`（任务清单、用量、提醒、元信息）。
//
// 事件类型清单的 owner 是后端 `internal/run/responses.go`；这里是同一份契约的前端侧。

/** Responses 规范里的流事件（前端的订阅清单；owner 在后端 responses.go）。 */
export const RESPONSE_EVENT_TYPES = [
  'response.created',
  'response.in_progress',
  'response.output_item.added',
  'response.output_item.done',
  'response.output_text.delta',
  'response.output_text.done',
  'response.reasoning_summary_text.delta',
  'response.reasoning_summary_text.done',
  'response.function_call_arguments.delta',
  'response.function_call_arguments.done',
  'response.completed',
  'response.failed',
] as const satisfies readonly RunEventType[]

/** 规范里没有、产品需要的那部分（独立命名空间 = "留后手"的位置）。 */
export const WIKIATLAS_EVENT_TYPES = [
  'wikiatlas.todo',
  'wikiatlas.usage',
  'wikiatlas.notice',
  'wikiatlas.meta',
  'wikiatlas.request',
  'wikiatlas.context',
  'wikiatlas.tree',
  'wikiatlas.content.staging',
  'wikiatlas.content.committed',
] as const satisfies readonly RunEventType[]

/** EventSource 只派发**显式订阅过**的命名事件：这份清单漏一个，那一类就静默收不到。 */
export const RUN_EVENT_TYPES: readonly RunEventType[] = [...RESPONSE_EVENT_TYPES, ...WIKIATLAS_EVENT_TYPES]

/** Responses 的流块。字段与规范同名（output_index / content_index / delta / item …）。 */
export interface ResponseChunk {
  type: string
  sequence_number?: number
  response?: Record<string, unknown>
  item?: Record<string, unknown>
  output_index?: number
  content_index?: number
  summary_index?: number
  delta?: string
  text?: string
  name?: string
  arguments?: string
  [k: string]: unknown
}

/** 归约出来的助手消息（Semi 的 Message）：我们只读这几个字段，其余原样交给 Dialogue。 */
export interface DialogueMessage {
  id: string
  role: string
  content?: unknown
  output_text?: string
  status?: string
  createdAt?: number
  model?: string
  [k: string]: unknown
}

export function isResponseEvent(type: string): boolean {
  return type.startsWith('response.')
}

/** payload 就是规范里的那个事件对象；缺字段的老数据用 seq 兜底。 */
function chunkOf(ev: RunEvent): ResponseChunk {
  const p = (ev.payload ?? {}) as ResponseChunk
  const seq = typeof p.sequence_number === 'number' ? p.sequence_number : ev.seq - 1
  return { ...p, type: ev.type, sequence_number: seq }
}

/**
 * 事件流 → 归约器能吃的流块序列。
 *
 * **为什么要重编号**：历史回放故意丢掉 `*.delta`（约定的流式增量，见后端
 * store/runs.go：不丢的话一个长工单的回放会被半句话碎片占满窗口），于是
 * sequence_number 会出现空洞，而归约器只在**连续**序号上推进。
 *
 * 重编号不丢信息，因为每个输出项都是自足的——`response.output_item.done`
 * 带的是最终形态（完整文本 / 完整参数 / 工具结果），`response.completed`
 * 更是带整份 response。丢掉增量只丢"打字过程"。
 */
export function responsesChunks(events: RunEvent[]): ResponseChunk[] {
  const chunks = events
    .filter((ev) => isResponseEvent(ev.type))
    .map(chunkOf)
    .sort((a, b) => (a.sequence_number ?? 0) - (b.sequence_number ?? 0))
  return chunks.map((c, i) => ({ ...c, sequence_number: i }))
}

/**
 * 一个 run 的助手消息 = 把它的 Responses 事件喂给 Semi 的归约器。
 *
 * 一次全量归约（不是增量喂）：事件列表最多几百条，线性代价可以忽略，
 * 而全量重算是幂等的——刷新、续传、回放都走同一条路，不会有两份状态。
 */
export function runToMessage(events: RunEvent[]): DialogueMessage | null {
  const chunks = responsesChunks(events)
  if (!chunks.length) return null
  const out = streamingResponseToMessage(chunks)
  if (!out?.message) return null
  const msg = out.message as unknown as DialogueMessage

  // 抹掉 output_text —— 这是 Semi 的一个坑，不是我们的数据问题：
  //   ① 它的归约器把 content 里的文本拼成 message.output_text（streamingResponse
  //      ToMessage.js 尾部）；
  //   ② 它的渲染器（dialogueContent.js）只要看到 output_text 非空，就**只渲染
  //      整段文本**，把 content 数组整个跳过。
  // 两件事都是它自己干的，结果是：一条助手消息只要有正文，同一消息里的
  // **思考块和工具调用就全部不渲染**（而它的文档恰恰写着 reasoning / tool call
  // 要靠 ContentItem[] 展示）。
  // 正文本来就以 message 项存在于 content 数组里，抹掉这个便利字段后渲染器
  // 走逐项分支，思考、工具卡、正文三样都在。类型上也站得住：原始 Responses
  // 响应对象里根本没有 output_text，那是官方 SDK 在客户端加的。
  delete msg.output_text
  return msg
}

/** 用户自己发的那句：会话视图里以 user 角色渲染。 */
export function userMessage(id: string, text: string): DialogueMessage {
  return { id, role: 'user', status: 'completed', content: text }
}

/**
 * 一个 run → 这一轮的对话消息（用户那句 + 归约出来的助手消息）。
 *
 * 助手消息的状态以 run 为准：归约器只认 `response.completed`，
 * 而"失败/被中断"是 run 的事，事件流里没有独立的一类。
 */
export function runMessages(
  run: { id: string; goal: string; status: string },
  events: RunEvent[],
): DialogueMessage[] {
  const out: DialogueMessage[] = [userMessage(`q-${run.id}`, run.goal)]
  const evs = dedupeEvents(events)
  const msg = runToMessage(evs)
  if (msg) {
    const { head, tail } = splitBoundaryNotices(evs)
    const items = Array.isArray(msg.content) ? (msg.content as unknown[]) : []
    // 边界事实贴进对话流：对话前的准备行 + 对话后的收尾行（对齐 dsh 的
    // turn/start · turn/end —— 生命周期事实是事件，渲染在内容的前后两端）。
    const content = [
      ...head.map((n) => ({ type: 'wikiatlas.turn', phase: 'start', text: n.text, tone: n.tone })),
      ...items,
      ...tail.map((n) => ({ type: 'wikiatlas.turn', phase: 'end', text: n.text, tone: n.tone })),
    ]
    out.push({ ...msg, content, id: `a-${run.id}`, status: runMessageStatus(run.status) })
  }
  return out
}

/**
 * 把 run 的 `wikiatlas.notice` 按**位置**分三段：
 *   · head —— 第一段内容之前：运行前的准备事实（如"已加载 skill…"）；
 *   · tail —— 最后一段内容之后：收尾结论（质量自检、失败原因）；
 *   · mid  —— 夹在中间的：过程性提醒（预算、护栏），仍归浮层 dock。
 * 没有内容项（起步即失败）时全部算 tail。
 */
export function splitBoundaryNotices(events: RunEvent[]): {
  head: Notice[]
  tail: Notice[]
  mid: Notice[]
} {
  let firstItem = -1
  let lastItem = -1
  for (const ev of events) {
    if (ev.type === 'response.output_item.added' && firstItem < 0) firstItem = ev.seq
    if (ev.type === 'response.output_item.done') lastItem = Math.max(lastItem, ev.seq)
  }
  const head: Notice[] = []
  const tail: Notice[] = []
  const mid: Notice[] = []
  for (const ev of events) {
    if (ev.type !== 'wikiatlas.notice') continue
    const text = typeof ev.payload?.text === 'string' ? ev.payload.text.trim() : ''
    if (!text) continue
    const tone = ev.payload?.tone
    const n: Notice = {
      id: ev.id,
      text,
      tone: tone === 'warn' || tone === 'error' ? tone : 'info',
      at: ev.createdAt,
    }
    if (firstItem < 0 || ev.seq > lastItem) tail.push(n)
    else if (ev.seq < firstItem) head.push(n)
    else mid.push(n)
  }
  return { head, tail, mid }
}

/**
 * 取**当前**的任务清单：最后一次 wikiatlas.todo 的全量列表。
 *
 * 用途是输入框上方那个任务面板——它是"现在该做什么"的实时视图。
 */
export function latestTasks(events: RunEvent[]): RunTaskInfo[] {
  let out: RunTaskInfo[] = []
  for (const ev of events) {
    if (ev.type !== 'wikiatlas.todo') continue
    const raw = ev.payload?.tasks as RunTaskInfo[] | undefined
    if (Array.isArray(raw) && raw.length) out = raw
  }
  return out
}

export interface UsageInfo {
  steps: number
  prompt: number
  completion: number
  total: number
  cacheRead: number
  cacheWrite: number
  /** 最后一轮的 promptTokens = 此刻窗口里装了多少（不是累计） */
  contextUsed: number
  /** 该网关到底报不报缓存用量：不报就是不可观测，不要假装命中率为 0 */
  cacheReported: boolean
  /** 见过非零命中之后又掉到 0 = 前缀被改写了（护栏信号） */
  cacheDropped: boolean
}

const asNum = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0)

/** 汇总 run 的用量（wikiatlas.usage，每轮一条）。 */
export function usageOf(events: RunEvent[]): UsageInfo {
  const u: UsageInfo = {
    steps: 0, prompt: 0, completion: 0, total: 0,
    cacheRead: 0, cacheWrite: 0, contextUsed: 0,
    cacheReported: false, cacheDropped: false,
  }
  // 网关会间歇性地某一轮不报缓存（实测：step3 报 0、step4 又回到 12544，
  // 而服务端追加式不变量没报改写）——所以连续**两轮**为 0 才算"掉到 0"，
  // 单轮闪断不再吓用户。
  let zeroStreak = 0
  for (const ev of events) {
    if (ev.type !== 'wikiatlas.usage') continue
    const p = ev.payload ?? {}
    const read = asNum(p.cacheReadTokens)
    u.steps += 1
    u.prompt += asNum(p.promptTokens)
    u.completion += asNum(p.completionTokens)
    u.total += asNum(p.totalTokens)
    u.cacheRead += read
    u.cacheWrite += asNum(p.cacheWriteTokens)
    u.contextUsed = asNum(p.promptTokens) || u.contextUsed
    if (read > 0) {
      u.cacheReported = true
      zeroStreak = 0
    } else if (u.cacheReported && asNum(p.step) > 1) {
      zeroStreak += 1
      if (zeroStreak >= 2) u.cacheDropped = true
    }
  }
  return u
}

export interface Notice {
  id: string
  text: string
  tone: 'info' | 'warn' | 'error'
  at: string
}

/**
 * 宿主的播报与护栏提醒（wikiatlas.notice）。
 *
 * 它们**不是模型说的话**，所以不进对话——以前混在叙事流里，靠"这句话以
 * 「收到工单：」开头"这种前缀规则再滤掉，那是补丁；现在是两条不同的通道。
 */
export function noticesOf(events: RunEvent[]): Notice[] {
  const out: Notice[] = []
  for (const ev of events) {
    if (ev.type !== 'wikiatlas.notice') continue
    const text = typeof ev.payload?.text === 'string' ? ev.payload.text.trim() : ''
    if (!text) continue
    const tone = ev.payload?.tone
    out.push({
      id: ev.id,
      text,
      tone: tone === 'warn' || tone === 'error' ? tone : 'info',
      at: ev.createdAt,
    })
  }
  return out
}

/** 工单元信息（wikiatlas.meta）：会话归属、意图、模型、涉及的节点。 */
export function metaOf(events: RunEvent[]): Record<string, unknown> {
  for (const ev of events) {
    if (ev.type === 'wikiatlas.meta') return ev.payload ?? {}
  }
  return {}
}

/** run 状态 → Semi 消息状态（归约器只认 completed，失败/中断由 run 说了算）。 */
export function runMessageStatus(status: string): string {
  switch (status) {
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

/** SSE 重连时可能收到同一 seq 的重复事件，按 seq 去重。 */
export function dedupeEvents(events: RunEvent[]): RunEvent[] {
  const seen = new Set<number>()
  const out: RunEvent[] = []
  for (const ev of events) {
    if (seen.has(ev.seq)) continue
    seen.add(ev.seq)
    out.push(ev)
  }
  return out
}

/**
 * 把一条事件并进列表，按 **seq** 去重。
 *
 * 不能按 id 去重：同一条事件从 `getRun` 拿到的是库里的 uuid、从 SSE 拿到的是
 * `${runId}:${seq}`，两个 id 对不上。seq 是落库序号，两边一致，也是唯一稳的键。
 */
export function mergeEvent(list: RunEvent[], ev: RunEvent): RunEvent[] {
  if (list.some((e) => e.seq === ev.seq)) return list
  return [...list, ev]
}
