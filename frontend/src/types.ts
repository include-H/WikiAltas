// Shared API contract for WikiAltas v2. Mirrors DESIGN_V2.md §3 and §6.
// Keep in sync with shared/types.ts

export type UUID = string

export type WorkKind = 'universe' | 'series' | 'work'
export type Medium = 'game' | 'movie' | 'tv' | 'anime' | 'manga' | 'novel' | 'book' | 'other'
export type WorkStatus = 'stub' | 'draft' | 'ready'
export type Visibility = 'public' | 'private'
export type Author = 'human' | 'llm' | 'import'
export type RelationType =
  | 'adaptation_of'
  | 'sequel_to'
  | 'spin_off_of'
  | 'remake_of'
  | 'expansion_of'
  | 'references'

export type LibrarySource = 'emby' | 'komga' | 'gameatlas'

export type RunIntent =
  | 'create_wiki'
  | 'continue_wiki'
  | 'rewrite_section'
  | 'write_doc'
  | 'organize_tree'
  | 'answer'
  | 'sync_library'

export type RunStatus = 'running' | 'interrupted' | 'completed' | 'failed' | 'expired'

export interface Work {
  id: UUID
  parentId: UUID | null
  kind: WorkKind
  medium: Medium | null
  title: string
  aliases: string[]
  contentMd: string | null
  contentVer: number
  status: WorkStatus
  visibility: Visibility
  sortOrder: number
  createdAt: string
  updatedAt: string
}

export interface WorkSummary {
  id: UUID
  parentId: UUID | null
  kind: WorkKind
  medium: Medium | null
  title: string
  status: WorkStatus
  visibility: Visibility
  hasContent: boolean
  hasLibraryLink: boolean
  sortOrder: number
  updatedAt: string
}

export interface Doc {
  id: UUID
  folderOf: UUID
  title: string
  contentMd: string
  contentVer: number
  links: UUID[]
  createdAt: string
  updatedAt: string
}

export interface Revision {
  id: UUID
  targetType: 'work' | 'doc'
  targetId: UUID
  version: number
  author: Author
  runId: UUID | null
  summary: string
  createdAt: string
  contentMd?: string
}

export interface Relation {
  id: UUID
  fromId: UUID
  toId: UUID
  type: RelationType
  createdAt: string
}

export interface LibraryLink {
  id: UUID
  workId: UUID
  source: LibrarySource
  externalId: string
  url: string | null
  titleHint: string | null
  createdAt: string
}

/** GameAtlas 建档建议：属于本系列、还没在 WikiAltas 挂链的条目。 */
export interface GameAtlasSuggestion {
  publicId: string
  title: string
  titleAlt: string | null
  releaseDate: string | null
  coverImage: string | null
  url: string
}

/** GameAtlas 搜索结果（关联已有条目用）：带挂链状态。 */
export interface GameAtlasSearchEntry {
  publicId: string
  title: string
  titleAlt: string | null
  releaseDate: string | null
  coverImage: string | null
  series: { id: number; name: string } | null
  url: string
  linked: boolean
  linkedWorkId?: string
}

export interface Run {
  id: UUID
  intent: RunIntent
  goal: string
  status: RunStatus
  result: Record<string, unknown> | null
  error: Record<string, unknown> | null
  /** 从 checkpoint 投影出来的上下文（workId 等），用于跳转 */
  context?: { workId?: UUID; docId?: UUID; parentId?: UUID; medium?: string }
  model: string
  startedAt: string
  lastActive: string
  expiresAt: string | null
  completedAt: string | null
}

/** 会话 = 一段连续对话（后端 runs.workspace 即会话 id）。 */
export interface Session {
  id: string
  /** 归属页面：work:<id> / doc:<id> / home */
  target: string
  title: string
  /** 最近一句用户消息：列表的副标题用它（标题只在建会话时定一次） */
  lastGoal: string
  runCount: number
  /** 最近一次执行的状态；有 running 优先；空串 = 还没有执行 */
  status: '' | RunStatus
  createdAt: string
  updatedAt: string
}

export interface BatchResult {
  batchId: string
  workspace: string
  runIds: string[]
  skippedIds?: string[]
}

/**
 * Run 事件类型。两条线**同流不同类**：
 *
 *  · `response.*` —— OpenAI Responses 规范的流事件。前端把它们整条喂给 Semi 的
 *    流式归约器（`streamingResponseToMessage`），模型说了什么、调了什么工具，
 *    都由归约器折出来，我们不再自己造形状。
 *  · `wikiatlas.*` —— 规范里没有、产品需要的东西（任务清单 / 用量与缓存命中 /
 *    宿主播报与护栏提醒 / 元信息）。它们交给宿主的部件渲染，**不进对话**。
 *
 * 类型清单的 owner 是后端 `internal/run/responses.go`；这里是同一份契约的前端侧。
 */
export type RunEventType =
  // ── Responses 流事件 ──
  | 'response.created'
  | 'response.in_progress'
  | 'response.output_item.added'
  | 'response.output_item.done'
  | 'response.output_text.delta'
  | 'response.output_text.done'
  | 'response.reasoning_summary_text.delta'
  | 'response.reasoning_summary_text.done'
  | 'response.function_call_arguments.delta'
  | 'response.function_call_arguments.done'
  | 'response.completed'
  | 'response.failed'
  // ── wikiatlas.* 旁路 ──
  | 'wikiatlas.todo'
  | 'wikiatlas.usage'
  | 'wikiatlas.notice'
  | 'wikiatlas.meta'
  | 'wikiatlas.request'
  | 'wikiatlas.context'
  | 'wikiatlas.tree'
  | 'wikiatlas.content.staging'
  | 'wikiatlas.content.committed'

