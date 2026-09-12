package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// newFakeKomgaAPI：系列页／书本格式／缩略图／PATCH 记账。
// 数据：S-comic 葬送的芙莉莲（cbz，书名含系列名）= 漫画系列；
// S-novel 龙与虎（epub，书名含系列名）= 小说系列；
// S-drawer 单行本（epub，书名不含系列名）= 抽屉 → 两个单册条目。
func newFakeKomgaAPI(t *testing.T) (*httptest.Server, *fakeKomgaState) {
	t.Helper()
	f := &fakeKomgaState{patches: map[string]map[string]any{}}
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("X-API-Key") != "komga-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	seriesList := []map[string]any{
		{"id": "S-comic", "name": "葬送的芙莉莲"},
		{"id": "S-novel", "name": "龙与虎"},
		{"id": "S-lns", "name": "为美好的世界献上祝福！"},
		{"id": "S-num", "name": "爆笑校园"},
		{"id": "S-drawer", "name": "单行本"},
	}
	booksBySeries := map[string][]map[string]any{
		// 卷名是「Vol.01」形态（真库里葬送的芙莉莲就这样）：必须判成分卷
		"S-comic": {
			{"id": "C1", "name": "Vol.01", "media": map[string]any{"mediaType": "application/vnd.comicbook+zip"}, "metadata": map[string]any{"title": "Vol.01"}},
			{"id": "C2", "name": "Vol.02", "media": map[string]any{"mediaType": "application/vnd.comicbook+zip"}, "metadata": map[string]any{"title": "Vol.02"}},
			{"id": "C3", "name": "贺图", "media": map[string]any{"mediaType": "application/vnd.comicbook+zip"}, "metadata": map[string]any{"title": "贺图"}},
		},
		"S-novel": {
			{"id": "N1", "name": "龙与虎 第1卷", "media": map[string]any{"mediaType": "application/epub+zip"}, "metadata": map[string]any{"title": "龙与虎 第1卷"}},
			{"id": "N2", "name": "龙与虎 第2卷", "media": map[string]any{"mediaType": "application/epub+zip"}, "metadata": map[string]any{"title": "龙与虎 第2卷"}},
		},
		"S-lns": {
			{"id": "L1", "name": "为美好的世界献上祝福！ 第1卷", "media": map[string]any{"mediaType": "application/vnd.comicbook+zip"}, "metadata": map[string]any{"title": "为美好的世界献上祝福！ 第1卷"}},
			{"id": "L2", "name": "为美好的世界献上祝福！ 第2卷", "media": map[string]any{"mediaType": "application/vnd.comicbook+zip"}, "metadata": map[string]any{"title": "为美好的世界献上祝福！ 第2卷"}},
		},
		// 卷名就是纯数字：必须判成分卷（系列一条），不能误拆成抽屉
		"S-num": {
			{"id": "M1", "name": "1", "media": map[string]any{"mediaType": "application/vnd.comicbook+zip"}, "metadata": map[string]any{"title": "1"}},
			{"id": "M2", "name": "2", "media": map[string]any{"mediaType": "application/vnd.comicbook+zip"}, "metadata": map[string]any{"title": "2"}},
		},
		"S-drawer": {
			{"id": "D1", "name": "哈利·波特（全集）", "media": map[string]any{"mediaType": "application/epub+zip"}, "metadata": map[string]any{"title": "哈利·波特（全集）"}},
			{"id": "D2", "name": "冰与火之歌1-5卷", "media": map[string]any{"mediaType": "application/epub+zip"}, "metadata": map[string]any{"title": "冰与火之歌1-5卷"}},
		},
	}
	mux.HandleFunc("GET /api/v1/series", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		write(w, map[string]any{"content": seriesList, "totalPages": 1})
	})
	mux.HandleFunc("GET /api/v1/series/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		for _, s := range seriesList {
			if s["id"] == r.PathValue("id") {
				write(w, s)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /api/v1/series/{id}/books", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		write(w, map[string]any{"content": booksBySeries[r.PathValue("id")], "totalPages": 1})
	})
	mux.HandleFunc("GET /api/v1/series/{id}/thumbnail", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("cover-series"))
	})
	mux.HandleFunc("GET /api/v1/books/{id}/thumbnail", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("cover-book"))
	})
	mux.HandleFunc("GET /api/v1/books/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		for _, books := range booksBySeries {
			for _, b := range books {
				if b["id"] == r.PathValue("id") {
					write(w, b)
					return
				}
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("PATCH /api/v1/series/{id}/metadata", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.patches["series:"+r.PathValue("id")] = body
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PATCH /api/v1/books/{id}/metadata", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.patches["book:"+r.PathValue("id")] = body
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, f
}

type fakeKomgaState struct {
	mu      sync.Mutex
	patches map[string]map[string]any
}

func configureKomga(t *testing.T, ts *httptest.Server, komgaURL string) {
	t.Helper()
	doJSON(t, "PUT", ts.URL+"/api/settings", map[string]any{
		"library": map[string]any{"komgaUrl": komgaURL, "komgaApiKey": "komga-key"},
	}, 200)
}

func TestKomgaSuggestByFormatAndKindHint(t *testing.T) {
	komga, _ := newFakeKomgaAPI(t)
	ts, _ := newTestServer(t)
	configureKomga(t, ts, komga.URL)

	// 同名系列：漫画
	node := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "series", "title": "葬送的芙莉莲"}, 201)
	sug := doJSON(t, "GET", ts.URL+"/api/library/komga/suggest?workId="+node["id"].(string), nil, 200)
	list, _ := sug["suggestions"].([]any)
	if len(list) != 1 {
		t.Fatalf("suggest = %#v, want 1", sug["suggestions"])
	}
	first, _ := list[0].(map[string]any)
	if first["format"] != "comic" || first["kind"] != "book" {
		t.Fatalf("first = %#v, want comic/book", first)
	}
	if cover, _ := first["coverImage"].(string); !strings.Contains(cover, "/api/library/komga/cover?") || !strings.Contains(cover, "id=S-comic") {
		t.Fatalf("cover = %q, want proxy path", first["coverImage"])
	}

	// (小说) 后缀：约束同型 → 龙与虎(小说) 命中；龙与虎(漫画) 不命中
	nNovel := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "series", "title": "龙与虎(小说)"}, 201)
	sug2 := doJSON(t, "GET", ts.URL+"/api/library/komga/suggest?workId="+nNovel["id"].(string), nil, 200)
	if l2, _ := sug2["suggestions"].([]any); len(l2) != 1 || l2[0].(map[string]any)["format"] != "novel" {
		t.Fatalf("novel suggest = %#v", sug2["suggestions"])
	}
	nComic := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "series", "title": "龙与虎(漫画)"}, 201)
	sug3 := doJSON(t, "GET", ts.URL+"/api/library/komga/suggest?workId="+nComic["id"].(string), nil, 200)
	if l3, _ := sug3["suggestions"].([]any); len(l3) != 0 {
		t.Fatalf("comic hint should not match novel series: %#v", sug3["suggestions"])
	}

	// 系列名尾部的 「！」 容错：为美好的世界献上祝福(漫画) ↔ 为美好的世界献上祝福！
	nLns := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "series", "title": "为美好的世界献上祝福(漫画)"}, 201)
	sugLns := doJSON(t, "GET", ts.URL+"/api/library/komga/suggest?workId="+nLns["id"].(string), nil, 200)
	if ll, _ := sugLns["suggestions"].([]any); len(ll) != 1 || ll[0].(map[string]any)["format"] != "comic" {
		t.Fatalf("lns suggest = %#v", sugLns["suggestions"])
	}

	// 卷名是纯数字的系列不许误拆成抽屉：爆笑校园 → 系列一条（comic）
	nNum := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "series", "title": "爆笑校园"}, 201)
	sugNum := doJSON(t, "GET", ts.URL+"/api/library/komga/suggest?workId="+nNum["id"].(string), nil, 200)
	if ln, _ := sugNum["suggestions"].([]any); len(ln) != 1 || ln[0].(map[string]any)["publicId"] != "S-num" {
		t.Fatalf("numeric-volume suggest = %#v, want the series itself", sugNum["suggestions"])
	}

	// 抽屉：单行本里的单册能被节点「哈利·波特」命中，cover 带 type=book
	nHP := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "series", "title": "哈利·波特"}, 201)
	sug4 := doJSON(t, "GET", ts.URL+"/api/library/komga/suggest?workId="+nHP["id"].(string), nil, 200)
	l4, _ := sug4["suggestions"].([]any)
	if len(l4) != 1 {
		t.Fatalf("drawer suggest = %#v", sug4["suggestions"])
	}
	d0, _ := l4[0].(map[string]any)
	if !strings.Contains(d0["title"].(string), "哈利·波特") || !strings.Contains(d0["coverImage"].(string), "type=book") {
		t.Fatalf("drawer entry = %#v", d0)
	}
	if urlStr, _ := d0["url"].(string); !strings.Contains(urlStr, "/book/D1") {
		t.Fatalf("drawer url = %q, want /book/D1", d0["url"])
	}
}

