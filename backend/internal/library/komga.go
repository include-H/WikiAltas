package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrCoverMissing：Komga 系列没有缩略图（代理端点据此回 404）。
var ErrCoverMissing = errors.New("komga: 没有封面")

// KomgaSeries 是 Komga 的"一部作品"（series）。
type KomgaSeries struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	BooksCount     int    `json:"booksCount"`
	BooksReadCount int    `json:"booksReadCount"`
	Metadata       struct {
		Title     string `json:"title"`
		TitleSort string `json:"titleSort"`
		Status    string `json:"status"`
		Summary   string `json:"summary"`
	} `json:"metadata"`
}

// KomgaClient：X-API-Key 认证。地址既可能是根（http://host:port）、
// 也可能带 /api/v1（用户从 Swagger 页复制），统一归一。
type KomgaClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewKomgaClient(rawURL, apiKey string) (*KomgaClient, error) {
	base := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	base = strings.TrimSuffix(base, "/api/v1")
	if base == "" || strings.TrimSpace(apiKey) == "" {
		return nil, ErrNotConfigured
	}
	return &KomgaClient{
		baseURL: base,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func (c *KomgaClient) BaseURL() string { return c.baseURL }

func (c *KomgaClient) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("komga 连接失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(raw))
		if runes := []rune(msg); len(runes) > 120 {
			msg = string(runes[:120])
		}
		if msg == "" {
			return fmt.Errorf("komga: HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("komga: HTTP %d %s", resp.StatusCode, msg)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// ListSeries 全量或按关键词搜索系列（35 个量级，翻页循环拉完）。
func (c *KomgaClient) ListSeries(ctx context.Context, q string) ([]KomgaSeries, error) {
	var all []KomgaSeries
	page := 0
	for {
		vals := url.Values{}
		vals.Set("page", strconv.Itoa(page))
		vals.Set("size", "100")
		vals.Set("sort", "metadata.titleSort,asc")
		if strings.TrimSpace(q) != "" {
			vals.Set("search", strings.TrimSpace(q))
		}
		var out struct {
			Content    []KomgaSeries `json:"content"`
			TotalPages int           `json:"totalPages"`
		}
		if err := c.do(ctx, http.MethodGet, "/api/v1/series?"+vals.Encode(), nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Content...)
		page++
		if len(out.Content) == 0 || page >= out.TotalPages {
			return all, nil
		}
	}
}

// GetSeries 单个系列。
func (c *KomgaClient) GetSeries(ctx context.Context, id string) (*KomgaSeries, error) {
	var s KomgaSeries
	if err := c.do(ctx, http.MethodGet, "/api/v1/series/"+url.PathEscape(id), nil, &s); err != nil {
		return nil, err
	}
	if s.ID == "" {
		return nil, errors.New("komga 里没找到这个系列")
	}
	return &s, nil
}

// PushSeriesSummary 把简介写回系列元数据（Komga 没有全文位置，只有元数据）。
func (c *KomgaClient) PushSeriesSummary(ctx context.Context, id, summary string) error {
	return c.do(ctx, http.MethodPatch, "/api/v1/series/"+url.PathEscape(id)+"/metadata",
		map[string]any{"summary": summary}, nil)
}

// KomgaBook 是一册书（只取判型与标题需要的字段）。
type KomgaBook struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Media struct {
		MediaType string `json:"mediaType"` // application/epub+zip = 小说；zip/pdf = 漫画
	} `json:"media"`
	Metadata struct {
		Title string `json:"title"`
	} `json:"metadata"`
}

// ListBooks 拉系列的全部册。
func (c *KomgaClient) ListBooks(ctx context.Context, seriesID string) ([]KomgaBook, error) {
	var books []KomgaBook
	page := 0
	for {
		var out struct {
			Content    []KomgaBook `json:"content"`
			TotalPages int         `json:"totalPages"`
		}
		path := fmt.Sprintf("/api/v1/series/%s/books?size=200&page=%d", url.PathEscape(seriesID), page)
		if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, err
		}
		books = append(books, out.Content...)
		page++
		if len(out.Content) == 0 || page >= out.TotalPages {
			return books, nil
		}
	}
}

// GetBook 单册详情（判反哺目标是系列还是单册时用）。
func (c *KomgaClient) GetBook(ctx context.Context, id string) (*KomgaBook, error) {
	var b KomgaBook
	if err := c.do(ctx, http.MethodGet, "/api/v1/books/"+url.PathEscape(id), nil, &b); err != nil {
		return nil, err
	}
	if b.ID == "" {
		return nil, errors.New("komga 里没找到这本书")
	}
	return &b, nil
}

// PushBookSummary 把简介写回单册元数据（抽屉型系列按单册建档时用）。
func (c *KomgaClient) PushBookSummary(ctx context.Context, id, summary string) error {
	return c.do(ctx, http.MethodPatch, "/api/v1/books/"+url.PathEscape(id)+"/metadata",
		map[string]any{"summary": summary}, nil)
}

// ThumbnailFor 拉封面字节：kind = series | book。
func (c *KomgaClient) ThumbnailFor(ctx context.Context, kind, id string) ([]byte, string, error) {
	seg := "series"
	if kind == "book" {
		seg = "books"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/v1/"+seg+"/"+url.PathEscape(id)+"/thumbnail", nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("komga 连接失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", ErrCoverMissing
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("komga: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, "", err
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/jpeg"
	}
	return b, ct, nil
}

// BookURL 单册的前端深链。
func (c *KomgaClient) BookURL(id string) string {
	return c.baseURL + "/book/" + id
}

// SeriesURL 前端深链（Komga Web 的系列页）。
func (c *KomgaClient) SeriesURL(id string) string {
	return c.baseURL + "/series/" + id
}
