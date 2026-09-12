import { useEffect, useReducer, useRef } from 'react'
import type { DialogueMessage } from './responses'

// 流式平滑（打字机回放）。
//
// 为什么需要：网关不总是逐字推流——实测出现过"整段 430 字一个增量砸下来"，
// 而重放历史时更是整条消息一次性到位。归约层（runToMessage）保持权威不动，
// 这一层只在**渲染前**给每个文本片段加一条"已吐字数"的水位线：大块按节奏
// 摊开，小块几乎无感；水位线追平后渲染的即是原对象（零开销）。
//
// 节奏：约每 50ms 释放 max(3, lag * 0.05 / 2.5) 个字符——积压 400 字上下
// 在 3~5 秒内排完，像打字；持续流入时水位线自动追着增量走。

const TICK_MS = 50
const TAU = 2.5 // 时间常数（秒）：积压随时间指数衰减的尺度

interface PartRef {
  key: string
  len: number
}

/** 找出消息里所有可平滑的文本片段（message 项的 output_text + reasoning 项的 summary）。 */
function textParts(msg: DialogueMessage): PartRef[] {
  const out: PartRef[] = []
  const items = Array.isArray(msg.content) ? (msg.content as Array<Record<string, unknown>>) : []
  items.forEach((it, ii) => {
    if (!it || typeof it !== 'object') return
    const id = String(it.id ?? `i${ii}`)
    if (it.type === 'message' && Array.isArray(it.content)) {
      ;(it.content as Array<Record<string, unknown>>).forEach((p, pi) => {
        if (typeof p?.text === 'string') out.push({ key: `${id}:c${pi}`, len: p.text.length })
      })
    } else if (it.type === 'reasoning' && Array.isArray(it.summary)) {
      ;(it.summary as Array<Record<string, unknown>>).forEach((p, pi) => {
        if (typeof p?.text === 'string') out.push({ key: `${id}:s${pi}`, len: p.text.length })
      })
    }
  })
  return out
}

/** 按水位线把消息截短（只截不补；水位线追平时返回原对象）。 */
function applyWatermark(msg: DialogueMessage, shown: Map<string, number>): DialogueMessage {
  if (!Array.isArray(msg.content)) return msg
  let changed = false
  const items = (msg.content as Array<Record<string, unknown>>).map((it, ii) => {
    if (!it || typeof it !== 'object') return it
    const id = String(it.id ?? `i${ii}`)
    const cutList = (list: Array<Record<string, unknown>>, tag: string, field: string) => {
      let itemChanged = false
      const parts = list.map((p, pi) => {
        const n = shown.get(`${id}:${tag}${pi}`)
        if (typeof p?.text === 'string' && typeof n === 'number' && n < p.text.length) {
          itemChanged = true
          return { ...p, text: p.text.slice(0, n) }
        }
        return p
      })
      return itemChanged ? { ...it, [field]: parts } : it
    }
    if (it.type === 'message' && Array.isArray(it.content)) {
      const next = cutList(it.content as Array<Record<string, unknown>>, 'c', 'content')
      if (next !== it) changed = true
      return next
    }
    if (it.type === 'reasoning' && Array.isArray(it.summary)) {
      const next = cutList(it.summary as Array<Record<string, unknown>>, 's', 'summary')
      if (next !== it) changed = true
      return next
    }
    return it
  })
  return changed ? ({ ...msg, content: items } as DialogueMessage) : msg
}

/**
 * 对会话列表的**最后一条助手消息**做打字机回放。
 * live=true（工单在跑）时持续追增量；工单结束后把剩余积压排空为止；
 * 其余消息（历史、用户）原样返回。
 */
export function useSmoothedChats(chats: DialogueMessage[], live: boolean): DialogueMessage[] {
  const [, force] = useReducer((x: number) => x + 1, 0)
  const shown = useRef(new Map<string, number>())
  const msgId = useRef<string | null>(null)

  const last = chats.length ? chats[chats.length - 1] : null
  const smoothable = last && last.role !== 'user' ? last : null

  // 换了一条消息（新一轮）：
  //   · live（正在流）→ 水位清零，新内容按打字机吐出来；
  //   · 非 live（历史 / 刷新后恢复）→ 水位**直接拉满**，整条一次性显示。
  // 以前一律清零，于是"刷新打开一条已完成的长消息"会把全程重打一遍
  // （用户真实现象："刷新后打开，依旧打字机"）。
  if (smoothable && smoothable.id !== msgId.current) {
    msgId.current = smoothable.id
    shown.current.clear()
    if (!live) {
      for (const p of textParts(smoothable)) shown.current.set(p.key, p.len)
    }
  }

  const parts = smoothable ? textParts(smoothable) : []
  let lag = 0
  for (const p of parts) lag += Math.max(0, p.len - (shown.current.get(p.key) ?? 0))
  const draining = lag > 0

  useEffect(() => {
    if (!smoothable || (!live && !draining)) return
    const t = window.setInterval(() => {
      const cur = smoothable
      const ps = textParts(cur)
      let moved = false
      for (const p of ps) {
        const have = shown.current.get(p.key) ?? 0
        if (have >= p.len) continue
        const release = Math.max(3, Math.ceil(((p.len - have) * (TICK_MS / 1000)) / TAU))
        shown.current.set(p.key, Math.min(p.len, have + release))
        moved = true
      }
      if (moved) force()
    }, TICK_MS)
    return () => window.clearInterval(t)
    // draining 变化时重估是否还值得起定时器；smoothable 换身份也要重挂
  }, [smoothable, live, draining])

  if (!smoothable || (!live && !draining)) return chats
  const transformed = applyWatermark(smoothable, shown.current)
  if (transformed === smoothable) return chats
  return [...chats.slice(0, -1), transformed]
}