func TestKomgaArchiveLinkMultiAndPush(t *testing.T) {
	komga, state := newFakeKomgaAPI(t)
	ts, _ := newTestServer(t)
	configureKomga(t, ts, komga.URL)

	// 建档：media=book；链接后建议消失
	node := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "series", "title": "葬送的芙莉莲"}, 201)
	arch := doJSON(t, "POST", ts.URL+"/api/library/komga/archive", map[string]any{
		"workId": node["id"].(string), "publicId": "S-comic",
	}, 201)
	work, _ := arch["work"].(map[string]any)
	if work["medium"] != "book" {
		t.Fatalf("archived medium = %v, want book", work["medium"])
	}
	childID := work["id"].(string)
	sug := doJSON(t, "GET", ts.URL+"/api/library/komga/suggest?workId="+node["id"].(string), nil, 200)
	if l, _ := sug["suggestions"].([]any); len(l) != 0 {
		t.Fatalf("after archive = %#v", sug["suggestions"])
	}

	// 子节点上先推系列一条；再挂一个抽屉单册（D1）成多链；重复挂 → 409
	doJSON(t, "PUT", ts.URL+"/api/works/"+childID+"/content", map[string]any{
		"contentMd": "# 葬送的芙莉莲\n\n这是自动提取的简介。\n\n## 概览\n\n正文。", "author": "human",
	}, 200)
	push1 := doJSON(t, "POST", ts.URL+"/api/library/komga/push", map[string]any{"workId": childID}, 200)
	if push1["count"] != float64(1) {
		t.Fatalf("push1 = %#v, want count 1", push1)
	}
	doJSON(t, "POST", ts.URL+"/api/library/komga/link", map[string]any{"workId": childID, "publicId": "D1"}, 201)
	doJSON(t, "POST", ts.URL+"/api/library/komga/link", map[string]any{"workId": childID, "publicId": "D1"}, 409)

	// 反哺：系列 + 单册各自 PATCH 到简介
	push := doJSON(t, "POST", ts.URL+"/api/library/komga/push", map[string]any{"workId": childID}, 200)
	if push["count"] != float64(2) {
		t.Fatalf("push = %#v, want count 2", push)
	}
	if state.patches["series:S-comic"]["summary"] != "这是自动提取的简介。" || state.patches["book:D1"]["summary"] != "这是自动提取的简介。" {
		t.Fatalf("patches = %#v", state.patches)
	}

	// 搜索：抽屉单册也在结果里、带 linked
	search := doJSON(t, "GET", ts.URL+"/api/library/komga/search?q="+urlEncode("哈利"), nil, 200)
	entries, _ := search["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["linked"] != true {
		t.Fatalf("search = %#v", search["entries"])
	}
}

