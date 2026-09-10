// Package domain mirrors shared/types.ts as Go structs with camelCase JSON tags.
package domain

import "github.com/google/uuid"

type UUID = uuid.UUID

type WorkKind string

const (
	WorkKindUniverse WorkKind = "universe"
	WorkKindSeries   WorkKind = "series"
	WorkKindWork     WorkKind = "work"
)

type Medium string

const (
	MediumGame  Medium = "game"
	MediumMovie Medium = "movie"
	MediumTV    Medium = "tv"
	MediumAnime Medium = "anime"
	MediumManga Medium = "manga"
	MediumNovel Medium = "novel"
	MediumBook  Medium = "book"
	MediumOther Medium = "other"
)

type WorkStatus string

// Visibility 决定访客能否看到该节点（public 且所有祖先都 public 才可见）。
type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)

const (
	WorkStatusStub  WorkStatus = "stub"
	WorkStatusDraft WorkStatus = "draft"
	WorkStatusReady WorkStatus = "ready"
)

type Author string

const (
	AuthorHuman  Author = "human"
	AuthorLLM    Author = "llm"
	AuthorImport Author = "import"
)

type RelationType string

const (
	RelationAdaptationOf RelationType = "adaptation_of"
	RelationSequelTo     RelationType = "sequel_to"
	RelationSpinOffOf    RelationType = "spin_off_of"
	RelationRemakeOf     RelationType = "remake_of"
	RelationExpansionOf  RelationType = "expansion_of"
	RelationReferences   RelationType = "references"
	// same_series is display-only; store only when not derivable from tree.
	RelationSameSeries RelationType = "same_series"
)

// ValidRelationTypes is the frozen dictionary. Code-change only; no runtime invention.
var ValidRelationTypes = map[RelationType]bool{
	RelationAdaptationOf: true,
	RelationSequelTo:     true,
	RelationSpinOffOf:    true,
	RelationRemakeOf:     true,
	RelationExpansionOf:  true,
	RelationReferences:   true,
	RelationSameSeries:   true,
}

type LibrarySource string

const (
	LibraryEmby      LibrarySource = "emby"
	LibraryKomga     LibrarySource = "komga"
	LibraryGameAtlas LibrarySource = "gameatlas"
)

type RunIntent string

const (
	RunIntentCreateWiki     RunIntent = "create_wiki"
	RunIntentContinueWiki   RunIntent = "continue_wiki"
	RunIntentRewriteSection RunIntent = "rewrite_section"
	RunIntentWriteDoc       RunIntent = "write_doc"
	RunIntentOrganizeTree   RunIntent = "organize_tree"
	RunIntentAnswer         RunIntent = "answer"
	RunIntentSyncLibrary    RunIntent = "sync_library"
)

type RunStatus string

const (
	RunStatusRunning     RunStatus = "running"
	RunStatusInterrupted RunStatus = "interrupted"
	RunStatusCompleted   RunStatus = "completed"
	RunStatusFailed      RunStatus = "failed"
	RunStatusExpired     RunStatus = "expired"
)

// Work is a tree node (universe / series / work).
type Work struct {
	ID         string     `json:"id"`
	ParentID   *string    `json:"parentId"`
	Kind       WorkKind   `json:"kind"`
	Medium     *Medium    `json:"medium"`
	Title      string     `json:"title"`
	Slug       string     `json:"slug"`
	Aliases    []string   `json:"aliases"`
	ContentMd  *string    `json:"contentMd"`
	ContentVer int64      `json:"contentVer"`
	Status     WorkStatus `json:"status"`
	// Visibility: public | private；访客只有"自身与祖先都 public"的节点可见
	Visibility Visibility `json:"visibility"`
	SortOrder  int64      `json:"sortOrder"`
	CreatedAt  string     `json:"createdAt"`
	UpdatedAt  string     `json:"updatedAt"`
}

// WorkSummary is the flat tree row.
type WorkSummary struct {
	ID             string     `json:"id"`
	ParentID       *string    `json:"parentId"`
	Kind           WorkKind   `json:"kind"`
	Medium         *Medium    `json:"medium"`
	Title          string     `json:"title"`
	Slug           string     `json:"slug"`
	Status         WorkStatus `json:"status"`
	Visibility     Visibility `json:"visibility"`
	HasContent     bool       `json:"hasContent"`
	HasLibraryLink bool       `json:"hasLibraryLink"`
	SortOrder      int64      `json:"sortOrder"`
	UpdatedAt      string     `json:"updatedAt"`
}

// Doc is a materials document hanging under a work folder.
type Doc struct {
	ID         string   `json:"id"`
	FolderOf   string   `json:"folderOf"`
	Title      string   `json:"title"`
	Slug       string   `json:"slug"`
	ContentMd  string   `json:"contentMd"`
	ContentVer int64    `json:"contentVer"`
	Links      []string `json:"links"`
	CreatedAt  string   `json:"createdAt"`
	UpdatedAt  string   `json:"updatedAt"`
}

