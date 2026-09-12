package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/library"
	"wikiatlas/backend/internal/llm"
)

// --- 媒体库：节点扫库（写完之后 → 关联媒体）---
//
// 视角是**节点 → 库**：拿一个刚建好/刚写完的作品标题，去三个源召回它的卫星
// （正片、改编影视、漫画小说版、解析/攻略这类混杂库视频），一次 LLM 判相关性，
// 产出带可信度的建议存进建议清单——节点页展示，用户点确认才挂链。
// 与「AI 对一遍」（库 → 节点的全量匹配）互补：这里查得干净、召回准，
// 新入库回流已有节点仍归池子。

const sweepCandidateCap = 36
const sweepSuggestionCap = 30
const sweepMinConfidence = 0.35

type sweepSourceStatus struct {
	Source     string `json:"source"`
	Status     string `json:"status"` // ok | unconfigured | error
	Message    string `json:"message,omitempty"`
	Candidates int    `json:"candidates"`
}

type sweepCandidate struct {
	Source     string `json:"-"`
	Key        string `json:"key"`
	EntryID    string `json:"-"`
	Title      string `json:"title"`
	Kind       string `json:"kind,omitempty"`
	Format     string `json:"format,omitempty"`
	Extra      string `json:"extra,omitempty"`
	CoverImage string `json:"-"`
	URL        string `json:"-"`
	rank       int
}

// sweepTitleVariants：标题 + 别名打底，再让模型补题名变体（缩写/外文名/简繁），
// 用于在库里做子串搜索。模型失败就只用打底词——扫库仍是确定性的。
func (s *Server) sweepTitleVariants(ctx context.Context, client llm.Client, work *domain.Work) []string {
	terms := []string{work.Title}
	for _, a := range work.Aliases {
		if a = strings.TrimSpace(a); a != "" {
			terms = append(terms, a)
		}
	}
	variants := sweepExtraVariants(ctx, client, work)
	seen := map[string]bool{}
	out := make([]string, 0, len(terms)+len(variants))
	for _, t := range append(terms, variants...) {
		t = strings.TrimSpace(t)
		norm := normalizeTitle(t)
		if norm == "" || seen[norm] || len([]rune(t)) > 40 {
			continue
		}
		seen[norm] = true
		out = append(out, t)
		if len(out) >= 6 {
			break
		}
	}
	return out
}

const sweepVariantsPrompt = `给一部作品列出用于媒体库子串搜索的**题名变体**：别名、缩写、外文原名、简繁写法。
要求：最多 4 条；每条 2–30 字；不含原题名的重复；不要通用词（"全集""剧场版""第一季""攻略"）。
只输出 JSON 字符串数组，不要任何多余文字，如：["最终幻想7","FF7","Final Fantasy VII"]`

func sweepExtraVariants(ctx context.Context, client llm.Client, work *domain.Work) []string {
	if client == nil {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"title": work.Title, "aliases": work.Aliases, "medium": string(mediumOf(work)),
	})
	if err != nil {
		return nil
	}
	res, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Content: sweepVariantsPrompt},
		{Role: "user", Content: string(payload)},
	}, nil)
	if err != nil {
		return nil
	}
	return parseSweepVariants(res.Content)
}

// parseSweepVariants 容忍代码围栏/多余文字；过滤空串、超长与重复。
func parseSweepVariants(raw string) []string {
	start := strings.Index(raw, "[")
	end := strings.LastIndex(raw, "]")
	if start < 0 || end <= start {
		return nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw[start:end+1]), &list); err != nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for _, v := range list {
		v = strings.TrimSpace(v)
		n := len([]rune(v))
		if n < 2 || n > 30 {
			continue
		}
		norm := normalizeTitle(v)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, v)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func mediumOf(w *domain.Work) domain.Medium {
	if w.Medium != nil {
		return *w.Medium
	}
	return ""
}

