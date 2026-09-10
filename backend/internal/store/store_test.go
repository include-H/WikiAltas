package store

import (
	"strings"
	"testing"

	"wikiatlas/backend/internal/domain"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateWorkAndTree(t *testing.T) {
	s := newTestStore(t)

	u, err := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindUniverse, Title: "猎魔人"})
	if err != nil {
		t.Fatalf("CreateWork universe: %v", err)
	}
	if u.Slug == "" || u.ID == "" {
		t.Fatalf("expected id and slug, got %+v", u)
	}
	if u.Status != domain.WorkStatusStub {
		t.Fatalf("new work status = %s, want stub", u.Status)
	}

	m := domain.MediumGame
	series, err := s.CreateWork(domain.CreateWorkBody{
		ParentID: &u.ID,
		Kind:     domain.WorkKindSeries,
		Medium:   &m,
		Title:    "巫师系列",
	})
	if err != nil {
		t.Fatalf("CreateWork series: %v", err)
	}

	nodes, err := s.ListTree()
	if err != nil {
		t.Fatalf("ListTree: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("tree len = %d, want 2", len(nodes))
	}
	// find series in tree
	found := false
	for _, n := range nodes {
		if n.ID == series.ID {
			found = true
			if n.ParentID == nil || *n.ParentID != u.ID {
				t.Fatalf("series parent = %v, want %s", n.ParentID, u.ID)
			}
			if n.HasContent {
				t.Fatal("series should not have content yet")
			}
			if n.HasLibraryLink {
				t.Fatal("series should not have library link")
			}
		}
	}
	if !found {
		t.Fatal("series not in tree")
	}
}

func TestPutContentVersionConflict(t *testing.T) {
	s := newTestStore(t)
	w, err := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "巫师3"})
	if err != nil {
		t.Fatalf("CreateWork: %v", err)
	}

	// first write, no expectedVersion
	res, err := s.PutWorkContent(w.ID, domain.PutContentBody{
		ContentMd: "# v1",
		Author:    domain.AuthorHuman,
	})
	if err != nil {
		t.Fatalf("PutWorkContent v1: %v", err)
	}
	if res.ContentVer != 1 {
		t.Fatalf("contentVer = %d, want 1", res.ContentVer)
	}

	// second write with correct expectedVersion
	ver := int64(1)
	res2, err := s.PutWorkContent(w.ID, domain.PutContentBody{
		ContentMd:      "# v2",
		Author:         domain.AuthorLLM,
		ExpectedVersion: &ver,
	})
	if err != nil {
		t.Fatalf("PutWorkContent v2: %v", err)
	}
	if res2.ContentVer != 2 {
		t.Fatalf("contentVer = %d, want 2", res2.ContentVer)
	}

	// stale expectedVersion → 409 conflict
	stale := int64(1)
	_, err = s.PutWorkContent(w.ID, domain.PutContentBody{
		ContentMd:      "# v3",
		Author:         domain.AuthorHuman,
		ExpectedVersion: &stale,
	})
	if err == nil {
		t.Fatal("expected version conflict error")
	}
	var cf ErrConflict
	if !errorAs(err, &cf) {
		t.Fatalf("err type = %T (%v), want ErrConflict", err, err)
	}

	// verify content still v2
	got, err := s.GetWork(w.ID)
	if err != nil {
		t.Fatalf("GetWork: %v", err)
	}
	if got.ContentVer != 2 || got.ContentMd == nil || *got.ContentMd != "# v2" {
		t.Fatalf("after conflict: ver=%d content=%v", got.ContentVer, got.ContentMd)
	}
	if got.Status != domain.WorkStatusDraft {
		t.Fatalf("status = %s, want draft (promoted from stub)", got.Status)
	}
}

func TestRevisionRestore(t *testing.T) {
	s := newTestStore(t)
	w, err := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "FFVII"})
	if err != nil {
		t.Fatalf("CreateWork: %v", err)
	}

	r1, err := s.PutWorkContent(w.ID, domain.PutContentBody{ContentMd: "first", Author: domain.AuthorHuman})
	if err != nil {
		t.Fatalf("put v1: %v", err)
	}
	if _, err := s.PutWorkContent(w.ID, domain.PutContentBody{ContentMd: "second", Author: domain.AuthorHuman}); err != nil {
		t.Fatalf("put v2: %v", err)
	}
	r3, err := s.PutWorkContent(w.ID, domain.PutContentBody{ContentMd: "third", Author: domain.AuthorLLM})
	if err != nil {
		t.Fatalf("put v3: %v", err)
	}

	// restore v1 → creates v4 with content "first"
	res, err := s.RestoreRevision("work", w.ID, r1.RevisionID, domain.AuthorHuman)
	if err != nil {
		t.Fatalf("RestoreRevision: %v", err)
	}
	if res.ContentVer != 4 {
		t.Fatalf("restored contentVer = %d, want 4", res.ContentVer)
	}

	got, err := s.GetWork(w.ID)
	if err != nil {
		t.Fatalf("GetWork: %v", err)
	}
	if got.ContentMd == nil || *got.ContentMd != "first" {
		t.Fatalf("content = %v, want first", got.ContentMd)
	}

	// history still has all 4 versions
	revs, err := s.ListRevisions("work", w.ID, 10, true)
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revs) != 4 {
		t.Fatalf("revisions = %d, want 4", len(revs))
	}
	// newest first
	if revs[0].Version != 4 || revs[0].ContentMd != "first" {
		t.Fatalf("latest rev = %+v", revs[0])
	}
	_ = r3
}

