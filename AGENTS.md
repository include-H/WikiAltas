# WikiAltas 工程约束

## 后端

- 分层：`cmd → httpapi → domain → store`；store 不 import run/llm
- 正文唯一入口：`PUT content`（人和 LLM 共用）
- SQL 全参数化；错误上抛，禁止 `_ = err`
- 无 Proposal 表；无 Entity/Fact 真相表
- 关系 type 枚举写死在代码
- ID：UUIDv7

## 前端

- Semi UI 组件优先；禁止 hex 色；禁止反向覆盖 `.semi-*`
- 视觉对齐 `VISUAL_SPEC.md`（飞书母本，AI 助手=右侧可收起侧栏，见 §1.5）；与 DESIGN_V2 冲突以 VISUAL_SPEC 为准
- 默认编辑态；AI 面板可收起
- Run 事件分两条线消费：`response.*` 整条交给 Semi 的流式归约器（不自己造投影），
  `wikiatlas.*` 归宿主部件（任务面板 / 用量环 / 提醒条）；两边都不 dump payload
- `src/views` `src/components/**` `src/lib`；单文件建议 ≤300 行

## 验证

```bash
# 后端
wsl -d Ubuntu -- bash -lc 'export PATH=/usr/local/go/bin:$PATH; export GOCACHE=/root/.cache/wikiatlas-go-build; cd /root/WikiAltas/backend && go test ./...'

# 前端
wsl -d Ubuntu -- bash -lc 'export PATH=/root/.nvm/versions/node/v24.20.0/bin:$PATH; cd /root/WikiAltas/frontend && npm run build'
```

## Skill

- 运行时读 `skills/wiki-writing/`，不注入 examples/
- create_wiki 加载 SKILL.md → core.md → 一个 media-*.md
- 目标是 series/universe 节点时，额外注入 readme.md（系列主文/合集写法）
- 内容规则（骨架、题记、字数、参考资料）以 skill 为唯一权威；系统提示只留宿主/流程，不复述细则
- **维护纪律**：看到新毛病，先问「现有哪条原则已经覆盖它？」
  - 已被覆盖 → 不是 skill 的问题，别加规则（去查提示/注入/模型行为）；
  - 没被覆盖 → **加原则（讲道理），不加个例（列情况）**；
  - 个例证据（逐篇判决、已知偏差、语域补丁）只进 `examples/`，不进运行时规则。

## 交接须知（新会话 / 小模型先读：上面各节之外的雷都在这）

### 操作纪律
- 重启后端前先查有没有在跑的工单（重启会杀掉 run）：读 `backend/data/wikiatlas.db` 的 runs 表，有 running 就等。
- 跑 `scripts/start-backend.sh` 时，**命令文本里别出现它 pkill 的目标名**（pkill -f 会自伤，exit 144）。
- `.secrets/credentials.md` 与 settings 库里的任何 key：只读来用，**永不打印 / 永不提交 / 永不写进代码与文档**。
- 推送 / 打 tag 前都先问用户。双远端：origin=Gitea；GitHub=`include-H/WikiAltas`
  （推送要走代理 `192.168.1.253:7890` + gh 一次性凭据，别改 git 配置）。
  tag `v*` → 自动 release + Docker Hub `hao0114/wikialtas`。
- 有并行会话在跑（GA / GA 前端线）：跨会话用 SendMessage；任务与 GA 无关就不碰 `/root/gamemanager`。

### LLM 使用
- 思考档位合法值只有 `none/minimal/low/medium/high/xhigh/max`——**没有 "off"**（配置入口已把旧值归一到 none）。
- 机械判定（媒体库扫描 / 层级判定 / 题名变体 / 相关性判官）用 `runs.ActiveClientFor("none")` 直出；
  写作 / 问答工单用默认档——别把无思考档拿去做需要推理的活。
- `ActiveClient()` 没配 Key 会退回 mock/echo：任何直接调模型的端点必须先用 `store.HasAPIKey()` 守卫。
- 模型偶发空答 / 畸形输出（真见过判官返回 `{"links":[]}`）：关键路径要有重试兜底 + 日志，别静默成"没找到"。

### 视觉
- `VISUAL_SPEC.md` 是视觉宪法（与 DESIGN_V2 冲突以它为准）；颜色全走 Semi token、禁新写 hex、禁反向覆盖 `.semi-*`。
- **紫色（purple / violet / indigo）是 AI 专属强调色**，非 AI 元素一律不许用。
- 组件 API 先查 Semi MCP（semi-mcp）文档再写，**禁止凭记忆造 props**。

### 媒体库模块（改这块必读）
- **LLM 永不主动建库 / 挂链**：AI 只产出建议（KV `library_ai_suggestions`），用户点确认才执行；
  "写完之后"的关联由宿主钩子做确定性扫库（node-sweep）。扫描是低频确定性流程，稳态零 LLM。
- Komga 层级判定按「书单指纹 + 口径版本」缓存：**改了判定 prompt 必须把 `komgaJudgeVersion` 加一**，
  否则旧判定永远不会重判；判定失败退回启发式，不允许挡扫描。
- `library_ai_suggestions` 是共用 KV（sweep 产出 / 池子建议 / per-node 忽略共存）：保存时只替换自己那份，
  并持 `suggestionsMu` 锁。两套忽略别混：池子全局忽略（manifest）vs 节点页 per-node 忽略（Dismissed）。
- API 形状纪律：空列表一律给 `[]` 不给 `null`（前端 `.length` 直接崩）；KV 里多字段共存时，
  别写"空列表 = 没有记录"这类守卫（会吞掉同记录里的其他字段）。
- 真库联调：192.168.1.4（Emby 8081 / Komga 8082 / GameAtlas 3000）；UI 改动要起 dev server（5173）
  用 playwright 无头真跑再截图（playwright 在 `/root/.npm/_npx/` 下），别只看构建通过。
