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
  slug: string
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
  slug: string
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
  slug: string
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

export interface RunTask {
  id: string
  title: string
  status: 'pending' | 'in_progress' | 'completed' | 'failed'
}

export interface Run {
  id: UUID
  intent: RunIntent
  goal: string
  status: RunStatus
  plan: RunTask[]
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

export interface BatchResult {
  batchId: string
  workspace: string
  runIds: string[]
  skippedIds?: string[]
}

export type RunEventType =
  | 'run.started'
  | 'plan.updated'
  | 'narrative'
  | 'tool.started'
  | 'tool.done'
  | 'content.staging'
  | 'content.committed'
  | 'tree.updated'
  | 'run.completed'
  | 'run.failed'

export interface RunEvent {
  id: string
  runId: UUID
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
  slug: string
}

export interface CreateWorkBody {
  parentId?: UUID | null
  kind: WorkKind
  medium?: Medium
  title: string
  slug?: string
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
  }
  search: {
    exaApiKey?: string
    clearExaApiKey?: boolean
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
