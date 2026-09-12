package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/library"
	"wikiatlas/backend/internal/llm"
)

// --- Komga 系列层级判定（LLM，扫描时刷新，按指纹缓存）---
//
// 真库的命名是脏的：分卷可能是「[暁 なつめ].为美好的世界献上祝福！.01」「01青春猪头…」
// 「[新世紀福音戰士]第01卷.kepub」，元数据题名还常和文件名不一致——堆正则是一场
// 打不赢的仗。这里把「哪些系列合并成一条、哪些拆成单册」交给当前模型看一遍，
// 结论按"书单指纹"落库：新系列或书单变了才重新判，之后的扫描仍是零 LLM。

const komgaJudgePrompt = `你在给一个 Komga 库（漫画 / 轻小说）做**层级判定**：每个"系列"是一部分卷作品，还是装着一堆独立作品的抽屉。

输入是 JSON 数组，每项 {id, name, books}：
- name：系列名（可能是作品名，也可能是"单行本""唐家三少"这类抽屉名）；
- books：书的标题样本（文件题名或元数据题名，可能很脏——带 [作者] 前缀、.01、第N卷、v2、"全本"等）。

对每个系列二选一：
- "series"：这些书是**作品的卷**——书名带卷号（01 / 第3卷 / Vol.04）、或同一主标题只差编号。**一个容器里混着几部作品的卷也算**（例如「A作品 01-17 + B外传 01-03 + C外传 1-7」这种一团）——展示时合并成一条，跳系列页。
- "drawer"：这些书**每本本身就是一部完整独立的作品**（不是任何作品的分卷）——典型是系列名叫"单行本""作者名"，书名各是完整作品名、常带"全本/全集/共X册"——展示时逐本拆开、每本跳自己的书页。

判别看实质：书名是**作品的卷**（哪怕多部作品混在一起）→ series；**每本书都是完整且互不相同的作品** → drawer。
拿不准时选 "series"（宁合并、可人工拆；误拆会把建议池淹掉）。

只输出严格 JSON，key 是系列 id，每个系列都要给：
{"0ABC...":"series","0DEF...":"drawer"}`

// komgaJudgeVersion 是判定口径的版本号：参与指纹——口径改了（prompt 改版）就 +1，
// 旧判定自动全部重判一次，不必手工清缓存。
const komgaJudgeVersion = 2

// komgaJudgmentFP 是书单指纹：口径版本 + 系列名 + 全部书名（元数据题名优先）排序后哈希。
// 书单没变就不重新判——这是扫描保持零 LLM 的关键。
func komgaJudgmentFP(seriesName string, titles []string) string {
	sorted := append([]string(nil), titles...)
	sort.Strings(sorted)
	h := sha256.New()
	fmt.Fprintf(h, "v%d\x00%s\x00%s", komgaJudgeVersion, seriesName, strings.Join(sorted, "\x00"))
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// parseKomgaJudgments 容忍代码围栏/多余文字；只认合法 id 与 series|drawer 两种值。
func parseKomgaJudgments(raw string, validIDs map[string]bool) (map[string]string, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil, errors.New("模型没有返回 JSON")
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw[start:end+1]), &m); err != nil {
		return nil, fmt.Errorf("解析层级判定 JSON 失败: %w", err)
	}
	out := make(map[string]string, len(m))
	for id, kind := range m {
		id = strings.TrimSpace(id)
		kind = strings.ToLower(strings.TrimSpace(kind))
		if !validIDs[id] {
			continue
		}
		if kind != "series" && kind != "drawer" {
			continue
		}
		out[id] = kind
	}
	if len(out) == 0 {
		return nil, errors.New("模型没有给出任何可用的判定")
	}
	return out, nil
}

// komgaJudgmentKinds 读判定缓存成 seriesID → kind 映射（供条目构建用）。
func (s *Server) komgaJudgmentKinds() map[string]string {
	out := map[string]string{}
	if j, err := s.store.GetKomgaJudgments(); err == nil && j != nil {
		for id, item := range j.Items {
			out[id] = item.Kind
		}
	}
	return out
}

