package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

// 资料夹里的长文必须支持"按节替换"：这是 6000 字分析稿能分节写入的前提（之前只支持 works）。
func TestPatchSectionOnDoc(t *testing.T) {
	d, st := newDeps(t)
	reg := tools.NewLibrarianRegistry(d)
	series, _ := st.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindSeries, Title: "辉煌时代"})
	doc, err := st.CreateDoc(series.ID, domain.CreateDocBody{Title: "从虚拟机到智能网卡"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutDocContent(doc.ID, domain.PutContentBody{
		ContentMd: "# 从虚拟机到智能网卡\n\n## 一、起点\n\n旧内容。\n\n## 二、展开\n\n二的内容。\n",
		Author:    domain.AuthorHuman,
	}); err != nil {
		t.Fatal(err)
	}

	res := call(t, reg, "patch_section", map[string]any{
		"targetType":  "doc",
		"targetId":    doc.ID,
		"heading":     "一、起点",
		"newMarkdown": "## 一、起点\n\n新内容，比原来更完整。\n",
	})
	if res["ok"] != true {
		t.Fatalf("patch doc: %v", res)
	}
	got, _ := st.GetDoc(doc.ID)
	if !contains(got.ContentMd, "新内容，比原来更完整") || contains(got.ContentMd, "旧内容") {
		t.Fatalf("doc content=%s", got.ContentMd)
	}
	if n := strings.Count(got.ContentMd, "## 一、起点"); n != 1 {
		t.Fatalf("标题重复 %d 次: %s", n, got.ContentMd)
	}
	if !contains(got.ContentMd, "二的内容") {
		t.Fatalf("下一节丢了: %s", got.ContentMd)
	}

	// 空内容同样要被拒绝（防止把整节删空）
	bad := call(t, reg, "patch_section", map[string]any{
		"targetType": "doc", "targetId": doc.ID, "heading": "二、展开", "newMarkdown": "   ",
	})
	if bad["ok"] != false {
		t.Fatalf("空内容应被拒绝: %v", bad)
	}

	// read_doc 能读回
	r := call(t, reg, "read_doc", map[string]any{"id": doc.ID})
	if r["ok"] != true || !contains(r["contentMd"].(string), "新内容，比原来更完整") {
		t.Fatalf("read_doc: %v", r)
	}
}

func TestNarrative(t *testing.T) {
	d, _ := newDeps(t)
	var lines []string
	d.Emit = func(typ string, payload map[string]any) {
		if typ == "narrative" {
			lines = append(lines, payload["text"].(string))
		}
	}
	reg := tools.NewLibrarianRegistry(d)
	call(t, reg, "narrative", map[string]any{"text": "正在检索"})
	if len(lines) != 1 || lines[0] != "正在检索" {
		t.Fatalf("lines=%v", lines)
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

// --- edit：字面串替换 ---

func TestEditLiteralSemantics(t *testing.T) {
	md := "甲。乙。丙。"
	if out, n, err := tools.EditLiteral(md, "乙", "乙二", false); err != nil || out != "甲。乙二。丙。" || n != 1 {
		t.Fatalf("唯一命中替换失败: out=%q n=%d err=%v", out, n, err)
	}
	// 未命中：原样返回 + 报错（绝不改动正文）
	if out, n, err := tools.EditLiteral(md, "丁", "戊", false); err == nil || out != md || n != 0 {
		t.Fatalf("未命中应拒绝并保持原样: out=%q n=%d err=%v", out, n, err)
	}
	// 多处命中且未开 replaceAll：报错，且错误里带行号供模型补上下文
	dup := "甲\n乙\n甲\n"
	if _, n, err := tools.EditLiteral(dup, "甲", "丙", false); err == nil || n != 2 {
		t.Fatalf("多处命中应拒绝: n=%d err=%v", n, err)
	} else if !strings.Contains(err.Error(), "第 1、3 行") {
		t.Fatalf("错误里应带命中行号: %v", err)
	}
	// replaceAll：统一术语用
	if out, n, err := tools.EditLiteral(dup, "甲", "丙", true); err != nil || n != 2 || strings.Contains(out, "甲") {
		t.Fatalf("replaceAll 失败: out=%q n=%d err=%v", out, n, err)
	}
	// 空 oldString 必须拒绝：否则等于整篇重写
	if _, _, err := tools.EditLiteral(md, "", "x", false); err == nil {
		t.Fatal("空 oldString 应被拒绝")
	}
}

// 回归：题记（:::epigraph）在第一个 ## 之前，patch_section 够不着，
// 改题记曾经只能 write_content 整篇重写（真实事故：13054 字全文重打一遍）。
// edit 按字面串匹配，与 markdown 结构无关，必须能直接改到。
func TestEditReachesEpigraphAboveFirstHeading(t *testing.T) {
	d, st := newDeps(t)
	reg := tools.NewLibrarianRegistry(d)
	w, err := st.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "题记测试"})
	if err != nil {
		t.Fatal(err)
	}
	head := "# 未来黎明（小说）Wiki\n\n> 说明：待核实项已标出。\n\n:::epigraph\n旧的场景式题记。\n:::\n\n"
	if _, err := st.PutWorkContent(w.ID, domain.PutContentBody{
		ContentMd: head + "## 1. 作品概览\n\n正文一。\n\n## 2. 基础信息速览\n\n正文二。\n",
		Author:    domain.AuthorHuman,
	}); err != nil {
		t.Fatal(err)
	}

	// 先钉住前提：patch_section 到不了题记
	blocked := call(t, reg, "patch_section", map[string]any{
		"targetId": w.ID, "heading": ":::epigraph", "newMarkdown": "新的引用式题记。",
	})
	if blocked["ok"] != false {
		t.Fatalf("patch_section 本不该够到题记: %v", blocked)
	}

	res := call(t, reg, "edit", map[string]any{
		"targetId":  w.ID,
		"oldString": ":::epigraph\n旧的场景式题记。\n:::",
		"newString": ":::epigraph\n新的引用式题记。\n——某角色《某作品》\n:::",
		"summary":   "题记改为引用式",
	})
	if res["ok"] != true {
		t.Fatalf("edit: %v", res)
	}
	wd, _ := st.GetWork(w.ID)
	got := *wd.ContentMd
	if !contains(got, "新的引用式题记") || contains(got, "旧的场景式题记") {
		t.Fatalf("题记没替换:\n%s", got)
	}
	if !contains(got, "> 说明：待核实项已标出。") {
		t.Fatalf("说明行被动了:\n%s", got)
	}
	if !contains(got, "## 1. 作品概览\n\n正文一。") || !contains(got, "## 2. 基础信息速览\n\n正文二。") {
		t.Fatalf("别的章节被动了:\n%s", got)
	}
	// 摘要进版本历史，用户在版本列表里看得见这次改的是什么
	revs, err := st.ListRevisions("work", w.ID, 5, false)
	if err != nil || len(revs) == 0 {
		t.Fatalf("revisions: %v %v", revs, err)
	}
	if !contains(revs[0].Summary, "题记改为引用式") {
		t.Fatalf("revision summary=%q", revs[0].Summary)
	}
}

func TestEditRefusalsAndDocTarget(t *testing.T) {
	d, st := newDeps(t)
	reg := tools.NewLibrarianRegistry(d)
	series, _ := st.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindSeries, Title: "系列"})
	doc, err := st.CreateDoc(series.ID, domain.CreateDocBody{Title: "资料稿"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutDocContent(doc.ID, domain.PutContentBody{
		ContentMd: "# 资料稿\n\n## 一、起点\n\n旧句子。\n",
		Author:    domain.AuthorHuman,
	}); err != nil {
		t.Fatal(err)
	}

	// 空 oldString 拒绝
	if r := call(t, reg, "edit", map[string]any{"targetType": "doc", "targetId": doc.ID, "oldString": "", "newString": "x"}); r["ok"] != false {
		t.Fatalf("空 oldString 应被拒绝: %v", r)
	}
	// old == new 拒绝（保证是空操作）
	if r := call(t, reg, "edit", map[string]any{"targetType": "doc", "targetId": doc.ID, "oldString": "旧句子", "newString": "旧句子"}); r["ok"] != false {
		t.Fatalf("原样替换应被拒绝: %v", r)
	}
	// 找不到也要拒绝，并且不能改动正文
	if r := call(t, reg, "edit", map[string]any{"targetType": "doc", "targetId": doc.ID, "oldString": "不存在的句子", "newString": "x"}); r["ok"] != false {
		t.Fatalf("未命中应被拒绝: %v", r)
	}
	got, _ := st.GetDoc(doc.ID)
	if !contains(got.ContentMd, "旧句子") {
		t.Fatalf("被拒绝的 edit 改了正文:\n%s", got.ContentMd)
	}
	// 资料正文同样改得动
	if r := call(t, reg, "edit", map[string]any{"targetType": "doc", "targetId": doc.ID, "oldString": "旧句子。", "newString": "新句子。"}); r["ok"] != true {
		t.Fatalf("doc edit: %v", r)
	}
	got, _ = st.GetDoc(doc.ID)
	if !contains(got.ContentMd, "新句子。") || contains(got.ContentMd, "旧句子。") {
		t.Fatalf("doc 内容=%s", got.ContentMd)
	}
}

// 模型常把 "## 1. 概览" 一起写进 newMarkdown：工具必须剥掉它，
// 否则正文里会出现重复标题（真实观测中同一章标题重复了 3 次）。
func TestReplaceSectionStripsDuplicatedHeading(t *testing.T) {
	md := "# T\n\n## 1. 概览\n\n旧概览内容。\n\n## 2. 设定\n\n设定内容。\n"
	out, err := tools.ReplaceSection(md, "1. 概览", "## 1. 概览\n\n新概览内容。\n")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "## 1. 概览"); n != 1 {
		t.Fatalf("标题重复了 %d 次:\n%s", n, out)
	}
	if !strings.Contains(out, "新概览内容") || strings.Contains(out, "旧概览内容") {
		t.Fatalf("替换结果不对:\n%s", out)
	}
	if !strings.Contains(out, "设定内容") {
		t.Fatalf("下一章丢失:\n%s", out)
	}
}

