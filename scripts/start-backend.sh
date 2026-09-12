#!/bin/bash
set -e
export PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin
export GOCACHE=/root/.cache/wikiatlas-go-build
# 配置一律在设置页（存 SQLite）；这里只认"监听地址"这类进程级选项，便于 Docker 一次性启动
ADDR="${WIKIATLAS_ADDR:-127.0.0.1:8080}"
pkill -f wikiatlas-v2 2>/dev/null || true
sleep 1
cd /root/WikiAltas/backend
go build -o /tmp/wikiatlas-v2 ./cmd/wikiatlas
mkdir -p /root/WikiAltas/backend/data
setsid /tmp/wikiatlas-v2 \
  -db /root/WikiAltas/backend/data/wikiatlas.db \
  -addr "$ADDR" \
  -seed \
  -skill /root/WikiAltas/skills/wiki-writing \
  >/tmp/wikiatlas-v2.log 2>&1 < /dev/null &
echo "started pid $!"
sleep 2
echo "--- log ---"
cat /tmp/wikiatlas-v2.log
echo "--- listen ---"
ss -ltnp | grep 8080 || true
echo "--- health ---"
curl -s -m 3 http://127.0.0.1:8080/api/health || echo "health failed"
echo
echo "--- tree nodes ---"
curl -s -m 3 http://127.0.0.1:8080/api/tree | head -c 400
echo
