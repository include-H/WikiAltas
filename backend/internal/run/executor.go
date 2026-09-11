package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/llm"
	"wikiatlas/backend/internal/skill"
	"wikiatlas/backend/internal/tools"
)

const maxToolRounds = 40

// 上下文预算：模型窗口有限，长工单的 tool 返回会把它撑爆（表现为后半程开始胡说、忘事）。
const (
	contextBudgetChars = 120_000 // 约 4 万 token，给 32k/128k 窗口都留余量
	contextKeepRecent  = 12      // 压缩时保留最近的消息条数
	maxSameToolCall    = 3       // 同名同参工具的重复上限（超过就拦）
)

// 会改动内容的工具：只读模式下这些一律拒绝执行。
var writeToolNames = map[string]bool{
	"write_content":       true,
	"patch_section":       true,
	"upsert_work":         true,
	"upsert_relation":     true,
	"create_doc":          true,
	"attach_library_link": true,
	"sync_library":        true,
}

// controlTool 是"播报/计划"这类控制面工具：即使重复调用也不拦（模型常用来汇报进度）。
func controlTool(name string) bool {
	switch name {
	case "narrative", "answer", "update_plan":
		return true
	default:
		return false
	}
}

// executeLLM runs the real tool-calling chat loop.
func (m *Manager) executeLLM(ctx context.Context, runID string, intent domain.RunIntent, goal string, ctxMap map[string]any, fromStep int) {
	client := m.activeClient()
	loader := m.skillLoader()

	workID, _ := ctxMap["workId"].(string)
	docID, _ := ctxMap["docId"].(string)
	mediumStr, _ := ctxMap["medium"].(string)
	medium := domain.Medium(mediumStr)
	// 用户发起工单时所在的文档模式（read | edit | revision）
	docMode, _ := ctxMap["docMode"].(string)
	if docMode == "" {
		docMode = "edit"
	}

	// resolve medium from work when missing
	if medium == "" && workID != "" {
		if w, err := m.store.GetWork(workID); err == nil && w.Medium != nil {
			medium = *w.Medium
			ctxMap["medium"] = string(medium)
		}
	}

	var skillFiles []skill.File
	var skillErr error
	if loader != nil {
		skillFiles, skillErr = loader.FilesForIntent(intent, medium)
	}

	m.emit(runID, "narrative", map[string]any{"text": "收到工单：" + goal})
	if skillErr != nil {
		m.emit(runID, "narrative", map[string]any{"text": "skill 加载失败：" + skillErr.Error() + "，将按通用写作边界继续。"})
	} else if len(skillFiles) > 0 {
		m.emit(runID, "narrative", map[string]any{
			"text": "已加载 wiki-writing skill：" + strings.Join(skill.Names(skillFiles), " + "),
		})
	}

	// build tool registry + cache
	cache := map[string]any{}
	if r, err := m.store.GetRun(runID); err == nil && r.ToolCache != nil {
		cache = r.ToolCache
	}
	cacheGet := func(key string) (json.RawMessage, bool) {
		v, ok := cache[key]
		if !ok {
			return nil, false
		}
		b, _ := json.Marshal(v)
		return b, true
	}
	cacheSet := func(key string, result json.RawMessage) {
		var v any
		if err := json.Unmarshal(result, &v); err == nil {
			cache[key] = v
		}
	}

	reg := tools.NewLibrarianRegistry(tools.LibrarianDeps{
		Store:     m.store,
		RunID:     runID,
		Intent:    intent,
		Goal:      goal,
		Context:   ctxMap,
		Skill:     loader,
		ExaAPIKey: m.exaKey(),
		CacheGet:  cacheGet,
		CacheSet:  cacheSet,
		Emit: func(eventType string, payload map[string]any) {
			m.emit(runID, eventType, payload)
		},
		OnPlan: func(tasks []domain.RunTask) {
			_ = m.store.UpdateRunPlan(runID, tasks)
		},
		QualityCheck: func(md string) (bool, []string) {
			q := CheckWikiQuality(md)
			return q.OK, q.Issues
		},
	})

	toolDefs := buildToolDefsFor(intent, docMode)

	// messages: restore from checkpoint on resume
	var messages []llm.Message
	iteration := 0
	if fromStep > 0 {
		if r, err := m.store.GetRun(runID); err == nil {
			if restored, ok := restoreMessages(r.Checkpoint); ok && len(restored) > 0 {
				messages = restored
			}
			if it, ok := r.Checkpoint["iteration"].(float64); ok {
				iteration = int(it)
			}
		}
	}
	if len(messages) == 0 {
		brief := buildContextBrief(m.store, workID, docID)
		messages = buildInitialMessages(intent, goal, ctxMap, skillFiles, medium, workID, docID, docMode, brief)
	}

	completed := map[string]bool{}
	if r, err := m.store.GetRun(runID); err == nil {
		for _, t := range r.Plan {
			if t.Status == "completed" {
				completed[t.ID] = true
			}
		}
	}

	wroteContent := false
	lastErr := ""
	// 同一章节的改写次数：防止模型对着某一章反复重写烧额度（观测中它连改 6 次）
	sectionRewrites := map[string]int{}
	// 同名同参调用次数：防止模型原地绕圈（同一个 read/search 调 5 遍）
	callCounts := map[string]int{}

	for round := 0; round < maxToolRounds; round++ {
		select {
		case <-ctx.Done():
			m.checkpointLLM(runID, ctxMap, messages, iteration, completed, cache, false)
			_ = m.store.InterruptRun(runID)
			return
		default:
		}

		iteration++
		// 上下文压缩：超过预算就丢掉中间的工具往返，避免把模型窗口撑爆
		if compacted, dropped := compactHistory(messages); dropped > 0 {
			messages = compacted
			m.emit(runID, "narrative", map[string]any{
				"text": fmt.Sprintf("上下文较长，已压缩 %d 条历史工具记录。", dropped),
			})
		}
		result, err := m.callModel(ctx, runID, client, messages, toolDefs)
		if err != nil {
			lastErr = err.Error()
			m.emit(runID, "narrative", map[string]any{"text": "模型调用失败：" + lastErr})
			m.checkpointLLM(runID, ctxMap, messages, iteration, completed, cache, wroteContent)
			m.emit(runID, "run.failed", map[string]any{"error": lastErr})
			_ = m.store.FailRun(runID, map[string]any{"error": lastErr, "iteration": iteration})
			return
		}

		// surface short assistant text as narrative (never raw CoT dumps)
		if txt := strings.TrimSpace(result.Content); txt != "" && len(result.ToolCalls) == 0 {
			// final answer without tools
			m.emit(runID, "narrative", map[string]any{"text": truncate(txt, 500)})
			messages = append(messages, llm.Message{Role: "assistant", Content: txt})
			break
		} else if txt := strings.TrimSpace(result.Content); txt != "" && len(txt) < 300 {
			m.emit(runID, "narrative", map[string]any{"text": truncate(txt, 300)})
		}

		if len(result.ToolCalls) == 0 {
			// no tools, no content — stop
			break
		}

		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   result.Content,
			ToolCalls: result.ToolCalls,
		})

		for _, tc := range result.ToolCalls {
			select {
			case <-ctx.Done():
				m.checkpointLLM(runID, ctxMap, messages, iteration, completed, cache, wroteContent)
				_ = m.store.InterruptRun(runID)
				return
			default:
			}

			name := tc.Function.Name
			args := tc.Function.Arguments
			inputSummary := summarizeToolInput(name, args)

			// 同名同参重复调用（模型绕圈子的典型症状）：超过上限就不再执行，
			// 直接把"别再重复了"喂回去，省额度也防止对着同一段反复改。
			callKey := name + "|" + shortHash(args)
			callCounts[callKey]++
			if callCounts[callKey] > maxSameToolCall && !controlTool(name) {
				out, _ := json.Marshal(map[string]any{
					"ok": false,
					"message": fmt.Sprintf("同一调用（%s）已重复 %d 次，不会再生效。请换一种做法，或直接收尾并给出结论。",
						name, callCounts[callKey]-1),
				})
				messages = append(messages, llm.Message{Role: "tool", ToolCallID: tc.ID, Name: name, Content: string(out)})
				m.emit(runID, "tool.done", map[string]any{
					"name": name, "outputSummary": "重复调用已拦截", "durationMs": 0,
				})
				continue
			}

			// 只读模式：写工具一律拒绝（模型仍然可以检索、阅读、回答、播报）
			if docMode == "read" && writeToolNames[name] {
				out, _ := json.Marshal(map[string]any{
					"ok":      false,
					"message": "当前文档处于只读模式：用户只是让 Altas「看」。请用 answer / narrative 给出分析与建议，不要修改正文。",
				})
				messages = append(messages, llm.Message{Role: "tool", ToolCallID: tc.ID, Name: name, Content: string(out)})
				m.emit(runID, "tool.done", map[string]any{
					"name": name, "outputSummary": "只读模式：已阻止写入", "durationMs": 0,
				})
				continue
			}

			// 同一章节最多重写 2 次：再改就要求它标注待核实并继续往下写
			if name == "patch_section" {
				heading := extractJSONString(args, "heading")
				if heading != "" {
					sectionRewrites[heading]++
					if sectionRewrites[heading] > 2 {
						out, _ := json.Marshal(map[string]any{
							"ok":      false,
							"message": "这一章已经改过 2 次了。停止重写：把不确定的地方标成「待核实」，然后继续写还没完成的章节。",
						})
						messages = append(messages, llm.Message{
							Role: "tool", ToolCallID: tc.ID, Name: name, Content: string(out),
						})
						m.emit(runID, "tool.done", map[string]any{
							"name": name, "outputSummary": "已阻止第 3 次重写（改用待核实并继续）", "durationMs": 0,
						})
						m.emit(runID, "narrative", map[string]any{
							"text": "「" + heading + "」已改 2 次，转入标注「待核实」，继续后面的章节。",
						})
						continue
					}
				}
			}

			// narrative / update_plan 是"控制工具"，它们自己就产生语义事件
			// （narrative / plan.updated），不再冒一条工具芯片出来。
			controlTool := name == "narrative" || name == "update_plan"
			if !controlTool {
				m.emit(runID, "tool.started", map[string]any{"name": name, "inputSummary": inputSummary})
			}

			// tool_cache hit for search_web
			if name == "search_web" && cacheGet != nil {
				q := extractJSONString(args, "q")
				if q != "" {
					key := "exa:" + shortHash(q)
					if cached, ok := cacheGet(key); ok {
						m.emit(runID, "tool.done", map[string]any{
							"name": name, "outputSummary": "命中 tool_cache", "durationMs": 0, "cached": true,
						})
						messages = append(messages, llm.Message{
							Role: "tool", ToolCallID: tc.ID, Name: name,
							Content: string(cached),
						})
						continue
					}
				}
			}

			tool, ok := reg.Get(name)
			if !ok {
				out, _ := json.Marshal(map[string]any{"ok": false, "message": "unknown tool: " + name})
				messages = append(messages, llm.Message{Role: "tool", ToolCallID: tc.ID, Name: name, Content: string(out)})
				m.emit(runID, "tool.done", map[string]any{"name": name, "outputSummary": "未知工具", "durationMs": 0})
				continue
			}

			out, err := tool.Execute(ctx, json.RawMessage(args))
			if err != nil {
				out, _ = json.Marshal(map[string]any{"ok": false, "message": err.Error()})
			}
			// mark write_content
			if name == "write_content" || name == "patch_section" {
				var probe struct {
					OK bool `json:"ok"`
				}
				_ = json.Unmarshal(out, &probe)
				if probe.OK {
					wroteContent = true
					completed["t3"] = true
					completed["t4"] = true
				}
			}
			if name == "update_plan" {
				completed["t1"] = true
			}

			summary := summarizeToolOutput(name, out)
			if !controlTool {
				payload := map[string]any{
					"name": name, "outputSummary": summary, "durationMs": 0,
				}
				// 可展开的工具产物（截断后的片段，前端只做只读展示）
				if art := artifactForTool(name, out); art != nil {
					payload["artifact"] = art
					if extra := artifactSummary(name, art); extra != "" {
						payload["outputSummary"] = summary + " · " + extra
					}
				}
				m.emit(runID, "tool.done", payload)
			}
			content := string(out)
			if len(content) > 8000 {
				content = content[:8000] + "…"
			}
			messages = append(messages, llm.Message{
				Role: "tool", ToolCallID: tc.ID, Name: name, Content: content,
			})
			m.checkpointLLM(runID, ctxMap, messages, iteration, completed, cache, wroteContent)
		}
	}

	// quality gate before promoting to ready
	if wroteContent && (intent == domain.RunIntentCreateWiki || intent == domain.RunIntentContinueWiki) {
		m.applyQualityGate(runID, workID, docID)
	}

	m.checkpointLLM(runID, ctxMap, messages, iteration, completed, cache, wroteContent)

	// final plan
	plan := []domain.RunTask{
		{ID: "t1", Title: "理解目标", Status: "completed"},
		{ID: "t2", Title: "检索资料", Status: "completed"},
		{ID: "t3", Title: "产出内容", Status: "completed"},
		{ID: "t4", Title: "写入并完成", Status: "completed"},
	}
	_ = m.store.UpdateRunPlan(runID, plan)
	m.emit(runID, "plan.updated", map[string]any{"tasks": taskMapsOf(plan)})

	summary := "工单完成"
	if !wroteContent && intent != domain.RunIntentAnswer {
		summary = "工单完成（未写入正文）"
	}
	if lastErr != "" {
		summary += "；最近错误：" + lastErr
	}
	m.emit(runID, "run.completed", map[string]any{"summary": summary})
	_ = m.store.CompleteRun(runID, map[string]any{
		"summary": summary, "intent": string(intent), "wroteContent": wroteContent,
	})
}