// read_skill 的上限按 rune 算：按字节切会把中文劈成半个字符，
// json.Marshal 再把它换成 U+FFFD，模型看到的尾巴就是一堆乱码。
func TestReadSkillTruncatesOnRuneBoundary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.md"), []byte(strings.Repeat("中", 30000)), 0o644); err != nil {
		t.Fatal(err)
	}
	d, _ := newDeps(t)
	d.Skill = skill.NewLoader(root)
	reg := tools.NewLibrarianRegistry(d)

	got := call(t, reg, "read_skill", map[string]any{"path": "big.md"})
	content, _ := got["content"].(string)
	if !utf8.ValidString(content) || strings.ContainsRune(content, utf8.RuneError) {
		t.Fatal("截断劈坏了多字节字符")
	}
	if !contains(content, "（截断）") {
		t.Fatalf("超限应标注截断: 长度 %d", len([]rune(content)))
	}
}

// todo 契约的三条硬校验（照 dsh：content 非空且唯一、至多一条 in_progress）。
// 静默接受等于把问题推给用户——清单是用户直接读的东西。
func TestTodoWriteValidation(t *testing.T) {
	d, _ := newDeps(t)
	reg := tools.NewLibrarianRegistry(d)

	ok := call(t, reg, "todo_write", map[string]any{"tasks": []map[string]any{
		{"id": "t1", "content": "核实上映年份", "activeForm": "正在核实上映年份", "status": "in_progress"},
		{"id": "t2", "content": "写第一章", "activeForm": "正在写第一章", "status": "pending"},
	}})
	if ok["ok"] != true || ok["inProgress"].(float64) != 1 {
		t.Fatalf("正常清单应通过：%v", ok)
	}

	bad := []struct {
		name  string
		tasks []map[string]any
		want  string
	}{
		{"空任务名", []map[string]any{{"content": "  ", "status": "pending"}}, "没有名字"},
		{"重名", []map[string]any{
			{"content": "写第一章", "status": "pending"},
			{"content": "写第一章", "status": "pending"},
		}, "任务名重复"},
		{"两条 in_progress", []map[string]any{
			{"content": "甲", "status": "in_progress"},
			{"content": "乙", "status": "in_progress"},
		}, "只能有一个 in_progress"},
	}
	for _, c := range bad {
		res := call(t, reg, "todo_write", map[string]any{"tasks": c.tasks})
		if res["ok"] != false {
			t.Fatalf("%s 应被拒绝：%v", c.name, res)
		}
		msg, _ := res["message"].(string)
		if !contains(msg, c.want) {
			t.Fatalf("%s 的拒绝理由应含 %q，实际 %q", c.name, c.want, msg)
		}
	}
}
