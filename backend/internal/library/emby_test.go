package library

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeEmby 是客户端测试用的最小仿真：校验 X-Emby-Token，
// Items 按 StartIndex 分页（每页最多 2 条）以便测翻页。
type fakeEmby struct {
	apiKey string
	items  []map[string]any
}

func newFakeEmby(t *testing.T) (*httptest.Server, *fakeEmby) {
	t.Helper()
	f := &fakeEmby{apiKey: "emby-key", items: []map[string]any{
		{"Id": "mv1", "Name": "王者之剑", "Type": "Movie", "ProductionYear": 2016,
			"OriginalTitle": "Kingsglaive", "ImageTags": map[string]string{"Primary": "t1"},
			"Path": "/media/动画电影/王者之剑/王者之剑.mp4"},
		{"Id": "mv2", "Name": "无图电影", "Type": "Movie"},
		{"Id": "sr1", "Name": "葬送的芙莉莲", "Type": "Series", "SeriesName": "葬送的芙莉莲"},
	}}
	mux := http.NewServeMux()
	check := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("X-Emby-Token") != f.apiKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return false
		}
		return true
	}
	mux.HandleFunc("GET /Users", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"Id": "u-guest", "Name": "guest", "Policy": map[string]any{"IsAdministrator": false}},
			{"Id": "u-hao", "Name": "hao", "Policy": map[string]any{"IsAdministrator": true}},
		})
	})
	mux.HandleFunc("GET /Users/u-hao/Views", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": []map[string]any{
			{"Id": "v-movie", "Name": "动画电影", "CollectionType": "movies"},
			{"Id": "v-art", "Name": "第九艺术", "CollectionType": ""},
		}})
	})
	mux.HandleFunc("GET /Users/u-hao/Items", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		start := 0
		if s := r.URL.Query().Get("StartIndex"); s != "" {
			for _, c := range s {
				start = start*10 + int(c-'0')
			}
		}
		end := start + 2
		if end > len(f.items) {
			end = len(f.items)
		}
		page := []map[string]any{}
		if start < len(f.items) {
			page = f.items[start:end]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": page, "TotalRecordCount": len(f.items)})
	})
	mux.HandleFunc("GET /Users/u-hao/Items/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !check(w, r) {
			return
		}
		id := r.PathValue("id")
		for _, it := range f.items {
			if it["Id"] == id {
				_ = json.NewEncoder(w).Encode(it)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, f
}

func TestEmbyListViewsResolvesAdminUser(t *testing.T) {
	ts, _ := newFakeEmby(t)
	client, err := NewEmbyClient(ts.URL, "emby-key")
	if err != nil {
		t.Fatalf("NewEmbyClient: %v", err)
	}
	views, err := client.ListViews(context.Background())
	if err != nil {
		t.Fatalf("ListViews: %v", err)
	}
	if len(views) != 2 || views[0].Name != "动画电影" || views[0].CollectionType != "movies" {
		t.Fatalf("views = %+v, want 2 with 动画电影/movies", views)
	}
}

func TestEmbyListItemsPaginatesAndParsesFields(t *testing.T) {
	ts, _ := newFakeEmby(t)
	client, _ := NewEmbyClient(ts.URL, "emby-key")
	items, err := client.ListItems(context.Background(), "", "Series,Movie")
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3 (两页)", len(items))
	}
	if items[0].Name != "王者之剑" || items[0].ImageTags["Primary"] != "t1" {
		t.Fatalf("items[0] = %+v", items[0])
	}

	item, err := client.GetItem(context.Background(), "sr1")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Name != "葬送的芙莉莲" || item.SeriesName == nil || *item.SeriesName != "葬送的芙莉莲" {
		t.Fatalf("item = %+v", item)
	}
}

func TestEmbyCoverURLRequiresPrimaryTagAndCarriesKey(t *testing.T) {
	ts, _ := newFakeEmby(t)
	client, _ := NewEmbyClient(ts.URL, "emby-key")
	withTag := EmbyItem{ID: "mv1", ImageTags: map[string]string{"Primary": "t1"}}
	u := client.CoverURL(&withTag)
	if u == nil || !strings.Contains(*u, "api_key=emby-key") || !strings.Contains(*u, "tag=t1") {
		t.Fatalf("CoverURL = %v", u)
	}
	if client.CoverURL(&EmbyItem{ID: "mv2"}) != nil {
		t.Fatalf("no Primary tag should yield nil cover")
	}
	if got := client.ItemURL("mv1"); !strings.Contains(got, "#!/item?id=mv1") {
		t.Fatalf("ItemURL = %s", got)
	}
}

func TestEmbyRejectsMissingConfigAndBadKey(t *testing.T) {
	if _, err := NewEmbyClient("", "k"); err != ErrNotConfigured {
		t.Fatalf("empty url err = %v, want ErrNotConfigured", err)
	}
	ts, _ := newFakeEmby(t)
	client, _ := NewEmbyClient(ts.URL, "wrong")
	if _, err := client.ListViews(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("wrong key err = %v, want 401 surfaced", err)
	}
}
