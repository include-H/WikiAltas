#!/usr/bin/env python3
"""拉一次真实工单的 SSE 流，核 Responses 形状与 sequence_number 连续性。

用途：把「SSE 是不是真的 Responses 形状」从自述变成实测——拉到的字节说了算。
用法：
    scripts/verify-sse.py                       # 默认 8080 + 密码 1234 + 一个会触发检索的问题
    scripts/verify-sse.py --goal "..." --intent answer
    scripts/verify-sse.py --dump /tmp/x.jsonl   # 落原始事件，事后可回看 / 喂 reduce-stream.cjs

退出码：硬不变量违反 → 1；只有告警 → 0（告警会逐条打出来）。
"""
import argparse
import http.cookiejar
import json
import socket
import sys
import time
import urllib.error
import urllib.request
from collections import Counter

# 一次响应的终局事件。
TERMINAL = {"response.completed", "response.failed", "response.incomplete"}
# run 级收尾：见到它才算"这条流真的完了"（兼容迁移前后两套命名）。
RUN_TERMINAL = {
    "wikiatlas.run.completed", "wikiatlas.run.failed",
    "run.completed", "run.failed",
}
# 单次 read 的等待上限：工具调用期间可能长时间没有事件，用较短超时循环等待，
# 而不是让 readline 阻塞到全局 deadline。
READ_TIMEOUT = 8


