package run

import (
	"context"
	"fmt"
	"strings"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/skill"
)

// executeMock is the no-LLM path. It still reads skill files and emits a
// realistic Feishu/MiMo-style event sequence so the frontend can be exercised.
func (m *Manager) executeMock(ctx context.Context, runID string, intent domain.RunIntent, goal string, ctxMap map[string]any, fromStep int) {
	workID, _ := ctxMap["workId"].(string)
	docID, _ := ctxMap["docId"].(string)
	mediumStr, _ := ctxMap["medium"].(string)
	medium := domain.Medium(mediumStr)
	if medium == "" && workID != "" {
		if w, err := m.store.GetWork(workID); err == nil && w.Medium != nil {
			medium = *w.Medium
		}
	}

	loader := m.skillLoader()
	var skillNames []string
	var skillOK bool
	if loader != nil {
		if files, err := loader.FilesForIntent(intent, medium); err == nil {
			skillNames = skill.Names(files)
			skillOK = len(skillNames) > 0
		}
	}

	steps := []struct {
		title string
		fn    func() error
	}{
		{"理解目标", func() error {
			m.emit(runID, "narrative", map[string]any{"text": fmt.Sprintf("收到工单：%s", goal)})
			m.emit(runID, "narrative", map[string]any{"text": "我先读 skill 和站内已有条目，再联网核实关键事实。"})
			m.emit(runID, "tool.started", map[string]any{
				"name": "read_skill", "inputSummary": "SKILL.md + core.md + media",
			})
			time.Sleep(20 * time.Millisecond)
			if skillOK {
				m.emit(runID, "tool.done", map[string]any{
					"name":          "read_skill",
					"outputSummary": "已加载 " + strings.Join(skillNames, " + "),
					"durationMs":    20,
				})
			} else {
				m.emit(runID, "tool.done", map[string]any{
					"name":          "read_skill",
					"outputSummary": "skill 未配置，按通用边界继续",
					"durationMs":    20,
				})
			}
			return nil
		}},
		{"检索资料", func() error {
			m.emit(runID, "tool.started", map[string]any{"name": "search_works", "inputSummary": "q=" + truncate(goal, 40)})
			time.Sleep(20 * time.Millisecond)
			hits, _ := m.store.Search(goal, "", 5)
			m.emit(runID, "tool.done", map[string]any{
				"name": "search_works", "outputSummary": fmt.Sprintf("命中 %d 条", len(hits)), "durationMs": 20,
			})
			m.emit(runID, "tool.started", map[string]any{"name": "search_web", "inputSummary": "q=" + truncate(goal, 40)})
			time.Sleep(20 * time.Millisecond)
			m.emit(runID, "tool.done", map[string]any{
				"name":          "search_web",
				"outputSummary": "no web：未配置 Exa key",
				"durationMs":    20,
			})
			m.emit(runID, "narrative", map[string]any{"text": "站内检索完毕；联网检索不可用，事实项将标「待核实」。"})
			return nil
		}},
		{"产出内容", func() error {
			m.emit(runID, "plan.updated", map[string]any{
				"tasks": []map[string]any{
					{"id": "t1", "title": "理解目标", "status": "completed"},
					{"id": "t2", "title": "检索资料", "status": "completed"},
					{"id": "t3", "title": "产出内容", "status": "in_progress"},
					{"id": "t4", "title": "写入并完成", "status": "pending"},
				},
			})
			m.emit(runID, "narrative", map[string]any{"text": "资料核实完毕。现在按骨架写入条目（无模型演示内容）。"})
			return nil
		}},
		{"写入并完成", func() error {
			if workID == "" && docID == "" {
				m.emit(runID, "narrative", map[string]any{
					"text": "（未绑定作品/资料，跳过写入。配置 WIKIATLAS_LLM_* 后可调用真实模型。）",
				})
				return nil
			}
			md := mockMarkdown(goal, intent, medium, skillNames)
			preview := md
			if r := []rune(preview); len(r) > 200 {
				preview = string(r[:200]) + "…"
			}
			targetType, targetID := "work", workID
			if targetID == "" {
				targetType, targetID = "doc", docID
			}
			m.emit(runID, "tool.started", map[string]any{
				"name":         "write_content",
				"inputSummary": fmt.Sprintf("target=%s:%s len=%d", targetType, targetID, len([]rune(md))),
			})
			m.emit(runID, "content.staging", map[string]any{
				"targetType": targetType,
				"targetId":   targetID,
				"previewMd":  preview,
			})
			author := domain.AuthorLLM
			summary := "mock librarian write"
			var err error
			if targetType == "doc" {
				_, err = m.store.PutDocContent(targetID, domain.PutContentBody{
					ContentMd: md,
					Author:    author,
					RunID:     &runID,
					Summary:   &summary,
				})
			} else {
				var res *domain.ContentCommitResult
				res, err = m.store.PutWorkContent(targetID, domain.PutContentBody{
					ContentMd: md,
					Author:    author,
					RunID:     &runID,
					Summary:   &summary,
				})
				if err == nil {
					m.emit(runID, "content.committed", map[string]any{
						"targetType": targetType,
						"targetId":   targetID,
						"version":    res.ContentVer,
					})
				}
			}
			if err != nil {
				return err
			}
			if targetType == "doc" {
				// committed event for docs
				if d, e := m.store.GetDoc(targetID); e == nil {
					m.emit(runID, "content.committed", map[string]any{
						"targetType": "doc",
						"targetId":   targetID,
						"version":    d.ContentVer,
					})
				}
			}
			m.emit(runID, "tool.done", map[string]any{
				"name": "write_content", "outputSummary": "已提交 revision", "durationMs": 30,
			})

			// quality gate
			q := CheckWikiQuality(md)
			if q.OK {
				if targetType == "work" {
					st := domain.WorkStatusReady
					_, _ = m.store.PatchWork(targetID, domain.PatchWorkBody{Status: &st})
				}
				m.emit(runID, "tree.updated", map[string]any{
					"works": []map[string]any{{"id": targetID, "status": "ready"}},
				})
				m.emit(runID, "narrative", map[string]any{"text": "质量自检通过，已标记 ready。"})
			} else {
				if targetType == "work" {
					st := domain.WorkStatusDraft
					_, _ = m.store.PatchWork(targetID, domain.PatchWorkBody{Status: &st})
				}
				m.emit(runID, "tree.updated", map[string]any{
					"works": []map[string]any{{"id": targetID, "status": "draft"}},
				})
				m.emit(runID, "narrative", map[string]any{
					"text": "质量自检未达标（演示稿），保留 draft：" + strings.Join(q.Issues, "；"),
				})
			}
			m.emit(runID, "narrative", map[string]any{"text": "已写入正文并提交 revision。"})
			return nil
		}},
	}

	if fromStep > 0 {
		plan := make([]domain.RunTask, len(steps))
		for i, s := range steps {
			st := "pending"
			if i < fromStep {
				st = "completed"
			}
			plan[i] = domain.RunTask{ID: fmt.Sprintf("t%d", i+1), Title: s.title, Status: st}
		}
		_ = m.store.UpdateRunPlan(runID, plan)
	}

	for i := fromStep; i < len(steps); i++ {
		select {
		case <-ctx.Done():
			_ = m.store.InterruptRun(runID)
			return
		default:
		}
		plan := make([]domain.RunTask, len(steps))
		for j, s := range steps {
			st := "pending"
			switch {
			case j < i:
				st = "completed"
			case j == i:
				st = "in_progress"
			}
			plan[j] = domain.RunTask{ID: fmt.Sprintf("t%d", j+1), Title: s.title, Status: st}
		}
		_ = m.store.UpdateRunPlan(runID, plan)

		if err := steps[i].fn(); err != nil {
			m.emit(runID, "run.failed", map[string]any{"error": err.Error()})
			_ = m.store.FailRun(runID, map[string]any{"error": err.Error(), "step": i})
			return
		}
		cp := map[string]any{"lastStep": i + 1, "context": ctxMap}
		_ = m.store.UpdateRunCheckpoint(runID, cp, map[string]any{})
	}

	plan := make([]domain.RunTask, len(steps))
	for j, s := range steps {
		plan[j] = domain.RunTask{ID: fmt.Sprintf("t%d", j+1), Title: s.title, Status: "completed"}
	}
	_ = m.store.UpdateRunPlan(runID, plan)
	m.emit(runID, "run.completed", map[string]any{"summary": "工单完成（mock executor）"})
	_ = m.store.CompleteRun(runID, map[string]any{"summary": "ok", "intent": string(intent), "mock": true})
}

