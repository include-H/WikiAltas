# WikiAltas v2 冻结设计

> 状态：FROZEN — 作为 v2 唯一实现依据，冲突时以本文为准。  
> 定位：飞书云文档交互骨架 + 作品宇宙树 + 严格按 skill 写作的 LLM 馆员。  
> 范围：游戏 / 影视 / 漫画 / 小说的中文百科，服务个人媒体库（GameAtlas / Emby / Komga）。

---

## 1. 产品一句话

**WikiAltas = 你的媒体库书架 + 一位有全权工具的馆员。**

- 读：左树点开作品，看 9 章中文长文  
- 写：右侧对话让馆员写/改/整理，MD 即时渲染进正文，写完即落库  
- 管：树、资料夹、库外链、版本回滚  

不是通用知识平台，不是聊天应用加编辑器。

---

## 2. 与 v1 的根本决裂

| v1 病根 | v2 |
|---------|-----|
| PageBlock + Entity/Fact/Relation 三套真相 | **一篇 Markdown 是唯一正文** |
| Proposal 多级审批 | **写完即 commit**，回滚靠 revision |
| Thread/Turn/Rollout 双栈 | **一个 Run + checkpoint** |
| 图谱/Fact/Compiler 主路径 | **全部后置或删除** |
| 读写双模式分裂 | **默认编辑态**，阅读可切 |

---

## 3. 领域模型

### 3.1 节点类型

```
Universe（宇宙/品牌）     最终幻想、猎魔人、使命召唤
├── Series（子系列）      巫师系列、FFVII、新水晶神话
│   └── Work（单作）      巫师1、FFVII Remake、圣子降临
└── Work（直属宇宙）      独立作品无系列时

Doc（资料）               不进主树，挂在某节点的资料夹
```

- 树用 `parent_id`，只表达归属。  
- 作品关系用少量类型化边（见 3.4），不进树。  
- 资料夹是节点的第二抽屉，不混进宇宙树叶子。

### 3.2 表结构（SQLite，共 6 张）

```sql
-- 作品树节点（宇宙 / 系列 / 单作）
CREATE TABLE works (
  id            TEXT PRIMARY KEY,          -- UUIDv7
  parent_id     TEXT REFERENCES works(id),
  kind          TEXT NOT NULL,             -- universe | series | work
  medium        TEXT,                      -- game | movie | tv | anime | manga | novel | book | other
  title         TEXT NOT NULL,
  slug          TEXT NOT NULL UNIQUE,
  aliases_json  TEXT NOT NULL DEFAULT '[]',
  content_md    TEXT,                      -- 正文：9 章 Wiki / 系列主文；universe 可空
  content_ver   INTEGER NOT NULL DEFAULT 0,
  status        TEXT NOT NULL DEFAULT 'stub',  -- stub | draft | ready
  visibility    TEXT NOT NULL DEFAULT 'private', -- public | private（简易用户系统）
  sort_order    INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

-- 资料文档（挂在任意 works 节点下）
CREATE TABLE docs (
  id            TEXT PRIMARY KEY,
  folder_of     TEXT NOT NULL REFERENCES works(id),  -- 归属作品
  title         TEXT NOT NULL,
  slug          TEXT NOT NULL UNIQUE,
  content_md    TEXT NOT NULL DEFAULT '',
  content_ver   INTEGER NOT NULL DEFAULT 0,
  links_json    TEXT NOT NULL DEFAULT '[]',          -- 可选：关联 works id
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

-- 正文版本（works 和 docs 共用）
CREATE TABLE revisions (
  id            TEXT PRIMARY KEY,
  target_type   TEXT NOT NULL,             -- work | doc
  target_id     TEXT NOT NULL,
  version       INTEGER NOT NULL,
  content_md    TEXT NOT NULL,
  author        TEXT NOT NULL,             -- human | llm | import
  run_id        TEXT,
  summary       TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL,
  UNIQUE(target_type, target_id, version)
);

-- 作品关系（冻结词典，有向）
CREATE TABLE relations (
  id            TEXT PRIMARY KEY,
  from_id       TEXT NOT NULL REFERENCES works(id),
  to_id         TEXT NOT NULL REFERENCES works(id),
  type          TEXT NOT NULL,             -- 见 3.4
  created_at    TEXT NOT NULL,
  UNIQUE(from_id, to_id, type)
);

-- 库外链（Emby / Komga / GameAtlas）
CREATE TABLE library_links (
  id            TEXT PRIMARY KEY,
  work_id       TEXT NOT NULL REFERENCES works(id),
  source        TEXT NOT NULL,             -- emby | komga | gameatlas
  external_id   TEXT NOT NULL,
  url           TEXT,
  title_hint    TEXT,
  created_at    TEXT NOT NULL,
  UNIQUE(source, external_id)
);

-- Atlas Run（可续工单）
CREATE TABLE runs (
  id            TEXT PRIMARY KEY,
  workspace     TEXT NOT NULL DEFAULT 'default',
  intent        TEXT NOT NULL,             -- create_wiki | continue_wiki | rewrite_section | write_doc | organize_tree | answer | sync_library
  goal          TEXT NOT NULL,
  status        TEXT NOT NULL,             -- running | interrupted | completed | failed | expired
  plan_json     TEXT NOT NULL DEFAULT '[]',
  checkpoint    TEXT NOT NULL DEFAULT '{}',
  tool_cache    TEXT NOT NULL DEFAULT '{}',
  result_json   TEXT,
  error_json    TEXT,
  model         TEXT NOT NULL DEFAULT '',
  started_at    TEXT NOT NULL,
  last_active   TEXT NOT NULL,
  expires_at    TEXT,
  completed_at  TEXT
);

CREATE TABLE run_events (
  id            TEXT PRIMARY KEY,
  run_id        TEXT NOT NULL REFERENCES runs(id),
  seq           INTEGER NOT NULL,
  type          TEXT NOT NULL,             -- 见 6.2
  payload       TEXT NOT NULL DEFAULT '{}',
  created_at    TEXT NOT NULL,
  UNIQUE(run_id, seq)
);
```

