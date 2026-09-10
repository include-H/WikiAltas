#!/bin/bash
set -e
export PATH=/root/.nvm/versions/node/v24.20.0/bin:/usr/local/bin:/usr/bin:/bin
cd /root/WikiAltas/frontend
if [ ! -d node_modules ]; then
  npm install
fi
pkill -f "wikiatlas-ui" 2>/dev/null || true
pkill -f "vite" 2>/dev/null || true
sleep 1
# setsid so the dev server survives when this SSH/WSL wrapper exits
setsid npm run dev >/tmp/wikiatlas-frontend.log 2>&1 < /dev/null &
echo "started pid $!"
sleep 3
echo "--- log ---"
tail -25 /tmp/wikiatlas-frontend.log
echo "--- listen ---"
ss -ltnp | grep 5173 || true
echo "--- health ---"
curl -s -m 5 http://127.0.0.1:5173/ -o /dev/null -w "http:%{http_code}\n" || echo "not ready"
curl -s -m 5 http://127.0.0.1:5173/api/health || echo "proxy not ready"
echo