/** 任务清单里的一条（todo_write 工具的全量清单）。 */
export interface RunTaskInfo {
  id: string
  content: string
  /** 进行式的说法：正在做的那条显示它（"正在核实主创" 而不是 "核实主创"） */
  activeForm?: string
  status: 'pending' | 'in_progress' | 'completed' | string
}

export interface RunEvent {
  id: string
  runId: UUID
  /** 落库序号（1 起）。协议里的 sequence_number = seq - 1，见 payload。 */
  seq: number
  type: RunEventType
  payload: Record<string, unknown>
  createdAt: string
}

export interface SearchHit {
  id: UUID
  kind: 'work' | 'doc'
  title: string
  snippet: string
}

export interface CreateWorkBody {
  parentId?: UUID | null
  kind: WorkKind
  medium?: Medium
  title: string
  visibility?: Visibility
}

export interface PatchWorkBody {
  title?: string
  parentId?: UUID | null
  medium?: Medium
  status?: WorkStatus
  visibility?: Visibility
  sortOrder?: number
}

/** 简易用户系统：当前会话状态（访客=authed:false）。 */
export interface Me {
  authed: boolean
  username: string
  adminPasswordConfigured: boolean
}

export interface PutContentBody {
  contentMd: string
  author: Author
  runId?: UUID
  summary?: string
  expectedVersion?: number
}

export interface CreateDocBody {
  title: string
  contentMd?: string
  links?: UUID[]
}

export interface CreateRunBody {
  intent: RunIntent
  goal: string
  /** 会话键：work:<id> / doc:<id> / home / batch:<id> */
  workspace?: string
  /** 思考等级（聊天框里选的档位）：off | low | medium | high | xhigh | max；留空用设置页的值 */
  reasoningEffort?: string
  context?: {
    workId?: UUID
    docId?: UUID
    parentId?: UUID
    medium?: Medium
    section?: string
    /** 发起工单时用户所处的文档模式：读=只分析、编辑=直接改、修订=定点改+给理由 */
    docMode?: 'read' | 'edit' | 'revision'
    /** 用户在正文里选中的文本（修订模式的作用域） */
    selection?: string
    extra?: Record<string, unknown>
  }
}

export interface Settings {
  llm: {
    endpoint: string
    model: string
    apiKeyConfigured: boolean
    temperature?: number
    maxTokens?: number
    /** 思考等级：off | low | medium | high | xhigh | max */
    reasoningEffort?: string
    /** 上下文窗口（token），面板用量环的分母；0/未设 = 默认 262144 */
    contextWindow?: number
  }
  library: {
    embyUrl?: string
    embyApiKeyConfigured?: boolean
    komgaUrl?: string
    komgaApiKeyConfigured?: boolean
    gameatlasUrl?: string
    gameatlasApiKeyConfigured?: boolean
  }
  runs: {
    expireDays: number
    keepEventsDays: number
    maxConcurrentRuns: number
  }
  search: {
    exaApiKeyConfigured: boolean
    /** 出外网的 HTTP 代理。fetch_url 直连超时时，模型会带 useProxy 用它重试同一页。 */
    proxyUrl?: string
  }
  admin: {
    username: string
    passwordConfigured: boolean
    usingDefaultPassword: boolean
    newNodeVisibility: Visibility
  }
  skillRoot: string
}

/** 设置页提交的载荷（密钥留空=不修改，clearXxx=清空）。 */
export interface SettingsPayload {
  llm: {
    endpoint: string
    model: string
    apiKey?: string
    clearApiKey?: boolean
    temperature?: number
    maxTokens?: number
    protocol?: string
    reasoningEffort?: string
    contextWindow?: number
  }
  search: {
    exaApiKey?: string
    clearExaApiKey?: boolean
    /** 空串 = 清掉代理（文本输入框的自然语义） */
    proxyUrl?: string
  }
  library: {
    embyUrl?: string
    embyApiKey?: string
    komgaUrl?: string
    komgaApiKey?: string
    gameatlasUrl?: string
    gameatlasApiKey?: string
  }
  runs: {
    expireDays: number
    keepEventsDays: number
    maxConcurrentRuns: number
  }
  admin?: {
    username?: string
    newPassword?: string
    clearPassword?: boolean
    newNodeVisibility?: Visibility
  }
  skillRoot: string
}

export interface RuntimeInfo {
  model: string
  /** 当前模型的上下文窗口（token），面板右下角用量环的分母 */
  contextWindow: number
  maxConcurrentRuns: number
  activeRuns: number
  queuedRuns: number
  skillRoot: string
  stats: {
    works: number
    docs: number
    revisions: number
    runs: number
    runEvents: number
    runningNow: number
  }
  features: string[]
}

export interface PutContentResult {
  id: UUID
  contentVer: number
  revisionId: UUID
}

export interface LibrarySourceInfo {
  source: LibrarySource
  configured: boolean
  url?: string
}

export interface OutlineHeading {
  id: string
  level: 2 | 3
  /** 目录编号，如 "1." / "2.1"；正文里已带编号时原样保留 */
  number: string
  /** 去掉编号的标题文本，用于渲染「编号 + 文本」 */
  text: string
}
