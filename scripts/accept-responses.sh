#!/bin/bash
# Responses 迁移的验收跑批：把"7 条清单"变成一条命令。
#
# 只读被测代码、不改任何源码。--live 会额外拉一次真实工单的 SSE（会花一次模型调用）。
# 用法：
#   scripts/accept-responses.sh            # 静态 + 构建 + 测试
#   scripts/accept-responses.sh --live     # 再加：真实流形状 + Semi 归约器
set -uo pipefail
export PATH=/usr/local/go/bin:/root/.nvm/versions/node/v24.20.0/bin:/usr/local/bin:/usr/bin:/bin
export GOCACHE=/root/.cache/wikiatlas-go-build
ROOT=/root/WikiAltas
BASE=http://127.0.0.1:8080
LIVE=0
[ "${1:-}" = "--live" ] && LIVE=1

PASS=0; FAIL=0
ok()   { echo "  ✅ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ❌ $1"; FAIL=$((FAIL+1)); }
head_() { echo; echo "── $1"; }

# ── 1. 自造投影层是否连根拔起 ───────────────────────────────────────────────
head_ "1. 自造投影层残留（runProjection / PlanSteps / dialogueChrome）"
HIT=$(grep -rn 'runProjection\|PlanSteps\|dialogueChrome' "$ROOT/frontend/src" 2>/dev/null)
if [ -z "$HIT" ]; then ok "frontend/src 无残留引用"; else bad "仍有引用："; echo "$HIT" | sed 's/^/      /'; fi

# ── 2. 旧协议与 Claude 协议是否删净 ─────────────────────────────────────────
head_ "2. 旧协议 / Claude 协议残留"
HIT=$(grep -rn 'anthropic\|Anthropic\|chat/completions\|ChatCompletions' \
        "$ROOT/backend/internal" "$ROOT/frontend/src" 2>/dev/null \
      | grep -v '_test.go')
if [ -z "$HIT" ]; then ok "backend/frontend 源码里无 anthropic / chat-completions 引用"; else bad "仍有残留："; echo "$HIT" | sed 's/^/      /'; fi

# ── 3. 协议差异是否收在 llm 层 ──────────────────────────────────────────────
head_ "3. 协议差异只该收在 llm 层"
# 上层（run / httpapi）不应直接提到具体协议名或直接构造 http 请求到 provider
HIT=$(grep -rn '"responses"\|responses.Response\|/v1/messages\|/chat/completions' \
        "$ROOT/backend/internal/run" "$ROOT/backend/internal/httpapi" 2>/dev/null)
if [ -z "$HIT" ]; then ok "run/ 与 httpapi/ 不出现具体协议细节"; else bad "协议细节漏到上层："; echo "$HIT" | sed 's/^/      /'; fi

# ── 4. 会话日志层与两条不变量是否健在 ───────────────────────────────────────
head_ "4. 会话日志层与不变量（sessionlog / requestShape / rebuildOK）"
for f in sessionlog.go; do
  [ -f "$ROOT/backend/internal/run/$f" ] && ok "存在 run/$f" || bad "缺 run/$f"
done
HIT=$(grep -c 'rebuildOK\|requestShape' "$ROOT/backend/internal/run/executor.go" 2>/dev/null || echo 0)
[ "$HIT" -gt 0 ] && ok "executor 仍带 rebuildOK / requestShape 检查（$HIT 处）" || bad "executor 丢了不变量检查"

# ── 5. 后端：vet + 竞态测试 ─────────────────────────────────────────────────
head_ "5. backend: go vet ./... && go test -race ./..."
( cd "$ROOT/backend" && go vet ./... ) && ok "go vet 通过" || bad "go vet 失败"
( cd "$ROOT/backend" && go test -race ./... >/tmp/accept-go-test.log 2>&1 ) \
  && ok "go test -race 通过" || { bad "go test -race 失败（尾巴：）"; tail -25 /tmp/accept-go-test.log | sed 's/^/      /'; }

# ── 6. 前端：类型检查 + 构建 ────────────────────────────────────────────────
head_ "6. frontend: tsc -b && npm run build"
( cd "$ROOT/frontend" && npx tsc -b ) && ok "tsc -b 通过" || bad "tsc -b 失败"
( cd "$ROOT/frontend" && npm run build >/tmp/accept-fe-build.log 2>&1 ) \
  && ok "npm run build 通过" || { bad "build 失败（尾巴：）"; tail -25 /tmp/accept-fe-build.log | sed 's/^/      /'; }

# ── 7. 真实工单：SSE 形状 + Semi 归约器 ─────────────────────────────────────
head_ "7. 真实工单（:8080）"
if [ "$LIVE" = "1" ]; then
  if curl -s -m 3 "$BASE/api/health" >/dev/null 2>&1; then
    dump=/tmp/accept-stream.jsonl
    if python3 "$ROOT/scripts/verify-sse.py" --timeout 180 --dump "$dump" >/tmp/accept-sse.log 2>&1; then
      ok "SSE 形状通过（明细见 /tmp/accept-sse.log）"
    else
      bad "SSE 形状未通过（尾巴：）"; tail -20 /tmp/accept-sse.log | sed 's/^/      /'
    fi
    if node "$ROOT/scripts/reduce-stream.cjs" "$dump" >/tmp/accept-reduce.log 2>&1; then
      ok "Semi 归约器能折出 message（明细见 /tmp/accept-reduce.log）"
    else
      bad "归约器折不出（尾巴：）"; tail -20 /tmp/accept-reduce.log | sed 's/^/      /'
    fi
  else
    bad ":$BASE 未运行（先 scripts/start-backend.sh）"
  fi
else
  echo "  · 跳过（加 --live 才会拉真实流）"
fi

echo
echo "════ 通过 $PASS 项，失败 $FAIL 项 ════"
exit $([ "$FAIL" -eq 0 ] && echo 0 || echo 1)
