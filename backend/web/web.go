// Package web 把构建好的前端产物（frontend/dist）嵌进二进制：
// 单容器部署时后端自己托管前端（见 Dockerfile / release.yml 的顺序：先建前端，再拷进 dist 编译）。
//
// 本地开发没有产物也没关系——embed 里只有一个 .gitkeep 占位，
// 静态处理器碰到缺 index.html 会 404，前端照旧走 vite dev server。
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
