#!/usr/bin/env python3
import json
import urllib.request
import urllib.error

BASE = "http://127.0.0.1:8080"

def req(method, path, body=None):
    data = None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8")
    r = urllib.request.Request(BASE + path, data=data, method=method)
    if body is not None:
        r.add_header("Content-Type", "application/json; charset=utf-8")
    try:
        with urllib.request.urlopen(r, timeout=10) as resp:
            raw = resp.read().decode("utf-8")
            print(f"{method} {path} -> {resp.status}")
            if raw:
                print(raw[:1200])
            return json.loads(raw) if raw else None
    except urllib.error.HTTPError as e:
        print(f"{method} {path} -> {e.code}")
        print(e.read().decode("utf-8")[:800])
        return None

print("HEALTH")
req("GET", "/api/health")
print("\nTREE")
tree = req("GET", "/api/tree")
print("\nCREATE FF")
ff = req("POST", "/api/works", {"kind": "universe", "title": "最终幻想", "medium": "game"})
if ff:
    fid = ff["id"]
    print("\nCREATE SERIES")
    ser = req("POST", "/api/works", {"parentId": fid, "kind": "series", "title": "最终幻想VII", "medium": "game"})
    if ser:
        sid = ser["id"]
        print("\nCREATE WORK")
        w = req("POST", "/api/works", {"parentId": sid, "kind": "work", "title": "最终幻想VII Remake", "medium": "game"})
        if w:
            wid = w["id"]
            print("\nPUT CONTENT v1")
            md = "# 《最终幻想VII Remake》Wiki\n\n> 说明：联调样例。\n\n## 1. 作品概览\n\n测试正文。\n"
            req("PUT", f"/api/works/{wid}/content", {"contentMd": md, "author": "human", "summary": "联调写入"})
            print("\nPUT STALE -> expect 409")
            req("PUT", f"/api/works/{wid}/content", {"contentMd": md + "x", "author": "human", "expectedVersion": 0})
            print("\nRUN create_wiki")
            run = req("POST", "/api/runs", {"intent": "create_wiki", "goal": "撰写最终幻想VII Remake Wiki", "context": {"workId": wid, "medium": "game"}})
            if run:
                rid = run.get("id") or run.get("runId")
                print("run id", rid)
                import time
                time.sleep(2)
                req("GET", f"/api/runs/{rid}")
                req("GET", f"/api/works/{wid}")
print("\nSEARCH")
from urllib.parse import quote
req("GET", "/api/search?q=" + quote("最终幻想"))
print("\nLIBRARY")
req("GET", "/api/library/sources")
print("\nSETTINGS")
req("GET", "/api/settings")
print("\nDOCS")
tree = req("GET", "/api/tree") or {}
nodes = tree.get("nodes") or []
work = next((n for n in nodes if n.get("kind") == "work"), None)
if work:
    doc = req("POST", f"/api/works/{work['id']}/docs", {"title": "剧情解析", "contentMd": "# 剧情解析\n\n测试资料。\n"})
    if doc:
        req("PUT", f"/api/docs/{doc['id']}/content", {"contentMd": "# 剧情解析\n\n更新后的资料。\n", "author": "human"})
print("\nDONE")
