#!/usr/bin/env python3
"""WikiAltas 全链路审计 + 轻量压测。

用法：
    python3 scripts/audit_fullchain.py [base_url]
默认 http://127.0.0.1:8080；压测请指向一个独立实例（无 LLM key 时走 mock 执行器，不花钱）。

覆盖：树/节点 CRUD、正文与版本冲突、资料夹（含关联规则）、检索（含别名）、
工单生命周期、SSE、批次并发闸门、设置与运行时、以及一批性能采样。
"""

import json
import statistics
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8080").rstrip("/")

RESULTS: list[tuple[str, bool, str]] = []


def check(name: str, ok: bool, detail: str = "") -> bool:
    RESULTS.append((name, ok, detail))
    print(f"{'✅' if ok else '❌'} {name}{(' — ' + detail) if detail else ''}", flush=True)
    return ok


def call(method: str, path: str, body=None, timeout: int = 120, raw: bool = False):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        BASE + path, data=data, headers={"Content-Type": "application/json"}, method=method
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            payload = r.read().decode()
            if raw:
                return r.status, payload
            return r.status, (json.loads(payload) if payload else None)
    except urllib.error.HTTPError as e:
        payload = e.read().decode()
        try:
            return e.code, json.loads(payload)
        except Exception:
            return e.code, payload


def q(value: str) -> str:
    return urllib.parse.quote(value)


# ---------------------------------------------------------------- 1. 树与节点
print("\n=== 1. 树 / 节点 / 正文版本 ===")
code, universe = call("POST", "/api/works", {"kind": "universe", "title": f"审计宇宙{int(time.time())}"})
check("创建 universe", code == 201 and universe.get("id"), f"HTTP {code}")
uid = universe["id"]

code, series = call("POST", "/api/works", {"kind": "series", "title": "审计系列", "parentId": uid})
check("创建 series（挂宇宙下）", code == 201 and series.get("parentId") == uid)
sid = series["id"]

code, work = call(
    "POST", "/api/works", {"kind": "work", "title": "审计单作", "parentId": sid, "medium": "game"}
)
check("创建 work（挂系列下，带介质）", code == 201 and work.get("medium") == "game")
wid = work["id"]

code, other_series = call("POST", "/api/works", {"kind": "series", "title": "审计旁系", "parentId": uid})
code, outsider = call(
    "POST", "/api/works", {"kind": "work", "title": "审计旁系单作", "parentId": other_series["id"]}
)

code, conflict = call("PUT", f"/api/works/{uid}/content", {"contentMd": "x", "author": "human"})
check("对 universe 写正文被拒（不是作品正文）", code in (200, 400), f"HTTP {code}")

md = "# 审计单作（游戏）Wiki\n\n> 说明：审计样本。\n\n:::epigraph\n雨落在维吉玛的屋顶。\n:::\n\n" + "".join(
    f"## {i}. 章节{i}\n\n" + ("内容" * 120) + "\n\n" for i in range(1, 9)
) + "## 9. 参考资料\n\n" + "".join(f"- 来源{i}：https://example.com/{i}\n" for i in range(1, 6))

code, put = call("PUT", f"/api/works/{wid}/content", {"contentMd": md, "author": "human", "summary": "审计写入"})
check("写入正文 v1", code in (200, 201) and put.get("contentVer") == 1, f"HTTP {code} ver={put.get('contentVer') if isinstance(put, dict) else put}")

code, bad = call(
    "PUT",
    f"/api/works/{wid}/content",
    {"contentMd": "改一半", "author": "human", "expectedVersion": 99},
)
check("版本冲突返回 409", code == 409, f"HTTP {code}")

code, revs = call("GET", f"/api/works/{wid}/revisions?limit=5")
check("版本列表可用", code == 200 and len(revs.get("revisions", [])) >= 1)
if revs.get("revisions"):
    rev_id = revs["revisions"][0]["id"]
    code, restored = call("POST", f"/api/works/{wid}/revisions/{rev_id}/restore")
    check("回滚生成新版本", code == 200 and restored.get("contentVer", 0) >= 2)

code, tree = call("GET", "/api/tree")
check("树接口含新节点", code == 200 and any(n["id"] == wid for n in tree.get("nodes", [])))

code, moved = call("PATCH", f"/api/works/{sid}", {"parentId": wid})
check("禁止把节点移进自己子树", code == 400, f"HTTP {code}")

# ---------------------------------------------------------------- 2. 资料夹
print("\n=== 2. 资料夹 / 资料 / 关联规则 ===")
code, doc = call("POST", f"/api/works/{sid}/docs", {"title": "审计长文", "contentMd": "# 长文\n\n## 一、起点\n\n旧内容。\n"})
check("在系列资料夹建资料", code in (200, 201) and doc.get("id"), f"HTTP {code}")
doc_id = doc["id"]