func (m *Manager) applyQualityGate(runID, workID, docID string) {
	targetID := workID
	targetType := "work"
	if targetID == "" {
		targetID = docID
		targetType = "doc"
	}
	if targetID == "" {
		return
	}
	var md string
	if targetType == "work" {
		w, err := m.store.GetWork(targetID)
		if err != nil {
			return
		}
		if w.ContentMd != nil {
			md = *w.ContentMd
		}
	} else {
		d, err := m.store.GetDoc(targetID)
		if err != nil {
			return
		}
		md = d.ContentMd
	}
	q := CheckWikiQuality(md)
	if q.OK {
		// promote to ready
		if targetType == "work" {
			st := domain.WorkStatusReady
			_, _ = m.store.PatchWork(targetID, domain.PatchWorkBody{Status: &st})
			m.emit(runID, "tree.updated", map[string]any{
				"works": []map[string]any{{"id": targetID, "status": "ready"}},
			})
		}
		m.emit(runID, "narrative", map[string]any{"text": "质量自检通过，已标记为 ready。"})
		return
	}
	// keep draft; notify
	m.emit(runID, "narrative", map[string]any{
		"text": "质量自检未完全达标，保留 draft：" + strings.Join(q.Issues, "；"),
	})
	// annotate latest revision summary via a no-op status ensure draft
	if targetType == "work" {
		st := domain.WorkStatusDraft
		_, _ = m.store.PatchWork(targetID, domain.PatchWorkBody{Status: &st})
	}
}

