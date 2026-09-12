package tools_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikiatlas/backend/internal/store"
	"wikiatlas/backend/internal/tools"
)

// 真实数据回归：在测试库副本上，对《未来黎明》的真题记做一次 edit。
// 这条路径以前不存在——改题记只能整篇 write_content 重写 13054 字。
// 测试库不在仓库里（本地联调数据），缺失时跳过。
func TestRealDataEpigraphEdit(t *testing.T) {
	src := filepath.Join("..", "..", "data", "wikiatlas-test.db")
	if _, err := os.Stat(src); err != nil {
		t.Skip("本地测试库不存在，跳过真实数据回归")
	}
	dir := t.TempDir()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(src + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("copy %s: %v", suffix, err)
		}
		if err := os.WriteFile(dir+"/real.db"+suffix, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(dir + "/real.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	hits, err := st.Search("未来黎明", "work", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("找不到《未来黎明》: %v", err)
	}
	id := hits[0].ID
	before, _ := st.GetWork(id)
	if before.ContentMd == nil {
		t.Fatal("正文为空")
	}
	old := *before.ContentMd

	const signature = "——Ardyn Izunia《FINAL FANTASY XV -The Dawn of the Future-》第一章「圣者的迷惘」"
	if !strings.Contains(old, signature) {
		t.Fatalf("题记署名行与预期不符，前 500 字：\n%s", old[:500])
	}

	d, _ := newDeps(t)
	d.Store = st
	reg := tools.NewLibrarianRegistry(d)
	res := call(t, reg, "edit", map[string]any{
		"targetId":  id,
		"oldString": signature,
		"newString": signature + "（改）",
		"summary":   "题记署名行修订",
	})
	if res["ok"] != true {
		t.Fatalf("edit: %v", res)
	}
	if res["replacements"].(float64) != 1 {
		t.Fatalf("应恰好替换 1 处: %v", res["replacements"])
	}
	after, _ := st.GetWork(id)
	if after.ContentVer != before.ContentVer+1 {
		t.Fatalf("版本应 +1: %d → %d", before.ContentVer, after.ContentVer)
	}
	got := *after.ContentMd
	// 整篇只多了「（改）」三个字：证明别的章节一个字符都没被重打
	if delta := len([]rune(got)) - len([]rune(old)); delta != 3 {
		t.Fatalf("正文长度变化 %d，应只 +3", delta)
	}
	oldCh, newCh := chaptersOf(old), chaptersOf(got)
	if len(oldCh) != len(newCh) {
		t.Fatalf("章节数变了: %d → %d", len(oldCh), len(newCh))
	}
	for heading, body := range oldCh {
		if newCh[heading] != body {
			t.Fatalf("章节「%s」被改动了", heading)
		}
	}
}

// chaptersOf 按 ## 标题行切块，标题做 key、正文做 value。
func chaptersOf(md string) map[string]string {
	out := map[string]string{}
	cur := ""
	for _, l := range strings.Split(md, "\n") {
		if strings.HasPrefix(l, "## ") {
			cur = l
			out[cur] = ""
			continue
		}
		if cur != "" {
			out[cur] += l + "\n"
		}
	}
	return out
}

// read_work 必须能一次读完整篇。13054 字 ≈ 39KB，远小于它自己的 30k rune 上限，
// 但仍然被执行器的旧截断（8000 字节 ≈ 2666 汉字）切成 6 次读——
// 模型因此读不全正文，只能凭记忆重写整篇。
func TestRealDataReadWorkReturnsWholeEntry(t *testing.T) {
	src := filepath.Join("..", "..", "data", "wikiatlas-test.db")
	if _, err := os.Stat(src); err != nil {
		t.Skip("本地测试库不存在，跳过真实数据回归")
	}
	dir := t.TempDir()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/real.db", b, 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir + "/real.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	hits, err := st.Search("未来黎明", "work", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("找不到《未来黎明》: %v", err)
	}
	w, _ := st.GetWork(hits[0].ID)
	want := len([]rune(*w.ContentMd))

	d, _ := newDeps(t)
	d.Store = st
	reg := tools.NewLibrarianRegistry(d)
	res := call(t, reg, "read_work", map[string]any{"id": hits[0].ID})
	got, _ := res["work"].(map[string]any)
	md, _ := got["contentMd"].(string)
	if n := len([]rune(md)); n != want {
		t.Fatalf("read_work 只返回 %d 字，正文有 %d 字——又被截断了", n, want)
	}
	if !strings.Contains(md, ":::epigraph") {
		t.Fatal("题记不在返回里：说明开头被切了")
	}
}
