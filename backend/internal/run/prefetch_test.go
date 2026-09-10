package run

import (
	"strings"
	"testing"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/store"
)

// 预取要让模型"开口就知道目标对象长什么样"：节点元信息 + 现有章节 + 正文开头 +
// 下级版图 + 资料夹清单。这段文本会直接进入首条 user 消息。
func TestContextBriefForSeries(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ff, err := st.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindUniverse, Title: "最终幻想"})
	if err != nil {
		t.Fatal(err)
	}
	series, err := st.CreateWork(domain.CreateWorkBody{
		Kind: domain.WorkKindSeries, Title: "最终幻想：新水晶神话", ParentID: &ff.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := "# 最终幻想：新水晶神话\n\n> 说明：系列主文。\n\n## 1. 系列概览\n\n水晶神话是跨作品共享的神话基底。\n\n## 2. 作品构成\n\n包含 13 与 零式。\n"
	if _, err := st.PutWorkContent(series.ID, domain.PutContentBody{
		ContentMd: body, Author: domain.AuthorHuman,
	}); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"最终幻想13", "最终幻想 零式"} {
		if _, err := st.CreateWork(domain.CreateWorkBody{
			Kind: domain.WorkKindWork, Title: title, ParentID: &series.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateDoc(series.ID, domain.CreateDocBody{Title: "神话基底梳理"}); err != nil {
		t.Fatal(err)
	}

	brief := buildContextBrief(st, series.ID, "")
	for _, want := range []string{
		"最终幻想：新水晶神话",
		"series",
		"所属层级：最终幻想(universe)",
		"现有章节：1. 系列概览 / 2. 作品构成",
		"正文开头",
		"下级节点（2）",
		"最终幻想13(work,stub)",
		"最终幻想 零式(work,stub)",
		"资料夹（1 份非标资料）：神话基底梳理",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief 缺少 %q\n---\n%s", want, brief)
		}
	}
}

// 空节点必须被明确标出来，避免模型对着 stub 编内容。
func TestContextBriefMarksEmptyNode(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w, err := st.CreateWork(domain.CreateWorkBody{Kind: domain.WorkKindWork, Title: "王者之剑"})
	if err != nil {
		t.Fatal(err)
	}
	brief := buildContextBrief(st, w.ID, "")
	if !strings.Contains(brief, "空节点") {
		t.Fatalf("空节点提示缺失:\n%s", brief)
	}
}
