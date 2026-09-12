#!/usr/bin/env python3
"""实时盯一条工单：直接轮询 run_events，把流打成人能读的一行一条。

为什么轮询库而不是订阅 SSE：能拿到增量事件（用于卡死检测），断线不影响，
也不需要 cookie。SSE 回放会跳过增量，这里不会。

用法：
  python3 -u scripts/watch-run.py                 # 等新工单出现并跟住
  python3 -u scripts/watch-run.py --run <id>      # 跟指定工单
  python3 -u scripts/watch-run.py --workspace 01a093ca   # 只认 workspace 含此串的
  python3 -u scripts/watch-run.py --stall 90 --timeout 3600

退出：工单进入终态且 8 秒无新事件，或超时。退出前打总结（含
sequence_number 连续性、工具调用序列、upsert_relation 是否被调用）。
"""
import argparse
import json
import sqlite3
import sys
import time

DB = "file:/root/WikiAltas/backend/data/wikiatlas.db?mode=ro"
DELTA_TYPES = {
    "response.output_text.delta",
    "response.reasoning_summary_text.delta",
    "response.function_call_arguments.delta",
}
TERMINAL = {"completed", "failed", "interrupted", "expired"}


def log(msg: str) -> None:
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)


def short(s, n=300) -> str:
    s = str(s).replace("\n", "\\n")
    return s if len(s) <= n else s[:n] + f"…（共 {len(s)}）"


