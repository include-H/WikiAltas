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

### 环境变量

**配置项一律在设置页里改**（存在 SQLite，不读 env）。进程级只认一个：

```
WIKIATLAS_ADDR=0.0.0.0:8080   # 监听地址，供 Docker/一次性启动用（默认 127.0.0.1:8080）
```

## 访问与隐私（软门槛）

- **访问密码**：新库出厂密码 **1234**（`/login` 输它即可进管理态，设置页「访问与隐私」里改）。
  改密码会立即让旧会话失效。
- **访客**：不登录**绝对看不到非公开内容**——只能只读浏览公开文档（树 / 详情 / 资料 / 检索），
  私有节点一律 404 或不出现，其余接口 401。
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

## 部署（Docker，单容器）

镜像把前端产物嵌进后端二进制，一个容器一个端口（8080），数据卷挂 `/app/data`（SQLite 库、
媒体库清单、会话与工单都在里面）：

```bash
docker run -d --name wikialtas -p 8080:8080 \
  -v /mnt/Docker/WikiAltas/data:/app/data \
  hao0114/wikialtas:latest
# 或 docker compose up -d（见 docker-compose.yml，DATA_DIR 可改数据目录）
```

- 首次打开用出厂访问密码 `1234` 进管理态，到设置页改掉。
- 写作 skill（9 章骨架/题记/事实边界）随镜像发布在 `/app/skills/wiki-writing`，
  启动参数已指好；要自定义就挂载覆盖或改设置页的参数。
- 打 tag（`v*`）即自动发布：GitHub Release（linux-amd64 二进制 + skills 的 tar/zip）
  + Docker Hub `hao0114/wikialtas`（精确版本 / major.minor / latest）。