### 3.3 内容规则

- **唯一正文真相 = `content_md`**。人和 LLM 都只改这一份。  
- 9 章骨架由 skill 约束，**不存结构化章节表**。  
- 左栏大纲从 MD 的 `##` / `###` 实时解析。  
- `:::epigraph` 等围栏语法在渲染层处理。  
- 缺事实写「待核实」，不编造。  
- 人机直接改同一 MD，commit 成 revision，**无审批**。

### 3.4 关系词典（冻结）

```
adaptation_of     改编作 -> 原作
sequel_to         续作 -> 前作
spin_off_of       衍生 -> 来源
remake_of         重制 -> 原作
expansion_of      资料片/扩展 -> 本体
references        引用/致敬
same_series       同系列（仅展示用，可由树推导则不存）
```

禁止自由文本谓词。新增关系走代码改词典，不是运行时发明。

### 3.5 库同步边界

### 3.6 访问与隐私（2026-09-10 追加）

- **简易用户系统（软门槛）**：**只有访问密码、没有账号体系**——密码哈希（PBKDF2）存 `settings` 表，登录后写 HttpOnly 会话 Cookie（HMAC 签名，密钥自动生成）；连续失败 5 次锁定 5 分钟。
  - 定位说明：这道密码用于**防止不该公开展示的内容（如以后的 Gal 条目）被顺路看到**，**不是安全边界**；真正的公开/私有边界由节点 `visibility` 承担。不要把敏感数据交给它保护。
- **访客（未登录）**：只能**只读浏览公开文档**——`GET tree / works / docs / search`，且只返回公开内容；其余接口一律 401。
- **节点可见性**：`works.visibility = public | private`，**资料继承所属节点**；节点对访客可见的条件是「自身与所有祖先都是 public」。
- 默认 `private`（发布显式发生）；设置页可配「新节点默认可见性」。

- `library_links` 只存外链与 ID，**不把 Emby description 当正文**。  
- 同步工具：从 Emby/Komga/GameAtlas 拉列表 → 比对 → 建 `stub` 节点 + 挂链。  
- 正文永远由 Wiki 侧写。  
- 树上显示「待建档」= `status='stub'` 且 `content_md` 空。

---

## 4. Atlas Run（Harness）

### 4.1 定位

Run 是**一次可续的馆员工单**，不是 Thread 产品。

```
用户指令 / 唤起馆员
  → 创建或 resume Run
  → 意图 → 计划 → 工具循环 → 产出 → 完成
```

### 4.2 生命周期

