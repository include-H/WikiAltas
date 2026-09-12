package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"wikiatlas/backend/web"
)

// --- 前端静态产物（单容器部署时后端自己托管）---
//
// 构建时把 frontend/dist 拷进 backend/web/dist 并 go:embed（Dockerfile / release.yml 都按这个顺序）。
// 本地开发态（没跑过拷贝）里没有产物：非 /api 的请求 404，前端照旧走 vite，互不影响。

// handleWebUI：静态文件照发；其余路径都回 index.html 交给前端路由（SPA 回退）。
func (s *Server) handleWebUI(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		http.NotFound(w, r) // 还没构建过产物（本地 go run 常见）
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p != "" {
		if f, err := sub.Open(p); err == nil {
			_ = f.Close()
			// 构建产物带内容哈希：可以长缓存。其余（favicon 等）交给默认值。
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.FileServer(http.FS(sub)).ServeHTTP(w, r)
			return
		}
	}
	// SPA 回退：/login、/w/:id、/library… 这些客户端路由都从 index.html 进来
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}
