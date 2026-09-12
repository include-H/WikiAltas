package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/llm"
)

// --- 媒体库：AI 建议（只建议，不执行）---
//
// 「AI 对一遍」把扫描清单里"未挂链且未忽略"的条目 + 现有节点树交给当前模型，
// 产出带可信度的建议（关联到某个节点 / 建档挂到某个节点 / 忽略），存成建议清单；
// 池子页按可信度展示，用户点确认才执行——LLM 绝不主动建库、也不自动写。

const aiSuggestSystemPrompt = `你是 WikiAltas 的媒体库管家：把媒体库条目和已有的 Wiki 节点树对上号，输出**建议**（不会自动执行，用户会逐条确认）。

输入 JSON 含两部分：
- tree：现有节点 [{id,title,kind,medium,path}]。kind: universe=宇宙 / series=系列 / work=单作；path 是从根到父级的标题路径。
- entries：库里"还没挂链"的条目 [{key,title,kind,format,extra}]。key 是回填用的稳定标识；kind: game=游戏 / tv=剧集 / movie=电影 / book=书籍 / collection=合集；format: comic=漫画 / novel=小说。

对每条条目给一个动作建议：
1. archive —— 库里有、Wiki 里没有，建议建新节点：targetNodeId = 建议挂在哪个已有节点下（优先选层级合适的 series/universe；拿不准就降 confidence 或改 ignore）。
2. link —— Wiki 里已经有对应节点：targetNodeId = 那个节点（同义名、简繁差异、外传/同系列都算对应）。一个 targetNodeId 可以同时接收多条（同一作品的漫画版+小说版+剧集）。
3. ignore —— 不可归档或判断不了：画集/设定集/攻略/单曲/演唱会；某条目本身就是另一条的合集（重复装帧）；标题无意义。

判别要点：
- 简繁与译名差异：魔卡少女樱 = 百变小樱魔术卡；葬送的芙莉莲 = 葬送的芙莉蓮
- 去掉装饰：哈利·波特（全集）→ 哈利·波特；《格林童话》【210篇】→ 格林童话
- 合集(collection) 只能用 link（整盒挂到对应系列节点）或 ignore，不要 archive
- 拿不准宁可低 confidence，不要硬凑

confidence：≥0.9 把握很大；0.6–0.9 有依据的推测；<0.6 存疑。
reason：≤20 字中文，说明判据。

只输出严格 JSON（不要任何多余文字、不要代码围栏）：
{"groups":[{"targetNodeId":"...","confidence":0.95,"reason":"...","entryKeys":["komga:xxx"]}],
 "links":[{"targetNodeId":"...","confidence":0.9,"reason":"...","entryKeys":["emby:xxx"]}],
 "ignores":[{"reason":"画集/设定集","entryKeys":["komga:yyy"]}]}
entryKeys 必须逐字回填输入里的 key。`

type aiGroupRaw struct {
	TargetNodeID string   `json:"targetNodeId"`
	Confidence   float64  `json:"confidence"`
	Reason       string   `json:"reason"`
	EntryKeys    []string `json:"entryKeys"`
}

