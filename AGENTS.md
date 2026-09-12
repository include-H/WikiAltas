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
