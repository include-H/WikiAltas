#!/usr/bin/env python3
"""把整个库逻辑导出成 JSON（摘掉 slug 列），供重建库后灌回。

用法：
    dump-db.py <db> <out.json>          # 导出
    dump-db.py --restore <db> <in.json> # 灌回（db 必须是新 schema、空库）

为什么全表导出而不是只导"最终幻想"子树：全量更简单、不丢东西（工单历史也在），
重建后一次灌回。摘 slug 是因为新 schema 里这列不存在了。
"""
import json
import sqlite3
import sys

# 这两张表在新 schema 里去掉了 slug
DROP_COLUMNS = {"works": {"slug"}, "docs": {"slug"}}

# 整表排除。settings 里是密钥（llm_api_key / exa_api_key / 密码哈希），
# **不能**落到明文 JSON 里；而且没必要——它们在原库的 settings 表里，重建后直接用 SQL 抄过去即可。
SKIP_TABLES = {"settings"}


def is_derived(t):
    """派生表：FTS 索引由触发器跟着 works/docs 走，插回主表时自动重建。"""
    return "_fts" in t

# 灌回顺序：被引用的表在前（外键）
RESTORE_ORDER = [
    "settings", "works", "docs", "relations", "library_links", "revisions",
    "sessions", "session_messages", "runs", "run_events", "schema_migrations",
]


def tables(con):
    return [r[0] for r in con.execute(
        "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")]


def columns(con, t):
    return [r[1] for r in con.execute(f"PRAGMA table_info({t})")]


def dump(db, out):
    con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    data = {}
    for t in tables(con):
        if t in SKIP_TABLES:
            print(f"  {t}: 跳过（含密钥，不落明文）")
            continue
        if is_derived(t):
            print(f"  {t}: 跳过（派生索引，插回主表时由触发器重建）")
            continue
        cols = [c for c in columns(con, t) if c not in DROP_COLUMNS.get(t, set())]
        rows = con.execute(f"SELECT {', '.join(cols)} FROM {t}").fetchall()
        data[t] = {"columns": cols, "rows": rows}
        print(f"  {t}: {len(rows)} 行  （列 {len(cols)}）")
    con.close()
    with open(out, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False)
    print(f"已导出到 {out}")


def restore(db, src):
    data = json.load(open(src, encoding="utf-8"))
    con = sqlite3.connect(db)
    con.execute("PRAGMA foreign_keys = OFF")
    order = [t for t in RESTORE_ORDER if t in data] + [t for t in data if t not in RESTORE_ORDER]
    for t in order:
        cols, rows = data[t]["columns"], data[t]["rows"]
        if not rows:
            print(f"  {t}: 0 行（跳过）")
            continue
        ph = ", ".join("?" * len(cols))
        con.executemany(
            f"INSERT OR REPLACE INTO {t} ({', '.join(cols)}) VALUES ({ph})", rows)
        print(f"  {t}: 灌回 {len(rows)} 行")
    con.commit()
    con.execute("PRAGMA foreign_keys = ON")
    con.close()
    print(f"已从 {src} 灌回")


if __name__ == "__main__":
    if sys.argv[1] == "--restore":
        restore(sys.argv[2], sys.argv[3])
    else:
        dump(sys.argv[1], sys.argv[2])
