#!/bin/bash
BASE=http://127.0.0.1:18080
echo "=== create work ==="
RESP=$(curl -s -X POST "$BASE/api/works" -H "Content-Type: application/json" -d '{"kind":"work","title":"SmokeTest"}')
echo "$RESP"
WID=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
echo "WID=$WID"
echo "=== put content v1 ==="
curl -s -X PUT "$BASE/api/works/$WID/content" -H "Content-Type: application/json" -d '{"contentMd":"# hello world","author":"human","summary":"first"}'
echo
echo "=== put content conflict ==="
curl -s -o /tmp/wa-smoke/conflict.json -w "HTTP %{http_code}\n" -X PUT "$BASE/api/works/$WID/content" -H "Content-Type: application/json" -d '{"contentMd":"# nope","author":"llm","expectedVersion":99}'
cat /tmp/wa-smoke/conflict.json
echo
echo "=== create run ==="
RUN=$(curl -s -X POST "$BASE/api/runs" -H "Content-Type: application/json" -d "{\"intent\":\"create_wiki\",\"goal\":\"为SmokeTest写Wiki\",\"context\":{\"workId\":\"$WID\"}}")
echo "$RUN"
sleep 1
echo "=== run status ==="
RID=$(echo "$RUN" | python3 -c "import sys,json; print(json.load(sys.stdin)['runId'])")
curl -s "$BASE/api/runs/$RID" | python3 -c "import sys,json; d=json.load(sys.stdin); print('status=', d['run']['status'], 'events=', len(d['events']))"
echo "=== work after run ==="
curl -s "$BASE/api/works/$WID" | python3 -c "import sys,json; d=json.load(sys.stdin); w=d['work']; print('ver=', w['contentVer'], 'hasContent=', bool(w.get('contentMd')))"
echo "=== search ==="
curl -s "$BASE/api/search?q=hello"
echo
echo "SMOKE_DONE"