func TestDeleteWorkWithChildrenBlocked(t *testing.T) {
	s := newTestStore(t)
	parent, err := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindUniverse, Title: "Parent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWork(domain.CreateWorkBody{ParentID: &parent.ID, Kind: domain.WorkKindWork, Title: "Child"}); err != nil {
		t.Fatal(err)
	}
	err = s.DeleteWork(parent.ID)
	if err == nil {
		t.Fatal("expected error deleting parent with children")
	}
	if !isValidation(err) {
		t.Fatalf("err = %T %v, want ErrValidation", err, err)
	}
}

func TestSearchFTS(t *testing.T) {
	s := newTestStore(t)
	w, err := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "荣誉勋章"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PutWorkContent(w.ID, domain.PutContentBody{
		ContentMd: "这是一篇关于太平洋战场的战役解析。",
		Author:    domain.AuthorHuman,
	})
	if err != nil {
		t.Fatal(err)
	}

	hits, err := s.Search("太平洋", "work", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected FTS hit for 太平洋")
	}
	if hits[0].ID != w.ID {
		t.Fatalf("hit id = %s, want %s", hits[0].ID, w.ID)
	}

	// doc search
	doc, err := s.CreateDoc(w.ID, domain.CreateDocBody{Title: "速通笔记", ContentMd: strPtr("世界纪录路线说明")})
	if err != nil {
		t.Fatal(err)
	}
	dhits, err := s.Search("世界纪录", "doc", 10)
	if err != nil {
		t.Fatalf("Search doc: %v", err)
	}
	found := false
	for _, h := range dhits {
		if h.ID == doc.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("doc not in search hits: %+v", dhits)
	}
}

// 别名必须能被检索到：search_works 声称"按标题/别名/正文检索"，
// 早期实现只把 title/content 灌进 FTS，FF7R 这类别名搜不到。
func TestSearchFindsAliases(t *testing.T) {
	s := newTestStore(t)
	w, err := s.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindWork, Title: "最终幻想VII 重制版",
	})
	if err != nil {
		t.Fatal(err)
	}
	aliases := []string{"FF7R", "FFVII Remake", "Final Fantasy VII Remake"}
	if _, err := s.PatchWork(w.ID, domain.PatchWorkBody{Aliases: &aliases}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"FF7R", "Remake"} {
		hits, err := s.Search(q, "work", 10)
		if err != nil {
			t.Fatalf("Search(%s): %v", q, err)
		}
		found := false
		for _, h := range hits {
			if h.ID == w.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("别名 %q 没检索到: %+v", q, hits)
		}
	}
	// 命中别名时 snippet 不该为 NULL（否则 Scan 报错会静默回退到 LIKE）
	hits, _ := s.Search("FF7R", "work", 10)
	if len(hits) == 0 || hits[0].Snippet == "" {
		t.Fatalf("别名命中缺少 snippet: %+v", hits)
	}
}

func TestDocCRUDAndContent(t *testing.T) {
	s := newTestStore(t)
	w, err := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "Work"})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.CreateDoc(w.ID, domain.CreateDocBody{Title: "剧情解析"})
	if err != nil {
		t.Fatalf("CreateDoc: %v", err)
	}
	if doc.ContentVer != 0 {
		t.Fatalf("new doc contentVer = %d, want 0", doc.ContentVer)
	}

	res, err := s.PutDocContent(doc.ID, domain.PutContentBody{ContentMd: "内容A", Author: domain.AuthorHuman})
	if err != nil {
		t.Fatalf("PutDocContent: %v", err)
	}
	if res.ContentVer != 1 {
		t.Fatalf("doc contentVer = %d, want 1", res.ContentVer)
	}

	stale := int64(0)
	_, err = s.PutDocContent(doc.ID, domain.PutContentBody{ContentMd: "冲突", Author: domain.AuthorHuman, ExpectedVersion: &stale})
	if err == nil {
		t.Fatal("expected conflict")
	}

	docs, err := s.ListDocsByWork(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs = %d, want 1", len(docs))
	}
}

