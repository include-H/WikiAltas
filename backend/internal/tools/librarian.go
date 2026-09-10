package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/skill"
	"wikiatlas/backend/internal/store"
)

// Emitter publishes run events from tools (no CoT dump — only summaries).
type Emitter func(eventType string, payload map[string]any)

// LibrarianDeps wires store + run context into real tool implementations.
type LibrarianDeps struct {
	Store     *store.Store
	RunID     string
	Intent    domain.RunIntent
	Goal      string
	Context   map[string]any // workId/docId/parentId/medium/section
	Skill     *skill.Loader
	Emit      Emitter
	ExaAPIKey string
	// CacheGet returns a cached tool result for key, if present.
	CacheGet func(key string) (json.RawMessage, bool)
	// CacheSet stores a tool result.
	CacheSet func(key string, result json.RawMessage)
	// OnPlan is invoked when the model updates the plan.
	OnPlan func(tasks []domain.RunTask)
	// QualityCheck returns issues for create_wiki drafts; nil disables.
	QualityCheck func(md string) (ok bool, issues []string)
}

// NewLibrarianRegistry builds the real tool registry bound to a run.
func NewLibrarianRegistry(d LibrarianDeps) *Registry {
	r := NewRegistry()
	emit := d.Emit
	if emit == nil {
		emit = func(string, map[string]any) {}
	}

	r.Register(NewFuncTool("read_skill", "读取 wiki-writing skill 文件（相对路径，如 SKILL.md / core.md / media-game.md）",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			if d.Skill == nil {
				return jsonOK(map[string]any{"ok": false, "message": "skill 未配置"})
			}
			content, err := d.Skill.ReadFile(p.Path)
			if err != nil {
				return jsonOK(map[string]any{"ok": false, "message": err.Error()})
			}
			if len(content) > 24000 {
				content = content[:24000] + "\n…（截断）"
			}
			return jsonOK(map[string]any{"ok": true, "path": p.Path, "content": content})
		}))

	r.Register(NewFuncTool("search_works", "按标题/别名/正文检索站内作品与资料",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				Q    string `json:"q"`
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			hits, err := d.Store.Search(p.Q, p.Kind, 10)
			if err != nil {
				return nil, err
			}
			items := make([]map[string]any, 0, len(hits))
			for _, h := range hits {
				items = append(items, map[string]any{
					"id": h.ID, "kind": h.Kind, "title": h.Title,
					"slug": h.Slug, "snippet": h.Snippet,
				})
			}
			return jsonOK(map[string]any{"ok": true, "count": len(hits), "hits": items})
		}))

	r.Register(NewFuncTool("read_work", "读取作品元数据与正文",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				ID string `json:"id"`
				// Section 非空时只读该 ## 章节（对齐飞书的 range 读，长条目才读得动）
				Section string `json:"section"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			w, err := d.Store.GetWork(p.ID)
			if err != nil {
				return jsonOK(map[string]any{"ok": false, "message": err.Error()})
			}
			content := ""
			if w.ContentMd != nil {
				content = *w.ContentMd
			}
			if p.Section != "" && content != "" {
				section, err := ExtractSection(content, p.Section)
				if err != nil {
					return jsonOK(map[string]any{"ok": false, "message": err.Error()})
				}
				content = section
			}
			if len([]rune(content)) > 30000 {
				content = string([]rune(content)[:30000]) + "\n…（截断，可用 section 参数只读某一章）"
			}
			medium := ""
			if w.Medium != nil {
				medium = string(*w.Medium)
			}
			return jsonOK(map[string]any{
				"ok": true,
				"work": map[string]any{
					"id": w.ID, "title": w.Title, "kind": string(w.Kind),
					"medium": medium, "status": string(w.Status),
					"parentId": w.ParentID, "contentVer": w.ContentVer,
					"contentMd": content,
				},
			})
		}))

	r.Register(NewFuncTool("get_tree", "获取作品树（可按 parentId 过滤子树）",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				ParentID string `json:"parentId"`
			}
			_ = json.Unmarshal(input, &p)
			nodes, err := d.Store.ListTree()
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(nodes))
			for _, n := range nodes {
				if p.ParentID != "" {
					if n.ParentID == nil || *n.ParentID != p.ParentID {
						continue
					}
				}
				item := map[string]any{
					"id": n.ID, "title": n.Title, "kind": string(n.Kind),
					"status": string(n.Status), "parentId": n.ParentID,
					"hasContent": n.HasContent,
				}
				if n.Medium != nil {
					item["medium"] = string(*n.Medium)
				}
				out = append(out, item)
			}
			if len(out) > 80 {
				out = out[:80]
			}
			return jsonOK(map[string]any{"ok": true, "nodes": out, "count": len(out)})
		}))

	r.Register(NewFuncTool("search_web", "Exa 联网检索（未配置 key 时返回 no web）",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				Q string `json:"q"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			q := strings.TrimSpace(p.Q)
			if q == "" {
				return jsonOK(map[string]any{"ok": false, "message": "q 为空"})
			}
			cacheKey := "exa:" + hashKey(q)
			if d.CacheGet != nil {
				if cached, ok := d.CacheGet(cacheKey); ok {
					return cached, nil
				}
			}
			if d.ExaAPIKey == "" {
				res, _ := jsonOK(map[string]any{
					"ok":      false,
					"message": "no web: 未配置 WIKIATLAS_EXA_API_KEY，跳过联网检索。无法核实的事实请标「待核实」。",
					"query":   q,
				})
				return res, nil
			}
			hits, err := SearchExa(ctx, d.ExaAPIKey, q)
			if err != nil {
				res, _ := jsonOK(map[string]any{"ok": false, "message": "exa error: " + err.Error(), "query": q})
				return res, nil
			}
			res, _ := jsonOK(map[string]any{"ok": true, "query": q, "results": hits, "count": len(hits)})
			if d.CacheSet != nil {
				d.CacheSet(cacheKey, res)
			}
			return res, nil
		}))

	r.Register(NewFuncTool("fetch_url", "抓取允许的网页正文（HTML 转文本）",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			text, err := FetchURL(ctx, p.URL)
			if err != nil {
				return jsonOK(map[string]any{"ok": false, "message": err.Error(), "url": p.URL})
			}
			return jsonOK(map[string]any{"ok": true, "url": p.URL, "text": text})
		}))

	r.Register(NewFuncTool("write_content", "整篇替换作品/资料正文（走 store，发 content.staging → content.committed）",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				TargetType string `json:"targetType"` // work | doc
				TargetID   string `json:"targetId"`
				ContentMd  string `json:"contentMd"`
				Summary    string `json:"summary"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			if p.TargetID == "" {
				if id, _ := d.Context["workId"].(string); id != "" && p.TargetType == "" {
					p.TargetType, p.TargetID = "work", id
				}
				if id, _ := d.Context["docId"].(string); id != "" && p.TargetType == "" {
					p.TargetType, p.TargetID = "doc", id
				}
			}
			if p.TargetType == "" {
				p.TargetType = "work"
			}
			if p.TargetID == "" {
				return jsonOK(map[string]any{"ok": false, "message": "targetId 缺失"})
			}
			if p.Summary == "" {
				p.Summary = "馆员写入"
			}
			qualityOK := true
			var qualityIssues []string
			if d.QualityCheck != nil &&
				(d.Intent == domain.RunIntentCreateWiki || d.Intent == domain.RunIntentContinueWiki) {
				qualityOK, qualityIssues = d.QualityCheck(p.ContentMd)
				if !qualityOK {
					p.Summary += "（low quality: " + strings.Join(qualityIssues, "；") + "）"
				}
			}
			preview := p.ContentMd
			if len([]rune(preview)) > 200 {
				r := []rune(preview)
				preview = string(r[:200]) + "…"
			}
			emit("content.staging", map[string]any{
				"targetType": p.TargetType,
				"targetId":   p.TargetID,
				"previewMd":  preview,
			})
			summary := p.Summary
			runID := d.RunID
			body := domain.PutContentBody{
				ContentMd: p.ContentMd,
				Author:    domain.AuthorLLM,
				Summary:   &summary,
			}
			if runID != "" {
				body.RunID = &runID
			}
			var (
				res *domain.ContentCommitResult
				err error
			)
			if p.TargetType == "doc" {
				res, err = d.Store.PutDocContent(p.TargetID, body)
			} else {
				res, err = d.Store.PutWorkContent(p.TargetID, body)
			}
			if err != nil {
				return jsonOK(map[string]any{"ok": false, "message": err.Error()})
			}
			emit("content.committed", map[string]any{
				"targetType": p.TargetType,
				"targetId":   p.TargetID,
				"version":    res.ContentVer,
			})
			if p.TargetType == "work" {
				emit("tree.updated", map[string]any{
					"works": []map[string]any{{"id": p.TargetID, "status": "draft"}},
				})
			}
			return jsonOK(map[string]any{
				"ok": true, "targetType": p.TargetType, "targetId": p.TargetID,
				"contentVer": res.ContentVer, "revisionId": res.RevisionID,
			})
		}))

	r.Register(NewFuncTool("patch_section", "按 ## 锚点局部替换章节正文",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				TargetID    string `json:"targetId"`
				Heading     string `json:"heading"`
				NewMarkdown string `json:"newMarkdown"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			if p.TargetID == "" {
				p.TargetID, _ = d.Context["workId"].(string)
			}
			w, err := d.Store.GetWork(p.TargetID)
			if err != nil {
				return jsonOK(map[string]any{"ok": false, "message": err.Error()})
			}
			cur := ""
			if w.ContentMd != nil {
				cur = *w.ContentMd
			}
			next, err := ReplaceSection(cur, p.Heading, p.NewMarkdown)
			if err != nil {
				return jsonOK(map[string]any{"ok": false, "message": err.Error()})
			}
			summary := "局部重写：" + p.Heading
			runID := d.RunID
			body := domain.PutContentBody{
				ContentMd: next,
				Author:    domain.AuthorLLM,
				Summary:   &summary,
			}
			if runID != "" {
				body.RunID = &runID
			}
			emit("content.staging", map[string]any{
				"targetType": "work", "targetId": p.TargetID,
				"previewMd": truncateRunes(p.NewMarkdown, 200),
			})
			res, err := d.Store.PutWorkContent(p.TargetID, body)
			if err != nil {
				return jsonOK(map[string]any{"ok": false, "message": err.Error()})
			}
			emit("content.committed", map[string]any{
				"targetType": "work", "targetId": p.TargetID, "version": res.ContentVer,
			})
			return jsonOK(map[string]any{"ok": true, "contentVer": res.ContentVer, "revisionId": res.RevisionID})
		}))

	r.Register(NewFuncTool("upsert_work", "创建或更新作品节点",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				ID       string `json:"id"`
				ParentID string `json:"parentId"`
				Kind     string `json:"kind"`
				Medium   string `json:"medium"`
				Title    string `json:"title"`
				Status   string `json:"status"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			if p.ID != "" {
				patch := domain.PatchWorkBody{}
				if p.Title != "" {
					patch.Title = &p.Title
				}
				if p.ParentID != "" {
					patch.ParentID = &p.ParentID
				}
				if p.Medium != "" {
					m := domain.Medium(p.Medium)
					patch.Medium = &m
				}
				if p.Status != "" {
					st := domain.WorkStatus(p.Status)
					patch.Status = &st
				}
				w, err := d.Store.PatchWork(p.ID, patch)
				if err != nil {
					return jsonOK(map[string]any{"ok": false, "message": err.Error()})
				}
				return jsonOK(map[string]any{"ok": true, "action": "updated", "id": w.ID, "title": w.Title})
			}
			if p.Title == "" {
				return jsonOK(map[string]any{"ok": false, "message": "title 必填"})
			}
			kind := domain.WorkKind(p.Kind)
			if kind == "" {
				kind = domain.WorkKindWork
			}
			body := domain.CreateWorkBody{Kind: kind, Title: p.Title}
			if p.ParentID != "" {
				body.ParentID = &p.ParentID
			}
			if p.Medium != "" {
				m := domain.Medium(p.Medium)
				body.Medium = &m
			}
			w, err := d.Store.CreateWork(body)
			if err != nil {
				return jsonOK(map[string]any{"ok": false, "message": err.Error()})
			}
			emit("tree.updated", map[string]any{
				"works": []map[string]any{{"id": w.ID, "title": w.Title, "status": string(w.Status)}},
			})
			return jsonOK(map[string]any{"ok": true, "action": "created", "id": w.ID, "title": w.Title})
		}))

	r.Register(NewFuncTool("upsert_relation", "创建有向关系（冻结词典）",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				FromID string `json:"fromId"`
				ToID   string `json:"toId"`
				Type   string `json:"type"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			rel, err := d.Store.CreateRelation(domain.CreateRelationBody{
				FromID: p.FromID,
				ToID:   p.ToID,
				Type:   domain.RelationType(p.Type),
			})
			if err != nil {
				return jsonOK(map[string]any{"ok": false, "message": err.Error()})
			}
			return jsonOK(map[string]any{"ok": true, "id": rel.ID, "type": string(rel.Type)})
		}))

	r.Register(NewFuncTool("create_doc", "在作品资料夹创建资料文档",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				WorkID    string   `json:"workId"`
				Title     string   `json:"title"`
				ContentMd string   `json:"contentMd"`
				Links     []string `json:"links"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			if p.WorkID == "" {
				p.WorkID, _ = d.Context["workId"].(string)
			}
			body := domain.CreateDocBody{Title: p.Title, Links: p.Links}
			if p.ContentMd != "" {
				body.ContentMd = &p.ContentMd
			}
			doc, err := d.Store.CreateDoc(p.WorkID, body)
			if err != nil {
				return jsonOK(map[string]any{"ok": false, "message": err.Error()})
			}
			return jsonOK(map[string]any{"ok": true, "id": doc.ID, "title": doc.Title, "slug": doc.Slug})
		}))

	r.Register(NewFuncTool("attach_library_link", "挂接 Emby/Komga/GameAtlas 外链",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				WorkID     string `json:"workId"`
				Source     string `json:"source"`
				ExternalID string `json:"externalId"`
				URL        string `json:"url"`
				TitleHint  string `json:"titleHint"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			var url, hint *string
			if p.URL != "" {
				url = &p.URL
			}
			if p.TitleHint != "" {
				hint = &p.TitleHint
			}
			l, err := d.Store.CreateLibraryLink(p.WorkID, domain.LibrarySource(p.Source), p.ExternalID, url, hint)
			if err != nil {
				return jsonOK(map[string]any{"ok": false, "message": err.Error()})
			}
			return jsonOK(map[string]any{"ok": true, "id": l.ID})
		}))

	r.Register(NewFuncTool("update_plan", "更新工单计划任务状态",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				Tasks []domain.RunTask `json:"tasks"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			if d.OnPlan != nil {
				d.OnPlan(p.Tasks)
			} else if d.Store != nil && d.RunID != "" {
				_ = d.Store.UpdateRunPlan(d.RunID, p.Tasks)
			}
			emit("plan.updated", map[string]any{"tasks": taskMaps(p.Tasks)})
			return jsonOK(map[string]any{"ok": true, "count": len(p.Tasks)})
		}))

	r.Register(NewFuncTool("narrative", "向用户播报一行进度（中文，无 CoT）",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			text := strings.TrimSpace(p.Text)
			if text == "" {
				return jsonOK(map[string]any{"ok": false, "message": "text 为空"})
			}
			emit("narrative", map[string]any{"text": text})
			return jsonOK(map[string]any{"ok": true})
		}))

	r.Register(NewFuncTool("answer", "口头回答用户，可附 work/doc id",
		func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			var p struct {
				Text    string   `json:"text"`
				WorkIDs []string `json:"workIds"`
				DocIDs  []string `json:"docIds"`
			}
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, err
			}
			emit("narrative", map[string]any{"text": p.Text})
			return jsonOK(map[string]any{"ok": true, "workIds": p.WorkIDs, "docIds": p.DocIDs})
		}))

	return r
}

// ToolSchemas returns OpenAI tool definitions matching NewLibrarianRegistry.
func ToolSchemas() []ToolSchema {
	return []ToolSchema{
		{Name: "read_skill", Desc: "读取 wiki-writing skill 文件", Props: map[string]any{
			"path": map[string]any{"type": "string", "description": "相对路径，如 SKILL.md"},
		}, Required: []string{"path"}},
		{Name: "search_works", Desc: "站内检索作品/资料", Props: map[string]any{
			"q":    map[string]any{"type": "string"},
			"kind": map[string]any{"type": "string", "description": "work|doc，可空"},
		}, Required: []string{"q"}},
		{Name: "read_work", Desc: "读取作品元数据与正文", Props: map[string]any{
			"id":      map[string]any{"type": "string"},
			"section": map[string]any{"type": "string", "description": "可空；只读该 ## 章节（标题关键词）"},
		}, Required: []string{"id"}},
		{Name: "get_tree", Desc: "获取作品树", Props: map[string]any{
			"parentId": map[string]any{"type": "string", "description": "可空，过滤子树"},
		}},
		{Name: "search_web", Desc: "Exa 联网检索", Props: map[string]any{
			"q": map[string]any{"type": "string"},
		}, Required: []string{"q"}},
		{Name: "fetch_url", Desc: "抓取网页正文", Props: map[string]any{
			"url": map[string]any{"type": "string"},
		}, Required: []string{"url"}},
		{Name: "write_content", Desc: "整篇替换正文并 commit revision", Props: map[string]any{
			"targetType": map[string]any{"type": "string", "description": "work|doc"},
			"targetId":   map[string]any{"type": "string"},
			"contentMd":  map[string]any{"type": "string"},
			"summary":    map[string]any{"type": "string"},
		}, Required: []string{"contentMd"}},
		{Name: "patch_section", Desc: "按 ## 锚点局部替换", Props: map[string]any{
			"targetId":    map[string]any{"type": "string"},
			"heading":     map[string]any{"type": "string"},
			"newMarkdown": map[string]any{"type": "string"},
		}, Required: []string{"heading", "newMarkdown"}},
		{Name: "upsert_work", Desc: "创建或更新作品节点", Props: map[string]any{
			"id":       map[string]any{"type": "string"},
			"parentId": map[string]any{"type": "string"},
			"kind":     map[string]any{"type": "string", "description": "universe|series|work"},
			"medium":   map[string]any{"type": "string"},
			"title":    map[string]any{"type": "string"},
			"status":   map[string]any{"type": "string", "description": "stub|draft|ready"},
		}},
		{Name: "upsert_relation", Desc: "创建有向关系", Props: map[string]any{
			"fromId": map[string]any{"type": "string"},
			"toId":   map[string]any{"type": "string"},
			"type":   map[string]any{"type": "string", "description": "adaptation_of|sequel_to|spin_off_of|remake_of|expansion_of|references|same_series"},
		}, Required: []string{"fromId", "toId", "type"}},
		{Name: "create_doc", Desc: "创建资料文档", Props: map[string]any{
			"workId":    map[string]any{"type": "string"},
			"title":     map[string]any{"type": "string"},
			"contentMd": map[string]any{"type": "string"},
			"links":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}, Required: []string{"title"}},
		{Name: "attach_library_link", Desc: "挂接媒体库外链", Props: map[string]any{
			"workId":     map[string]any{"type": "string"},
			"source":     map[string]any{"type": "string", "description": "emby|komga|gameatlas"},
			"externalId": map[string]any{"type": "string"},
			"url":        map[string]any{"type": "string"},
			"titleHint":  map[string]any{"type": "string"},
		}, Required: []string{"source", "externalId"}},
		{Name: "update_plan", Desc: "更新计划任务", Props: map[string]any{
			"tasks": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":     map[string]any{"type": "string"},
						"title":  map[string]any{"type": "string"},
						"status": map[string]any{"type": "string"},
					},
				},
			},
		}, Required: []string{"tasks"}},
		{Name: "narrative", Desc: "一行中文进度播报", Props: map[string]any{
			"text": map[string]any{"type": "string"},
		}, Required: []string{"text"}},
		{Name: "answer", Desc: "回答用户", Props: map[string]any{
			"text":    map[string]any{"type": "string"},
			"workIds": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"docIds":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}, Required: []string{"text"}},
	}
}

// ToolSchema is a compact description converted to llm.ToolDef.
type ToolSchema struct {
	Name     string
	Desc     string
	Props    map[string]any
	Required []string
}

func jsonOK(v map[string]any) (json.RawMessage, error) {
	if v == nil {
		v = map[string]any{"ok": true}
	}
	return json.Marshal(v)
}

func taskMaps(tasks []domain.RunTask) []map[string]any {
	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, map[string]any{"id": t.ID, "title": t.Title, "status": t.Status})
	}
	return out
}

func hashKey(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// --- section patch ---

var sectionHeading = regexp.MustCompile(`(?m)^(#{2,3})\s+(.+?)\s*$`)

// ExtractSection 返回按标题关键词匹配到的 ##/### 章节（含标题行，到下一个同级或更高级标题为止）。
func ExtractSection(md, heading string) (string, error) {
	heading = strings.TrimSpace(heading)
	if heading == "" {
		return "", fmt.Errorf("heading 为空")
	}
	locs := sectionHeading.FindAllStringSubmatchIndex(md, -1)
	if len(locs) == 0 {
		return "", fmt.Errorf("未找到任何 ## 章节")
	}
	target := -1
	var level int
	for i, loc := range locs {
		title := md[loc[4]:loc[5]]
		if strings.Contains(title, heading) || strings.EqualFold(strings.TrimSpace(title), heading) {
			target = i
			level = len(md[loc[2]:loc[3]])
			break
		}
	}
	if target < 0 {
		return "", fmt.Errorf("未找到章节: %s", heading)
	}
	end := len(md)
	for i := target + 1; i < len(locs); i++ {
		lvl := len(md[locs[i][2]:locs[i][3]])
		if lvl <= level {
			end = locs[i][0]
			break
		}
	}
	return strings.TrimSpace(md[locs[target][0]:end]), nil
}

// ReplaceSection replaces the body of a ##/### section matched by heading substring.
func ReplaceSection(md, heading, newMarkdown string) (string, error) {
	heading = strings.TrimSpace(heading)
	if heading == "" {
		return "", fmt.Errorf("heading 为空")
	}
	locs := sectionHeading.FindAllStringSubmatchIndex(md, -1)
	if len(locs) == 0 {
		return "", fmt.Errorf("未找到任何 ## 章节")
	}
	target := -1
	var level int
	for i, loc := range locs {
		title := md[loc[4]:loc[5]]
		if strings.Contains(title, heading) || strings.EqualFold(strings.TrimSpace(title), heading) {
			target = i
			level = len(md[loc[2]:loc[3]])
			break
		}
	}
	if target < 0 {
		return "", fmt.Errorf("未找到章节: %s", heading)
	}
	start := locs[target][1]
	end := len(md)
	for i := target + 1; i < len(locs); i++ {
		lvl := len(md[locs[i][2]:locs[i][3]])
		if lvl <= level {
			end = locs[i][0]
			break
		}
	}
	body := strings.TrimSpace(newMarkdown)
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return md[:start] + "\n" + body + md[end:], nil
}

// --- web ---

type exaResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Text    string `json:"text"`
		Snippet string `json:"snippet"`
	} `json:"results"`
}

// SearchExa calls the Exa search API.
func SearchExa(ctx context.Context, apiKey, query string) ([]map[string]any, error) {
	payload := map[string]any{
		"query":      query,
		"numResults": 5,
		"contents":   map[string]any{"text": true, "maxCharacters": 1800},
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.exa.ai/search", strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("exa http %d: %s", resp.StatusCode, truncateRunes(string(body), 200))
	}
	var er exaResponse
	if err := json.Unmarshal(body, &er); err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(er.Results))
	for _, r := range er.Results {
		text := r.Text
		if text == "" {
			text = r.Snippet
		}
		if len(text) > 1500 {
			text = text[:1500] + "…"
		}
		out = append(out, map[string]any{"title": r.Title, "url": r.URL, "text": text})
	}
	return out, nil
}

var (
	htmlTag   = regexp.MustCompile(`(?s)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`)
	htmlStrip = regexp.MustCompile(`<[^>]+>`)
	spaceRe   = regexp.MustCompile(`[ \t\r\f\v]+`)
	multiNL   = regexp.MustCompile(`\n{3,}`)
)

// FetchURL downloads a URL and returns plain text (best-effort HTML strip).
func FetchURL(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("only http/https allowed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "WikiAltas/2.0 (+librarian)")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	s := string(body)
	s = htmlTag.ReplaceAllString(s, " ")
	s = htmlStrip.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = spaceRe.ReplaceAllString(s, " ")
	s = multiNL.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	if len(s) > 12000 {
		s = s[:12000] + "…"
	}
	if s == "" {
		return "", fmt.Errorf("empty body")
	}
	return s, nil
}

// ExaKeyFromEnv returns the Exa API key from env.
func ExaKeyFromEnv() string {
	if v := os.Getenv("WIKIATLAS_EXA_API_KEY"); v != "" {
		return v
	}
	return os.Getenv("EXA_API_KEY")
}
