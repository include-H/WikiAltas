package library

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeKomga：两页系列、按 id 区分书本格式、缩略图 404 特例、PATCH 记账。
type fakeKomga struct {
	mu      sync.Mutex
	patches map[string]map[string]any
	apiKey  string
}

func newFakeKomga(t *testing.T) (*httptest.Server, *fakeKomga) {
	t.Helper()
	f := &fakeKomga{patches: map[string]map[string]any{}, apiKey: "kk"}
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("X-API-Key") != f.apiKey {
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	write := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	mux.HandleFunc("GET /api/v1/series", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		page := r.URL.Query().Get("page")
		all := []map[string]any{
			{"id": "S1", "name": "葬送的芙莉莲", "booksCount": 2},
			{"id": "S2", "name": "龙与虎", "booksCount": 2},
		}
		content := []map[string]any{}
		if page == "0" {
			content = all[:1]
		} else if page == "1" {
			content = all[1:]
		}
		write(w, map[string]any{"content": content, "totalPages": 2})
	})
	mux.HandleFunc("GET /api/v1/series/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		id := r.PathValue("id")
		for _, s := range []map[string]any{{"id": "S1", "name": "葬送的芙莉莲"}, {"id": "S2", "name": "龙与虎"}} {
			if s["id"] == id {
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
		id := r.PathValue("id")
		books := []map[string]any{}
		switch id {
		case "S1":
			for i := 1; i <= 2; i++ {
				books = append(books, map[string]any{
					"id": "B" + string(rune('0'+i)), "name": "葬送的芙莉莲 第" + string(rune('0'+i)) + "卷",
					"media":    map[string]any{"mediaType": "application/vnd.comicbook+zip"},
					"metadata": map[string]any{"title": "葬送的芙莉莲 第" + string(rune('0'+i)) + "卷"},
				})
			}
		case "S2":
			for i := 1; i <= 2; i++ {
				books = append(books, map[string]any{
					"id": "N" + string(rune('0'+i)), "name": "龙与虎 第" + string(rune('0'+i)) + "卷",
					"media":    map[string]any{"mediaType": "application/epub+zip"},
					"metadata": map[string]any{"title": "龙与虎 第" + string(rune('0'+i)) + "卷"},
				})
			}
		}
		write(w, map[string]any{"content": books, "totalPages": 1})
	})
	mux.HandleFunc("GET /api/v1/series/{id}/thumbnail", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.PathValue("id") == "S-none" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("imgbytes"))
	})
	mux.HandleFunc("GET /api/v1/books/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.PathValue("id") == "N1" {
			write(w, map[string]any{"id": "N1", "name": "龙与虎 第1卷"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /api/v1/books/{id}/thumbnail", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("bookimg"))
	})
	mux.HandleFunc("PATCH /api/v1/series/{id}/metadata", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.patches["series:"+r.PathValue("id")] = body
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("PATCH /api/v1/books/{id}/metadata", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.patches["book:"+r.PathValue("id")] = body
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, f
}

func TestKomgaClientNormalizesBaseURL(t *testing.T) {
	c, err := NewKomgaClient("http://host:8082/api/v1/", "k")
	if err != nil {
		t.Fatalf("NewKomgaClient: %v", err)
	}
	if c.BaseURL() != "http://host:8082" {
		t.Fatalf("BaseURL = %s, want normalized", c.BaseURL())
	}
	if _, err := NewKomgaClient("", "k"); err != ErrNotConfigured {
		t.Fatalf("empty url err = %v", err)
	}
	if _, err := NewKomgaClient("http://host", ""); err != ErrNotConfigured {
		t.Fatalf("empty key err = %v", err)
	}
}

func TestKomgaListSeriesPaginatesAndListBooks(t *testing.T) {
	ts, _ := newFakeKomga(t)
	c, _ := NewKomgaClient(ts.URL, "kk")
	series, err := c.ListSeries(context.Background(), "")
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("series = %d, want 2 (两页)", len(series))
	}
	books, err := c.ListBooks(context.Background(), "S2")
	if err != nil {
		t.Fatalf("ListBooks: %v", err)
	}
	if len(books) != 2 || !strings.Contains(books[0].Media.MediaType, "epub") {
		t.Fatalf("books = %+v", books)
	}
}

func TestKomgaPushAndThumbnail(t *testing.T) {
	ts, f := newFakeKomga(t)
	c, _ := NewKomgaClient(ts.URL, "kk")
	if err := c.PushSeriesSummary(context.Background(), "S1", "简介A"); err != nil {
		t.Fatalf("PushSeriesSummary: %v", err)
	}
	if err := c.PushBookSummary(context.Background(), "N1", "简介B"); err != nil {
		t.Fatalf("PushBookSummary: %v", err)
	}
	if f.patches["series:S1"]["summary"] != "简介A" || f.patches["book:N1"]["summary"] != "简介B" {
		t.Fatalf("patches = %+v", f.patches)
	}

	b, ct, err := c.ThumbnailFor(context.Background(), "series", "S1")
	if err != nil || string(b) != "imgbytes" || ct != "image/jpeg" {
		t.Fatalf("thumbnail series = %q %s %v", b, ct, err)
	}
	b, ct, err = c.ThumbnailFor(context.Background(), "book", "N1")
	if err != nil || string(b) != "bookimg" || ct != "image/png" {
		t.Fatalf("thumbnail book = %q %s %v", b, ct, err)
	}
	if _, _, err := c.ThumbnailFor(context.Background(), "series", "S-none"); err != ErrCoverMissing {
		t.Fatalf("missing cover err = %v, want ErrCoverMissing", err)
	}

	if _, err := NewKomgaClient(ts.URL, "wrong"); err != nil {
		t.Fatal(err)
	}
	cBad, _ := NewKomgaClient(ts.URL, "wrong")
	if _, err := cBad.ListSeries(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("bad key err = %v", err)
	}
}
