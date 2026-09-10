package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/skill"
	"wikiatlas/backend/internal/store"
	"wikiatlas/backend/internal/tools"
)

func newDeps(t *testing.T) (tools.LibrarianDeps, *store.Store) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# skill\n内容\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "core.md"), []byte("# core\n"), 0o644)
	d := tools.LibrarianDeps{
		Store:   st,
		RunID:   "run-test",
		Intent:  domain.RunIntentCreateWiki,
		Goal:    "测试",
		Skill:   skill.NewLoader(root),
		Context: map[string]any{},
		Emit:    func(string, map[string]any) {},
	}
	return d, st
}

func call(t *testing.T, reg *tools.Registry, name string, input map[string]any) map[string]any {
	t.Helper()
	tool, ok := reg.Get(name)
	if !ok {
		t.Fatalf("tool %s not registered", name)
	}
	b, _ := json.Marshal(input)
	out, err := tool.Execute(context.Background(), b)
	if err != nil {
		t.Fatalf("%s execute: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func TestReadSkillAndSearch(t *testing.T) {
	d, st := newDeps(t)
	reg := tools.NewLibrarianRegistry(d)

	got := call(t, reg, "read_skill", map[string]any{"path": "SKILL.md"})
	if got["ok"] != true {
		t.Fatalf("read_skill: %v", got)
	}
	if content, _ := got["content"].(string); content == "" {
		t.Fatal("empty skill content")
	}

	// path traversal
	bad := call(t, reg, "read_skill", map[string]any{"path": "../x"})
	if bad["ok"] != false {
		t.Fatalf("traversal should fail: %v", bad)
	}

	w, err := st.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "萨菲罗斯外传"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutWorkContent(w.ID, domain.PutContentBody{
		ContentMd: "关于萨菲罗斯的资料", Author: domain.AuthorHuman,
	}); err != nil {
		t.Fatal(err)
	}
	hits := call(t, reg, "search_works", map[string]any{"q": "萨菲罗斯"})
	if hits["ok"] != true {
		t.Fatalf("search: %v", hits)
	}
}

func TestWriteContentEmitsAndCommits(t *testing.T) {
	d, st := newDeps(t)
	var events []string
	d.Emit = func(typ string, _ map[string]any) { events = append(events, typ) }
	reg := tools.NewLibrarianRegistry(d)

	w, err := st.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "提交测试"})
	if err != nil {
		t.Fatal(err)
	}
	res := call(t, reg, "write_content", map[string]any{
		"targetType": "work",
		"targetId":   w.ID,
		"contentMd":  "# 初稿\n\n## 概览\n\n内容足够。\n",
		"summary":    "test",
	})
	if res["ok"] != true {
		t.Fatalf("write_content: %v", res)
	}
	if res["contentVer"].(float64) != 1 {
		t.Fatalf("ver=%v", res["contentVer"])
	}
	joined := ""
	for _, e := range events {
		joined += e + ","
	}
	if !contains(joined, "content.staging") || !contains(joined, "content.committed") {
		t.Fatalf("events=%s", joined)
	}
	wd, _ := st.GetWork(w.ID)
	if wd.ContentVer != 1 {
		t.Fatalf("store ver=%d", wd.ContentVer)
	}
}

func TestSearchWebNoKey(t *testing.T) {
	d, _ := newDeps(t)
	reg := tools.NewLibrarianRegistry(d)
	res := call(t, reg, "search_web", map[string]any{"q": "巫师3 发售日期"})
	if res["ok"] != false {
		t.Fatalf("expected no web, got %v", res)
	}
	msg, _ := res["message"].(string)
	if !contains(msg, "no web") {
		t.Fatalf("message=%s", msg)
	}
}

