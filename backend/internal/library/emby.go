package library

import (
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

// EmbyItem 是 Emby 条目（只取用得到的字段）。
type EmbyItem struct {
	ID             string            `json:"Id"`
	Name           string            `json:"Name"`
	OriginalTitle  *string           `json:"OriginalTitle"`
	Type           string            `json:"Type"`
	ProductionYear *int              `json:"ProductionYear"`
	SeriesName     *string           `json:"SeriesName"`
	ParentID       string            `json:"ParentId"`
	Path           *string           `json:"Path"`
	ImageTags      map[string]string `json:"ImageTags"`
}

// EmbyView 是一个媒体库（/Users/{uid}/Views）。
type EmbyView struct {
	ID             string `json:"Id"`
	Name           string `json:"Name"`
	CollectionType string `json:"CollectionType"`
}

// EmbyClient 是会话期客户端：API Key 静态认证（X-Emby-Token），
// 首次用时解析一个管理员用户（Items 接口都挂在用户下）。
type EmbyClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
	userID  string
}

func NewEmbyClient(baseURL, apiKey string) (*EmbyClient, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" || strings.TrimSpace(apiKey) == "" {
		return nil, ErrNotConfigured
	}
	return &EmbyClient{
		baseURL: base,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func (c *EmbyClient) do(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Emby-Token", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("emby 连接失败: %w", err)
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
			return fmt.Errorf("emby: HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("emby: HTTP %d %s", resp.StatusCode, msg)
	}
	return json.Unmarshal(raw, out)
}

func (c *EmbyClient) ensureUser(ctx context.Context) error {
	if c.userID != "" {
		return nil
	}
	var users []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Policy struct {
			IsAdministrator bool `json:"IsAdministrator"`
		} `json:"Policy"`
	}
	if err := c.do(ctx, "/Users", &users); err != nil {
		return err
	}
	for _, u := range users {
		if u.Policy.IsAdministrator && u.ID != "" {
			c.userID = u.ID
			return nil
		}
	}
	if len(users) > 0 && users[0].ID != "" {
		c.userID = users[0].ID
		return nil
	}
	return errors.New("emby 里没有可用用户（API Key 是否有效？）")
}

// ListViews 列媒体库。
func (c *EmbyClient) ListViews(ctx context.Context) ([]EmbyView, error) {
	if err := c.ensureUser(ctx); err != nil {
		return nil, err
	}
	var out struct {
		Items []EmbyView `json:"Items"`
	}
	if err := c.do(ctx, "/Users/"+c.userID+"/Views", &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

const embyItemFields = "ProductionYear,OriginalTitle,SeriesName,Path,ImageTags"

// ListItems 在某个父节点下按类型拉条目（递归、翻页）。parentID 空 = 全部库。
func (c *EmbyClient) ListItems(ctx context.Context, parentID, includeTypes string) ([]EmbyItem, error) {
	if err := c.ensureUser(ctx); err != nil {
		return nil, err
	}
	var all []EmbyItem
	start := 0
	for {
		q := url.Values{}
		q.Set("Recursive", "true")
		q.Set("IncludeItemTypes", includeTypes)
		q.Set("Fields", embyItemFields)
		q.Set("SortBy", "SortName")
		q.Set("StartIndex", strconv.Itoa(start))
		q.Set("Limit", "500")
		if parentID != "" {
			q.Set("ParentId", parentID)
		}
		var page struct {
			Items []EmbyItem `json:"Items"`
			Total int        `json:"TotalRecordCount"`
		}
		if err := c.do(ctx, "/Users/"+c.userID+"/Items?"+q.Encode(), &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		start += len(page.Items)
		if len(page.Items) == 0 || start >= page.Total {
			return all, nil
		}
	}
}

// GetItem 取单个条目。
func (c *EmbyClient) GetItem(ctx context.Context, id string) (*EmbyItem, error) {
	if err := c.ensureUser(ctx); err != nil {
		return nil, err
	}
	var item EmbyItem
	if err := c.do(ctx, "/Users/"+c.userID+"/Items/"+url.PathEscape(id)+"?Fields="+embyItemFields, &item); err != nil {
		return nil, err
	}
	if item.ID == "" {
		return nil, errors.New("emby 里没找到这条")
	}
	return &item, nil
}

// ItemURL 前端深链（电影/剧集/专辑统一走 item 页）。
func (c *EmbyClient) ItemURL(id string) string {
	return c.baseURL + "/web/index.html#!/item?id=" + url.QueryEscape(id)
}

// CoverURL 给浏览器直接可用的封面地址（图片带 api_key 查询参数）；没有 Primary 图返回 nil。
func (c *EmbyClient) CoverURL(item *EmbyItem) *string {
	if item == nil || item.ID == "" {
		return nil
	}
	tag := item.ImageTags["Primary"]
	if tag == "" {
		return nil
	}
	u := fmt.Sprintf("%s/Items/%s/Images/Primary?maxHeight=300&api_key=%s&tag=%s",
		c.baseURL, url.PathEscape(item.ID), url.QueryEscape(c.apiKey), url.QueryEscape(tag))
	return &u
}