func (m *Manager) checkpointLLM(runID string, ctxMap map[string]any, messages []llm.Message, iteration int, completed map[string]bool, cache map[string]any, wrote bool) {
	cp := map[string]any{
		"lastStep":     iteration,
		"iteration":    iteration,
		"context":      ctxMap,
		"wroteContent": wrote,
	}
	if len(messages) > 0 {
		cp["messages"] = compactMessages(messages)
	}
	if len(completed) > 0 {
		ids := make([]string, 0, len(completed))
		for id := range completed {
			ids = append(ids, id)
		}
		cp["completedTaskIds"] = ids
	}
	_ = m.store.UpdateRunCheckpoint(runID, cp, cache)
}

func restoreMessages(cp map[string]any) ([]llm.Message, bool) {
	raw, ok := cp["messages"]
	if !ok {
		return nil, false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var msgs []llm.Message
	if err := json.Unmarshal(b, &msgs); err != nil {
		return nil, false
	}
	return msgs, true
}

// compactMessages keeps assistant/tool structure but truncates long tool payloads.
func compactMessages(in []llm.Message) []llm.Message {
	out := make([]llm.Message, len(in))
	for i, msg := range in {
		out[i] = msg
		if msg.Role == "tool" && len(msg.Content) > 2000 {
			out[i].Content = msg.Content[:2000] + "…"
		}
		if msg.Role == "assistant" && len(msg.Content) > 4000 {
			out[i].Content = msg.Content[:4000] + "…"
		}
	}
	// drop earlier system if we already have a later one? keep simple: keep all
	return out
}

func buildToolDefs() []llm.ToolDef {
	defs := make([]llm.ToolDef, 0)
	for _, s := range tools.ToolSchemas() {
		props := s.Props
		if props == nil {
			props = map[string]any{}
		}
		defs = append(defs, llm.NewFunctionTool(s.Name, s.Desc, llm.ObjectSchema(props, s.Required)))
	}
	return defs
}

// buildToolDefsFor 按意图收口工具面：**问答/只读模式根本不下发写工具**。
// 为什么要在"工具清单"这层拦：模型看不到 write_content 就不会尝试去写，
// 比事后拒绝更可靠（真实事故：点「翻译这段」→ intent=continue_wiki →
// 模型顺手把翻译结果写回正文，落了一条 revision）。
func buildToolDefsFor(intent domain.RunIntent, docMode string) []llm.ToolDef {
	all := buildToolDefs()
	if intent != domain.RunIntentAnswer && docMode != "read" {
		return all
	}
	out := make([]llm.ToolDef, 0, len(all))
	for _, d := range all {
		if writeToolNames[d.Function.Name] {
			continue
		}
		out = append(out, d)
	}
	return out
}

func buildInitialMessages(intent domain.RunIntent, goal string, ctxMap map[string]any, skillFiles []skill.File, medium domain.Medium, workID, docID, docMode, brief string) []llm.Message {
	var sys strings.Builder
	sys.WriteString("你是 WikiAltas 的 Altas，为个人媒体库撰写中文百科条目。严格遵守以下规则：\n")
	sys.WriteString("1. 使用工具完成检索与写入；不要在回复里倾倒思考过程（CoT）。\n")
	sys.WriteString("2. narrative 是你对用户说的话：一次一句中文，≤40 字，只说\"打算做什么/已确认什么\"，" +
		"不写思考草稿、不复述工具参数。用户看到的就是这些句子。\n")
	sys.WriteString("3. 查不到的事实写「待核实」，绝不编造；无 Exa 检索能力时不要凭记忆填事实。\n")
	sys.WriteString("4. **先认准对象再检索**：同名作品极多。用预取里的「所属层级 + 介质 + 同级单作」锁定目标；" +
		"检索结果如果与所属宇宙/系列不符（例如把《白狼崛起》当成同名网文），一律不要采用，改用「系列名 + 作品名 + 作者/厂商」重查。\n")
	sys.WriteString("5. 题记只有一块，位置在说明行之后、第 1 章之前，用 :::epigraph … ::: 围栏：\n")
	sys.WriteString("   · 优先「引用式」——作品里有名的原句：必须给**可核对的原文**+逐句中文译文+署名行（—— 说话人／出处）；" +
		"原文核实不到就不要用引用式。\n")
	sys.WriteString("   · 拿不到原文就用「场景式／诗句式」：站在作品世界里写、落到具体人物与事件，**不写署名行**，也不许编台词。\n")
	sys.WriteString("6. 资料夹 = 系列：非标资料（解析、设定稿）用 create_doc 挂到对应**系列**节点下；" +
		"这些资料只能关联**本系列子树内**的单作（links 目标不能跨系列）。\n")
	sys.WriteString("\n=== 开工第一步：先探测，再决定写法和范围（必须执行）===\n")
	sys.WriteString("用 get_tree / search_works / read_work 判断目标节点的实际情况，然后按下面分支行动：\n")
	sys.WriteString("· 节点不存在 → 先 upsert_work 建节点（kind=work；父节点优先用上下文 parentId，否则挂到检索到的宇宙/系列下）\n")
	sys.WriteString("· 节点存在但正文为空 → 直接建档，不要重复建节点\n")
	sys.WriteString("· 节点已有正文 + 用户只要求改某章/某段 → 只用 patch_section 局部替换，禁止整篇重写\n")
	sys.WriteString("· 节点已有正文 + 用户要求整体重写 → 用 write_content，并说明会进 revision 可回滚\n")
	sys.WriteString("· 用户只是提问 → 用 answer 回答，不要改正文\n")
	sys.WriteString("\n=== 建档写法（9 章）===\n")
	sys.WriteString("先用 write_content 写入骨架：正文标题 + 说明块（> 说明：…）+ 题记围栏 + 9 个章标题；\n")
	sys.WriteString("再用 patch_section 逐章填入正文，每章一次——用户会看到正文一章一章长出来。\n")
	sys.WriteString("收尾前逐章自检（不通过就不许结束）：\n")
	sys.WriteString("   · 1–9 章每章都必须有正文，不能只剩标题；\n")
	sys.WriteString("   · 第 9 章参考资料列出**你实际用过的来源**，≥5 条（带链接或来源名）；\n")
	sys.WriteString("   · 同一章最多改 2 次：还想改就把不确定处标「待核实」，然后继续下一章。\n")

	// 用户打开文档时的模式决定 Altas 的作业方式（面板会把当前模式一起发过来）
	sys.WriteString("\n=== 当前文档模式（决定你怎么干活）===\n")
	switch docMode {
	case "read":
		sys.WriteString("用户现在处于【只读模式】——他只让你「看」：\n")
		sys.WriteString("· 只做阅读、分析、评估、总结、答疑；write_content / patch_section / upsert_work / create_doc 等写操作会被系统拒绝。\n")
		sys.WriteString("· 结论用 answer 给出：先给判断，再给理由与位置（第几章/哪一句）。\n")
		sys.WriteString("· 可以指出问题、给出改写建议的文字示例，但不要落库。\n")
	case "revision":
		sys.WriteString("用户现在处于【修订模式】——你是一个提建议的编辑。按下面的流程干活：\n")
		sys.WriteString("1. 先读清作用域：如果工单里带了「用户在正文里选中的内容」，那就是本次修订的范围，只改它；\n")
		sys.WriteString("   没有选区时，用 read_work 通读后自己划定最小范围（通常是相邻的几个段落）。\n")
		sys.WriteString("2. 逐段判定「保留 / 改」：符合设定与事实的段落原样保留，不要顺手润色。\n")
		sys.WriteString("3. 只做定点替换：能用一句话改好的，绝不动三句话；不做整段推倒重写。\n")
		sys.WriteString("4. 每条改动都要能解释理由，summary 必须以「修订：<理由>」开头（例：修订：术语不统一，统一为「绝境」）。\n")
		sys.WriteString("5. 未核实的细节不要删，也不要编：要么标「待核实」，要么按已核实的部分软化表述（对齐飞书「谨慎处理」的做法）。\n")
		sys.WriteString("6. 保留作者原来的风格与用词习惯，不要改成你自己的腔调。\n")
		sys.WriteString("7. 需要结构调整或大段重写时先别动手：用 narrative 说明「这里建议大改，要不要切到编辑模式处理」。\n")
		sys.WriteString("8. 写完后必须回读验证：再用 read_work 读一遍改动结果，确认改动生效、没伤到别的段落。\n")
		sys.WriteString("9. 结束时给改动清单：改了几处、每处属于哪种类型（措辞 / 逻辑 / 事实）。\n")
		sys.WriteString("注意：替换章节后如果标题文字变了，下一次 patch_section 要用新的标题锚点。\n")
	default:
		sys.WriteString("用户现在处于【编辑模式】——你是写手，改完即生效：\n")
		sys.WriteString("· 可以直接润色、重写、补内容、纠错；整段替换用 write_content，局部改写用 patch_section。\n")
		sys.WriteString("· 每次写入都会进 revision，用户可一键回滚，所以不必畏手畏脚。\n")
	}
	switch intent {
	case domain.RunIntentCreateWiki, domain.RunIntentContinueWiki:
		sys.WriteString("本工单是完整建档：按 skill 的 9 章骨架写作，禁止只写大纲就结束。\n")
	case domain.RunIntentRewriteSection:
		sys.WriteString("本工单是增量编辑：只改指定章节，不套 9 章骨架，不重写其他章节。\n")
	case domain.RunIntentWriteDoc:
		sys.WriteString("本工单是资料文档（资料夹里的长文/分析稿）：不强制 9 章，按用户要求自由结构写作。\n")
		sys.WriteString("· 先读现状：read_doc 看已有正文（也可用 section 只看一节），别凭空重写用户已有的稿子；\n")
		sys.WriteString("· 先写骨架再逐节填：write_content(targetType=doc) 写 # 标题 + ## 小节，然后 patch_section(targetType=doc) 逐节补正文；\n")
		sys.WriteString("· 单次输出有上限（约 4000 token），6000 字这种长文必须一节一节写，不要尝试一次写完；\n")
		sys.WriteString("· 保留作者原有结构、用词与论证顺序，只补事实、理顺逻辑、统一术语。\n")
	case domain.RunIntentAnswer:
		sys.WriteString("本工单是问答：读取必要上下文后用 answer 回答。\n")
	}
	if len(skillFiles) > 0 {
		sys.WriteString("\n=== wiki-writing skill（请遵守）===\n")
		// 上下文预算：skill 是每次工单都要塞的固定成本，三份文件（SKILL+core+media-*）
		// 曾经各留 18k 字符 = 最多 5.4 万字符，足以把模型窗口挤爆。
		// 现在单文件 8k、总量 1.6 万字符封顶；不够就按需用 read_skill 再读。
		const (
			maxSkillFileChars  = 8000
			maxSkillTotalChars = 16000
		)
		used := 0
		for _, f := range skillFiles {
			if used >= maxSkillTotalChars {
				sys.WriteString("\n（skill 其余文件已省略，需要时用 read_skill 读取。）\n")
				break
			}
			sys.WriteString("\n----- " + f.Name + " -----\n")
			c := f.Content
			if len(c) > maxSkillFileChars {
				c = string([]rune(c)[:maxSkillFileChars]) + "\n…（截断，完整内容用 read_skill 读）"
			}
			used += len(c)
			sys.WriteString(c)
			sys.WriteString("\n")
		}
	}

	var user strings.Builder
	user.WriteString("## 工单\n")
	user.WriteString("意图：" + string(intent) + "\n")
	user.WriteString("目标：" + goal + "\n")
	if workID != "" {
		user.WriteString("作品 ID：" + workID + "\n")
	}
	if docID != "" {
		user.WriteString("资料 ID：" + docID + "\n")
	}
	if medium != "" {
		user.WriteString("介质：" + string(medium) + "\n")
	}
	if docMode != "" {
		user.WriteString("文档模式：" + docMode + "（" +
			map[string]string{"read": "只读：只分析不改", "edit": "编辑：直接改", "revision": "修订：定点改+给理由"}[docMode] + "）\n")
	}
	if sel, ok := ctxMap["selection"].(string); ok && sel != "" {
		user.WriteString("\n## 用户在正文里选中的内容（修订模式优先只改这一段）\n")
		user.WriteString(strings.TrimSpace(sel) + "\n")
	}
	if mentioned, ok := ctxMap["mentioned"].(string); ok && mentioned != "" {
		user.WriteString("\n## 用户在本条消息里 @ 的条目\n")
		user.WriteString(mentioned + "\n")
		user.WriteString("需要这些内容时用 read_work / read_doc 读取后再动手，不要凭记忆写。\n")
	}
	if brief != "" {
		user.WriteString(brief)
	}
	if prev, ok := ctxMap["previous"].([]map[string]any); ok && len(prev) > 0 {
		user.WriteString("\n## 这段会话之前的工单（用户可能是在接着聊）\n")
		for _, p := range prev {
			fmt.Fprintf(&user, "· [%v] %v → %v\n", p["status"], p["goal"], p["summary"])
		}
		user.WriteString("如果这轮是追问或追加要求：先看现状（read_work / read_doc），" +
			"不要重复上一单已经做过的检索；上一单没做完的部分接着做。\n")
	}
	user.WriteString("\n请先用 read_skill（如需更多 skill 细节）、search_works、search_web 核实信息，")
	user.WriteString("再 update_plan 更新计划，用 narrative 播报进度，最后 write_content 写入正文并结束。\n")

	return []llm.Message{
		{Role: "system", Content: sys.String()},
		{Role: "user", Content: user.String()},
	}
}

func summarizeToolInput(name, args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return name
	}
	if len(args) > 160 {
		args = args[:160] + "…"
	}
	return args
}

