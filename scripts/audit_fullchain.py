#!/usr/bin/env python3
"""WikiAltas 全链路审计 + 轻量压测。

用法：
    python3 scripts/audit_fullchain.py [base_url]
默认 http://127.0.0.1:8080；压测请指向一个独立实例（无 LLM key 时走 mock 执行器，不花钱）。

覆盖：树/节点 CRUD、正文与版本冲突、资料夹（含关联规则）、检索（含别名）、
工单生命周期、SSE、批次并发闸门、设置与运行时、访客只读与节点可见性，
以及一批性能采样。

会话：脚本自己引导管理员密码并登录（新库出厂密码 1234）；访客用例走匿名 opener。
已设密码的实例请用 WA_AUDIT_PASSWORD 传入口令。
"""

import http.cookiejar
import json
import os
import statistics
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8080").rstrip("/")
# 出厂密码 1234；本地这台机器我压测时改成了 audit-pass-123，两个都试。
PASSWORD_CANDIDATES = [
    p for p in (os.environ.get("WA_AUDIT_PASSWORD"), "1234", "audit-pass-123") if p
]
AUDIT_PASSWORD = PASSWORD_CANDIDATES[0]

# 已登录会话（管理员）
AUTH_JAR = http.cookiejar.CookieJar()
AUTH_OPENER = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(AUTH_JAR))
# 访客：不带任何 Cookie
GUEST_OPENER = urllib.request.build_opener()

RESULTS: list[tuple[str, bool, str]] = []


def check(name: str, ok: bool, detail: str = "") -> bool:
    RESULTS.append((name, ok, detail))
    print(f"{'✅' if ok else '❌'} {name}{(' — ' + detail) if detail else ''}", flush=True)
    return ok


def call(
    method: str,
    path: str,
    body=None,
    timeout: int = 120,
    raw: bool = False,
    opener=None,
):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        BASE + path, data=data, headers={"Content-Type": "application/json"}, method=method
    )
    try:
        with (opener or AUTH_OPENER).open(req, timeout=timeout) as r:
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


def call_guest(method: str, path: str, body=None, timeout: int = 30, raw: bool = False):
    """匿名请求：模拟未登录访客（不带会话 Cookie）。"""
    return call(method, path, body, timeout=timeout, raw=raw, opener=GUEST_OPENER)


def q(value: str) -> str:
    return urllib.parse.quote(value)


# ---------------------------------------------------------------- 0. 会话
print("\n=== 0. 会话（访问密码 / 硬门槛）===")
code, _ = call_guest("PUT", "/api/settings", {"admin": {"newPassword": AUDIT_PASSWORD}})
if code == 200:
    check("首次运行：初始化访问密码", True, "库里原本没有密码")
elif code == 401:
    print("   库里已有密码（新库出厂密码 = 1234），按候选口令登录")
else:
    check("初始化访问密码", False, f"HTTP {code}")

code, bad = call_guest("POST", "/api/auth/login", {"password": AUDIT_PASSWORD + "-wrong"})
check("错误口令被拒（401 + 剩余次数）", code == 401 and "remainingAttempts" in (bad or {}), f"HTTP {code}")

# 登录必须走 AUTH_OPENER，Cookie 才会进 AUTH_JAR
code, ok = 0, None
for candidate in PASSWORD_CANDIDATES:
    code, ok = call("POST", "/api/auth/login", {"password": candidate})
    if code == 200:
        AUDIT_PASSWORD = candidate
        break
check("访问密码登录（不带用户名）", code == 200 and ok.get("ok") is True,
      f"HTTP {code} · 口令 {AUDIT_PASSWORD}")
check("登录下发 HttpOnly 会话 Cookie",
      any(c.name == "wa_session" for c in AUTH_JAR), f"cookies={[c.name for c in AUTH_JAR]}")
if code != 200:
    print("  ❌ 无法登录：换空的库，或用 WA_AUDIT_PASSWORD 传正确口令")
    sys.exit(2)

code, me = call("GET", "/api/auth/me")
check("会话可用（me=admin）", code == 200 and me.get("authed") and me.get("role") == "admin")
code, guest_me = call_guest("GET", "/api/auth/me")
check("匿名 me=guest", code == 200 and guest_me.get("role") == "guest")

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
    with AUTH_OPENER.open(req, timeout=3) as resp:
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