```
created → running → completed
                 ↘ interrupted → (resume) → running
                 ↘ failed → (retry) → running
                 ↘ expired（超期清理）
```

- 断线/刷新 → `interrupted`，checkpoint 保留。  
- 点「继续」或再发一句 → **同一 Run resume**，跳过已完成步骤，命中 `tool_cache` 不重打 Exa。  
- `completed` 后默认不续；可「接着做下一部」开新 Run，新 Run 可读已 commit 内容。

### 4.3 生命周期清理

| 数据 | 保留 |
|------|------|
| checkpoint + tool_cache | running/interrupted 最多 7 天；completed 最多 48h |
| run_events 叙事摘要 | 90 天 |
| Exa 结果按 query hash | 全局 LRU 30 天（跨 Run 省额度） |
| 已 commit 的 works/docs/revisions | **永不随 Run 删** |

内容是资产，Run 是工单。

### 4.4 工具面

**读**

| 工具 | 说明 |
|------|------|
| `search_works` | 标题/别名/FTS |
| `read_work` | 元数据 + content_md |
| `read_doc` | 资料正文 |
| `get_tree` | 子树 |
| `read_skill` | wiki-writing skill 文件 |
| `search_web` | Exa 联网检索 |
| `fetch_url` | 读允许的搜索结果页 |
| `list_library` | Emby/Komga/GameAtlas 条目 |

**写**（直接生效，进 revision）

| 工具 | 说明 |
|------|------|
| `upsert_work` | 建/改节点（title/medium/parent/status） |
| `write_content` | 整篇替换 works/docs 的 content_md |
| `patch_section` | 按 `##` 锚点局部替换 |
| `upsert_relation` | 按冻结词典 |
| `attach_library_link` | 挂外链 |
| `create_doc` | 在资料夹建资料 |
| `sync_library` | 从库同步 stub |

**答**

| 工具 | 说明 |
|------|------|
| `answer` | 口头回答，可附跳转 work/doc id |

### 4.5 权限

| 类别 | 策略 |
|------|------|
| 建节点、写新正文、挂链、建资料 | **自动** |
| 整篇重写已 `ready` 的文 | 自动，但进 revision，可一键回滚 |
| 移动已有子树 | 自动 + revision 可回滚 |
| 删节点 | 需用户在对话中明确说删除 |
| 合并重复作品 | 需明确确认 |

**没有 Proposal 审核队列。**

### 4.6 写作管线（create_wiki）

```
1. 读 skill：SKILL.md → core.md → 对应 media-*.md（只读一个）
2. 确认对象边界：哪一部、哪个版本、剧透、深度
3. 检索：站内相关条目 + Exa 窄域查询
4. 按 9 章骨架写 MD（或局部重写指定章）
5. 自检：章节齐、待核实标注、参考 ≥5、无编造
6. write_content → revision+1 → status=ready
7. 事件流汇报「已写入 / 差异摘要」
```

### 4.7 临时文件 → Push 渲染

与飞书同构的产出路径：

```
LLM 产出 MD
  → 服务端暂存（run 内 staging，或直接 write_content 草稿）
  → API push
  → 前端 SSE/WebSocket 收到 content 更新
  → 正文区即时渲染（生成中可半透明/标记）
  → 过质量线 → 成为最新 revision
```

草稿层：生成中内容可在前端 overlay 展示；**默认阅读读最新 commit**。生成完成自动 commit 后刷新为正式内容。

---

## 5. 前端信息架构

### 5.1 布局（照抄飞书）

```
┌────────┬──────────┬────────────────────┬─────────────┐
│ 全局栏  │ 宇宙树    │ 大纲    │ 正文       │ AI 面板      │
│ 48px   │ 240px    │ 160px  │ flex       │ 360px       │
└────────┴──────────┴────────────────────┴─────────────┘
```

- **全局栏**：WikiAltas 标识、搜索、库同步入口、设置  
- **宇宙树**：Universe → Series → Work；资料夹入口  
- **大纲**：当前文 `##`/`###` 目录，点击滚动  
- **正文**：**默认编辑态**；顶部可切 编辑 / 阅读  
- **AI 面板**：默认可收起；打开后是馆员对话 + 折叠工作流  

关掉 AI = 干净文档界面（你的图 1）。

### 5.2 宇宙树