// sweepCandidates 从三个源收集候选条目：命中任一题名词、未挂链、未被本节点忽略。
// 每个源带状态（未配置 / 出错），让前端能提示"Emby 库角色还没标"这类情况。
func (s *Server) sweepCandidates(ctx context.Context, work *domain.Work, terms []string) ([]sweepCandidate, []sweepSourceStatus) {
	norms := make([]string, 0, len(terms))
	for _, t := range terms {
		if n := normalizeTitle(t); n != "" {
			norms = append(norms, n)
		}
	}
	liveLinks := s.allLinkedIDs()
	dismissed := s.dismissedSet(work.ID)

	statuses := make([]sweepSourceStatus, 0, 3)
	out := make([]sweepCandidate, 0, 64)
	appendCand := func(c sweepCandidate) {
		if liveLinks[c.Key] != "" || dismissed[c.Key] {
			return
		}
		out = append(out, c)
	}

	// GameAtlas：按目录缓存匹配标题 / 别名 / GA 系列名。
	gaStatus := sweepSourceStatus{Source: "gameatlas", Status: "unconfigured"}
	if ga, err := s.gaClient(); err != nil {
		gaStatus.Message = "还没配置 GameAtlas"
	} else if entries, err := s.gaCatalogEntries(ctx, ga); err != nil {
		gaStatus.Status, gaStatus.Message = "error", err.Error()
	} else {
		gaStatus.Status = "ok"
		for _, e := range entries {
			if !anyNormMatches(norms, func(n string) bool { return gaEntryMatches(e, n) }) {
				continue
			}
			extra := ""
			if e.Series != nil {
				extra = "GA系列：" + e.Series.Name
			}
			cover := ""
			if abs := absCover(ga.BaseURL(), e.CoverImage); abs != nil {
				cover = *abs
			}
			appendCand(sweepCandidate{
				Source: "gameatlas", Key: "gameatlas:" + e.PublicID, EntryID: e.PublicID,
				Title: e.Title, Kind: "game", Extra: extra,
				CoverImage: cover, URL: ga.GameURL(e.PublicID),
				rank: nameRank(normalizeTitle(e.Title), norms),
			})
		}
	}
	statuses = append(statuses, gaStatus)

	// Emby：正片库 + 混杂库（第九艺术的攻略/解析视频正是这里的价值所在）。
	emStatus := sweepSourceStatus{Source: "emby", Status: "unconfigured"}
	if em, err := s.embyClient(); err != nil {
		emStatus.Message = "还没配置 Emby"
	} else if workViews, mixedViews, err := s.embyRoleViews(ctx, em); err != nil {
		emStatus.Status, emStatus.Message = "error", err.Error()
	} else if len(workViews)+len(mixedViews) == 0 {
		emStatus.Message = "库角色还没标：到设置页把正片 / 混杂内容库选上"
	} else {
		emStatus.Status = "ok"
		views := make([]library.EmbyView, 0, len(workViews)+len(mixedViews))
		views = append(views, workViews...)
		views = append(views, mixedViews...)
		seen := map[string]bool{}
		for _, v := range views {
			items, err := em.ListItems(ctx, v.ID, "Series,Movie,BoxSet")
			if err != nil {
				log.Printf("node sweep: emby %s: %v", v.Name, err)
				continue
			}
			for i := range items {
				it := items[i]
				if seen[it.ID] {
					continue
				}
				if !anyNormMatches(norms, func(n string) bool { return embyItemRelatedTo(&it, n) }) {
					continue
				}
				seen[it.ID] = true
				kind := map[string]string{"Series": "tv", "BoxSet": "collection"}[it.Type]
				if kind == "" {
					kind = "movie"
				}
				extra := v.Name
				if it.Type == "BoxSet" {
					extra += " · 合集"
				}
				cover := ""
				if u := em.CoverURL(&it); u != nil {
					cover = *u
				}
				appendCand(sweepCandidate{
					Source: "emby", Key: "emby:" + it.ID, EntryID: it.ID,
					Title: it.Name, Kind: kind, Extra: extra,
					CoverImage: cover, URL: em.ItemURL(it.ID),
					rank: nameRank(normalizeTitle(it.Name), norms),
				})
			}
		}
	}
	statuses = append(statuses, emStatus)

	// Komga：条目集（系列 + 抽屉单册）。
	kmStatus := sweepSourceStatus{Source: "komga", Status: "unconfigured"}
	if km, err := s.komgaClient(); err != nil {
		kmStatus.Message = "还没配置 Komga"
	} else if entries, err := s.komgaEntries(ctx, km); err != nil {
		kmStatus.Status, kmStatus.Message = "error", err.Error()
	} else {
		kmStatus.Status = "ok"
		for _, e := range entries {
			n := normalizeTitle(e.Name)
			if !anyNormMatches(norms, func(norm string) bool { return strings.Contains(n, norm) }) {
				continue
			}
			appendCand(sweepCandidate{
				Source: "komga", Key: "komga:" + e.ID, EntryID: e.ID,
				Title: e.Name, Kind: "book", Format: e.formatStr(), Extra: e.SeriesName,
				CoverImage: s.komgaCoverPath(e), URL: komgaEntryURL(km, e),
				rank: nameRank(n, norms),
			})
		}
	}
	statuses = append(statuses, kmStatus)

	// 排序（命中更精确的在前）→ 截断。
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		if out[i].Source != out[j].Source {
			return sourceOrder(out[i].Source) < sourceOrder(out[j].Source)
		}
		return out[i].Title < out[j].Title
	})
	if len(out) > sweepCandidateCap {
		out = out[:sweepCandidateCap]
	}
	for i := range statuses {
		for _, c := range out {
			if c.Source == statuses[i].Source {
				statuses[i].Candidates++
			}
		}
	}
	return out, statuses
}