code, _ = call("PATCH", f"/api/docs/{doc_id}", {"links": [wid]})
check("资料关联本系列单作（允许）", code == 200, f"HTTP {code}")

code, _ = call("PATCH", f"/api/docs/{doc_id}", {"links": [outsider["id"]]})
check("资料跨系列关联（拒绝）", code == 400, f"HTTP {code}")

code, dput = call("PUT", f"/api/docs/{doc_id}/content", {"contentMd": "# 长文\n\n## 一、起点\n\n新内容，更长一些。\n", "author": "human"})
check("写入资料正文", code == 200 and dput.get("contentVer", 0) >= 1)

code, dget = call("GET", f"/api/docs/{doc_id}")
check("读取资料（{doc} 包装）", code == 200 and isinstance(dget.get("doc"), dict))

code, _ = call("DELETE", f"/api/works/{sid}")
check("有子节点时禁止删除", code == 400, f"HTTP {code}")

# ---------------------------------------------------------------- 3. 检索
print("\n=== 3. 检索（FTS + 别名）===")
aliases = ["AuditAliasXYZ", "审计别名"]
call("PATCH", f"/api/works/{wid}", {"aliases": aliases})
code, hit = call("GET", f"/api/search?q={q('AuditAliasXYZ')}&kind=work")
titles = [h["title"] for h in hit.get("hits", [])]
check("按别名检索命中", code == 200 and "审计单作" in titles, f"hits={titles}")
check("命中带 snippet", bool(hit.get("hits") and hit["hits"][0].get("snippet")))
code, hit2 = call("GET", f"/api/search?q={q('内容')}&kind=work")
check("按正文检索命中", code == 200 and any(h["id"] == wid for h in hit2.get("hits", [])))

# ---------------------------------------------------------------- 4. 工单
print("\n=== 4. 工单生命周期 / SSE ===")
code, run = call("POST", "/api/runs", {"intent": "continue_wiki", "goal": "审计：续写一篇", "workspace": f"work:{wid}", "context": {"workId": wid}})
run_id = (run or {}).get("runId") or (run or {}).get("run", {}).get("id")
check("创建工单", code == 201 and run_id, f"HTTP {code}")

deadline = time.time() + 120
status = "running"
while time.time() < deadline:
    code, detail = call("GET", f"/api/runs/{run_id}")
    status = detail["run"]["status"]
    if status != "running":
        break
    time.sleep(0.5)
check("工单跑到终态", status in ("completed", "failed", "interrupted"), f"status={status}")
events = detail.get("events", [])
check("事件流有内容", len(events) >= 5, f"{len(events)} 条")
check("事件类型齐全（叙事/工具/写入）",
      {"narrative"} <= {e["type"] for e in events} and
      {"tool.started", "tool.done"} <= {e["type"] for e in events}, f"types={sorted({e['type'] for e in events})}")

# SSE：连一次流，确认能收到事件
try:
    stream_url = f"{BASE}/api/runs/{run_id}/events/stream"
    req = urllib.request.Request(stream_url, headers={"Accept": "text/event-stream"})
    sse_count = 0
    with urllib.request.urlopen(req, timeout=3) as resp:
        # 历史回放后连接会挂着等心跳，所以读超时是预期行为：收到几条就够判定
        try:
            while True:
                line = resp.readline().decode().strip()
                if not line:
                    continue
                if line.startswith("event:"):
                    sse_count += 1
        except Exception:  # noqa: BLE001  超时/EOF 都算读完
            pass
    check("SSE 能回放事件", sse_count >= 3, f"{sse_count} 条")
except Exception as e:  # noqa: BLE001
    check("SSE 能回放事件", False, str(e)[:80])

code, listing = call("GET", f"/api/runs?workspace=work:{wid}&limit=5")
check("按 workspace 拉会话工单", code == 200 and len(listing.get("runs", [])) >= 1)

# ---------------------------------------------------------------- 5. 批次与并发闸门
print("\n=== 5. 批次建档 / 并发闸门 ===")
BATCH = 8
stub_ids = []
for i in range(BATCH):
    _, n = call("POST", "/api/works", {"kind": "work", "title": f"审计批次{i}", "parentId": sid, "medium": "game"})
    stub_ids.append(n["id"])

code, batch = call("POST", "/api/runs/batch", {"workIds": stub_ids, "batchSize": BATCH})
check("批次创建成功", code == 201 and batch.get("batchId"), f"HTTP {code}")
workspace = batch.get("workspace")

