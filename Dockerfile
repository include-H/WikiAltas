# ---- 前端构建 ----
FROM node:22-alpine AS frontend-builder
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# ---- 后端构建（modernc sqlite 是纯 Go，无需 CGO）----
FROM golang:1.25-alpine AS backend-builder
WORKDIR /app/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
# 前端产物拷进嵌入位（go:embed all:dist），后端自己托管前端——单容器单端口
COPY --from=frontend-builder /app/frontend/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags '-s -w' -o /wikiatlas ./cmd/wikiatlas

# ---- 运行镜像 ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=backend-builder /wikiatlas .
# 写作 skill 是运行时内容（9 章骨架/题记/事实边界），随镜像发布
COPY skills /app/skills
EXPOSE 8080
VOLUME ["/app/data"]
CMD ["./wikiatlas", "-db", "/app/data/wikiatlas.db", "-addr", "0.0.0.0:8080", "-skill", "/app/skills/wiki-writing"]
