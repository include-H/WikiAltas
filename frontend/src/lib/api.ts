import type {
  BatchResult,
  CreateDocBody,
  CreateRunBody,
  CreateWorkBody,
  Doc,
  LibraryLink,
  LibrarySourceInfo,
  Me,
  PatchWorkBody,
  PutContentBody,
  PutContentResult,
  Relation,
  Revision,
  Run,
  RunEvent,
  RuntimeInfo,
  SearchHit,
  Settings,
  SettingsPayload,
  Work,
  WorkSummary,
} from '../types'

const BASE = ''

export class ApiError extends Error {
  code: string
  status: number

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response
  try {
    res = await fetch(`${BASE}${path}`, {
      headers: {
        'Content-Type': 'application/json',
        ...(init?.headers ?? {}),
      },
      ...init,
    })
  } catch {
    throw new ApiError(0, 'network', '无法连接后端服务，请确认 API 已启动')
  }

  if (res.status === 204) return undefined as T

  const text = await res.text()
  let body: unknown = null
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      body = null
    }
  }

  if (!res.ok) {
    const err = body as { error?: { code?: string; message?: string } } | null
    throw new ApiError(
      res.status,
      err?.error?.code ?? 'http_error',
      err?.error?.message ?? `请求失败 (${res.status})`,
    )
  }

  return body as T
}

function qs(params: Record<string, string | number | undefined | null>): string {
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') sp.set(k, String(v))
  }
  const s = sp.toString()
  return s ? `?${s}` : ''
}

// --- Tree & Works ---

export function getTree(): Promise<{ nodes: WorkSummary[] }> {
  return request('/api/tree')
}

export function getWork(
  id: string,
): Promise<{ work: Work; libraryLinks: LibraryLink[]; relations: Relation[] }> {
  return request(`/api/works/${id}`)
}

export function createWork(body: CreateWorkBody): Promise<Work> {
  return request('/api/works', { method: 'POST', body: JSON.stringify(body) })
}

export function patchWork(id: string, body: PatchWorkBody): Promise<Work> {
  return request(`/api/works/${id}`, { method: 'PATCH', body: JSON.stringify(body) })
}

export function putWorkContent(id: string, body: PutContentBody): Promise<PutContentResult> {
  return request(`/api/works/${id}/content`, { method: 'PUT', body: JSON.stringify(body) })
}

export function deleteWork(id: string): Promise<void> {
  return request(`/api/works/${id}`, { method: 'DELETE' })
}

// --- Docs ---

export function getWorkDocs(workId: string): Promise<{ docs: Doc[] }> {
  return request(`/api/works/${workId}/docs`)
}

/** GET /api/docs/:id —— 后端历史上有裸 doc / {doc} 两种形状，这里统一兜住。 */
export async function getDoc(id: string): Promise<{ doc: Doc }> {
  const raw = await request<Doc | { doc: Doc }>(`/api/docs/${id}`)
  if (raw && typeof raw === 'object' && 'doc' in raw && raw.doc) return raw
  return { doc: raw as Doc }
}

export function createDoc(workId: string, body: CreateDocBody): Promise<Doc> {
  return request(`/api/works/${workId}/docs`, { method: 'POST', body: JSON.stringify(body) })
}

export function putDocContent(id: string, body: PutContentBody): Promise<PutContentResult> {
  return request(`/api/docs/${id}/content`, { method: 'PUT', body: JSON.stringify(body) })
}

export function patchDoc(
  id: string,
  body: { title?: string; links?: string[] },
): Promise<Doc> {
  return request(`/api/docs/${id}`, { method: 'PATCH', body: JSON.stringify(body) })
}

// --- 后置能力（VISUAL_SPEC §5 首期不做，接口先留着，界面暂不接）---

export function getWorkRelations(id: string): Promise<{ relations: Relation[] }> {
  return request(`/api/works/${id}/relations`)
}

export function createRelation(body: {
  fromId: string
  toId: string
  type: string
}): Promise<Relation> {
  return request('/api/relations', { method: 'POST', body: JSON.stringify(body) })
}

