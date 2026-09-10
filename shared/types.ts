// Shared API contract for WikiAltas v2. Mirrors DESIGN_V2.md §3 and §6.

export type UUID = string;

export type WorkKind = 'universe' | 'series' | 'work';
export type Medium = 'game' | 'movie' | 'tv' | 'anime' | 'manga' | 'novel' | 'book' | 'other';
export type WorkStatus = 'stub' | 'draft' | 'ready';
export type Author = 'human' | 'llm' | 'import';
export type RelationType =
  | 'adaptation_of'
  | 'sequel_to'
  | 'spin_off_of'
  | 'remake_of'
  | 'expansion_of'
  | 'references';

export type LibrarySource = 'emby' | 'komga' | 'gameatlas';

export type RunIntent =
  | 'create_wiki'
  | 'continue_wiki'
  | 'rewrite_section'
  | 'write_doc'
  | 'organize_tree'
  | 'answer'
  | 'sync_library';

export type RunStatus = 'running' | 'interrupted' | 'completed' | 'failed' | 'expired';

export interface Work {
  id: UUID;
  parentId: UUID | null;
  kind: WorkKind;
  medium: Medium | null;
  title: string;
  slug: string;
  aliases: string[];
  contentMd: string | null;
  contentVer: number;
  status: WorkStatus;
  sortOrder: number;
  createdAt: string;
  updatedAt: string;
}

export interface WorkSummary {
  id: UUID;
  parentId: UUID | null;
  kind: WorkKind;
  medium: Medium | null;
  title: string;
  slug: string;
  status: WorkStatus;
  hasContent: boolean;
  hasLibraryLink: boolean;
  sortOrder: number;
  updatedAt: string;
}

export interface Doc {
  id: UUID;
  folderOf: UUID;
  title: string;
  slug: string;
  contentMd: string;
  contentVer: number;
  links: UUID[];
  createdAt: string;
  updatedAt: string;
}

export interface Revision {
  id: UUID;
  targetType: 'work' | 'doc';
  targetId: UUID;
  version: number;
  author: Author;
  runId: UUID | null;
  summary: string;
  createdAt: string;
  // contentMd omitted in list responses unless detail=true
  contentMd?: string;
}

export interface Relation {
  id: UUID;
  fromId: UUID;
  toId: UUID;
  type: RelationType;
  createdAt: string;
}

export interface LibraryLink {
  id: UUID;
  workId: UUID;
  source: LibrarySource;
  externalId: string;
  url: string | null;
  titleHint: string | null;
  createdAt: string;
}

export interface RunTask {
  id: string;
  title: string;
  status: 'pending' | 'in_progress' | 'completed' | 'failed';
}

export interface Run {
  id: UUID;
  intent: RunIntent;
  goal: string;
  status: RunStatus;
  plan: RunTask[];
  result: Record<string, unknown> | null;
  error: Record<string, unknown> | null;
  model: string;
  startedAt: string;
  lastActive: string;
  expiresAt: string | null;
  completedAt: string | null;
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
  | 'run.failed';

export interface RunEvent {
  id: string;
  runId: UUID;
  seq: number;
  type: RunEventType;
  payload: Record<string, unknown>;
  createdAt: string;
}

export interface SearchHit {
  id: UUID;
  kind: 'work' | 'doc';
  title: string;
  snippet: string;
  slug: string;
}

export interface ApiError {
  error: { code: string; message: string };
}

export interface CreateWorkBody {
  parentId?: UUID | null;
  kind: WorkKind;
  medium?: Medium;
  title: string;
  slug?: string;
}

export interface PatchWorkBody {
  title?: string;
  parentId?: UUID | null;
  medium?: Medium;
  status?: WorkStatus;
  sortOrder?: number;
}

export interface PutContentBody {
  contentMd: string;
  author: Author;
  runId?: UUID;
  summary?: string;
  expectedVersion?: number;
}

export interface CreateDocBody {
  title: string;
  contentMd?: string;
  links?: UUID[];
}

export interface CreateRunBody {
  intent: RunIntent;
  goal: string;
  context?: {
    workId?: UUID;
    docId?: UUID;
    parentId?: UUID;
    medium?: Medium;
    section?: string;
    extra?: Record<string, unknown>;
  };
}

export interface Settings {
  llm: {
    endpoint: string;
    model: string;
    apiKeyConfigured: boolean;
    temperature?: number;
    maxTokens?: number;
  };
  library: {
    embyUrl?: string;
    embyApiKey?: string;
    komgaUrl?: string;
    komgaApiKey?: string;
    gameatlasUrl?: string;
    gameatlasApiKey?: string;
  };
  runs: {
    expireDays: number;
    keepEventsDays: number;
  };
  skillRoot: string;
}
