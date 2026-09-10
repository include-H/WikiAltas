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
- 视觉对齐 `VISUAL_SPEC.md`（飞书母本，悬浮 AI 面板）；与 DESIGN_V2 冲突以 VISUAL_SPEC 为准
- 默认编辑态；AI 面板可收起
- Run 事件只做折叠叙事投影，不 dump JSON
- `src/views` `src/components/**` `src/lib`；单文件建议 ≤300 行

## 验证

```bash
# 后端
wsl -d Ubuntu -- bash -lc 'export PATH=/usr/local/go/bin:$PATH; export GOCACHE=/tmp/wikiatlas-go-cache; cd /root/WikiAltas/backend && go test ./...'

# 前端
wsl -d Ubuntu -- bash -lc 'export PATH=/root/.nvm/versions/node/v24.20.0/bin:$PATH; cd /root/WikiAltas/frontend && npm run build'
```

## Skill

- 运行时读 `skills/wiki-writing/`，不注入 examples/
- create_wiki 加载 SKILL.md → core.md → 一个 media-*.md