# ---------------------------------------------------------------- 8. 访客与可见性
print("\n=== 8. 访客只读 / 节点可见性（硬门槛：未登录看不到私有）===")
ts = int(time.time())
token = f"Zorblax{ts}"  # 用 ASCII 唯一词：中文+数字在 FTS 里会粘成一个 token
code, pu = call("POST", "/api/works", {"kind": "universe", "title": f"访客审计宇宙{ts}"})
check("新节点默认 private", code == 201 and pu.get("visibility") == "private", f"visibility={pu.get('visibility')}")
pu_id = pu["id"]
code, pw = call("POST", "/api/works", {"kind": "work", "title": f"访客审计单作{ts}", "parentId": pu_id, "medium": "game"})
pw_id = pw["id"]
call("PUT", f"/api/works/{pw_id}/content", {"contentMd": f"# 私密样本{ts}\n\n{token} 不该被访客看到。\n", "author": "human"})
# 公开但挂在私密祖先下：继承判定应当仍然藏起来
_, pubw = call("POST", "/api/works", {"kind": "work", "title": f"公开审计单作{ts}", "parentId": sid, "medium": "game", "visibility": "public"})
pubw_id = pubw["id"]
call("PUT", f"/api/works/{pubw_id}/content", {"contentMd": f"# 公开样本{ts}\n\n{token} 谁都能看。\n", "author": "human"})
# 全公开链路：宇宙 + 单作都 public
_, pgu = call("POST", "/api/works", {"kind": "universe", "title": f"公开审计宇宙{ts}", "visibility": "public"})
pgu_id = pgu["id"]
_, pgw = call("POST", "/api/works", {"kind": "work", "title": f"公链审计单作{ts}", "parentId": pgu_id, "medium": "game", "visibility": "public"})
pgw_id = pgw["id"]
call("PUT", f"/api/works/{pgw_id}/content", {"contentMd": f"# 公链样本{ts}\n\n{token} 公开链路内容。\n", "author": "human"})

code, gtree = call_guest("GET", "/api/tree")
guest_ids = {n["id"] for n in (gtree or {}).get("nodes", [])}
check("访客树过滤私密节点", code == 200 and pu_id not in guest_ids and pw_id not in guest_ids)
check("公开节点挂在私密祖先下 → 访客仍看不到", pubw_id not in guest_ids)
check("全公开链路 → 访客可见", pgu_id in guest_ids and pgw_id in guest_ids)
code, atree = call("GET", "/api/tree")
admin_ids = {n["id"] for n in atree.get("nodes", [])}
check("管理员树能看到私密节点", pu_id in admin_ids and pw_id in admin_ids)

code, _ = call_guest("GET", f"/api/works/{pw_id}")
check("访客直取私密单作 → 404", code == 404, f"HTTP {code}")
code, _ = call_guest("GET", f"/api/works/{pw_id}/docs")
check("访客列私密单作资料 → 404", code == 404, f"HTTP {code}")
code, _ = call_guest("GET", f"/api/settings")
check("访客读设置 → 401", code == 401, f"HTTP {code}")
code, _ = call_guest("POST", "/api/works", {"kind": "work", "title": "访客偷建的节点"})
check("访客建节点 → 401", code == 401, f"HTTP {code}")
code, _ = call_guest("POST", "/api/runs", {"intent": "continue_wiki", "goal": "访客偷跑工单", "workspace": f"work:{wid}"})
check("访客创建工单 → 401", code == 401, f"HTTP {code}")
code, _ = call_guest("PUT", f"/api/works/{wid}/content", {"contentMd": "访客篡改", "author": "human"})
check("访客改正文 → 401", code == 401, f"HTTP {code}")

code, gsearch = call_guest("GET", f"/api/search?q={q(token)}")
gids = {h["id"] for h in (gsearch or {}).get("hits", [])}
check("访客检索只回全公开链路的节点",
      code == 200 and pgw_id in gids and pw_id not in gids and pubw_id not in gids,
      f"hits={sorted(gids)}")
_, asearch = call("GET", f"/api/search?q={q(token)}")
check("管理员检索能看到私密节点", pw_id in {h["id"] for h in asearch.get("hits", [])})

# 只公开祖先：子节点仍私密
call("PATCH", f"/api/works/{pu_id}", {"visibility": "public"})
code, gtree2 = call_guest("GET", "/api/tree")
gids2 = {n["id"] for n in (gtree2 or {}).get("nodes", [])}
check("祖先公开后访客可见宇宙", pu_id in gids2)
check("子节点仍私密（继承判定按祖先链）", pw_id not in gids2)
code, _ = call_guest("GET", f"/api/works/{pw_id}")
check("访客仍取不到私密子节点 → 404", code == 404, f"HTTP {code}")

# 全部公开：可读
call("PATCH", f"/api/works/{pw_id}", {"visibility": "public"})
code, gwork = call_guest("GET", f"/api/works/{pw_id}")
check("公开后可读正文", code == 200 and f"私密样本{ts}" in (gwork.get("work", {}).get("contentMd") or ""))
code, _ = call_guest("GET", f"/api/works/{pw_id}/docs")
check("公开后可列资料", code == 200, f"HTTP {code}")
code, _ = call_guest("DELETE", f"/api/works/{pw_id}")
check("访客删节点 → 401", code == 401, f"HTTP {code}")

# 收尾：管理员清理这批节点（先删子，再删宇宙）
call("DELETE", f"/api/works/{pw_id}")
call("DELETE", f"/api/works/{pu_id}")
call("DELETE", f"/api/works/{pubw_id}")
call("DELETE", f"/api/works/{pgw_id}")
call("DELETE", f"/api/works/{pgu_id}")
code, _ = call("GET", f"/api/works/{pu_id}")
check("清理完成（已删节点 404）", code == 404, f"HTTP {code}")

# ---------------------------------------------------------------- 汇总
print("\n=== 汇总 ===")
failed = [r for r in RESULTS if not r[1]]
print(f"通过 {len(RESULTS) - len(failed)}/{len(RESULTS)}")
for name, _, detail in failed:
    print(f"  ❌ {name} — {detail}")
sys.exit(1 if failed else 0)