func mockMarkdown(goal string, intent domain.RunIntent, medium domain.Medium, skillNames []string) string {
	var b strings.Builder
	title := goal
	if title == "" {
		title = "未命名条目"
	}
	b.WriteString("# " + title + "\n\n")
	b.WriteString("> 说明：mock Altas 生成的演示稿。配置 `WIKIATLAS_LLM_API_KEY` 后将调用真实模型并按 skill 写作。\n")
	if len(skillNames) > 0 {
		b.WriteString("> 已参考 skill：" + strings.Join(skillNames, "、") + "。\n")
	}
	b.WriteString("> 剧透提示：以下为结构演示，不含真实剧情细节。\n\n")
	b.WriteString(":::epigraph\n演示题记：此处应由场景式题记占据。\n:::\n\n")

	if intent == domain.RunIntentWriteDoc {
		b.WriteString("## 资料说明\n\n本篇为资料文档演示，不强制 9 章骨架。\n\n")
		b.WriteString("## 要点\n\n- 唯一正文真相是 `content_md`\n- 写完即 commit，无审批\n- 回滚靠 revision\n\n")
		return b.String()
	}

	b.WriteString("## 1. 作品概览\n\n本条目由 Altas 工单自动写入，用于验证 SSE 推送与 revision 落库。介质：" + string(medium) + "。\n\n")
	b.WriteString("## 2. 基础信息速览\n\n### 2.1 基本资料\n\n- 原名：待核实\n- 首发日期：待核实\n- 首发平台：待核实\n\n### 2.2 版本关系\n\n- 版本关系：待核实\n\n### 2.3 核心卖点\n\n- 结构演示，非真实卖点\n\n### 2.4 关键词\n\n- 待核实\n\n")
	b.WriteString("## 3. 故事背景与设定\n\n背景设定演示段落。查不到的事实一律标「待核实」。\n\n")
	b.WriteString("## 4. 剧情概要\n\n### 4.1 开端\n\n演示性开端描述，真实条目应按作品结构分节写到结局。\n\n### 4.2 转折\n\n演示性转折。\n\n### 4.3 结局\n\n演示性结局。\n\n")
	b.WriteString("## 5. 创作特色\n\n### 5.1 手法\n\n机制/镜头/叙事手法应挂到具体场景。\n\n### 5.2 效果\n\n读者实际感受到的结果。\n\n")
	b.WriteString("## 6. 主要角色与创作团队\n\n### 6.1 角色\n\n角色名：说明（演示）\n\n### 6.2 关系简述\n\n用一小段点出 2–3 层关键关系。\n\n")
	b.WriteString("## 7. 评价与历史定位\n\n- 评分：待核实\n- 历史定位：待核实\n\n")
	b.WriteString("## 8. 一句话总结\n\n这是一篇用于验证管线的演示稿。\n\n")
	b.WriteString("## 9. 参考资料\n\n1. [待核实](about:blank)\n")
	return b.String()
}