func TestKomgaCoverProxy(t *testing.T) {
	komga, _ := newFakeKomgaAPI(t)
	ts, _ := newTestServer(t)
	configureKomga(t, ts, komga.URL)

	// 匿名：401（withAuth 默认拒绝）
	guest := &http.Client{}
	resp, err := guest.Get(ts.URL + "/api/library/komga/cover?id=S-comic")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anon cover = %d, want 401", resp.StatusCode)
	}

	// 登录后：系列与单册都能取到字节
	authed := clientFor(ts.URL)
	for _, probe := range []struct{ url, want string }{
		{ts.URL + "/api/library/komga/cover?id=S-comic", "cover-series"},
		{ts.URL + "/api/library/komga/cover?id=D1&type=book", "cover-book"},
	} {
		resp, err := authed.Get(probe.url)
		if err != nil {
			t.Fatal(err)
		}
		body := make([]byte, 64)
		n, _ := resp.Body.Read(body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body[:n]) != probe.want {
			t.Fatalf("cover %s = %d %q", probe.url, resp.StatusCode, body[:n])
		}
	}
}

func TestKomgaPoolIncludesKomga(t *testing.T) {
	komga, _ := newFakeKomgaAPI(t)
	ts, _ := newTestServer(t)
	configureKomga(t, ts, komga.URL)

	// 扫描落清单 → 池子读清单
	doJSON(t, "POST", ts.URL+"/api/library/scan", nil, 200)
	pool := doJSON(t, "GET", ts.URL+"/api/library/pool", nil, 200)

	findGroup := func(groups []any, container string) map[string]any {
		for _, g := range groups {
			m, _ := g.(map[string]any)
			if m["source"] == "komga" && m["containerTitle"] == container {
				return m
			}
		}
		return nil
	}
	groups, _ := pool["groups"].([]any)
	seriesGroup := findGroup(groups, "葬送的芙莉莲")
	if seriesGroup == nil {
		t.Fatalf("komga 系列组缺失: %#v", pool["groups"])
	}
	items, _ := seriesGroup["unlinked"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "葬送的芙莉莲" {
		t.Fatalf("系列组条目 = %#v", seriesGroup["unlinked"])
	}
	drawerGroup := findGroup(groups, "单行本")
	if drawerGroup == nil {
		t.Fatalf("抽屉组缺失: %#v", pool["groups"])
	}
	drawerItems, _ := drawerGroup["unlinked"].([]any)
	foundHP := false
	for _, it := range drawerItems {
		if strings.Contains(it.(map[string]any)["title"].(string), "哈利·波特") {
			foundHP = true
		}
	}
	if !foundHP {
		t.Fatalf("抽屉单册缺失: %#v", drawerGroup["unlinked"])
	}
	if sc, _ := pool["sourcesConfigured"].(map[string]any); sc["komga"] != true {
		t.Fatalf("sourcesConfigured = %#v", pool["sourcesConfigured"])
	}
}

func TestKomgaEndpointsRequireLogin(t *testing.T) {
	ts, _ := newTestServer(t)
	guest := &http.Client{}
	for _, probe := range []struct{ method, url string }{
		{"GET", ts.URL + "/api/library/komga/suggest?workId=x"},
		{"POST", ts.URL + "/api/library/komga/archive"},
		{"GET", ts.URL + "/api/library/komga/search?q=x"},
		{"POST", ts.URL + "/api/library/komga/link"},
		{"DELETE", ts.URL + "/api/library/komga/link?id=x"},
		{"POST", ts.URL + "/api/library/komga/push"},
		{"GET", ts.URL + "/api/library/komga/cover?id=x"},
	} {
		req, _ := http.NewRequest(probe.method, probe.url, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		resp, err := guest.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", probe.method, probe.url, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401", probe.method, probe.url, resp.StatusCode)
		}
	}
}