// invalidateKomgaCatalog 作废进程内的 Komga 目录缓存（判定刚更新时用）。
func (s *Server) invalidateKomgaCatalog() {
	c := &s.komgaCatalog
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fetchedAt = time.Time{}
}

// refreshKomgaJudgments：扫描开头跑一次——把"没判过 / 书单变了"的系列一次性交给
// 当前模型判定，结果并入 KV（其余系列不动）。没配模型、拉不到库、模型失败都只是
// 跳过（退回确定性启发式），不挡扫描。
func (s *Server) refreshKomgaJudgments(ctx context.Context, client *library.KomgaClient) {
	if !s.store.HasAPIKey() {
		return
	}
	// 层级分类是机械判定：不带思考直出（none 档），35 个系列一次判完只要几秒。
	llmClient := s.runs.ActiveClientFor("none")
	if llmClient == nil {
		return
	}
	series, err := client.ListSeries(ctx, "")
	if err != nil {
		log.Printf("komga judgments: list series: %v", err)
		return
	}
	prev, _ := s.store.GetKomgaJudgments()
	items := map[string]domain.KomgaJudgment{}
	if prev != nil {
		for id, item := range prev.Items {
			items[id] = item
		}
	}

	type judgeItem struct {
		ID    string   `json:"id"`
		Name  string   `json:"name"`
		Books []string `json:"books"`
		fp    string
	}
	validIDs := make(map[string]bool, len(series))
	var pending []judgeItem
	for _, sr := range series {
		validIDs[sr.ID] = true
		books, err := client.ListBooks(ctx, sr.ID)
		if err != nil {
			log.Printf("komga judgments: books of %s: %v", sr.Name, err)
			continue
		}
		titles := make([]string, 0, len(books))
		for _, b := range books {
			t := strings.TrimSpace(b.Metadata.Title)
			if t == "" {
				t = strings.TrimSpace(b.Name)
			}
			if t != "" {
				titles = append(titles, t)
			}
		}
		fp := komgaJudgmentFP(sr.Name, titles)
		if item, ok := items[sr.ID]; ok && item.FP == fp {
			continue // 判过且书单没变
		}
		// 每系列最多 40 个书名样本：形态看头就够了，别把整柜书塞进一次请求。
		sample := titles
		if len(sample) > 40 {
			sample = sample[:40]
		}
		pending = append(pending, judgeItem{ID: sr.ID, Name: sr.Name, Books: sample, fp: fp})
	}
	if len(pending) == 0 {
		return
	}

	payload, err := json.Marshal(pending)
	if err != nil {
		return
	}
	res, err := llmClient.Chat(ctx, []llm.Message{
		{Role: "system", Content: komgaJudgePrompt},
		{Role: "user", Content: string(payload)},
	}, nil)
	if err != nil {
		log.Printf("komga judgments: llm: %v", err)
		return // 判定失败就退回启发式，不挡扫描
	}
	verdicts, err := parseKomgaJudgments(res.Content, validIDs)
	if err != nil {
		log.Printf("komga judgments: %v", err)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, p := range pending {
		if kind, ok := verdicts[p.ID]; ok {
			items[p.ID] = domain.KomgaJudgment{Kind: kind, FP: p.fp, JudgedAt: now}
		}
	}
	if err := s.store.SaveKomgaJudgments(&domain.KomgaJudgments{
		GeneratedAt: now, Model: llmClient.Model(), Items: items,
	}); err != nil {
		log.Printf("komga judgments: save: %v", err)
		return
	}
	s.invalidateKomgaCatalog() // 形态可能刚变，别让 2 分钟缓存端着旧的
	drawers := 0
	for _, p := range pending {
		if verdicts[p.ID] == "drawer" {
			drawers++
		}
	}
	log.Printf("komga judgments: 判定 %d 个系列（抽屉 %d），共缓存 %d", len(pending), drawers, len(items))
}