func summarizeToolOutput(name string, out json.RawMessage) string {
	var probe map[string]any
	if err := json.Unmarshal(out, &probe); err != nil {
		return truncate(string(out), 120)
	}
	if msg, ok := probe["message"].(string); ok && msg != "" {
		return truncate(msg, 120)
	}
	if n, ok := probe["count"].(float64); ok {
		return fmt.Sprintf("count=%d", int(n))
	}
	if _, ok := probe["ok"].(bool); ok {
		return "ok"
	}
	return truncate(string(out), 120)
}

func extractJSONString(raw, key string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func shortHash(s string) string {
	// reuse tools.hash via simple local hash to avoid export
	h := fnv64(s)
	return fmt.Sprintf("%x", h)
}

func fnv64(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func taskMapsOf(tasks []domain.RunTask) []map[string]any {
	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, map[string]any{"id": t.ID, "title": t.Title, "status": t.Status})
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// streamEmitInterval 是流式增量的节流间隔：太密会把 run_events 和 SSE 刷爆，
// 太疏就看不见"边想边打"（飞书那种打字感大约就是这个量级）。
const streamEmitInterval = 250 * time.Millisecond

// 每轮 LLM 调用的上限与重试策略：
//
//	· 单轮最多 20 分钟（长章节够用），由 ctx 控制，用户取消能立刻打断；
//	· 限流 / 5xx / 网络抖动重试 3 次（1s → 4s → 10s），每次都在叙事流里说一声；
//	· 已经吐过字的流式失败不重试（重放会把同一段话重复给用户）。
const (
	llmRoundTimeout = 20 * time.Minute
	llmMaxRetries   = 3
)

var llmRetryBackoff = []time.Duration{time.Second, 4 * time.Second, 10 * time.Second}

// callModel 优先走流式：模型吐字时立刻发 narrative.delta / tool.delta，
// 让面板"边想边打"、工具芯片先以运行中出现；不支持流的客户端自动退回 Chat。
func (m *Manager) callModel(
	ctx context.Context,
	runID string,
	client llm.Client,
	messages []llm.Message,
	tools []llm.ToolDef,
) (llm.ChatResult, error) {
	var lastErr error
	for attempt := 0; attempt <= llmMaxRetries; attempt++ {
		if attempt > 0 {
			wait := llmRetryBackoff[min(attempt-1, len(llmRetryBackoff)-1)]
			m.emit(runID, "narrative", map[string]any{
				"text": fmt.Sprintf("模型接口不稳（%s），%s 后重试第 %d 次。", shortReason(lastErr), wait, attempt),
			})
			select {
			case <-ctx.Done():
				return llm.ChatResult{}, ctx.Err()
			case <-time.After(wait):
			}
		}
		res, err, streaming := m.callModelOnce(ctx, runID, client, messages, tools)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if streaming || !llm.IsRetryable(err) {
			return res, err
		}
	}
	return llm.ChatResult{}, lastErr
}

// callModelOnce 发一次请求（流式优先），返回 (结果, 错误, 这次是否用了流)。
func (m *Manager) callModelOnce(
	ctx context.Context,
	runID string,
	client llm.Client,
	messages []llm.Message,
	tools []llm.ToolDef,
) (llm.ChatResult, error, bool) {
	roundCtx, cancel := context.WithTimeout(ctx, llmRoundTimeout)
	defer cancel()

	sc, ok := client.(llm.StreamingClient)
	if !ok || !streamEnabled() {
		res, err := client.Chat(roundCtx, messages, tools)
		return res, err, false
	}
	var (
		lastEmit    time.Time
		pendingText strings.Builder
		pendingTool = map[int]llm.Delta{}
		toolArgsRaw = map[int]string{}
		emitted     = false
	)
	// flush 把"上次推送之后新攒的"增量发出去：
	// 节流只影响发送频率，不会丢字（早先按帧节流会把窗口里的文本直接扔掉）。
	flush := func() {
		if pendingText.Len() > 0 {
			m.emit(runID, "narrative.delta", map[string]any{"text": pendingText.String()})
			pendingText.Reset()
		}
		for index, d := range pendingTool {
			m.emit(runID, "tool.delta", map[string]any{
				"index": index,
				"id":    d.ToolID,
				"name":  d.ToolName,
				// args 必须是"累计到现在的参数"：前端要从中抠出 answer/narrative 的
				// text 字段做流式打字，只给碎片的话永远是半截（观测到过 args='》'）。
				"args": toolArgsRaw[index],
			})
		}
		pendingTool = map[int]llm.Delta{}
	}
	onDelta := func(d llm.Delta) {
		emitted = true
		switch d.Kind {
		case "text":
			pendingText.WriteString(d.Text)
		case "reasoning":
			// 思维链只当"还在动"的心跳，不把原文铺给用户（VISUAL_SPEC：不做 CoT dump）
		case "tool":
			toolArgsRaw[d.ToolIndex] += d.Text
			pendingTool[d.ToolIndex] = d
		}
		now := time.Now()
		if now.Sub(lastEmit) < streamEmitInterval {
			return
		}
		lastEmit = now
		flush()
	}

	res, err := sc.ChatStream(roundCtx, messages, tools, onDelta)
	flush()
	if err != nil && res.Content == "" && len(res.ToolCalls) == 0 {
		if emitted {
			// 已经吐过字了：重放会重复内容，这次不重试
			return res, err, true
		}
		// provider 不支持 stream（或中途断流）：退回一次性请求，功能不受影响
		m.emit(runID, "narrative", map[string]any{"text": "流式中断，已回退普通请求。"})
		res, err = client.Chat(roundCtx, messages, tools)
		return res, err, false
	}
	// 收尾：告诉前端这一轮流式结束（前端据此关掉打字光标）
	m.emit(runID, "narrative.delta", map[string]any{"text": "", "final": true})
	return res, err, true
}

// shortReason 把错误压成一句话，供叙事流播报。
func shortReason(err error) string {
	if err == nil {
		return "未知原因"
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.SplitN(msg, "\n", 2)[0]
	return truncate(msg, 80)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// compactHistory 把超预算的历史压掉：保留系统提示 + 首条用户消息 + 最近若干条，
// 中间整段替换成一条说明（整段丢才安全 —— 只丢 assistant.tool_calls 或只丢 tool
// 会让消息序列非法，provider 直接 400）。
func compactHistory(messages []llm.Message) ([]llm.Message, int) {
	total := 0
	for _, m := range messages {
		total += len(m.Content)
	}
	if total <= contextBudgetChars || len(messages) <= contextKeepRecent+3 {
		return messages, 0
	}
	keepFrom := len(messages) - contextKeepRecent
	if keepFrom < 3 {
		return messages, 0
	}
	// 起点必须是"非 tool 返回"的消息，否则会出现没有对应 tool_calls 的孤儿回复
	start := -1
	for i := keepFrom; i < len(messages); i++ {
		if messages[i].Role == "user" {
			start = i
			break
		}
	}
	if start < 0 {
		for i := keepFrom; i < len(messages); i++ {
			if messages[i].Role != "tool" {
				start = i
				break
			}
		}
	}
	if start <= 2 {
		return messages, 0
	}
	dropped := start - 2
	out := make([]llm.Message, 0, len(messages)-dropped+1)
	out = append(out, messages[:2]...)
	out = append(out, llm.Message{
		Role: "user",
		Content: fmt.Sprintf(
			"（上下文压缩：中间 %d 条工具调用与返回已省略，因为窗口放不下。需要时重新调用工具读取，不要凭记忆写。）",
			dropped),
	})
	out = append(out, messages[start:]...)
	return out, dropped
}

// streamEnabled 允许用 WIKIATLAS_LLM_STREAM=0 关掉流式（排障用，默认开）。
func streamEnabled() bool {
	v := strings.TrimSpace(os.Getenv("WIKIATLAS_LLM_STREAM"))
	return !(v == "0" || strings.EqualFold(v, "false") || strings.EqualFold(v, "off"))
}
