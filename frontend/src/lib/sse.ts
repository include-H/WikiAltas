import type { RunEvent, RunEventType } from '../types'
import { RUN_EVENT_TYPES } from './responses'

// Run 事件流客户端：后端 SSE 的 `data:` 只带事件对象本身，事件类型在 `event:`
// 字段上；这里统一还原成完整 RunEvent 信封。
//
// 类型清单的 owner 是 `lib/responses.ts`（与后端 responses.go 一一对应）——
// EventSource 只派发**显式订阅过**的命名事件，所以这份清单必须整份订阅，
// 漏一个那一类就静默收不到（不报错、不提示，只是界面少了一块）。

export type SseHandler = (event: RunEvent) => void

export interface SseHandle {
  close: () => void
}

const NAMED_EVENTS: readonly RunEventType[] = RUN_EVENT_TYPES

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
