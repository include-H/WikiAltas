import type { RunEvent, RunEventType } from '../types'

// Run 事件流客户端：后端 SSE 的 `data:` 只带 payload，事件类型在 `event:`
// 字段上；这里统一还原成完整 RunEvent 信封，投影层只认 RunEvent。

export type SseHandler = (event: RunEvent) => void

export interface SseHandle {
  close: () => void
}

const NAMED_EVENTS: RunEventType[] = [
  'run.started',
  'plan.updated',
  'narrative',
  // 流式增量：漏订阅的话面板就退化成"整段蹦出来"（EventSource 只派发显式订阅的命名事件）
  'narrative.delta',
  'tool.started',
  'tool.delta',
  'tool.done',
  'content.staging',
  'content.committed',
  'tree.updated',
  'run.completed',
  'run.failed',
]

/** 订阅 Run 事件流；断线按 Last-Event-ID 续传。 */
export function streamRunEvents(
  runId: string,
  onEvent: SseHandler,
  opts?: { lastEventId?: string; onOpen?: () => void; onError?: (e: Event) => void },
): SseHandle {
  let closed = false
  let lastEventId = opts?.lastEventId ?? ''
  let es: EventSource | null = null
  let retryTimer: ReturnType<typeof setTimeout> | null = null
  let retries = 0

  const connect = () => {
    if (closed) return
    const url = lastEventId
      ? `/api/runs/${runId}/events/stream?lastEventId=${encodeURIComponent(lastEventId)}`
      : `/api/runs/${runId}/events/stream`

    es = new EventSource(url)
    es.onopen = () => {
      retries = 0
      opts?.onOpen?.()
    }

    for (const name of NAMED_EVENTS) {
      es.addEventListener(name, (msg) => {
        const me = msg as MessageEvent
        lastEventId = me.lastEventId || lastEventId
        try {
          const payload = JSON.parse(me.data) as Record<string, unknown>
          const seq = Number(lastEventId) || 0
          onEvent({
            id: lastEventId ? `${runId}:${lastEventId}` : `${runId}:${name}`,
            runId,
            seq,
            type: name,
            payload,
            createdAt: new Date().toISOString(),
          })
        } catch {
          // ignore malformed events
        }
      })
    }

    es.onerror = (e) => {
      opts?.onError?.(e)
      es?.close()
      es = null
      if (closed) return
      retries += 1
      retryTimer = setTimeout(connect, Math.min(1000 * 2 ** retries, 15000))
    }
  }

  connect()

  return {
    close: () => {
      closed = true
      if (retryTimer) clearTimeout(retryTimer)
      es?.close()
    },
  }
}