```
猎魔人                          universe
├── 小说
│   ├── 猎魔人（短篇集）         work/book    [stub]
│   └── 白狼崛起                work/book    [ready]
├── 巫师系列                     series
│   ├── 巫师1                   work/game    [ready]  📎
│   ├── 巫师2                   work/game    [ready]  📎
│   └── 巫师3                   work/game    [stub]   📎
└── 电视剧
    └── 猎魔人                  work/tv      [ready]

📎 = 有 library_link
```

- 节点状态徽标：stub（待建档）/ draft / ready  
- 右键或悬停：新建子节点、打开资料夹、馆员建档  
- **不把资料塞进树**；点作品的「资料夹」→ 左栏切到该作品资料列表

### 5.3 资料夹

```
左栏切到：巫师1 ▸ 剧情解析 / 速通笔记 / …
顶部：← 返回宇宙树 | 巫师1 Wiki
正文：打开选中资料（自由 MD，无 9 章约束）
```

### 5.4 AI 面板（照抄飞书任务流）

**空态**：「今天有什么工作要处理？」+ 输入框（发消息 / 使用技能 @ 添加资料）

**工作态**（MiMo/飞书折叠流）：

```
已处理  4m 12s
我先读 skill 和站内已有条目，再联网核实关键事实。

▸ 已读取 wiki-writing skill
▸ 已搜索 2 次 · 参考 14 篇资料
▸ 核实主创与发行信息

资料核实完毕。现在按 9 章骨架写入条目。

▸ 正在写入文件

初稿完成，弱化两处单源细节（关卡数、某声优）。

▸ 已写入《荣誉勋章：血战太平洋》Wiki
✓ 已完成
```

- 默认显示叙事句 + 可点折叠的操作芯片  
- 展开后是工具入参摘要、检索词、URL（不展示原始 CoT）  
- 输入框：继续对话 / 纠正 / 「下一章太水，重写第 4 章」

### 5.5 默认编辑态

- 打开作品 = 可编辑 Markdown（所见即所得或源码双模式，后续定）  
- 顶栏：编辑（默认）/ 阅读  
- LLM 写入时：正文区提示「馆员正在写入…」，完成后刷成新内容  
- 人直接打字保存 = `author=human` 的 revision  

### 5.6 页面路由

```
/                               首页：最近作品、待建档、进行中 Run
/w/:slug                        作品（树定位 + 大纲 + 正文）
/w/:slug/folder                 资料夹
/w/:slug/folder/:docSlug        某篇资料
/runs                           馆员工单列表（进行中/中断/完成）
/settings                       LLM、库连接、清理策略
```

---

## 6. 前后端契约

### 6.1 原则

- REST + JSON；流式用 SSE。  
- ID 一律 UUIDv7 字符串。  
- 时间 ISO8601。  
- 错误：`{ "error": { "code": string, "message": string } }`  
- 正文提交幂等：`Idempotency-Key` 头（可选）。

### 6.2 事件类型（SSE `run_events`）

| type | payload 要点 |
|------|----------------|
| `run.started` | runId, goal |
| `plan.updated` | tasks[]: {id,title,status} |
| `narrative` | text（一句话叙述） |
| `tool.started` | name, inputSummary |
| `tool.done` | name, outputSummary, durationMs |
| `content.staging` | targetType, targetId, previewMd? |
| `content.committed` | targetType, targetId, version |
| `tree.updated` | works[] 变更摘要 |
| `run.completed` | result 摘要 |
| `run.failed` | error |

前端投影规则见 5.4：只消费叙事 + 芯片，不 dump payload。

### 6.3 API

**树与作品**

```
GET    /api/tree
       → { nodes: TreeNode[] }   // 嵌套或扁平+parent_id 均可，建议扁平
GET    /api/works/:id
       → { work, revisions?: latest, libraryLinks[], relations[] }
POST   /api/works
       body: { parentId?, kind, medium, title, slug? }
       → work
PATCH  /api/works/:id
       body: { title?, parentId?, medium?, status?, sortOrder? }
PUT    /api/works/:id/content
       body: { contentMd, author: 'human'|'llm', runId?, summary?, expectedVersion? }
       → { id, contentVer, revisionId }
       // expectedVersion 冲突返回 409
DELETE /api/works/:id
       → 204   // 软删或硬删由实现定，须有子节点校验
```

**资料**

