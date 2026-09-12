import type {
  BatchResult,
  CreateDocBody,
  CreateRunBody,
  CreateWorkBody,
  Doc,
  EmbyLibraryView,
  LibraryLink,
  LibraryPoolResponse,
  LibrarySearchEntry,
  LibrarySuggestion,
  LibrarySourceInfo,
  Me,
  NodeSweepResponse,
  NodeSweepRunResponse,
  PatchWorkBody,
  PutContentBody,
  PutContentResult,
  Relation,
  Revision,
  Run,
  RunEvent,
  RuntimeInfo,
  SearchHit,
  Session,
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

/** 手工删除工单（正在跑的先停再删）。 */
export function deleteRun(id: string): Promise<void> {
  return request(`/api/runs/${id}`, { method: 'DELETE' })
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
  // 默认要 1000 条权威事件：长工单（几十轮工具调用）200 条装不下，
  // 面板重建时前面的章节会看不到。增量事件后端已经默认过滤掉。
  return request(
    `/api/runs/${id}${qs({ afterSeq: opts?.afterSeq, limit: opts?.limit ?? 1000 })}`,
  )
}

// --- Sessions（会话 = 一段连续对话） ---

/** 列出会话。target 省略 = 全部会话（工单页要的就是"所有可继续的对话"）。 */
export function listSessions(target?: string): Promise<{ sessions: Session[] }> {
  return request(`/api/sessions${qs({ target })}`)
}

export function createSession(body: { target: string; title?: string }): Promise<{ session: Session }> {
  return request('/api/sessions', { method: 'POST', body: JSON.stringify(body) })
}

export function renameSession(id: string, title: string): Promise<{ ok: boolean }> {
  return request(`/api/sessions/${id}`, { method: 'PATCH', body: JSON.stringify({ title }) })
}

export function deleteSession(id: string): Promise<{ ok: boolean }> {
  return request(`/api/sessions/${id}`, { method: 'DELETE' })
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

// --- Library · 媒体库（GameAtlas / Emby 通用）---

export type LibraryApiSource = 'gameatlas' | 'emby' | 'komga'

/** 集合页建档建议：库里属本系列、还没挂链的条目。 */
export function suggestLibrary(
  source: LibraryApiSource,
  workId: string,
): Promise<{ suggestions: LibrarySuggestion[] }> {
  return request(`/api/library/${source}/suggest?workId=${encodeURIComponent(workId)}`)
}

/** 一键建档：建 stub 子节点并挂上该库的外链。 */
export function archiveLibrary(
  source: LibraryApiSource,
  workId: string,
  publicId: string,
): Promise<{ work: Work; link: LibraryLink }> {
  return request(`/api/library/${source}/archive`, {
    method: 'POST',
    body: JSON.stringify({ workId, publicId }),
  })
}

/** 关联既有条目（Emby 允许多条、GameAtlas 一条）。 */
export function linkLibrary(
  source: LibraryApiSource,
  workId: string,
  publicId: string,
): Promise<{ link: LibraryLink }> {
  return request(`/api/library/${source}/link`, {
    method: 'POST',
    body: JSON.stringify({ workId, publicId }),
  })
}

/** 全库搜索（关联弹窗用）。 */
export function searchLibrary(
  source: LibraryApiSource,
  q: string,
): Promise<{ entries: LibrarySearchEntry[] }> {
  return request(`/api/library/${source}/search?q=${encodeURIComponent(q)}`)
}

/** 反哺：把节点正文（可选带简介）写回 GameAtlas 条目。 */
export function pushGameAtlasWiki(
  workId: string,
  summary?: string,
): Promise<{ ok: boolean; gameUrl?: string; summary?: string }> {
  return request('/api/library/gameatlas/push', {
    method: 'POST',
    body: JSON.stringify(summary != null ? { workId, summary } : { workId }),
  })
}

/** 解除 GameAtlas 关联（一条，按 workId）。 */
export function unlinkGameAtlas(workId: string): Promise<{ ok: boolean }> {
  return request(`/api/library/gameatlas/link?workId=${encodeURIComponent(workId)}`, {
    method: 'DELETE',
  })
}

/** 解除 Emby 关联（多条，按链接 id）。 */
export function unlinkEmbyLink(linkId: string): Promise<{ ok: boolean }> {
  return request(`/api/library/emby/link?id=${encodeURIComponent(linkId)}`, {
    method: 'DELETE',
  })
}

/** 解除 Komga 关联（多条，按链接 id）。 */
export function unlinkKomgaLink(linkId: string): Promise<{ ok: boolean }> {
  return request(`/api/library/komga/link?id=${encodeURIComponent(linkId)}`, {
    method: 'DELETE',
  })
}

/** 反哺到 Komga：把简介 PATCH 到节点所有 Komga 链（系列/单册）的元数据。 */
export function pushKomgaSummary(
  workId: string,
  summary?: string,
): Promise<{ ok: boolean; count?: number; summary?: string }> {
  return request('/api/library/komga/push', {
    method: 'POST',
    body: JSON.stringify(summary != null ? { workId, summary } : { workId }),
  })
}

/** 节点页的 Emby 相关影像/OST 候选（混杂库）。 */
export function relatedEmby(workId: string): Promise<{ related: LibrarySearchEntry[] }> {
  return request(`/api/library/emby/related?workId=${encodeURIComponent(workId)}`)
}

/** 设置页：拉 Emby 媒体库清单（给每个库挑角色用）。 */
export function getEmbyViews(): Promise<{ views: EmbyLibraryView[] }> {
  return request('/api/library/emby/views')
}

/** 建议池：读后台扫描清单（秒开，不实时轰库）。 */
export function getLibraryPool(): Promise<LibraryPoolResponse> {
  return request('/api/library/pool')
}

/** 扫描媒体库（后台低频扫描的手动触发）：把三源清单落成快照。 */
export function scanLibrary(): Promise<{
  ok: boolean
  scannedAt: string
  entries: number
  linked: number
  unlinked: number
}> {
  return request('/api/library/scan', { method: 'POST' })
}

/** 忽略：单条（entryId）或整组（containerKey），持久化在清单里。 */
export function ignoreLibrary(body: {
  source: string
  entryId?: string
  containerKey?: string
  containerTitle?: string
}): Promise<{ ok: boolean }> {
  return request('/api/library/ignore', { method: 'POST', body: JSON.stringify(body) })
}

/** 恢复忽略。 */
export function unignoreLibrary(q: {
  source: string
  entryId?: string
  containerKey?: string
}): Promise<{ ok: boolean }> {
  const params = new URLSearchParams({ source: q.source })
  if (q.entryId) params.set('entryId', q.entryId)
  if (q.containerKey) params.set('containerKey', q.containerKey)
  return request(`/api/library/ignore?${params.toString()}`, { method: 'DELETE' })
}

/** AI 对一遍：让当前模型产出建议清单（只建议，绝不自动执行）。 */
export function aiSuggestLibrary(): Promise<{
  ok: boolean
  model: string
  entries: number
  matched: number
  archives: number
  links: number
  ignored: number
}> {
  return request('/api/library/ai-suggest', { method: 'POST' })
}

/** 节点扫库：读这个节点现存的挂链建议（写完之后自动扫出来的那份）。 */
export function getNodeSweep(workId: string): Promise<NodeSweepResponse> {
  return request(`/api/library/node-sweep?workId=${encodeURIComponent(workId)}`)
}

/** 节点扫库：立即扫一遍（题名变体 → 三源候选 → 判官），产出待确认的建议。 */
export function runNodeSweep(workId: string): Promise<NodeSweepRunResponse> {
  return request('/api/library/node-sweep', { method: 'POST', body: JSON.stringify({ workId }) })
}

/** 逐条忽略一条建议（per-node：只是不再向这个节点提起，不等于全局忽略）。 */
export function dismissNodeSweep(workId: string, key: string): Promise<{ ok: boolean }> {
  return request('/api/library/node-sweep/dismiss', {
    method: 'POST',
    body: JSON.stringify({ workId, key }),
  })
}

/** 恢复被忽略的一条建议。 */
export function restoreNodeSweep(workId: string, key: string): Promise<{ ok: boolean }> {
  const params = new URLSearchParams({ workId, key })
  return request(`/api/library/node-sweep/dismiss?${params.toString()}`, { method: 'DELETE' })
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