// dismissedSet 读「这个节点被逐条忽略过」的 key 集合（per-node，不是全局忽略）。
func (s *Server) dismissedSet(workID string) map[string]bool {
	out := map[string]bool{}
	if ai, err := s.store.GetLibraryAiSuggestions(); err == nil && ai != nil {
		for _, d := range ai.Dismissed {
			if d.WorkID == workID {
				out[d.Key] = true
			}
		}
	}
	return out
}

func anyNormMatches(norms []string, match func(string) bool) bool {
	for _, n := range norms {
		if match(n) {
			return true
		}
	}
	return false
}

// nameRank：0 = 完全相等、1 = 前缀、2 = 包含（多词取最好）。
func nameRank(norm string, norms []string) int {
	best := 2
	for _, n := range norms {
		switch {
		case norm == n:
			return 0
		case strings.HasPrefix(norm, n):
			if best > 1 {
				best = 1
			}
		}
	}
	return best
}

const sweepJudgePrompt = `你在给一部 Wiki 作品找媒体库里**属于它**的条目（这一步只产出建议，用户会逐条确认后才会关联）。

输入 JSON：
- work：{title, medium, aliases}——medium: game=游戏 / movie=电影 / tv=剧集 / manga=漫画 / book=书籍 / other。
- candidates：库里命中了标题的条目 [{key, source, title, kind, extra}]。source: gameatlas=游戏库 / emby=影视库（含"混杂库"里的解析、攻略、访谈这类围绕作品的视频）/ komga=漫画小说库。

判断标准——**这条是不是这部作品的媒体形态**：
- 是：正片（游戏本体/剧集/电影）、改编或衍生影视、漫画/小说版、围绕它的解析/攻略/访谈视频、整盒合集。
- 不是：名字碰巧像但讲别的作品；只是同一系列里的另一部（那属于它自己的节点，别拉过来）；泛泛的"某系列盘点"合集视频。

对判为"是"的条目：confidence（0~1，拿不准就低）、reason（≤20 字中文判据）。判为"不是"的直接不输出，别硬凑。
只输出严格 JSON，不要代码围栏：
{"links":[{"key":"emby:123","confidence":0.9,"reason":"正片剧集"}]}`

type sweepJudgedLink struct {
	Key        string  `json:"key"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// parseSweepJudgement 容忍围栏；只认在候选集里的 key；低置信丢弃。
func parseSweepJudgement(raw string, validKeys map[string]bool) ([]sweepJudgedLink, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil, errors.New("模型没有返回 JSON")
	}
	var out struct {
		Links []sweepJudgedLink `json:"links"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &out); err != nil {
		return nil, fmt.Errorf("解析判官 JSON 失败: %w", err)
	}
	seen := map[string]bool{}
	list := make([]sweepJudgedLink, 0, len(out.Links))
	for _, l := range out.Links {
		key := strings.TrimSpace(l.Key)
		if !validKeys[key] || seen[key] {
			continue
		}
		seen[key] = true
		conf := l.Confidence
		if conf <= 0 {
			conf = 0.5
		}
		if conf > 1 {
			conf = 1
		}
		if conf < sweepMinConfidence {
			continue
		}
		reason := strings.TrimSpace(l.Reason)
		if runes := []rune(reason); len(runes) > 40 {
			reason = string(runes[:40])
		}
		list = append(list, sweepJudgedLink{Key: key, Confidence: conf, Reason: reason})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Confidence > list[j].Confidence })
	if len(list) > sweepSuggestionCap {
		list = list[:sweepSuggestionCap]
	}
	return list, nil
}

