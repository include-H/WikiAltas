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
	if u.ID == "" {
		t.Fatalf("expected an id, got %+v", u)
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
		ContentMd:       "# v2",
		Author:          domain.AuthorLLM,
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
		ContentMd:       "# v3",
		Author:          domain.AuthorHuman,
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
	// 资料挂在容器节点（宇宙 / 系列）上，单作没有自己的资料夹
	w, err := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindSeries, Title: "Work"})
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

// 资料夹挂在宇宙 / 系列上：里面的资料只能关联到**该节点子树内**的条目，不能跨出去。
// 系列可以嵌套系列，子系列的子孙也算在自己的子树里。
func TestDocLinksStayInsideTheirFolderSubtree(t *testing.T) {
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
	// 嵌套系列：新水晶神话 ▸ 最终幻想15 ▸ 最终幻想15（单作）
	nested, err := s.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindSeries, Title: "最终幻想15", ParentID: &series.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	nestedWork, err := s.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindWork, Title: "最终幻想15", ParentID: &nested.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 另一个宇宙下的节点：任何资料夹都不该关联到它
	otherUniverse, err := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindUniverse, Title: "猎魔人"})
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := s.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindWork, Title: "白狼崛起", ParentID: &otherUniverse.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	links := []string{work.ID}

	// 系列资料夹：自己子树内的单作、以及嵌套系列里的单作，都可以关联
	seriesDoc, err := s.CreateDoc(series.ID, domain.CreateDocBody{Title: "神话基底解析"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchDoc(seriesDoc.ID, domain.PatchDocBody{Links: &links}); err != nil {
		t.Fatalf("系列资料夹应可关联子树内的单作: %v", err)
	}
	nestedLinks := []string{nested.ID, nestedWork.ID}
	if _, err := s.PatchDoc(seriesDoc.ID, domain.PatchDocBody{Links: &nestedLinks}); err != nil {
		t.Fatalf("系列资料夹应可关联嵌套系列的子孙: %v", err)
	}

	// 跨子树关联：不允许
	crossLinks := []string{outsider.ID}
	if _, err := s.PatchDoc(seriesDoc.ID, domain.PatchDocBody{Links: &crossLinks}); !isValidation(err) {
		t.Fatalf("跨子树关联应被拒绝，err = %v", err)
	}

	// 宇宙资料夹：子树内的单作可以关联，别的宇宙的不行
	universeDoc, err := s.CreateDoc(universe.ID, domain.CreateDocBody{Title: "IP 年表"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchDoc(universeDoc.ID, domain.PatchDocBody{Links: &links}); err != nil {
		t.Fatalf("宇宙资料夹应可关联子树内的单作: %v", err)
	}
	if _, err := s.PatchDoc(universeDoc.ID, domain.PatchDocBody{Links: &crossLinks}); !isValidation(err) {
		t.Fatalf("宇宙资料夹不该关联到别的宇宙，err = %v", err)
	}
}

// 单作资料夹是「谁写了我」的视图：内容 = 祖先资料夹里关联到这篇的资料。
func TestWorkFolderShowsDocsLinkingToIt(t *testing.T) {
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
		Kind: domain.WorkKindSeries, Title: "新水晶神话", ParentID: &universe.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	work, err := s.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindWork, Title: "未来黎明", ParentID: &series.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 两篇资料：一篇关联这篇单作，一篇不关联
	linked, err := s.CreateDoc(series.ID, domain.CreateDocBody{Title: "迪诺设定考"})
	if err != nil {
		t.Fatal(err)
	}
	links := []string{work.ID}
	if _, err := s.PatchDoc(linked.ID, domain.PatchDocBody{Links: &links}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDoc(series.ID, domain.CreateDocBody{Title: "神话基底解析"}); err != nil {
		t.Fatal(err)
	}

	docs, err := s.ListDocsByWork(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ID != linked.ID {
		t.Fatalf("单作资料夹应只列出关联到它的资料，got %d 篇", len(docs))
	}

	// 系列资料夹仍是"挂在我名下的全部资料"
	seriesDocs, err := s.ListDocsByWork(series.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(seriesDocs) != 2 {
		t.Fatalf("系列资料夹应列出名下全部资料，got %d 篇", len(seriesDocs))
	}
}

// 方向性关系（改编/续作/衍生/重制/扩充）合起来必须是 DAG：
// A --sequel_to--> B 之后，B --sequel_to--> A 必须被拒——两条都留着的话，
// 读取侧会看到「A 是 B 的续作」和「B 是 A 的续作」两句互相矛盾的话。
// 而 same_series 天然是互相的，两边各存一条是正常的，不能一起拒掉。
func TestRelationCycleRejectedButMutualAllowed(t *testing.T) {
	s := newTestStore(t)
	a, err := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "B"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRelation(domain.CreateRelationBody{
		FromID: b.ID, ToID: a.ID, Type: domain.RelationSequelTo,
	}); err != nil {
		t.Fatalf("第一条方向边应通过: %v", err)
	}
	// 反向的续作边：成环，必须拒
	if _, err := s.CreateRelation(domain.CreateRelationBody{
		FromID: a.ID, ToID: b.ID, Type: domain.RelationSequelTo,
	}); err == nil {
		t.Fatal("反向的续作边（成环）应被拒绝")
	}
	// 但 same_series 成对出现是正常的
	if _, err := s.CreateRelation(domain.CreateRelationBody{
		FromID: a.ID, ToID: b.ID, Type: domain.RelationSameSeries,
	}); err != nil {
		t.Fatalf("对称关系不该被环检查拦下: %v", err)
	}
	if _, err := s.CreateRelation(domain.CreateRelationBody{
		FromID: b.ID, ToID: a.ID, Type: domain.RelationSameSeries,
	}); err != nil {
		t.Fatalf("对称关系反向也该通过: %v", err)
	}
}