```
GET    /api/works/:id/docs          → docs[]（folder_of=:id）
GET    /api/docs/:id
POST   /api/works/:id/docs          body: { title, contentMd?, links? }
PUT    /api/docs/:id/content        body 同 work content
PATCH  /api/docs/:id                body: { title?, links? }
```

**关系**

```
GET    /api/works/:id/relations
POST   /api/relations               body: { fromId, toId, type }
DELETE /api/relations/:id
```

**版本**

```
GET    /api/works/:id/revisions?limit=20
GET    /api/docs/:id/revisions?limit=20
POST   /api/works/:id/revisions/:revId/restore
       → 新 revision（回滚也是新版本，不删历史）
```

**馆员 Run**

```
POST   /api/runs
       body: {
         intent, goal,
         context?: { workId?, docId?, parentId?, extra? }
       }
       → { runId }

POST   /api/runs/:id/resume
       → { runId }   // interrupted/failed 续跑

POST   /api/runs/:id/cancel
GET    /api/runs?status=&limit=
GET    /api/runs/:id
       → run + plan + 近期 events
GET    /api/runs/:id/events/stream          // SSE
       // 支持 Last-Event-ID 续传
```

**库**

```
GET    /api/library/sources               // 已配置的 emby/komga/gameatlas
POST   /api/library/sync                  body: { source? } → 新建或接续 Run(intent=sync_library)
GET    /api/library/preview?source=emby   // 同步预览（将新增的 stub 列表）
```

**搜索**

```
GET    /api/search?q=&kind=work|doc
       → [{ id, kind, title, snippet, slug }]
```

**设置**

```
GET    /api/settings
PUT    /api/settings
       // llm: endpoint, model, keyRef
       // library: embyUrl, komgaUrl, gameatlasUrl, keys
       // runs: expireDays, keepEventsDays
```

### 6.4 内容提交语义

- `PUT .../content` 服务端：校验 version → 写 content_md → content_ver+1 → 写 revisions → 返回。  
- LLM 工具 `write_content` 走同一 Domain 入口，**禁止**第二套写路径。  
- 409 时前端/馆员需重新读版本再写（不做静默三方合并）。

---

## 7. Semi UI 使用规范

### 7.1 原则

- 组件优先 Semi；Token 优先；禁止新写 hex / 反向覆盖 `.semi-*`。  
- 主题：`document.body` 的 `theme-mode`。  
- 布局密度对齐飞书：偏紧凑、内容优先。

### 7.2 何时用 Semi（强制）

| 场景 | Semi 组件 |
|------|-----------|
| 全局壳 | `Layout`, `Layout.Sider`, `Layout.Content`, `Layout.Header` |
| 树 | `Tree`（宇宙树、资料夹列表） |
| 大纲 | 自定义轻量列表或 `Typography` 锚点（不必上重型组件） |
| 正文编辑 | 自研/CodeMirror6 + MD；工具条用 `Button` `Dropdown` `Tooltip` |
| 模式切换 | `RadioGroup` 或 `Dropdown`（编辑/阅读） |
| AI 对话流 | `AIChatDialogue` / `AIChatInput`（有则用；无则仿结构但 token 一致） |
| 进度折叠 | `Collapse` + 自定义事件行（对齐飞书芯片，不 JSON dump） |
| 表单/设置 | `Form` `Input` `Select` `Switch` `InputNumber` |
| 反馈 | `Modal` `Toast` `Spin` `Empty` `Banner` |
| 版本列表 | `Table` 或 `Timeline` |
| 搜索 | `Input` + `AutoComplete` 或 `Select` 远程搜索 |
| 标签/状态 | `Tag`（stub/draft/ready） |

### 7.3 何时不用 Semi

| 场景 | 做法 |
|------|------|
| MD 正文渲染 | 专用渲染器（允许 `:::epigraph`），样式用 CSS 变量对齐 Semi token |
| 大纲滚动高亮 | IntersectionObserver + 轻量 DOM |
| AI 事件时间线里的芯片排版 | 半自定义，仅用 `Tag`/`Icon`/`Typography` 拼 |
| 纯阅读排版 | 字号行距按内容优先，不必强行套 Card |

### 7.4 文件组织

