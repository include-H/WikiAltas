package httpapi

import (
	"context"
	"sync"
	"time"

	"wikiatlas/backend/internal/library"
)

// GameAtlas 目录缓存：首次用 /games/all 全量拉；之后每次按「距上次同步的天数」
// 用 /games/recent 做增量合并。池子 / 建议 / 搜索都读这份缓存，
// 不再每次请求全量轰 GA。进程内存缓存，重启后自然回到"首次全拉"。
type gameAtlasCatalog struct {
	mu       sync.Mutex
	baseURL  string
	entries  map[string]library.GameAtlasEntry
	lastSync time.Time
}

func (s *Server) gaCatalogEntries(ctx context.Context, client *library.Client) ([]library.GameAtlasEntry, error) {
	c := &s.gaCatalog
	c.mu.Lock()
	defer c.mu.Unlock()

	// 换了 GA 地址（重新配置）→ 缓存作废，重新全量
	if c.entries != nil && c.baseURL != client.BaseURL() {
		c.entries = nil
	}
	if c.entries == nil {
		all, err := client.ListAll(ctx)
		if err != nil {
			return nil, err
		}
		c.entries = make(map[string]library.GameAtlasEntry, len(all))
		for _, e := range all {
			c.entries[e.PublicID] = e
		}
		c.baseURL = client.BaseURL()
		c.lastSync = time.Now()
		return flattenGACatalog(c.entries), nil
	}

	days := int(time.Since(c.lastSync).Hours()/24) + 1
	if days > 365 {
		days = 365
	}
	// 增量拉失败不致命：继续用旧缓存（GA 掉线时池子/建议还能看）
	if recent, err := client.ListRecent(ctx, days); err == nil {
		for _, e := range recent {
			c.entries[e.PublicID] = e
		}
		c.lastSync = time.Now()
	}
	return flattenGACatalog(c.entries), nil
}

func flattenGACatalog(m map[string]library.GameAtlasEntry) []library.GameAtlasEntry {
	out := make([]library.GameAtlasEntry, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	return out
}