def login(base, password):
    cj = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))
    req = urllib.request.Request(
        base + "/api/auth/login",
        data=json.dumps({"password": password}).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        op.open(req, timeout=10).read()
    except urllib.error.HTTPError as e:
        sys.exit(f"登录失败 HTTP {e.code}: {e.read().decode(errors='replace')[:200]}")
    return op


def create_run(op, base, intent, goal, workspace):
    payload = {"intent": intent, "goal": goal}
    if workspace:
        payload["workspace"] = workspace
    req = urllib.request.Request(
        base + "/api/runs",
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        res = json.loads(op.open(req, timeout=15).read())
    except urllib.error.HTTPError as e:
        sys.exit(f"建单失败 HTTP {e.code}: {e.read().decode(errors='replace')[:300]}")
    return res["runId"]


def stream(op, base, run_id, timeout):
    """逐条产出 (event_type, payload_dict, raw_data)。"""
    req = urllib.request.Request(
        base + f"/api/runs/{run_id}/events/stream",
        headers={"Accept": "text/event-stream"},
    )
    resp = op.open(req, timeout=READ_TIMEOUT)
    deadline = time.time() + timeout
    ev_type, buf = None, []
    seen_terminal = False

    while time.time() < deadline:
        try:
            line = resp.readline()
        except (socket.timeout, TimeoutError):
            # 读到终局后静默 → 认为流结束；否则继续等（工具调用可能很慢）。
            if seen_terminal:
                return
            continue
        if not line:
            return
        text = line.decode("utf-8", "replace").rstrip("\r\n")
        if text == "":
            raw = "\n".join(buf)
            if buf:
                try:
                    payload = json.loads(raw)
                except json.JSONDecodeError:
                    payload = {"__unparsed__": raw}
                yield (ev_type or "message"), payload, raw
                if payload.get("type") in TERMINAL:
                    seen_terminal = True
                if payload.get("type") in RUN_TERMINAL or ev_type in RUN_TERMINAL:
                    return
            ev_type, buf = None, []
        elif text.startswith("event:"):
            ev_type = text[6:].strip()
        elif text.startswith("data:"):
            buf.append(text[5:].lstrip())


def analyse(events):
    hard, soft = [], []
    types = [t for t, _, _ in events]

    if not events:
        return ["一条事件都没收到"], []

    # 1. 必须有 Responses 事件，且最后一条 response.* 是终局
    resp_types = [t for t in types if t.startswith("response.")]
    if not resp_types:
        hard.append("没有任何 response.* 事件 —— 这不是 Responses 流")
    elif resp_types[-1] not in TERMINAL:
        hard.append(f"最后一条 response.* 是 {resp_types[-1]!r}，不是 {sorted(TERMINAL)} 之一")

    # 1b. 一个用户轮次 = 一个 Response（多轮工具往返要在后端重新编号，seq 不能重启）
    created = resp_types.count("response.created")
    if created > 1:
        hard.append(
            f"出现 {created} 次 response.created —— 一个轮次应只有一个 Response；"
            "多轮工具往返必须由后端连续编号，不能让每轮 vendor 调用各从 0 起"
        )

    # 2. sequence_number 连续性（只对**带号**的事件要求）
    seqs = [(t, p.get("sequence_number")) for t, p, _ in events if isinstance(p, dict)]
    numbered = [(t, s) for t, s in seqs if isinstance(s, int)]
    if not numbered:
        soft.append("没有任何事件带 sequence_number —— 前端归约器会失序")
    else:
        prev = None
        for t, s in numbered:
            if prev is not None and s != prev + 1:
                hard.append(f"sequence_number 不连续：{prev} → {s}（事件 {t}）")
            prev = s
        if len(numbered) < len(seqs):
            soft.append(f"{len(seqs) - len(numbered)} 条事件没有 sequence_number（若属 wikiatlas.* 则正常）")

    # 2b. wikiatlas.* 绝不能吃号（否则归约器看到 seq 跳空）
    wik_numbered = [t for t, s in seqs if isinstance(s, int) and t.startswith("wikiatlas.")]
    if wik_numbered:
        hard.append(f"wikiatlas.* 事件带了 sequence_number：{wik_numbered[:3]} —— 会污染归约器")

    # 3. output_item 配对
    added = types.count("response.output_item.added")
    done = types.count("response.output_item.done")
    if added != done:
        hard.append(f"output_item.added={added} 与 .done={done} 不配对")

    # 4. 工具调用要有参数增量
    fc_delta = types.count("response.function_call_arguments.delta")
    fc_done = types.count("response.function_call_arguments.done")
    if added and fc_delta == 0 and fc_done == 0:
        soft.append("有 output_item 但没有 function_call_arguments 增量/完成事件")

    # 5. 命名空间：wikiatlas.* 应与 Responses 事件同流
    wik = [t for t in types if t.startswith("wikiatlas.")]
    if not wik:
        soft.append("没有 wikiatlas.* 事件 —— 任务清单/用量环这类自造状态会没地方来")

    # 6. 文本增量
    if "response.output_text.delta" not in types:
        soft.append("没有 response.output_text.delta（正文可能整段蹦出）")

    return hard, soft


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://127.0.0.1:8080")
    ap.add_argument("--password", default="1234")
    ap.add_argument("--intent", default="answer")
    ap.add_argument("--goal", default="用一句话回答：你现在的上下文里有没有历史会话？不要改任何内容。")
    ap.add_argument("--workspace", default="")
    ap.add_argument("--timeout", type=int, default=180)
    ap.add_argument("--dump", default="")
    args = ap.parse_args()

    op = login(args.base, args.password)
    run_id = create_run(op, args.base, args.intent, args.goal, args.workspace)
    print(f"runId = {run_id}\n")

    events = []
    for t, p, raw in stream(op, args.base, run_id, args.timeout):
        events.append((t, p, raw))
        seq = p.get("sequence_number") if isinstance(p, dict) else None
        tail = ""
        if t == "response.output_text.delta":
            tail = repr(p.get("delta", ""))[:60]
        elif t in ("response.output_item.added", "response.output_item.done"):
            item = p.get("item") or {}
            tail = f"{item.get('type', '?')} {item.get('name', '')}".strip()
        elif t.startswith("wikiatlas."):
            tail = json.dumps(p, ensure_ascii=False)[:80]
        print(f"  {seq if seq is not None else '-':>5}  {t:<44} {tail}")

    if args.dump:
        with open(args.dump, "w", encoding="utf-8") as f:
            for t, p, _ in events:
                f.write(json.dumps({"event": t, "data": p}, ensure_ascii=False) + "\n")
        print(f"\n原始事件已落盘：{args.dump}")

    print("\n事件类型计数：")
    for t, n in Counter(t for t, _, _ in events).most_common():
        print(f"  {n:>4}  {t}")

    hard, soft = analyse(events)
    print()
    for s in soft:
        print(f"  [告警] {s}")
    for h in hard:
        print(f"  [失败] {h}")
    if not hard and not soft:
        print("  全部通过。")
    sys.exit(1 if hard else 0)


if __name__ == "__main__":
    main()