// Revision stores one content commit for works and docs.
type Revision struct {
	ID         string  `json:"id"`
	TargetType string  `json:"targetType"` // work | doc
	TargetID   string  `json:"targetId"`
	Version    int64   `json:"version"`
	Author     Author  `json:"author"`
	RunID      *string `json:"runId"`
	Summary    string  `json:"summary"`
	CreatedAt  string  `json:"createdAt"`
	ContentMd  string  `json:"contentMd,omitempty"`
}

// Relation is a typed directed edge between works.
type Relation struct {
	ID        string       `json:"id"`
	FromID    string       `json:"fromId"`
	ToID      string       `json:"toId"`
	Type      RelationType `json:"type"`
	CreatedAt string       `json:"createdAt"`
}

// LibraryLink points at Emby / Komga / GameAtlas.
type LibraryLink struct {
	ID         string        `json:"id"`
	WorkID     string        `json:"workId"`
	Source     LibrarySource `json:"source"`
	ExternalID string        `json:"externalId"`
	URL        *string       `json:"url"`
	TitleHint  *string       `json:"titleHint"`
	CreatedAt  string        `json:"createdAt"`
}

// RunTask is one step in a run plan.
type RunTask struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // pending | in_progress | completed | failed
}

// Run is a librarian work order.
type Run struct {
	ID         string         `json:"id"`
	Workspace  string         `json:"workspace"`
	Intent     RunIntent      `json:"intent"`
	Goal       string         `json:"goal"`
	Status     RunStatus      `json:"status"`
	Plan       []RunTask      `json:"plan"`
	Checkpoint map[string]any `json:"checkpoint,omitempty"`
	ToolCache  map[string]any `json:"toolCache,omitempty"`
	Result     map[string]any `json:"result"`
	Error      map[string]any `json:"error"`
	// Context 是从 checkpoint 里投影出来的上下文（workId/docId…），
	// 供前端做跳转与批次卡片；不是新增字段，读的是 checkpoint.context。
	Context     map[string]any `json:"context,omitempty"`
	Model       string         `json:"model"`
	StartedAt   string         `json:"startedAt"`
	LastActive  string         `json:"lastActive"`
	ExpiresAt   *string        `json:"expiresAt"`
	CompletedAt *string        `json:"completedAt"`
}

