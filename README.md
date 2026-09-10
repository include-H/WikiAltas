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

## 访问与隐私（软门槛）

- **访问密码**：设置页「访问与隐私」里设一次；`/login` 输密码进管理态。
  它只是一道**防止不该公开的内容（比如以后的 Gal 条目）被顺路看到**的软门槛，**不是安全边界**。
- **访客**：不登录只能**只读浏览公开文档**（树 / 详情 / 资料 / 检索），其余接口一律 401。
- **节点可见性**：每个宇宙/系列/单作/资料夹都有 `public | private`，**默认 private**；
  访客可见 = 自身与所有祖先都是 public。树上 ⋯ 菜单或 🔒 图标可切换。

## 核心概念

| 概念 | 说明 |
|------|------|
| works | 宇宙/系列/单作树节点，`content_md` 为唯一正文 |
| docs | 资料，挂在 works 资料夹下 |
| visibility | 节点的公开/私有（继承判定），访客只读的边界 |
| revisions | 写完即 commit，可回滚，无审批 |
| runs | 可续馆员工单 + checkpoint + tool_cache |
| library_links | Emby / Komga / GameAtlas 外链 |

## 验收

见 DESIGN_V2.md §12。