export function deleteRelation(id: string): Promise<void> {
  return request(`/api/relations/${id}`, { method: 'DELETE' })
}

// --- Revisions ---

export function getWorkRevisions(
  id: string,
  limit = 20,
): Promise<{ revisions: Revision[] }> {
  return request(`/api/works/${id}/revisions${qs({ limit })}`)
}

export function getDocRevisions(id: string, limit = 20): Promise<{ revisions: Revision[] }> {
  return request(`/api/docs/${id}/revisions${qs({ limit })}`)
}

export function restoreWorkRevision(id: string, revId: string): Promise<Revision> {
  return request(`/api/works/${id}/revisions/${revId}/restore`, { method: 'POST' })
}

// --- Runs ---

export function createRun(body: CreateRunBody): Promise<{ runId: string } | Run> {
  return request('/api/runs', { method: 'POST', body: JSON.stringify(body) })
}

/** 批量建档：一批 stub 节点各开一个 create_wiki 工单，共享一个批次会话。 */
export function createBatchWiki(body: {
  workIds: string[]
  batchSize?: number
  medium?: string
}): Promise<BatchResult> {
  return request('/api/runs/batch', { method: 'POST', body: JSON.stringify(body) })
}

export function resumeRun(id: string): Promise<{ runId: string } | Run> {
  return request(`/api/runs/${id}/resume`, { method: 'POST' })
}

export function cancelRun(id: string): Promise<void> {
  return request(`/api/runs/${id}/cancel`, { method: 'POST' })
}

export function listRuns(
  status?: string,
  limit = 50,
  workspace?: string,
): Promise<{ runs: Run[] }> {
  return request(`/api/runs${qs({ status, limit, workspace })}`)
}

export function getRun(
  id: string,
  opts?: { afterSeq?: number; limit?: number },
): Promise<{ run: Run; events?: RunEvent[] }> {
  return request(`/api/runs/${id}${qs({ afterSeq: opts?.afterSeq, limit: opts?.limit })}`)
}

// --- Library ---

// 库同步（Emby / Komga / GameAtlas）属首期范围外，接口保留
export function getLibrarySources(): Promise<{ sources: LibrarySourceInfo[] }> {
  return request('/api/library/sources')
}

export function syncLibrary(source?: string): Promise<{ runId: string }> {
  return request('/api/library/sync', {
    method: 'POST',
    body: JSON.stringify({ source }),
  })
}

// --- Search ---

export function search(q: string, kind?: 'work' | 'doc'): Promise<{ hits: SearchHit[] }> {
  return request(`/api/search${qs({ q, kind })}`)
}

// --- Settings ---

export function getSettings(): Promise<Settings> {
  return request('/api/settings')
}

export function putSettings(body: SettingsPayload): Promise<Settings> {
  return request('/api/settings', { method: 'PUT', body: JSON.stringify(body) })
}

/** 用当前配置发一次极小请求，验证模型连通性。 */
export function testLLM(): Promise<{
  ok: boolean
  model?: string
  latencyMs?: number
  reply?: string
  error?: string
}> {
  return request('/api/settings/test-llm', { method: 'POST' })
}

/** 把进程环境里的 WIKIALTAS_* 重新导入设置（覆盖当前值）。 */
export function importEnvSettings(): Promise<{ imported: boolean; settings: Settings }> {
  return request('/api/settings/import-env', { method: 'POST' })
}

export function getRuntime(): Promise<RuntimeInfo> {
  return request('/api/runtime')
}

// --- 简易用户系统 ---

export function getMe(): Promise<Me> {
  return request('/api/auth/me')
}

/** 访问密码登录（用户名可省略，默认管理员）。 */
export function login(password: string, username?: string): Promise<{ ok: boolean; username: string }> {
  return request('/api/auth/login', {
    method: 'POST',
    body: JSON.stringify(username ? { username, password } : { password }),
  })
}

export function logout(): Promise<{ ok: boolean }> {
  return request('/api/auth/logout', { method: 'POST' })
}

// --- SSE（实现见 lib/sse.ts，这里保持统一入口）---
export { streamRunEvents } from './sse'
export type { SseHandle, SseHandler } from './sse'