// runNodeSweep：题名变体 → 三源候选 → 一次判官 → 合并落 KV；返回这个节点的新建议。
func (s *Server) runNodeSweep(ctx context.Context, work *domain.Work, client llm.Client) ([]domain.LibraryAiSuggestion, []sweepSourceStatus, error) {
	terms := s.sweepTitleVariants(ctx, client, work)
	cands, statuses := s.sweepCandidates(ctx, work, terms)
	if len(cands) == 0 {
		s.saveWorkSweepSuggestions(work.ID, nil, client.Model())
		return nil, statuses, nil
	}

	validKeys := make(map[string]bool, len(cands))
	byKey := make(map[string]sweepCandidate, len(cands))
	inv := make([]sweepCandidate, 0, len(cands))
	for _, c := range cands {
		validKeys[c.Key] = true
		byKey[c.Key] = c
		inv = append(inv, c)
	}
	payload, err := json.Marshal(map[string]any{
		"work": map[string]any{
			"title": work.Title, "medium": string(mediumOf(work)), "aliases": work.Aliases,
		},
		"candidates": inv,
	})
	if err != nil {
		return nil, statuses, err
	}
	msgs := []llm.Message{
		{Role: "system", Content: sweepJudgePrompt},
		{Role: "user", Content: string(payload)},
	}
	res, err := client.Chat(ctx, msgs, nil)
	if err != nil {
		return nil, statuses, err
	}
	judged, perr := parseSweepJudgement(res.Content, validKeys)
	// 偶发空答（真见过：同一输入下一次就正常，首次却给空 {"links":[]}，静默成"没找到"）：
	// 候选明明有，判官却一条不给时重试一次；最终仍空就记日志，让下次排查有据可查。
	if perr != nil || len(judged) == 0 {
		log.Printf("node sweep %s: 判官零建议（候选 %d），重试一次；原文 %.160s", work.ID, len(cands), res.Content)
		if res2, err2 := client.Chat(ctx, msgs, nil); err2 == nil {
			if j2, e2 := parseSweepJudgement(res2.Content, validKeys); e2 == nil && len(j2) > 0 {
				judged, perr = j2, nil
			}
		}
	}
	if perr != nil {
		return nil, statuses, perr
	}
	if len(judged) == 0 {
		log.Printf("node sweep %s: 重试后仍零建议（候选 %d）", work.ID, len(cands))
	}

	suggestions := make([]domain.LibraryAiSuggestion, 0, len(judged))
	for _, j := range judged {
		c := byKey[j.Key]
		suggestions = append(suggestions, domain.LibraryAiSuggestion{
			Key: c.Key, Source: c.Source, Action: "link", TargetNodeID: work.ID,
			Confidence: j.Confidence, Reason: j.Reason, Origin: "sweep",
			Title: c.Title, Kind: c.Kind, Format: c.Format, Extra: c.Extra,
			CoverImage: c.CoverImage, URL: c.URL,
		})
	}
	if err := s.saveWorkSweepSuggestions(work.ID, suggestions, client.Model()); err != nil {
		return nil, statuses, err
	}
	return suggestions, statuses, nil
}

// saveWorkSweepSuggestions：只替换"这个节点、sweep 产出"的那一份建议；
// 池子的 AI 对一遍结果、别的节点的 sweep 结果、忽略记录都原样保留。
func (s *Server) saveWorkSweepSuggestions(workID string, fresh []domain.LibraryAiSuggestion, model string) error {
	s.suggestionsMu.Lock()
	defer s.suggestionsMu.Unlock()
	out := &domain.LibraryAiSuggestions{}
	if prev, err := s.store.GetLibraryAiSuggestions(); err == nil && prev != nil {
		out = prev
	}
	kept := out.Suggestions[:0]
	for _, sug := range out.Suggestions {
		if sug.Origin == "sweep" && sug.TargetNodeID == workID {
			continue
		}
		kept = append(kept, sug)
	}
	out.Suggestions = append(kept, fresh...)
	out.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	out.Model = model
	return s.store.SaveLibraryAiSuggestions(out)
}

