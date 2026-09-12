package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newFakeEmbyAPI 是接口测试用的 Emby 仿真：4 个库（两个正片、两个混杂），
// Items 按 ParentId 分表返回，BoxSet 成员单独一张表。
func newFakeEmbyAPI(t *testing.T) *httptest.Server {
	t.Helper()
	views := []map[string]any{
		{"Id": "v-movie", "Name": "电影", "CollectionType": "movies"},
		{"Id": "v-drama", "Name": "番剧", "CollectionType": "tvshows"},
		{"Id": "v-mixed", "Name": "第九艺术", "CollectionType": ""},
		{"Id": "v-ost", "Name": "OST", "CollectionType": "music"},
	}
	movieA := map[string]any{"Id": "mv007a", "Name": "007：大战皇家赌场", "Type": "Movie",
		"ProductionYear": 2006, "ImageTags": map[string]string{"Primary": "ta"}}
	movieB := map[string]any{"Id": "mv007b", "Name": "007：大破天幕杀机", "Type": "Movie", "ProductionYear": 2012}
	boxset := map[string]any{"Id": "bs007", "Name": "007（系列）", "Type": "BoxSet"}
	frieren := map[string]any{"Id": "sr-frieren", "Name": "葬送的芙莉莲", "Type": "Series", "ProductionYear": 2023,
		"ImageTags": map[string]string{"Primary": "tf"}}
	art1 := map[string]any{"Id": "art1", "Type": "Movie",
		"Name": "《德军总部》系列到底讲了什么？20分钟带你看完游戏《重返德军总部》（上）",
		"Path": "/mnt/media/第九艺术/德军总部(系列)/上.mp4"}
	art2 := map[string]any{"Id": "art2", "Name": "《德军总部》系列到底讲了什么？30分钟看完（中）",
		"Type": "Movie", "Path": "/mnt/media/第九艺术/德军总部(系列)/中.mp4"}
	album := map[string]any{"Id": "al-wukong", "Name": "黑神话：悟空 游戏音乐集", "Type": "MusicAlbum", "ProductionYear": 2024}
	byParent := map[string][]map[string]any{
		"v-movie": {movieA, movieB, boxset},
		"v-drama": {frieren},
		"v-mixed": {art1, art2},
		"v-ost":   {album},
		"bs007":   {movieA, movieB},
	}
	all := []map[string]any{movieA, movieB, boxset, frieren, art1, art2, album}

	typeOK := func(it map[string]any, types string) bool {
		if types == "" {
			return true
		}
		for _, tt := range strings.Split(types, ",") {
			if tt == it["Type"] {
				return true
			}
		}
		return false
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /Users", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"Id": "u1", "Name": "hao", "Policy": map[string]any{"IsAdministrator": true}},
		})
	})
	mux.HandleFunc("GET /Users/u1/Views", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": views})
	})
	mux.HandleFunc("GET /Users/u1/Items", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		src := all
		if p := q.Get("ParentId"); p != "" {
			src = byParent[p]
		}
		page := []map[string]any{}
		for _, it := range src {
			if typeOK(it, q.Get("IncludeItemTypes")) {
				page = append(page, it)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": page, "TotalRecordCount": len(page)})
	})
	mux.HandleFunc("GET /Users/u1/Items/{id}", func(w http.ResponseWriter, r *http.Request) {
		for _, it := range all {
			if it["Id"] == r.PathValue("id") {
				_ = json.NewEncoder(w).Encode(it)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// configureEmbyLibs 把测试 Emby 配进设置，并按角色标好库。
func configureEmbyLibs(t *testing.T, ts *httptest.Server, embyURL string) {
	t.Helper()
	doJSON(t, "PUT", ts.URL+"/api/settings", map[string]any{
		"library": map[string]any{
			"embyUrl":    embyURL,
			"embyApiKey": "k",
			"embyLibraryRoles": []map[string]any{
				{"id": "v-movie", "name": "电影", "role": "work"},
				{"id": "v-drama", "name": "番剧", "role": "work"},
				{"id": "v-mixed", "name": "第九艺术", "role": "mixed"},
				{"id": "v-ost", "name": "OST", "role": "mixed"},
			},
		},
	}, 200)
}

func TestEmbySuggestArchiveByLibraryRole(t *testing.T) {
	emby := newFakeEmbyAPI(t)
	ts, _ := newTestServer(t)
	configureEmbyLibs(t, ts, emby.URL)

	views := doJSON(t, "GET", ts.URL+"/api/library/emby/views", nil, 200)
	if vs, _ := views["views"].([]any); len(vs) != 4 {
		t.Fatalf("views = %#v, want 4", views["views"])
	}

	// 007 系列页：BoxSet「007（系列）」展开出两条电影建议
	series := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "series", "title": "007"}, 201)
	sid := series["id"].(string)
	sug := doJSON(t, "GET", ts.URL+"/api/library/emby/suggest?workId="+sid, nil, 200)
	list, _ := sug["suggestions"].([]any)
	if len(list) != 2 {
		t.Fatalf("007 suggestions = %#v, want 2", sug["suggestions"])
	}
	first, _ := list[0].(map[string]any)
	if first["kind"] != "movie" || !strings.Contains(first["title"].(string), "007") {
		t.Fatalf("first suggestion = %#v", first)
	}

	// 建档 → 子节点 medium=movie，链挂上；建议少一条
	arch := doJSON(t, "POST", ts.URL+"/api/library/emby/archive", map[string]any{"workId": sid, "publicId": "mv007a"}, 201)
	work, _ := arch["work"].(map[string]any)
	if work["medium"] != "movie" || work["title"] != "007：大战皇家赌场" {
		t.Fatalf("archived work = %#v", work)
	}
	sug2 := doJSON(t, "GET", ts.URL+"/api/library/emby/suggest?workId="+sid, nil, 200)
	if l2, _ := sug2["suggestions"].([]any); len(l2) != 1 {
		t.Fatalf("after archive suggestions = %#v, want 1", sug2["suggestions"])
	}

	// 同名 Series → kind=tv，建档 medium=tv
	series2 := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "series", "title": "葬送的芙莉莲"}, 201)
	sug3 := doJSON(t, "GET", ts.URL+"/api/library/emby/suggest?workId="+series2["id"].(string), nil, 200)
	l3, _ := sug3["suggestions"].([]any)
	if len(l3) != 1 || l3[0].(map[string]any)["kind"] != "tv" {
		t.Fatalf("frieren suggestions = %#v", sug3["suggestions"])
	}
	arch2 := doJSON(t, "POST", ts.URL+"/api/library/emby/archive", map[string]any{
		"workId": series2["id"].(string), "publicId": "sr-frieren",
	}, 201)
	if w2, _ := arch2["work"].(map[string]any); w2["medium"] != "tv" {
		t.Fatalf("series medium = %v, want tv", w2["medium"])
	}
}

func TestEmbyRelatedLinkMultipleAndUnlink(t *testing.T) {
	emby := newFakeEmbyAPI(t)
	ts, _ := newTestServer(t)
	configureEmbyLibs(t, ts, emby.URL)

	work := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "work", "title": "德军总部", "medium": "game"}, 201)
	wid := work["id"].(string)

	// 混杂库候选：两条解说；未关联
	rel := doJSON(t, "GET", ts.URL+"/api/library/emby/related?workId="+wid, nil, 200)
	items, _ := rel["related"].([]any)
	if len(items) != 2 {
		t.Fatalf("related = %#v, want 2", rel["related"])
	}
	if items[0].(map[string]any)["linked"] != false {
		t.Fatalf("expected unlinked, got %#v", items[0])
	}

	// 一个节点可以挂多条：两条解说 + 一张专辑
	l1 := doJSON(t, "POST", ts.URL+"/api/library/emby/link", map[string]any{"workId": wid, "publicId": "art1"}, 201)
	doJSON(t, "POST", ts.URL+"/api/library/emby/link", map[string]any{"workId": wid, "publicId": "art2"}, 201)
	doJSON(t, "POST", ts.URL+"/api/library/emby/link", map[string]any{"workId": wid, "publicId": "al-wukong"}, 201)
	link1, _ := l1["link"].(map[string]any)
	link1ID, _ := link1["id"].(string)

	// 重复关联同一条 → 409
	doJSON(t, "POST", ts.URL+"/api/library/emby/link", map[string]any{"workId": wid, "publicId": "art1"}, 409)

	// 别的节点抢已关联条目 → 409
	other := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "work", "title": "另一个游戏"}, 201)
	doJSON(t, "POST", ts.URL+"/api/library/emby/link", map[string]any{"workId": other["id"].(string), "publicId": "art1"}, 409)

	// related 反映挂链状态
	rel2 := doJSON(t, "GET", ts.URL+"/api/library/emby/related?workId="+wid, nil, 200)
	items2, _ := rel2["related"].([]any)
	linkedCount := 0
	for _, it := range items2 {
		if it.(map[string]any)["linked"] == true && it.(map[string]any)["linkedWorkId"] == wid {
			linkedCount++
		}
	}
	if linkedCount != 2 {
		t.Fatalf("linked entries = %d, want 2", linkedCount)
	}

	// 按链接 id 解除（同一节点多链，不能按 workId 一刀切）
	doJSON(t, "DELETE", ts.URL+"/api/library/emby/link?id="+link1ID, nil, 200)
	doJSON(t, "DELETE", ts.URL+"/api/library/emby/link?id="+link1ID, nil, 404)

	// 搜索：命中混杂库条目与专辑
	search1 := doJSON(t, "GET", ts.URL+"/api/library/emby/search?q="+urlEncode("德军总部"), nil, 200)
	if entries, _ := search1["entries"].([]any); len(entries) != 2 {
		t.Fatalf("search 德军总部 = %#v, want 2", search1["entries"])
	}
	search2 := doJSON(t, "GET", ts.URL+"/api/library/emby/search?q="+urlEncode("黑神话"), nil, 200)
	e2, _ := search2["entries"].([]any)
	if len(e2) != 1 || e2[0].(map[string]any)["kind"] != "album" {
		t.Fatalf("search 黑神话 = %#v", search2["entries"])
	}

	// 合集（BoxSet）也搜得到、能整盒挂到集合节点上（007 ↔ 詹姆斯·邦德（系列）式的对不上号场景）
	search3 := doJSON(t, "GET", ts.URL+"/api/library/emby/search?q="+urlEncode("007"), nil, 200)
	foundCollection := false
	for _, it := range search3["entries"].([]any) {
		if it.(map[string]any)["kind"] == "collection" {
			foundCollection = true
		}
	}
	if !foundCollection {
		t.Fatalf("search 007 should include boxset entry: %#v", search3["entries"])
	}
	seriesNode := doJSON(t, "POST", ts.URL+"/api/works", map[string]any{"kind": "series", "title": "007"}, 201)
	doJSON(t, "POST", ts.URL+"/api/library/emby/link", map[string]any{
		"workId": seriesNode["id"].(string), "publicId": "bs007",
	}, 201)
}

func TestEmbyEndpointsRequireLogin(t *testing.T) {
	ts, _ := newTestServer(t)
	guest := &http.Client{}
	for _, probe := range []struct{ method, url string }{
		{"GET", ts.URL + "/api/library/emby/views"},
		{"GET", ts.URL + "/api/library/emby/suggest?workId=x"},
		{"POST", ts.URL + "/api/library/emby/archive"},
		{"GET", ts.URL + "/api/library/emby/related?workId=x"},
		{"GET", ts.URL + "/api/library/emby/search?q=x"},
		{"POST", ts.URL + "/api/library/emby/link"},
		{"DELETE", ts.URL + "/api/library/emby/link?id=x"},
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

// urlEncode 避免在测试里 import net/url 的名字冲突（doJSON 直接用完整 URL）。
func urlEncode(s string) string {
	out := make([]byte, 0, len(s)*3)
	for _, b := range []byte(s) {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_' || b == '.' || b == '~' {
			out = append(out, b)
		} else {
			out = append(out, '%', "0123456789ABCDEF"[b>>4], "0123456789ABCDEF"[b&0xF])
		}
	}
	return string(out)
}