```
src/
  views/          # Library(树+文), Folder, RunList, Settings, Home
  components/
    shell/        # AppShell, GlobalRail, TreePane, OutlinePane, AiPanel
    editor/       # MarkdownEditor, toolbar
    run/          # RunTimeline, ToolChip, NarrativeLine
  lib/            # api.ts, types.ts, mdOutline.ts, theme.ts
```

单文件建议 ≤ 300 行；业务不堆进 `main.tsx`。

---

## 8. 技术栈（v2）

| 层 | 选型 | 说明 |
|----|------|------|
| 前端 | React + Vite + Semi UI | 延续现有 |
| 后端 | Go 单体（或 Node，二选一） | 仅保留 Domain + SQLite；**删 harness/compiler 旧栈** |
| DB | SQLite 单文件 | 6 张表 |
| 搜索 | SQLite FTS5 | works/docs 标题+正文 |
| 流式 | SSE | run events |
| LLM | OpenAI 兼容 Chat Completions | 从 settings 读 |
| 联网 | Exa API | tool_cache 按 query hash |

**不做进 v2**：Redis、向量库、Postgres、图数据库、多租户、Thread 树、Graph UI、Fact 表、Proposal 表。

---

## 9. skill 接入

- skill 目录：`.claude/skill/wiki-writing/`（保持 Git 管理）。  
- Harness 启动扫描 frontmatter：`name / description / version / path`。  
- `create_wiki` 固定加载：`SKILL.md` → `core.md` → 一个 `media-*.md`。  
- `write_doc`（资料）不强制 9 章，只加载通用写作边界（事实/待核实/剧透策略）。  
- **运行时默认不注入 examples/**；examples 仅离线评测。  
- `read_skill` 是真实工具；未选中前不把整包 skill 塞进上下文。

---

## 10. 分阶段实现（一次做完整，按序提交）

| Phase | 交付 | 验收 |
|-------|------|------|
| **P0** | SQLite schema + works/docs CRUD + content PUT + revisions | 人能建树、存 MD、回滚 |
| **P1** | 飞书壳：宇宙树 + 大纲 + 编辑态正文 + 阅读切换 | 布局对齐 5.1；默认可编辑 |
| **P2** | Run：create/resume/SSE + 工具面 + write_content 推送渲染 | 说「写 X Wiki」能出 9 章并落库 |
| **P3** | 资料夹 + 同步 library stub + 搜索 + 设置页 | Emby 同步出 stub；资料可挂可读 |
| **P4** | 清理策略 + Run 列表 + 一键恢复 | 过期清 cache；中断可续；Exa 不重复打 |

每个 Phase 结束应用 `go test` / `npm run build` 或等价验证通过。

---

## 11. 明确不做（Out of Scope）

- Entity / Fact / Citation 一等公民与知识图真相  
- Proposal / 多级审批 / 待确认队列  
- Thread / Turn / Rollout / Fork 产品化  
- 全库力导向图谱主 UI  
- 多人实时协同（飞书那样 CRDT）  
- 把 9 章写回 Emby description  
- 通用开放 Agent 框架、插件市场  

需要时用 v3 议题单独立项，不回灌进 v2 范围。

---

## 12. 验收清单（v2 Done）

- [ ] 左栏宇宙树可建/可移，FF 式层级可表达猎魔人全库  
- [ ] 打开作品即编辑态；大纲从 MD 解析；可切阅读  
- [ ] 关 AI 后界面与飞书文档同构干净  
- [ ] 馆员按 skill 写出 9 章，MD 经 API push 即时进正文  
- [ ] 写完即 revision，无审批；可回滚  
- [ ] 资料夹独立于树，可关联作品  
- [ ] Run 可中断可续，tool_cache 防重复 Exa；周期过期  
- [ ] library_links 挂 Emby/Komga/GameAtlas，stub 待建档可见  
- [ ] UI 事件为折叠叙事流，不是 JSON dump  
- [ ] 无 Entity/Fact/Proposal/Graph 表混入主路径  

---

## 13. 实现约束（给后续编码）

1. 所有正文写入走 `PUT content` 同一入口。  
2. 关系 type 枚举写死在代码。  
3. Run resume 从 `checkpoint.lastStep` 起，先查 `tool_cache`。  
4. SSE 断线用 `Last-Event-ID`。  
5. 前端禁止 hex 色；AI 面板组件结构对齐第 5.4、7.2。  
6. 新增表/字段必须先改本文再写迁移。