// parseAiSuggestions 容忍代码围栏/多余文字；非法 key 与编造的节点 id 直接丢弃。
func parseAiSuggestions(raw string, validKeys, validNodes map[string]bool) ([]domain.LibraryAiSuggestion, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil, errors.New("模型没有返回 JSON")
	}
	var out struct {
		Groups  []aiGroupRaw `json:"groups"`
		Links   []aiGroupRaw `json:"links"`
		Ignores []aiGroupRaw `json:"ignores"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &out); err != nil {
		return nil, fmt.Errorf("解析建议 JSON 失败: %w", err)
	}
	suggestions := make([]domain.LibraryAiSuggestion, 0, 64)
	seen := map[string]bool{}
	appendEntries := func(action string, list []aiGroupRaw) {
		for _, g := range list {
			conf := g.Confidence
			if conf <= 0 {
				conf = 0.5
			}
			if conf > 1 {
				conf = 1
			}
			reason := strings.TrimSpace(g.Reason)
			if runes := []rune(reason); len(runes) > 40 {
				reason = string(runes[:40])
			}
			target := strings.TrimSpace(g.TargetNodeID)
			if action != "ignore" && !validNodes[target] {
				continue
			}
			for _, key := range g.EntryKeys {
				key = strings.TrimSpace(key)
				if !validKeys[key] || seen[key] {
					continue
				}
				src, _, ok := strings.Cut(key, ":")
				if !ok {
					continue
				}
				seen[key] = true
				suggestions = append(suggestions, domain.LibraryAiSuggestion{
					Key: key, Source: src, Action: action,
					TargetNodeID: target, Confidence: conf, Reason: reason,
				})
			}
		}
	}
	appendEntries("archive", out.Groups)
	appendEntries("link", out.Links)
	appendEntries("ignore", out.Ignores)
	if len(suggestions) == 0 {
		return nil, errors.New("模型没有给出任何可用的建议")
	}
	return suggestions, nil
}

type aiInvItem struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Format string `json:"format,omitempty"`
	Extra  string `json:"extra,omitempty"`
}

// handleLibraryAiSuggest 是 POST /api/library/ai-suggest：
// 逐源调用当前模型产出建议清单并持久化；只建议，不执行。
func (s *Server) handleLibraryAiSuggest(w http.ResponseWriter, r *http.Request) {
	manifest, err := s.store.GetLibraryManifest()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if manifest == nil {
		writeErr(w, http.StatusBadRequest, "no_manifest", "还没有扫描清单：先点「扫描媒体库」")
		return
	}
	// 没配 Key 时 ActiveClient 会退回 mock/echo（那是给测试和离线演示的），
	// 这里要的是真模型——先查 Key，给用户一个干净的错误。
	if !s.store.HasAPIKey() {
		writeErr(w, http.StatusBadRequest, "no_model", "还没配置模型：先到设置页填好 LLM 的 API Key")
		return
	}
	client := s.runs.ActiveClient()
	if client == nil {
		writeErr(w, http.StatusBadRequest, "no_model", "还没配置模型：先到设置页填好 LLM")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
	defer cancel()

	nodes, err := s.store.ListTree()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	byID := make(map[string]domain.WorkSummary, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	validNodes := make(map[string]bool, len(nodes))
	type treeNodeCompact struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Kind   string `json:"kind"`
		Medium string `json:"medium,omitempty"`
		Path   string `json:"path,omitempty"`
	}
	treeCompact := make([]treeNodeCompact, 0, len(nodes))
	for _, n := range nodes {
		var parts []string
		for cur := n.ParentID; cur != nil; {
			p, ok := byID[*cur]
			if !ok {
				break
			}
			parts = append([]string{p.Title}, parts...)
			cur = p.ParentID
		}
		medium := ""
		if n.Medium != nil {
			medium = string(*n.Medium)
		}
		validNodes[n.ID] = true
		treeCompact = append(treeCompact, treeNodeCompact{ID: n.ID, Title: n.Title, Kind: string(n.Kind), Medium: medium, Path: strings.Join(parts, "/")})
	}

	liveLinks := s.allLinkedIDs()
	ignoredKeys := make(map[string]bool, len(manifest.IgnoredKeys))
	for _, k := range manifest.IgnoredKeys {
		ignoredKeys[k] = true
	}
	ignoredGroups := make(map[string]bool, len(manifest.IgnoredGroups))
	for _, g := range manifest.IgnoredGroups {
		ignoredGroups[g.Source+"|"+g.ContainerKey] = true
	}
	bySource := map[string][]domain.LibraryManifestEntry{}
	for _, e := range manifest.Entries {
		if liveLinks[e.Key] != "" || ignoredKeys[e.Key] || ignoredGroups[e.Source+"|"+e.ContainerKey] {
			continue
		}
		bySource[e.Source] = append(bySource[e.Source], e)
	}

	model := client.Model()
	suggestions := make([]domain.LibraryAiSuggestion, 0, 128)
	prompted := 0
	for _, src := range []string{"gameatlas", "emby", "komga"} {
		list := bySource[src]
		if len(list) == 0 {
			continue
		}
		validKeys := make(map[string]bool, len(list))
		inv := make([]aiInvItem, 0, len(list))
		for _, e := range list {
			validKeys[e.Key] = true
			inv = append(inv, aiInvItem{Key: e.Key, Title: e.Title, Kind: e.Kind, Format: e.Format, Extra: e.Extra})
		}
		payload, err := json.Marshal(map[string]any{"tree": treeCompact, "entries": inv})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		res, err := client.Chat(ctx, []llm.Message{
			{Role: "system", Content: aiSuggestSystemPrompt},
			{Role: "user", Content: string(payload)},
		}, nil)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "llm", err.Error())
			return
		}
		prompted += len(list)
		parsed, err := parseAiSuggestions(res.Content, validKeys, validNodes)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "llm_parse", fmt.Sprintf("%s: %s", src, err.Error()))
			return
		}
		suggestions = append(suggestions, parsed...)
	}

	// 保留「节点扫库」的建议与节点页忽略记录——「AI 对一遍」只重刷自己的那份。
	s.suggestionsMu.Lock()
	defer s.suggestionsMu.Unlock()
	var kept []domain.LibraryAiSuggestion
	var dismissed []domain.LibraryAiDismissal
	if prev, err := s.store.GetLibraryAiSuggestions(); err == nil && prev != nil {
		dismissed = prev.Dismissed
		for _, sug := range prev.Suggestions {
			if sug.Origin == "sweep" {
				kept = append(kept, sug)
			}
		}
	}

	out := &domain.LibraryAiSuggestions{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Model:       model,
		Suggestions: append(kept, suggestions...),
		Dismissed:   dismissed,
	}
	if err := s.store.SaveLibraryAiSuggestions(out); err != nil {
		writeStoreErr(w, err)
		return
	}
	counts := map[string]int{}
	for _, sug := range suggestions {
		counts[sug.Action]++
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "model": model,
		"entries": prompted, "matched": len(suggestions),
		"archives": counts["archive"], "links": counts["link"], "ignored": counts["ignore"],
	})
}