func TestRelationDictionary(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "A"})
	b, _ := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "B"})

	rel, err := s.CreateRelation(domain.CreateRelationBody{FromID: a.ID, ToID: b.ID, Type: domain.RelationSequelTo})
	if err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	if rel.Type != domain.RelationSequelTo {
		t.Fatalf("type = %s", rel.Type)
	}

	// duplicate
	_, err = s.CreateRelation(domain.CreateRelationBody{FromID: a.ID, ToID: b.ID, Type: domain.RelationSequelTo})
	if err == nil {
		t.Fatal("expected conflict on duplicate relation")
	}

	// invalid type
	_, err = s.CreateRelation(domain.CreateRelationBody{FromID: a.ID, ToID: b.ID, Type: "likes"})
	if err == nil {
		t.Fatal("expected validation error for free-text predicate")
	}
}

func TestRunEventsAndExpiry(t *testing.T) {
	s := newTestStore(t)
	r, err := s.CreateRun(domain.RunIntentCreateWiki, "写巫师3", "echo", "")
	if err != nil {
		t.Fatal(err)
	}
	ev, err := s.AppendRunEvent(r.ID, "run.started", map[string]any{"runId": r.ID})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Seq != 1 {
		t.Fatalf("seq = %d, want 1", ev.Seq)
	}
	ev2, err := s.AppendRunEvent(r.ID, "narrative", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if ev2.Seq != 2 {
		t.Fatalf("seq = %d, want 2", ev2.Seq)
	}

	events, err := s.ListRunEvents(r.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}

	// complete then expire via sweep with short window is hard without time travel;
	// just call ExpireStaleRuns with current state (running, last_active now → not expired)
	n, err := s.ExpireStaleRuns(7)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expired %d, want 0 for fresh run", n)
	}
	got, _ := s.GetRun(r.ID)
	if got.Status != domain.RunStatusRunning {
		t.Fatalf("status = %s", got.Status)
	}
}

func strPtr(s string) *string { return &s }

func errorAs(err error, target *ErrConflict) bool {
	if err == nil {
		return false
	}
	if cf, ok := err.(ErrConflict); ok {
		*target = cf
		return true
	}
	if strings.Contains(err.Error(), "version conflict") {
		*target = ErrConflict{Message: err.Error()}
		return true
	}
	return false
}

func isValidation(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(ErrValidation)
	return ok
}

// 系列 = 资料夹：只有挂在系列节点下的资料才允许关联条目。
func TestDocLinksOnlyAllowedInSeriesFolder(t *testing.T) {
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	universe, err := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindUniverse, Title: "最终幻想"})
	if err != nil {
		t.Fatal(err)
	}
	series, err := s.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindSeries, Title: "最终幻想：新水晶神话", ParentID: &universe.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	work, err := s.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindWork, Title: "最终幻想13", ParentID: &series.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 系列资料夹：可以关联
	seriesDoc, err := s.CreateDoc(series.ID, domain.CreateDocBody{Title: "神话基底解析"})
	if err != nil {
		t.Fatal(err)
	}
	links := []string{work.ID}
	if _, err := s.PatchDoc(seriesDoc.ID, domain.PatchDocBody{Links: &links}); err != nil {
		t.Fatalf("系列资料夹的资料应可关联: %v", err)
	}

	// 跨系列关联：不允许（本系列之外的条目不能挂）
	otherSeries, err := s.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindSeries, Title: "最终幻想15", ParentID: &universe.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := s.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindWork, Title: "王者之剑", ParentID: &otherSeries.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	crossLinks := []string{outsider.ID}
	if _, err := s.PatchDoc(seriesDoc.ID, domain.PatchDocBody{Links: &crossLinks}); !isValidation(err) {
		t.Fatalf("跨系列关联应被拒绝，err = %v", err)
	}

	// 作品资料夹：不允许关联
	workDoc, err := s.CreateDoc(work.ID, domain.CreateDocBody{Title: "关卡笔记"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchDoc(workDoc.ID, domain.PatchDocBody{Links: &links}); !isValidation(err) {
		t.Fatalf("作品资料夹的资料不应允许关联，err = %v", err)
	}

	// 宇宙资料夹：不允许关联
	universeDoc, err := s.CreateDoc(universe.ID, domain.CreateDocBody{Title: "IP 年表"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchDoc(universeDoc.ID, domain.PatchDocBody{Links: &links}); !isValidation(err) {
		t.Fatalf("宇宙资料夹的资料不应允许关联，err = %v", err)
	}
}