// RunEvent is one SSE stream record.
type RunEvent struct {
	ID        string         `json:"id"`
	RunID     string         `json:"runId"`
	Seq       int64          `json:"seq"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	CreatedAt string         `json:"createdAt"`
}

// SearchHit is one FTS result.
type SearchHit struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // work | doc
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
	Slug    string `json:"slug"`
}

// Settings is the persisted app configuration.
type Settings struct {
	LLM struct {
		Endpoint         string   `json:"endpoint"`
		Model            string   `json:"model"`
		APIKeyConfigured bool     `json:"apiKeyConfigured"`
		Temperature      *float64 `json:"temperature,omitempty"`
		MaxTokens        *int     `json:"maxTokens,omitempty"`
	} `json:"llm"`
	Library struct {
		EmbyURL         *string `json:"embyUrl,omitempty"`
		EmbyAPIKey      *string `json:"embyApiKey,omitempty"`
		KomgaURL        *string `json:"komgaUrl,omitempty"`
		KomgaAPIKey     *string `json:"komgaApiKey,omitempty"`
		GameAtlasURL    *string `json:"gameatlasUrl,omitempty"`
		GameAtlasAPIKey *string `json:"gameatlasApiKey,omitempty"`
		// 是否已配置（响应里只回标记，不回密钥原文）
		EmbyAPIKeyConfigured      bool `json:"embyApiKeyConfigured"`
		KomgaAPIKeyConfigured     bool `json:"komgaApiKeyConfigured"`
		GameAtlasAPIKeyConfigured bool `json:"gameatlasApiKeyConfigured"`
	} `json:"library"`
	Runs struct {
		ExpireDays        int `json:"expireDays"`
		KeepEventsDays    int `json:"keepEventsDays"`
		MaxConcurrentRuns int `json:"maxConcurrentRuns"`
	} `json:"runs"`
	// Search 联网检索（Exa）。key 单独存 settings 表，接口只回"是否已配置"。
	Search struct {
		ExaAPIKeyConfigured bool `json:"exaApiKeyConfigured"`
	} `json:"search"`
	// Admin 简易用户系统（单管理员）。密码只存哈希，接口只回是否已设置。
	Admin struct {
		Username           string `json:"username"`
		PasswordConfigured bool   `json:"passwordConfigured"`
		// UsingDefaultPassword 仍是出厂密码 1234（设置页提醒改掉）
		UsingDefaultPassword bool `json:"usingDefaultPassword"`
		// NewNodeVisibility 新节点默认可见性（public|private）
		NewNodeVisibility Visibility `json:"newNodeVisibility"`
	} `json:"admin"`
	SkillRoot string `json:"skillRoot"`
}

// DefaultSettings returns sane defaults.
func DefaultSettings() Settings {
	var s Settings
	s.LLM.Endpoint = "https://api.openai.com/v1"
	s.LLM.Model = "gpt-4o-mini"
	s.LLM.APIKeyConfigured = false
	s.Runs.ExpireDays = 7
	s.Runs.KeepEventsDays = 90
	s.Runs.MaxConcurrentRuns = 2
	s.Admin.Username = "admin"
	s.Admin.NewNodeVisibility = VisibilityPrivate
	s.SkillRoot = "/root/WikiAltas/skills/wiki-writing"
	return s
}

// CreateWorkBody is POST /api/works.
type CreateWorkBody struct {
	ParentID *string  `json:"parentId"`
	Kind     WorkKind `json:"kind"`
	Medium   *Medium  `json:"medium"`
	Title    string   `json:"title"`
	Slug     *string  `json:"slug"`
	// Visibility 省略时用设置页的"新节点默认可见性"
	Visibility *Visibility `json:"visibility"`
}

// PatchWorkBody is PATCH /api/works/:id.
type PatchWorkBody struct {
	Title      *string     `json:"title"`
	ParentID   *string     `json:"parentId"`
	Medium     *Medium     `json:"medium"`
	Status     *WorkStatus `json:"status"`
	Visibility *Visibility `json:"visibility"`
	SortOrder  *int64      `json:"sortOrder"`
	Aliases    *[]string   `json:"aliases"`
}

// PutContentBody is PUT content endpoints.
type PutContentBody struct {
	ContentMd       string  `json:"contentMd"`
	Author          Author  `json:"author"`
	RunID           *string `json:"runId"`
	Summary         *string `json:"summary"`
	ExpectedVersion *int64  `json:"expectedVersion"`
}

// CreateDocBody is POST /api/works/:id/docs.
type CreateDocBody struct {
	Title     string   `json:"title"`
	ContentMd *string  `json:"contentMd"`
	Links     []string `json:"links"`
}

// PatchDocBody is PATCH /api/docs/:id.
type PatchDocBody struct {
	Title *string   `json:"title"`
	Links *[]string `json:"links"`
}

// CreateRelationBody is POST /api/relations.
type CreateRelationBody struct {
	FromID string       `json:"fromId"`
	ToID   string       `json:"toId"`
	Type   RelationType `json:"type"`
}

// CreateRunBody is POST /api/runs.
type CreateRunBody struct {
	Intent RunIntent `json:"intent"`
	Goal   string    `json:"goal"`
	// Workspace 是"会话键"（work:<id> / home / batch:<id>）。
	// 面板刷新后按它召回同一段对话；留空按 default 处理。列已存在于 runs 表。
	Workspace string      `json:"workspace"`
	Context   *RunContext `json:"context"`
}

// RunContext 是一次工单要绑定的上下文（作品/资料/父节点/介质/章节）。
type RunContext struct {
	WorkID   *string `json:"workId"`
	DocID    *string `json:"docId"`
	ParentID *string `json:"parentId"`
	Medium   *Medium `json:"medium"`
	Section  *string `json:"section"`
	// DocMode 是发起工单时用户所处的文档模式：read | edit | revision。
	// 它决定 Altas 的作业方式（只读=只分析不改、编辑=直接改、修订=定点改+给理由）。
	DocMode *string `json:"docMode"`
	// Selection 是用户在正文里选中的文本（修订模式的作用域，对齐飞书「选定内容」）。
	Selection *string        `json:"selection"`
	Extra     map[string]any `json:"extra"`
}

// CreateBatchBody is POST /api/runs/batch —— 批量建档（Emby/Komga/GameAtlas
// 扫出来的一堆 stub，一键让 Altas 逐个写）。
// 不新建表：一个批次 = 一个共享 workspace（batch:<id>）+ N 个子工单。
type CreateBatchBody struct {
	WorkIDs   []string `json:"workIds"`
	BatchSize int      `json:"batchSize"`
	Medium    *Medium  `json:"medium"`
	// GoalTemplate 可选，缺省用「写《{title}》的 Wiki」。
	GoalTemplate string `json:"goalTemplate"`
}

// BatchResult 返回批次 id 与实际创建的工单。
type BatchResult struct {
	BatchID    string   `json:"batchId"`
	Workspace  string   `json:"workspace"`
	RunIDs     []string `json:"runIds"`
	SkippedIDs []string `json:"skippedIds"`
}

// ContentCommitResult is returned by PUT content.
type ContentCommitResult struct {
	ID         string `json:"id"`
	ContentVer int64  `json:"contentVer"`
	RevisionID string `json:"revisionId"`
}

// WorkDetail is GET /api/works/:id response.
type WorkDetail struct {
	Work           Work          `json:"work"`
	LatestRevision *Revision     `json:"latestRevision,omitempty"`
	LibraryLinks   []LibraryLink `json:"libraryLinks"`
	Relations      []Relation    `json:"relations"`
}