class Watch:
    def __init__(self, args):
        self.args = args
        self.db = sqlite3.connect(DB, uri=True, timeout=5)
        self.run_id = args.run
        self.workspace = args.workspace
        self.last_seq = 0
        self.last_event_at = time.time()
        self.last_stall_print = 0.0
        self.buf_text = 0          # 正在流出的正文累计字数（含增量）
        self.buf_reason = 0
        self.msg_txt = ""
        self.last_heartbeat = 0.0
        self.turns = 0
        self.step = 0
        self.calls = []            # [{name, args, ok, summary}]
        self.done_args = {}        # call_id -> args（fnargs.done 的定稿）
        self.notices = []          # (tone, text)
        self.sn_prev = -1
        self.sn_gaps = []
        self.sn_wa = 0             # wikiatlas.* 带 sn 的（必须为 0）
        self.events = 0
        self.deltas = 0
        self.status = "?"
        self.model = ""
        self.intent = ""
        self.started = None
        self.t0 = time.time()
        self.ss = None             # 记录哪条内容以 sn 计

    def query(self, sql, *p):
        for _ in range(3):
            try:
                return self.db.execute(sql, p).fetchall()
            except sqlite3.OperationalError:
                time.sleep(0.5)
        return []

    def pick_run(self):
        if self.run_id:
            return
        rows = self.query(
            "SELECT id, workspace, intent, status, model FROM runs "
            "WHERE status='running' ORDER BY id DESC LIMIT 1")
        if not rows:
            snap = self.args.snapshot
            rows = self.query(
                "SELECT id, workspace, intent, status, model FROM runs "
                "WHERE id > ? ORDER BY id DESC LIMIT 1", snap)
        if rows:
            r = rows[0]
            if self.workspace and self.workspace not in (r[1] or ""):
                return  # 不是我们要那条，继续等
            self.run_id, self.workspace, self.intent, self.status, self.model = r
            log(f"跟住工单 {self.run_id}\n         workspace={self.workspace} intent={self.intent}")

    def handle(self, seq, typ, p):
        sn = p.get("sequence_number")
        if typ.startswith("response.") and isinstance(sn, int):
            if self.sn_prev >= 0 and sn != self.sn_prev + 1:
                self.sn_gaps.append((self.sn_prev, sn))
            self.sn_prev = sn
        elif typ.startswith("wikiatlas.") and isinstance(sn, int):
            self.sn_wa += 1

        if typ in DELTA_TYPES:
            self.deltas += 1
            d = p.get("delta", "")
            if typ == "response.output_text.delta":
                self.buf_text += len(d)
                now = time.time()
                if now - self.last_heartbeat > 2.5:
                    self.last_heartbeat = now
                    log(f"   …正文流出中 {self.buf_text} 字")
            elif typ == "response.reasoning_summary_text.delta":
                self.buf_reason += len(d)
            return  # 不逐条打印

        if typ in ("response.created", "response.in_progress"):
            self.turns += 1
            resp = p.get("response", {})
            log(f"── 响应开始（第 {self.turns} 段）model={resp.get('model','')}")
        elif typ == "response.output_item.added":
            it = p.get("item", {}) or {}
            k = it.get("type")
            if k == "function_call":
                log(f"▶ 调用工具 {it.get('name')}  (call {str(it.get('call_id'))[:14]})")
            elif k == "message":
                self.buf_text = 0
                self.last_heartbeat = 0
                log("▶ 开始说正文")
            elif k == "reasoning":
                self.buf_reason = 0
                log("▶ 开始思考")
            else:
                log(f"▶ 输出项 {k}")
        elif typ == "response.function_call_arguments.done":
            cid = str(p.get("call_id"))
            args = p.get("arguments", "")
            self.done_args[cid] = args
            log(f"   参数定稿 {p.get('name')}（{len(args)} 字符）：{short(args, 220)}")
        elif typ == "response.output_item.done":
            it = p.get("item", {}) or {}
            k = it.get("type")
            if k == "function_call":
                extra = (it.get("wikiatlas") or {}).get("outputSummary", "")
                ok = it.get("status") == "completed"
                cid = str(it.get("call_id"))
                if cid in self.done_args and it.get("arguments") != self.done_args[cid]:
                    log(f"   ⚠ item.done 的参数与 fnargs.done 不一致！")
                self.calls.append({"name": it.get("name"), "ok": ok,
                                   "args": it.get("arguments", ""), "summary": extra})
                mark = "✔" if ok else "✘"
                log(f"{mark} {it.get('name')} → {short(extra, 260) if extra else it.get('status')}")
            elif k == "message":
                texts = [c.get("text", "") for c in it.get("content", []) if isinstance(c, dict)]
                t = "\n".join(texts)
                if t.strip():
                    self.msg_txt = t
                    log(f"✔ 正文片段（{len(t)} 字）尾部：…{short(t[-260:], 260)}")
            elif k == "reasoning":
                log(f"✔ 思考片段（{len(it.get('summary', [{}])[0].get('text', '')) if it.get('summary') else self.buf_reason} 字）")
            else:
                log(f"✔ 输出项 {k} 完成")
        elif typ == "response.completed":
            resp = p.get("response", {})
            u = resp.get("usage", {})
            wa = p.get("wikiatlas", {})
            log(f"══ 第 {self.turns} 段完成：{wa.get('summary','')}  wroteContent={wa.get('wroteContent')}"
                f"  tok in={u.get('input_tokens')} out={u.get('output_tokens')}"
                f" cache={ (u.get('input_tokens_details') or {}).get('cached_tokens') }")
        elif typ == "response.failed":
            resp = p.get("response", {})
            log(f"✘✘ 响应失败：{resp.get('error')}")
        elif typ == "wikiatlas.usage":
            log(f"· 用量 step={p.get('step')} in={p.get('promptTokens')}"
                f" (cache r={p.get('cacheReadTokens')} w={p.get('cacheWriteTokens')})"
                f" out={p.get('completionTokens')}")
        elif typ == "wikiatlas.notice":
            self.notices.append((p.get("tone"), p.get("text", "")))
            log(f"⚠ 播报[{p.get('tone')}] {short(p.get('text'), 300)}")
        elif typ == "wikiatlas.todo":
            tasks = p.get("tasks", [])
            n_done = sum(1 for t in tasks if isinstance(t, dict)
                         and str(t.get("status")) in ("completed", "done"))
            cur = next((t.get("content") or t.get("title") or ""
                        for t in tasks if isinstance(t, dict)
                        and str(t.get("status")) not in ("completed", "done")), "")
            log(f"☑ 待办 {n_done}/{len(tasks)}；当前：{short(cur, 80)}")
        elif typ == "wikiatlas.request":
            bits = [f"step={p.get('step')}", f"messages={p.get('messages')}",
                    f"shape={p.get('shapeReason')}"]
            for k in ("headerReason", "divergedAt", "rebuildOK"):
                if p.get(k) is not None:
                    bits.append(f"{k}={p.get(k)}")
            log("· 请求 " + " ".join(bits))
        elif typ == "wikiatlas.context":
            log(f"· 上下文 {short(p, 240)}")
        elif typ == "wikiatlas.tree":
            log(f"· 树 {short(p, 240)}")
        elif typ == "wikiatlas.content.staging":
            log(f"✎ 正文暂存 {short(p, 200)}")
        elif typ == "wikiatlas.content.committed":
            log(f"● 正文落库 {short(p, 240)}")
        elif typ == "wikiatlas.meta":
            log(f"· meta {short(p, 200)}")
        else:
            log(f"? {typ} {short(p, 200)}")

    def tick(self):
        if not self.run_id:
            self.pick_run()
            return
        rows = self.query("SELECT id, workspace, intent, status, model, started_at, error_json "
                          "FROM runs WHERE id = ?", self.run_id)
        if rows:
            self.workspace, self.intent, self.status, self.model, self.started = \
                rows[0][1], rows[0][2], rows[0][3], rows[0][4], rows[0][5]
            err = rows[0][6]
        else:
            err = None
        evs = self.query("SELECT seq, type, payload FROM run_events "
                         "WHERE run_id = ? AND seq > ? ORDER BY seq", self.run_id, self.last_seq)
        for seq, typ, payload in evs:
            self.events += 1
            self.last_seq = seq
            self.last_event_at = time.time()
            try:
                p = json.loads(payload) if payload else {}
            except json.JSONDecodeError:
                p = {}
            self.handle(seq, typ, p)
        # 卡死检测
        idle = time.time() - self.last_event_at
        if self.status == "running" and idle > self.args.stall and \
                time.time() - self.last_stall_print > 30:
            self.last_stall_print = time.time()
            log(f"⏳ 已 {int(idle)}s 无事件（连接没断——看门狗按**字节**计空闲，"
                f"上游在大段工具参数时只发心跳/缓冲；真正的死连接要 150s 无字节才会被断开）")

    def summary(self):
        dur = time.time() - self.t0
        log("──────── 总结 ────────")
        log(f"run={self.run_id} workspace={self.workspace} intent={self.intent} "
            f"model={self.model} status={self.status}")
        log(f"时长 {dur/60:.1f} 分钟；事件 {self.events} 条（其中增量 {self.deltas}）")
        log(f"响应段数 {self.turns}；sequence_number 0..{self.sn_prev}"
            f"（缺口 {len(self.sn_gaps)}{'：' + str(self.sn_gaps[:5]) if self.sn_gaps else ''}）；"
            f"wikiatlas.* 带 sn 的 {self.sn_wa}（应为 0）")
        log(f"工具调用 {len(self.calls)} 次（按序）：")
        for i, c in enumerate(self.calls, 1):
            log(f"  {i}. {'✔' if c['ok'] else '✘'} {c['name']}  args={short(c['args'], 120)}")
        names = {c["name"] for c in self.calls}
        if "write_content" in names:
            log(f"建边调用：{'有 upsert_relation' if 'upsert_relation' in names else '❌ 没有 upsert_relation'}")
        if self.notices:
            log(f"播报 {len(self.notices)} 条：")
            for tone, text in self.notices[-8:]:
                log(f"  [{tone}] {short(text, 200)}")
        if self.msg_txt:
            log(f"最后一段正文/发言（尾 400 字）：\n…{self.msg_txt[-400:]}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--run", default=None)
    ap.add_argument("--workspace", default=None)
    ap.add_argument("--stall", type=int, default=100)
    ap.add_argument("--timeout", type=int, default=3600)
    ap.add_argument("--quiet-after", type=int, default=8)
    args = ap.parse_args()

    w = Watch(args)
    snap = w.db.execute("SELECT COALESCE(MAX(id),'') FROM runs").fetchone()[0]
    args.snapshot = snap
    log(f"等待新工单…（当前 max run id={snap[:20]}）" if not args.run else f"盯住 {args.run}")
    quiet_since = None
    while True:
        w.tick()
        if w.run_id and w.status in TERMINAL:
            if quiet_since is None:
                quiet_since = time.time()
            elif time.time() - quiet_since > args.quiet_after:
                break
        if time.time() - w.t0 > args.timeout:
            log("（到达 --timeout）")
            break
        time.sleep(2)
    w.summary()


if __name__ == "__main__":
    sys.exit(main())