// SweepLibraryForWork 是写完之后的后台钩子入口（executor 触发，best-effort）：
// 没配模型、库都没配、标题太短都静默跳过；失败只记日志，不打扰用户。
func (s *Server) SweepLibraryForWork(workID string) {
	if workID == "" || !s.store.HasAPIKey() {
		return
	}
	work, err := s.store.GetWork(workID)
	if err != nil || len([]rune(work.Title)) < 2 {
		return
	}
	// 扫库是机械判定（题名变体 + 相关性判官）：不带思考直出（none 档），快一个量级。
	client := s.runs.ActiveClientFor("none")
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	select {
	case s.sweepSem <- struct{}{}:
		defer func() { <-s.sweepSem }()
	case <-ctx.Done():
		return
	}
	if _, _, err := s.runNodeSweep(ctx, work, client); err != nil {
		log.Printf("node sweep %s（%s）: %v", workID, work.Title, err)
	}
}

// --- HTTP ---

// nodeSweepSuggestionOut 是节点页行：建议 + 条目显示字段。
type nodeSweepSuggestionOut struct {
	Key        string  `json:"key"`
	Source     string  `json:"source"`
	EntryID    string  `json:"entryId"`
	Title      string  `json:"title"`
	Kind       string  `json:"kind,omitempty"`
	Format     string  `json:"format,omitempty"`
	Extra      string  `json:"extra,omitempty"`
	CoverImage string  `json:"coverImage,omitempty"`
	URL        string  `json:"url"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

func suggestionOutOf(sug domain.LibraryAiSuggestion) nodeSweepSuggestionOut {
	_, entryID, _ := strings.Cut(sug.Key, ":")
	return nodeSweepSuggestionOut{
		Key: sug.Key, Source: sug.Source, EntryID: entryID,
		Title: sug.Title, Kind: sug.Kind, Format: sug.Format, Extra: sug.Extra,
		CoverImage: sug.CoverImage, URL: sug.URL,
		Confidence: sug.Confidence, Reason: sug.Reason,
	}
}

// handleNodeSweepRead 是 GET /api/library/node-sweep?workId=：
// 读这个节点现存的挂链建议（sweep 与池子产出的都算），过滤已挂链与已忽略的。
func (s *Server) handleNodeSweepRead(w http.ResponseWriter, r *http.Request) {
	workID := strings.TrimSpace(r.URL.Query().Get("workId"))
	if workID == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "workId 必填")
		return
	}
	if _, err := s.store.GetWork(workID); err != nil {
		writeStoreErr(w, err)
		return
	}
	ai, err := s.store.GetLibraryAiSuggestions()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	liveLinks := s.allLinkedIDs()
	dismissed := map[string]bool{}
	out := make([]nodeSweepSuggestionOut, 0)
	generatedAt, model := "", ""
	if ai != nil {
		generatedAt, model = ai.GeneratedAt, ai.Model
		for _, d := range ai.Dismissed {
			if d.WorkID == workID {
				dismissed[d.Key] = true
			}
		}
		for _, sug := range ai.Suggestions {
			if sug.Action != "link" || sug.TargetNodeID != workID {
				continue
			}
			if liveLinks[sug.Key] != "" || dismissed[sug.Key] {
				continue
			}
			out = append(out, suggestionOutOf(sug))
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	}
	dismissedOut := make([]domain.LibraryAiDismissal, 0)
	if ai != nil {
		for _, d := range ai.Dismissed {
			if d.WorkID == workID {
				dismissedOut = append(dismissedOut, d)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workId": workID, "generatedAt": generatedAt, "model": model,
		"suggestions": out, "dismissed": dismissedOut,
	})
}

// handleNodeSweepRun 是 POST /api/library/node-sweep {workId}：
// 立即扫一遍这个节点（题名变体 → 三源候选 → 判官），产出并持久化建议。
func (s *Server) handleNodeSweepRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkID string `json:"workId"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	body.WorkID = strings.TrimSpace(body.WorkID)
	if body.WorkID == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "workId 必填")
		return
	}
	work, err := s.store.GetWork(body.WorkID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if len([]rune(work.Title)) < 2 {
		writeErr(w, http.StatusBadRequest, "title_too_short", "标题太短，搜不出可信的候选")
		return
	}
	if !s.store.HasAPIKey() {
		writeErr(w, http.StatusBadRequest, "no_model", "还没配置模型：先到设置页填好 LLM 的 API Key")
		return
	}
	client := s.runs.ActiveClientFor("none") // 机械判定，同后台钩子
	if client == nil {
		writeErr(w, http.StatusBadRequest, "no_model", "还没配置模型：先到设置页填好 LLM")
		return
	}
	// 并发闸：手动与后台钩子共用，满了就明说。
	select {
	case s.sweepSem <- struct{}{}:
		defer func() { <-s.sweepSem }()
	default:
		writeErr(w, http.StatusConflict, "busy", "另一个扫库还在跑，稍等")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	suggestions, sources, err := s.runNodeSweep(ctx, work, client)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "sweep", err.Error())
		return
	}
	out := make([]nodeSweepSuggestionOut, 0, len(suggestions))
	for _, sug := range suggestions {
		out = append(out, suggestionOutOf(sug))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "workId": work.ID, "model": client.Model(),
		"generatedAt": time.Now().UTC().Format(time.RFC3339),
		"sources":     sources, "suggestions": out,
		"total": len(out),
	})
}