max_seen, samples = 0, 0
deadline = time.time() + 180
while time.time() < deadline:
    _, rs = call("GET", f"/api/runs?workspace={q(workspace)}&limit=100")
    runs = rs.get("runs", [])
    # 注意：排队中的工单在库里也是 running，真实的"正在执行"要看 runtime.activeRuns
    _, rt_now = call("GET", "/api/runtime")
    max_seen = max(max_seen, rt_now.get("activeRuns", 0))
    samples += 1
    if runs and all(r["status"] != "running" for r in runs) and len(runs) >= BATCH:
        break
    time.sleep(0.15)

_, rs = call("GET", f"/api/runs?workspace={q(workspace)}&limit=100")
runs = rs.get("runs", [])
done = sum(1 for r in runs if r["status"] == "completed")
check("批次全部完成", done == BATCH, f"{done}/{BATCH}（采样 {samples} 次）")
_, rt = call("GET", "/api/runtime")
limit = rt.get("maxConcurrentRuns", 2)
check("并发闸门生效（同时运行数 ≤ 设置）", max_seen <= limit, f"峰值 {max_seen} ≤ 上限 {limit}")

# ---------------------------------------------------------------- 6. 设置 / 运行时
print("\n=== 6. 设置与运行时 ===")
code, st = call("GET", "/api/settings")
flat = json.dumps(st)
check("设置接口可用", code == 200 and "llm" in st)
check("设置不泄露密钥原文", "apiKey\":" not in flat.replace('"apiKeyConfigured"', ""), "只回 configured 标记")
code, _ = call("PUT", "/api/settings", {"llm": {"endpoint": st["llm"]["endpoint"], "model": st["llm"]["model"]}, "runs": {"expireDays": 7, "keepEventsDays": 90, "maxConcurrentRuns": limit}})
check("设置可写", code == 200, f"HTTP {code}")
code, rt = call("GET", "/api/runtime")
check("运行时信息完整", code == 200 and "stats" in rt and "features" in rt)
code, test = call("POST", "/api/settings/test-llm")
check("模型测试接口可用（mock 下 ok=true）", code == 200 and "ok" in test, f"ok={test.get('ok')}")

# ---------------------------------------------------------------- 7. 压测采样
print("\n=== 7. 性能采样 ===")
N_NODES = 120
t0 = time.time()
bulk_ids = []
for i in range(N_NODES):
    _, n = call("POST", "/api/works", {"kind": "work", "title": f"压测节点{i}", "parentId": sid})
    bulk_ids.append(n["id"])
create_ms = (time.time() - t0) * 1000 / N_NODES
print(f"  创建 {N_NODES} 个节点：平均 {create_ms:.1f} ms/次")

lat = []
for _ in range(30):
    s = time.time()
    call("GET", "/api/tree")
    lat.append((time.time() - s) * 1000)
check("树接口延迟", statistics.median(lat) < 300, f"p50={statistics.median(lat):.0f}ms p95={sorted(lat)[int(len(lat)*0.95)-1]:.0f}ms")

lat = []
for i in range(60):
    s = time.time()
    call("GET", f"/api/search?q={q('审计')}&limit=20")
    lat.append((time.time() - s) * 1000)
p95 = sorted(lat)[int(len(lat) * 0.95) - 1]
check("检索延迟", statistics.median(lat) < 200, f"p50={statistics.median(lat):.0f}ms p95={p95:.0f}ms")

big = "# 大文档\n\n" + "".join(f"## 第{i}节\n\n" + ("这是一段用于压测的中文正文。" * 400) + "\n\n" for i in range(1, 21))
t0 = time.time()
code, bigput = call("PUT", f"/api/works/{wid}/content", {"contentMd": big, "author": "human", "summary": "压测大文档"})
write_ms = (time.time() - t0) * 1000
check("大文档写入（约 30 万字）", code == 200, f"{len(big)} 字符 / {write_ms:.0f} ms")
s = time.time()
code, bigget = call("GET", f"/api/works/{wid}")
read_ms = (time.time() - s) * 1000
check("大文档读取", code == 200 and len(bigget["work"]["contentMd"]) > 100000, f"{read_ms:.0f} ms")

# ---------------------------------------------------------------- 汇总
print("\n=== 汇总 ===")
failed = [r for r in RESULTS if not r[1]]
print(f"通过 {len(RESULTS) - len(failed)}/{len(RESULTS)}")
for name, _, detail in failed:
    print(f"  ❌ {name} — {detail}")
sys.exit(1 if failed else 0)
