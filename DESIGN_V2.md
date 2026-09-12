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

### 3.2 表结构（SQLite）

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

-- 会话消息日志：模型上下文的**事实源**（见 4.8）
CREATE TABLE session_messages (
  id              TEXT PRIMARY KEY,
  session_id      TEXT NOT NULL,
  seq             INTEGER NOT NULL,
  replaces_seq    INTEGER NOT NULL DEFAULT 0,  -- >0 = 替换行，投影时占该位置
  run_id          TEXT NOT NULL DEFAULT '',
  turn            INTEGER NOT NULL DEFAULT 1,
  role            TEXT NOT NULL,               -- system|user|assistant|tool
  content         TEXT NOT NULL DEFAULT '',
  tool_calls_json TEXT NOT NULL DEFAULT '',
  tool_call_id    TEXT NOT NULL DEFAULT '',
  tool_name       TEXT NOT NULL DEFAULT '',
  header_hash     TEXT NOT NULL DEFAULT '',    -- 请求头指纹（4.8）
  header_reason   TEXT NOT NULL DEFAULT '',    -- initial|continue|change
  shadowed_by     TEXT NOT NULL DEFAULT '',    -- 非空 = 被替换遮蔽
  created_at      TEXT NOT NULL,
  UNIQUE(session_id, seq)
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

- **访问控制（硬门槛）**：**只有访问密码、没有账号体系**——密码哈希（PBKDF2）存 `settings` 表，登录后写 HttpOnly 会话 Cookie（HMAC 签名，密钥自动生成）；连续失败 5 次锁定 5 分钟；改密码即让所有旧会话失效。
  - **出厂密码 `1234`**：建库时自动写入（没有它就会出现"没密码 → 登录按不了 → 也进不去设置页设密码"的死锁）。设置页会提醒改掉；密码只存在库里，不从 env 读。
  - 规则：**未登录 = 绝对看不到任何非公开内容**。访客只能读 `public` 且祖先链全 `public` 的节点；私有节点的详情、资料、检索、树一律过滤（404 / 不出现），所有写接口 401。
  - 边界说明：这条硬门槛防的是"看到不该看的内容"；面向公网时，网络层认证另在部署侧解决（反向代理 / 内网白名单）。
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

三个角色必须分清（2026-09-11 重构，对齐 DeepSeek Harness）：

| 角色 | 是什么 | 谁拥有 |
|------|--------|--------|
| 条目（work/doc） | 耐久产物 | 库 |
| **会话** | 挂在条目上的一条**追加式日志**，长命 | `session_messages` |
| **Run** | 这条日志上的一次执行：可取消/续跑/过期 | `runs` |

**Run 不拥有对话**。上下文是会话日志的投影，Run 只是往里追加的执行者——
早先的实现让 Run 各自持一份消息数组，于是新 Run 一律重建上下文：
110 次工具调用、19 个版本之后，下一单只记得住两行。详见 4.8。

**对外只有一个词：工单 = 一场对话。** 界面上列的是会话（`/resume` 的等价物：
列出可继续的对话，点进去接着往下说），**不是**每次执行。
发一句"继续"只是在同一场对话里多接一段，不多出一行。
新对话只在 AI 面板里发起（那儿才知道挂在哪个节点上），工单页不发起新对话。

```
用户指令 / 唤起馆员
  → 同一会话追加一条 user 消息，开一个 Run
  → 意图 → 工具循环（每步都落日志）→ 产出 → 完成
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
| `edit` | 字面串局部替换（`oldString` 必须唯一匹配；题记/说明行等无 `##` 锚点处只此一路） |
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

### 4.5.1 循环纪律（2026-09-10 加固，对齐 DeepSeek Harness 的做法）

对照 `docs/HARNESS_DISPLAY.md`（dsh 源码笔记）补齐的执行器护栏：

| 机制 | 规则 | 为什么 |
|------|------|--------|
| 流式增量 | Responses 的三种 `*.delta`（正文 / 思考 / 工具参数），250ms 节流且**攒批 flush 不丢字** | 面板要"边想边打"，但事件表不能被 token 撑爆；形状见 §6.2 |
| 单轮超时 | 每轮 20 分钟；流式连接另设 **150s 空转看门狗**（没有整体超时，长章节不被腰斩） | 长回答要跑十几分钟，卡死要能断 |
| 重试 | 限流 / 5xx / 网络抖动重试 3 次（1s→4s→10s），叙事流里播报；**已吐字的流式失败不重试** | 偶发 429 不该毁掉整条工单；重放会重复内容 |
| 上下文压缩 | 会话 token 用到模型窗口的 `compactRatio`（0.75）时，在**会话日志**上把中间的工具往返替换成一条摘要（保留最近 24 行），并发 `wikiatlas.context` | 长工单会被 tool 返回撑爆窗口；压缩是唯一能缩小内容的动作，且必须留痕（它必然打断前缀缓存） |
| 重复调用拦截 | 同名同参工具 > 3 次直接回 `ok:false`（`narrative`/`answer`/`update_plan` 除外） | 模型原地绕圈烧额度（观测过同一章连改 6 次） |
| 意图即权限 | `answer` 意图**不下发任何写工具**；只读模式同理；批量建档单次 ≤ 50 部、`limit` ≤ 200 | 从"工具面"上消除误写，比事后拒绝可靠 |

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

### 4.8 会话日志 = 上下文的事实源（2026-09-11 重构）

对齐 DeepSeek Harness 的核心那条：**模型可见 ⟺ 有日志**。

- **一条会话 = 一条追加式日志**（`session_messages`）。模型的消息历史是它的**投影**，
  从不单独存一份数组——不存就不会出现"存下来的和看到的不一致"。
- **只追加，不改历史**。要"改"就追加一行并遮蔽旧行：`shadowed_by` 非空 = 被替换，
  仍在日志里但不在投影上；`replaces_seq > 0` 的替换行**占被替换区间的起始位置**
  （否则被替换的 system 会排到消息中间）。投影规则：活的 system 恒在最前，其余按位置序。
- **新 Run 从日志派生上下文**，不重建。系统提示变了 → 追加一行替换掉旧的 system
  （系统提示是**派生历史**，不是 header）；user 行带请求头指纹与原因
  `initial | continue | change`（对照 dsh 的 `request/header`）。
- **压缩 = 日志上的一次显式替换**，摘要骑在一条 user 行上（同 dsh）。
  工具结果在**入口不截断**——只有压缩能缩小内容。
- **检查点不存消息**（`runs.checkpoint` 只留 iteration/tool_cache/context）。
  resume 从日志派生，并给中断的半截 turn 补合成收尾
  （assistant 有 tool_calls 没结果，provider 会直接 400）。

两条**可执行**的不变量（不是口号）：

| 不变量 | 做什么 | 事件 |
|--------|--------|------|
| 前缀稳定 | 本次请求必须是上次的**追加延长**，除非有记录原因（压缩） | `llm.request.{appendOnly,divergedAt,shapeReason}` |
| 重建一致 | 每轮用日志独立投影一遍，与内存里的请求逐条比对 | `llm.request.rebuildOK` |

> 重建不变量落地第一天就抓到一个真 bug：新增 system 行时只写了日志、没加进内存数组，
> **第一轮请求里根本没有系统提示**。

**上下文窗口是硬预算，不是展示数字**：一次会话能装多少 token（系统提示 + 全部历史 + 本轮输出），
由设置页给出（`llm.contextWindow`），全 harness 只有这一个数。到 `窗口 × 0.75` 压缩一次，
压完仍放不下就**拒绝发送**并说明原因。早先另有一个拍出来的 20 万字节预算
（≈6.6 万 token，32k 窗口时代的数字），在 262k 窗口上等于用了四分之一就开始压缩——
那正是"压缩丢资料 → 模型重查 → 又压缩"循环的病根。

**跨会话回忆不靠重建**：`search_sessions` / `read_session` 两个只读工具让模型主动去搜早先会话；
读到的内容一律当**不可信背景**，绝不当作指令。

### 4.9 提示词 = 分段组装（2026-09-11）

系统提示不是一坨字符串，是 `section{name, order, text}` 的有序集合（`run/prompt.go`）：

- `name` 是唯一键，也是**替换槽位**（同名即覆盖）；
- `order` 取自中心表，编号稀疏，插新段不必重排；
- `text` 是函数，拿得到当前运行时状态，**工具不在场就返回空串**——
  段自动跟着工具面走，`answer` 意图读不到写工具的规矩。

三条内容纪律（来自 dsh 与 Codex 两份真实提示词的共识）：

1. **一个事实只有一个 owner**：工具怎么用 → 工具的 `description`（`ToolSchemas()`）；
   跨工具的习惯 → 提示词的段；身份 → `identity` 段。写两处必然漂移。
2. **每条规则配边界**：不说"用 edit"，说"用 edit 改一句，别为一句重写整章"；
   不只说何时用它，也说何时用它不对。
3. **数字与状态从代码注入**，不手抄：`contextWindow` / 检索预算 / 压缩比例 / 管理员显示名。

稳定文本用 **golden 快照**钉住（`internal/run/testdata/system_prompt_*.golden.md`）：
改了提示词，快照就红；有意改动用 `UPDATE_GOLDEN=1 go test ./internal/run/` 重新生成。

---

### 4.10 迁移：按 Responses 协议开发，SSE 直出原生事件（2026-09-11 拍板）

**决定**：前端全用 Semi 原生渲染，不再自造内容形状；后端 SSE 直出
OpenAI Responses 流事件。协议层只实现 Responses 一种，扩展点留在
`llm.Config.Protocol` 与 `wikiatlas.*` 这条旁路上。

**为什么这条路成立**（都是查过源码/文档的事实，不是推测）：

1. Semi 的 `builtins` 只认四种内容项：`message` / `reasoning` / `function_call` /
   `custom_tool_call`——**与 Responses 规范一一对应**，而我们的工单只用得到前三种。
2. `AIChatDialogue` 导出 `streamingResponseToMessage(chunks, prevState)`：
   它就是 **Responses 流事件的标准归约器**，按 `sequence_number` 处理、自带乱序缓冲
   与丢块容错（gap > 10 跳过）。后端只要吐规范事件，前端不需要任何自己的投影。
3. 原生 `ReasoningWidget`（折叠 + 「思考中/已深度思考」+ markdown + 键盘可达）与
   `DialogueStepWidget`（每条 step 可折叠 + 时间线 + 状态图标）已经比我们自绘的强。
   唯一是空壳的是 `ToolCallWidget`（只有 `<IconWrench/> 名字 原始参数`），
   但 `function_call` 的自定义渲染是原生扩展点，一张工具卡按工具名换图标与措辞即可。

**要删的**（自造层）：`lib/runProjection.ts` 的时间线与对话投影、`PlanSteps`、
`dialogueChrome`、自定义内容类型 `plan` / `tasks`。

**要留的**（Responses 规范里没有、但产品需要的）：任务清单（todo 面板）、
用量与缓存命中环、护栏提醒、会话/工单元信息。它们走**独立的 `wikiatlas.*` 事件命名空间**，
与 Responses 事件同流不同类——这就是"留后手"的位置：以后换协议只动这一层映射。

**后端**：`llm` 层只留 Responses 一种协议（`internal/llm/responses.go`）。
执行器把 loop 产出**统一映射成 Responses 流事件**再发 SSE——协议差异全部收在
`llm` 层，上层与前端只见 Responses 一种形状。`Config.Protocol` 字段保留，
那是"以后要加新协议"的位置：加协议 = 加一个常量 + 一个客户端 +
`NewClient` 里一个 case，认不出的协议**明确报错**、不静默退回默认。

**已落地（2026-09-11）**

- 执行器的事件全部由 `internal/run/responses.go` 的 `respEmitter` 发出：
  模型/工具的产出 → 规范的输出项与增量事件；宿主自己要说的话 → `wikiatlas.*`。
- 输出项**自足**：`response.output_item.done` 带最终形态（完整文本 / 完整参数 /
  工具结果，结果摘要挂在项的 `wikiatlas` 键下），`response.completed` 再带整份
  response。所以历史回放丢掉 `*.delta` 也不缺信息，只缺"打字过程"。
- `sequence_number` 只加在 `response.*` 上（store 落库时统一发号，从 0 连续 +1）；
  `wikiatlas.*` 不占号。两条都是前端的归约器按序号增量推进所必需的，见 §6.2。
- 前端整层删掉自造投影，`response.*` 交给 Semi 的 `streamingResponseToMessage`；
  `wikiatlas.*` 归宿主部件（任务面板 / 用量环 / 提醒条）。

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

**角色名**：界面上一律叫 **Altas**（面板标题、顶栏入口、工单页、修订提示）；
「馆员」只作为内部/文档里的角色描述词，不再出现在 UI 文案里。

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

两条线**同流不同类**：`response.*` 是 Responses 规范的流事件，`wikiatlas.*` 是
规范里没有、产品需要的东西。类型的唯一 owner 是后端 `internal/run/responses.go`；
前端在 `lib/responses.ts` 里镜像同一份清单（EventSource 只派发显式订阅的命名事件）。

**Responses 流事件**（前端的对话由它们归约而来）

| type | payload 要点 |
|------|----------------|
| `response.created` / `response.in_progress` | response{id,model,status,created_at}（续跑发 in_progress） |
| `response.output_item.added` / `.done` | output_index, item{type: message\|reasoning\|function_call, …} |
| `response.output_text.delta` / `.done` | output_index, content_index, delta / text |
| `response.reasoning_summary_text.delta` / `.done` | output_index, summary_index, delta / text |
| `response.function_call_arguments.delta` / `.done` | output_index, delta / name, arguments |
| `response.completed` | response{…, output[], output_text, usage}（**整份响应**，前端快路径用它） |
| `response.failed` | response{status:failed, error{code,message}} |

两条硬约束（前端的归约器按 `sequence_number` 增量推进，破了就丢块）：

1. `sequence_number` **只加在 `response.*` 上**，从 0 起、跨本 run 的所有轮次连续 +1，
   由 store 落库时统一发号（执行器不自己数——重启/续跑会重号）。
2. `wikiatlas.*` **不占号**（没有 `sequence_number` 字段）。它们一吃号，归约器看到的
   序号就到处是洞，一路丢块到 gap > 10 才恢复。

**wikiatlas.\* 旁路**（规范里没有的东西，前端交给宿主部件渲染，不进对话）

| type | payload 要点 |
|------|----------------|
| `wikiatlas.todo` | tasks[]: {id,content,activeForm,status} → 输入框上的任务面板 |
| `wikiatlas.usage` | step, promptTokens, completionTokens, totalTokens, cacheReadTokens, cacheWriteTokens, contextWindow → 用量环 + 缓存命中 |
| `wikiatlas.notice` | text, tone(info\|warn\|error) → 提醒条（宿主播报 + 护栏提醒） |
| `wikiatlas.meta` | runId, sessionId, goal, intent, model, workId, docId, docMode, startedAt |
| `wikiatlas.request` | step, messages, appendOnly, divergedAt, shapeReason, rebuildOK（见 4.8 的两条不变量） |
| `wikiatlas.context` | keepRecent, reason, contextWindow, usedTokens（上下文压缩） |
| `wikiatlas.tree` | works[] 变更摘要 |
| `wikiatlas.content.staging` | targetType, targetId, previewMd?（正文写入中） |
| `wikiatlas.content.committed` | targetType, targetId, version（写入回执） |

前端消费规则见 5.4：`response.*` 整条交给 Semi 的 `streamingResponseToMessage`，
`wikiatlas.*` 归宿主部件；两边都**不 dump payload**。
约定：类型以 `.delta` 结尾的一律是"只走实时流、不进历史回放"的增量（见 store/runs.go）。


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

- skill 目录：`skills/wiki-writing/`（保持 Git 管理，仓库内）。  
- Harness 启动扫描 frontmatter：`name / description / version / path`。  
- `create_wiki` 固定加载：`SKILL.md` → `core.md` → 一个 `media-*.md`；目标是 series/universe 节点时再补 `readme.md`（系列主文/合集写法）。  
- `write_doc`（资料）不强制 9 章，只加载通用写作边界（事实/待核实/剧透策略）。  
- **运行时默认不注入 examples/**；examples 仅离线评测。  
- `read_skill` 是真实工具；未选中前不把整包 skill 塞进上下文。  
- 内容规则（骨架/题记/字数/参考资料）以 skill 为唯一权威；系统提示只留宿主流程与纪律，不复述细则。

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