// handleNodeSweepDismiss 是 POST /api/library/node-sweep/dismiss {workId, key}：
// 忽略一条（per-node：只是不再向这个节点提起，不等于池子的全局忽略）。
func (s *Server) handleNodeSweepDismiss(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkID string `json:"workId"`
		Key    string `json:"key"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	body.WorkID, body.Key = strings.TrimSpace(body.WorkID), strings.TrimSpace(body.Key)
	if body.WorkID == "" || body.Key == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "workId 与 key 必填")
		return
	}
	_, source, ok := strings.Cut(body.Key, ":")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad_request", "key 形状应为 source:entryId")
		return
	}
	s.suggestionsMu.Lock()
	defer s.suggestionsMu.Unlock()
	out := &domain.LibraryAiSuggestions{}
	if prev, err := s.store.GetLibraryAiSuggestions(); err == nil && prev != nil {
		out = prev
	}
	title := ""
	kept := out.Suggestions[:0]
	for _, sug := range out.Suggestions {
		if sug.Key == body.Key && sug.TargetNodeID == body.WorkID && sug.Action == "link" {
			title = sug.Title
			continue
		}
		kept = append(kept, sug)
	}
	out.Suggestions = kept
	exists := false
	for _, d := range out.Dismissed {
		if d.WorkID == body.WorkID && d.Key == body.Key {
			exists = true
			break
		}
	}
	if !exists {
		out.Dismissed = append(out.Dismissed, domain.LibraryAiDismissal{
			WorkID: body.WorkID, Key: body.Key, Source: source, Title: title,
		})
	}
	if err := s.store.SaveLibraryAiSuggestions(out); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleNodeSweepRestore 是 DELETE /api/library/node-sweep/dismiss?workId=&key=：恢复被忽略的一条。
func (s *Server) handleNodeSweepRestore(w http.ResponseWriter, r *http.Request) {
	workID := strings.TrimSpace(r.URL.Query().Get("workId"))
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if workID == "" || key == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "workId 与 key 必填")
		return
	}
	s.suggestionsMu.Lock()
	defer s.suggestionsMu.Unlock()
	out := &domain.LibraryAiSuggestions{}
	if prev, err := s.store.GetLibraryAiSuggestions(); err == nil && prev != nil {
		out = prev
	}
	kept := out.Dismissed[:0]
	for _, d := range out.Dismissed {
		if d.WorkID == workID && d.Key == key {
			continue
		}
		kept = append(kept, d)
	}
	out.Dismissed = kept
	if err := s.store.SaveLibraryAiSuggestions(out); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
