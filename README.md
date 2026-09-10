# WikiAltas v2

飞书式文档壳 + 作品宇宙树 + LLM 馆员（严格按 `skills/wiki-writing` 写作）。

设计冻结见 [DESIGN_V2.md](./DESIGN_V2.md)。

## 目录

```
backend/    Go + SQLite API + Run harness
frontend/   React + Semi UI（飞书布局）
skills/     wiki-writing skill（Git 管理）
shared/     前后端共享类型
data/       SQLite（运行时生成）
```

## 快速开始

```bash
# 后端（seed + skill）
wsl -d Ubuntu -- bash /root/WikiAltas/scripts/start-backend.sh

# 前端
wsl -d Ubuntu -- bash /root/WikiAltas/scripts/start-frontend.sh

# 打开 http://localhost:5173
```

手动：

```bash
cd backend
go test ./...
go run ./cmd/wikiatlas -db ./data/wikiatlas.db -addr :8080 -seed \
  -skill /root/WikiAltas/skills/wiki-writing

cd frontend && npm run dev
```

### 环境变量（LLM / Exa，可选）

```
WIKIATLAS_LLM_BASE_URL=
WIKIATLAS_LLM_MODEL=
WIKIATLAS_LLM_API_KEY=
WIKIATLAS_EXA_API_KEY=
```

也可在前端 Settings 里配置。

## 核心概念

| 概念 | 说明 |
|------|------|
| works | 宇宙/系列/单作树节点，`content_md` 为唯一正文 |
| docs | 资料，挂在 works 资料夹下 |
| revisions | 写完即 commit，可回滚，无审批 |
| runs | 可续馆员工单 + checkpoint + tool_cache |
| library_links | Emby / Komga / GameAtlas 外链 |

## 验收

见 DESIGN_V2.md §12。