func TestUpsertAndRelation(t *testing.T) {
	d, _ := newDeps(t)
	reg := tools.NewLibrarianRegistry(d)

	a := call(t, reg, "upsert_work", map[string]any{
		"kind": "work", "title": "A作品", "medium": "game",
	})
	if a["ok"] != true {
		t.Fatalf("upsert a: %v", a)
	}
	b := call(t, reg, "upsert_work", map[string]any{
		"kind": "work", "title": "B作品", "medium": "game",
	})
	aid, _ := a["id"].(string)
	bid, _ := b["id"].(string)
	rel := call(t, reg, "upsert_relation", map[string]any{
		"fromId": bid, "toId": aid, "type": "sequel_to",
	})
	if rel["ok"] != true {
		t.Fatalf("relation: %v", rel)
	}
	// invalid type
	bad := call(t, reg, "upsert_relation", map[string]any{
		"fromId": aid, "toId": bid, "type": "buddy",
	})
	if bad["ok"] != false {
		t.Fatalf("bad relation should fail: %v", bad)
	}
}

func TestPatchSection(t *testing.T) {
	d, st := newDeps(t)
	reg := tools.NewLibrarianRegistry(d)
	w, _ := st.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "P"})
	_, _ = st.PutWorkContent(w.ID, domain.PutContentBody{
		ContentMd: "# T\n\n## 概览\n\n旧。\n\n## 设定\n\n设。\n",
		Author:    domain.AuthorHuman,
	})
	res := call(t, reg, "patch_section", map[string]any{
		"targetId":    w.ID,
		"heading":     "概览",
		"newMarkdown": "新概览内容。",
	})
	if res["ok"] != true {
		t.Fatalf("patch: %v", res)
	}
	wd, _ := st.GetWork(w.ID)
	if !contains(*wd.ContentMd, "新概览内容") {
		t.Fatalf("content=%s", *wd.ContentMd)
	}
}

func TestNarrativeAndPlan(t *testing.T) {
	d, _ := newDeps(t)
	var lines []string
	var plans []string
	d.Emit = func(typ string, payload map[string]any) {
		if typ == "narrative" {
			lines = append(lines, payload["text"].(string))
		}
		if typ == "plan.updated" {
			plans = append(plans, "plan")
		}
	}
	reg := tools.NewLibrarianRegistry(d)
	call(t, reg, "narrative", map[string]any{"text": "正在检索"})
	call(t, reg, "update_plan", map[string]any{
		"tasks": []map[string]any{{"id": "t1", "title": "a", "status": "completed"}},
	})
	if len(lines) != 1 || lines[0] != "正在检索" {
		t.Fatalf("lines=%v", lines)
	}
	if len(plans) != 1 {
		t.Fatalf("plans=%v", plans)
	}
}

func TestWriteContentQualityMarksSummary(t *testing.T) {
	d, st := newDeps(t)
	d.QualityCheck = func(string) (bool, []string) { return false, []string{"篇幅不足"} }
	reg := tools.NewLibrarianRegistry(d)
	w, _ := st.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "Q"})
	res := call(t, reg, "write_content", map[string]any{
		"targetType": "work",
		"targetId":   w.ID,
		"contentMd":  "## 短\n\nx\n",
		"summary":    "初稿",
	})
	if res["ok"] != true {
		t.Fatalf("write: %v", res)
	}
	revs, err := st.ListRevisions("work", w.ID, 1, false)
	if err != nil || len(revs) == 0 {
		t.Fatalf("revs err=%v len=%d", err, len(revs))
	}
	if !strings.Contains(revs[0].Summary, "low quality") {
		t.Fatalf("summary=%q", revs[0].Summary)
	}
}

func TestReplaceSectionDirect(t *testing.T) {
	md := "# T\n\n## 1. 概览\n\n旧概览内容。\n\n## 2. 设定\n\n设定内容。\n"
	out, err := tools.ReplaceSection(md, "概览", "新概览内容，更详细。")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "新概览内容") || strings.Contains(out, "旧概览内容") {
		t.Fatalf("bad replace: %s", out)
	}
	if !strings.Contains(out, "设定内容") {
		t.Fatalf("next section lost: %s", out)
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
