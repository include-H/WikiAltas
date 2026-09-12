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

// 上下文窗口是**模型一次能装多少 token**：系统提示 + 全部历史 + 本轮输出。
// 它是硬预算——超过了请求就发不出去，除非先把历史压下来。所以它由设置页给出
// （`llm.contextWindow`），整个 harness 就这一个数。
//
// 触发压缩的点 = 窗口 × compactRatio（留出输出与工具往返的余量）。
//
// 以前这里是个拍出来的 contextBudgetBytes = 200_000 **字节**（≈6.6 万 token）：
// 那是 32k/128k 窗口时代的数字，在 262k 窗口上等于只用了四分之一就压缩——
// 正好制造了"压缩丢资料 → 模型重查 → 又压缩"这个循环，而这段注释上方的
// 长文一直在抱怨这个循环，却没发现数字本身就是病根。
const (
	defaultContextWindow = 262144
	compactRatio         = 0.75
	// bytesPerToken 只在"还没测到真实用量"时用来估算（中文约 3 字节/token）。
	// 一旦有过一次模型调用，就直接用 provider 报的 promptTokens——那是实测值，
	// 不需要估算。
	bytesPerToken = 3
)

// 联网检索预算。经验值：一篇条目 4–8 次检索就够动笔（Claude 那边两三次），
// 旧值 32 次等于没限制——一次检索的返回（含上下文里的留存）就有数 KB，
// 32 次足够把整个上下文预算填满，模型却还在搜。
const (
	maxWebSearches  = 12
	maxWebFetches   = 12
	webBudgetWarnAt = 8 // 到这儿开始每次结果都提醒收手
	// 重复调用的三级阈值（照 dsh 的 [3,5,8]）：温和提醒 → 具体提醒 → 硬拦。
	// 只有最后一级才否决——前两级是"加注"，模型可以照常拿到结果再决定。
	maxRepeatGentle   = 3
	maxRepeatDetailed = 5
	maxRepeatHard     = 8
	// 任务清单多久没更新就提醒（工具调用次数）。一项检索类任务通常 2~4 次
	// 调用完成，4 次还没动清单基本就是"攒着"了。
	todoStaleAfter    = 4
	contextKeepRecent = 24 // 压缩时至少保留最近这么多行
	// 一轮回答要留的余量：窗口里得给模型自己留出写东西的地方，
	// 不能把上下文塞到 100%——塞满了它连话都说不出来。
	outputReserveTokens = 8192
)

// 不在这里截断工具结果。
//
// 入口处逐条裁剪会同时做两件坏事：把模型需要的信息切掉，以及把前缀缓存打碎
// （改过的结果和 provider 侧缓存的前缀对不上）。dsh 的做法是入口完全不动、
// **只有压缩能缩小内容**，我们照此办理——常规上限归各工具自己
// （read_work 30k rune / read_skill 24k / fetch_url 12k / search_web 约 9k），
// 超预算时由 compactSession 在会话日志上做一次替换，并记一条 context.compacted 事件。

// 会改动内容的工具：只读模式下这些一律拒绝执行。
var writeToolNames = map[string]bool{
	"write_content":       true,
	"patch_section":       true,
	"edit":                true,
	"upsert_work":         true,
	"upsert_relation":     true,
	"create_doc":          true,
	"attach_library_link": true,
	"sync_library":        true,
}

// controlTool 是"播报/任务面板"这类控制面工具：即使重复调用也不拦（模型常用来汇报进度），
// 也不出工具芯片——它们自己就有语义事件（wikiatlas 里的 message / todo）。
func controlTool(name string) bool {
	switch name {
	case "narrative", "answer", "todo_write":
		return true
	default:
		return false
	}
}

// executeLLM runs the real tool-calling chat loop.
func (m *Manager) executeLLM(ctx context.Context, runID string, intent domain.RunIntent, goal string, ctxMap map[string]any, fromStep int) {
	effortOverride, _ := ctxMap["reasoningEffort"].(string)
	client := m.activeClientFor(effortOverride)
	loader := m.skillLoader()

	// 这一段 loop 的产出统一由 respEmitter 映射成 Responses 流事件（见 responses.go）。
	em := m.newRespEmitter(runID, client.Model(), fromStep > 0)
	em.created(fromStep == 0)
	// 工单元信息（规范里没有）：会话归属、意图、模型、涉及的节点。
	// 前端拿它把这条消息挂回正确的会话/页面，并在工单页显示归属。
	em.emit(EvWAMeta, map[string]any{
		"runId": runID, "sessionId": ctxMap["session"], "goal": goal,
		"intent": string(intent), "model": client.Model(),
		"workId": ctxMap["workId"], "docId": ctxMap["docId"], "docMode": ctxMap["docMode"],
		"startedAt": time.Now().UTC().Format(time.RFC3339Nano),
	})

	workID, _ := ctxMap["workId"].(string)
	docID, _ := ctxMap["docId"].(string)
	mediumStr, _ := ctxMap["medium"].(string)
	medium := domain.Medium(mediumStr)
	// 用户发起工单时所在的文档模式（read | edit | revision）
	docMode, _ := ctxMap["docMode"].(string)
	if docMode == "" {
		docMode = "edit"
	}

	// resolve medium / node kind from work when missing
	nodeKind := domain.WorkKind("")
	if workID != "" {
		if w, err := m.store.GetWork(workID); err == nil {
			nodeKind = w.Kind
			if medium == "" && w.Medium != nil {
				medium = *w.Medium
				ctxMap["medium"] = string(medium)
			}
		}
	}

	var skillFiles []skill.File
	var skillErr error
	if loader != nil {
		skillFiles, skillErr = loader.FilesForIntentKind(intent, medium, nodeKind)
	}

	// 「收到工单：X」不再播报：用户气泡里就有这句话，再回一声只是回声。
	if skillErr != nil {
		em.notice("skill 加载失败："+skillErr.Error()+"，将按通用写作边界继续。", NoticeWarn)
	} else if len(skillFiles) > 0 {
		em.notice("已加载 wiki-writing skill："+strings.Join(skill.Names(skillFiles), " + "), NoticeInfo)
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

	// 同一句话连续出现只留一条：模型既走 narrative 工具、又在正文里复述一遍时，
	// 面板会"同一句说两边"（真实观测）。所有模型口吻的叙事都从这里出去。
	lastNarrative := ""
	// skipNarrative：控制工具流式时已经把这句话吐出来过，工具执行到这里时
	// 它的 Emit 只是把同一句再说一遍——置位期间丢弃（否则同一句出现两次）。
	skipNarrative := false
	emitNarrative := func(text string) {
		if skipNarrative {
			return
		}
		text = strings.TrimSpace(text)
		if text == "" || text == lastNarrative {
			return
		}
		// 包含也算重复：模型常把**刚流式说过的一大段里的某段**再走 narrative
		// 复述（真实观测：流式说了"自检完成…＋总结"，narrative 又把总结原样
		// 说一遍，界面上同一段出现两次）。太短的不做包含判断——可能只是碰巧
		// 撞上长文里的几个字。
		if len([]rune(text)) >= 8 && strings.Contains(lastNarrative, text) {
			return
		}
		lastNarrative = text
		em.message(text)
	}

	// 联网检索计数：既要给工具读（构造预算提醒），也要给下面的护栏读，
	// 所以必须在建 registry 之前声明。
	toolUsage := map[string]int{}
	// 任务清单新鲜度：模型常把"划勾"攒到收尾一块打（真实观察：整轮只建单、
	// 到写正文都不更新一次）。每次工具调用 +1，todo_write 成功清零；落后了就
	// 搭在工具结果里提醒一句——和重复提醒同一条通道，只建议、不否决。
	toolCallsSinceTodo := 0
	hasTodoList := false

	reg := tools.NewLibrarianRegistry(tools.LibrarianDeps{
		Store:     m.store,
		RunID:     runID,
		Intent:    intent,
		Goal:      goal,
		Context:   ctxMap,
		Skill:     loader,
		ExaAPIKey: m.exaKey(),
		ProxyURL:  m.proxyURL(),
		CacheGet:  cacheGet,
		CacheSet:  cacheSet,
		// 工具说的话到这里统一**翻译**成线上事件（工具不需要知道线上协议，
		// 协议的形状只由 respEmitter 一处拥有）。这张表是穷举的——
		// 认不出的类型会被当成提醒条露出来，而不是静默消失。
		Emit: func(eventType string, payload map[string]any) {
			switch eventType {
			case "narrative": // 模型借 narrative / answer 工具说的那句话 → 一个 message 项
				if t, ok := payload["text"].(string); ok {
					emitNarrative(t)
				}
			case "tasks.updated": // 任务清单（规范里没有）→ wikiatlas.todo
				em.todo(payload["tasks"])
			case "content.staging": // 正文写入中
				em.emit(EvWAStage, payload)
			case "content.committed": // 写入回执
				em.emit(EvWACommit, payload)
			case "tree.updated": // 作品树变更
				em.emit(EvWATree, payload)
			default:
				em.notice("未映射的工具事件 "+eventType+" "+fmt.Sprint(payload), NoticeWarn)
			}
		},
		QualityCheck: func(md string) (bool, []string) {
			q := CheckWikiQuality(md)
			return q.OK, q.Issues
		},
		// 检索预算快照：由工具在构造结果时写进返回里（执行器不再事后改写）。
		// 上限的权威在执行器这边，工具只读。
		WebBudget: func() tools.WebBudget {
			return tools.WebBudget{
				Used:   toolUsage["search_web"],
				Limit:  maxWebSearches,
				WarnAt: webBudgetWarnAt,
			}
		},
	})

	toolDefs := buildToolDefsFor(intent, docMode)

	// 上下文来自**会话日志的投影**，不重建。
	// resume 走的也是同一套——日志就是事实源，所以"断之前模型看到什么"和
	// "续上之后看到什么"天然一致，不存在存下来的和看到的不一样这回事。
	sessionID, _ := ctxMap["session"].(string)
	wroteContent := false
	// 本轮请求头相对上一轮的原因（initial | continue | change）。
	// 它随**第一条** llm.request 一起发出：开局单独发一条 llm.request 只会让人
	// 以为发生了两次请求（真实观测：同一 step 出现两条形状还不一样的 llm.request）。
	headerReason := ""
	// 问题型工单答过没有：用来在执行器里真正收住循环（见下面的 answer 分支）
	answered := false
	lastErr := ""
	iteration := 0

	if fromStep > 0 {
		if r, err := m.store.GetRun(runID); err == nil {
			if it, ok := r.Checkpoint["iteration"].(float64); ok {
				iteration = int(it)
			}
		}
		// 崩在工具执行中间时，日志末尾会留一条没有对应结果的 assistant 行，
		// 直接请求会被 provider 拒；先补收尾（和 dsh 的崩溃修复同理）。
		if closed, err := m.closeOpenTurn(sessionID); err != nil {
			em.notice("补中断收尾失败："+err.Error(), NoticeWarn)
		} else if closed > 0 {
			em.notice(fmt.Sprintf("上一轮中断在工具执行中间，已为 %d 次没返回的调用补上收尾。", closed), NoticeInfo)
		}
	}

	sysPrompt := m.buildSystemPrompt(docMode, intent, medium, skillFiles)
	brief := buildContextBrief(m.store, workID, docID)

	var (
		messages []llm.Message
		turn     int
	)
	if fromStep > 0 {
		msgs, err := m.deriveSessionMessages(sessionID)
		if err != nil {
			m.failRun(em, ctxMap, iteration, cache, "读取会话日志失败："+err.Error())
			return
		}
		messages = msgs
		em.request(map[string]any{
			"step": iteration, "messages": len(messages), "shapeReason": "resume",
		})
	} else {
		// 老会话（有工单、还没日志）先补种一次，让它也走同一套记忆
		if err := m.seedSessionFromRuns(sessionID, runID); err != nil {
			em.notice("补种会话历史失败："+err.Error(), NoticeWarn)
		}
		msgs, tn, reason, err := m.openSession(sessionID, runID, sysPrompt, summarizeToolNames(toolDefs),
			func(t int) string {
				return buildUserTurn(intent, goal, ctxMap, medium, workID, docID, docMode, brief, t)
			})
		if err != nil {
			m.failRun(em, ctxMap, iteration, cache, "准备会话上下文失败："+err.Error())
			return
		}
		messages, turn = msgs, tn
		// 请求头的原因留给循环里第一条 llm.request 一起发（见 headerReason）
		headerReason = reason
	}
	// logTurn 把一条消息追加进本会话的日志（幂等：sessionID 为空就什么都不做）。
	logTurn := func(msg llm.Message) { m.logMessage(sessionID, runID, turn, msg) }
	// pushTool 把工具结果同时写进内存数组与会话日志——两处**必须**是同一份内容，
	// 否则"模型看到的"和"日志里的"就分了家，重建不变量立刻会喊。
	pushTool := func(toolCallID, name, content string) {
		msg := llm.Message{Role: "tool", ToolCallID: toolCallID, Name: name, Content: content}
		messages = append(messages, msg)
		logTurn(msg)
	}

	// 同一章节的改写次数：防止模型对着某一章反复重写烧额度（观测中它连改 6 次）
	sectionRewrites := map[string]int{}
	// 同名同参调用次数：防止模型原地绕圈（同一个 read/search 调 5 遍）。
	//
	// 它是**每次执行一份**：一个 run 就是用户的一句请求，所以用户再插一句话
	// 天然得到一份全新的计数——dsh 在 agent/pre-step 里专门清链就是为了这件事
	// （"换了个上下文，跨过去的重复不算绕圈"），我们靠作用域天然满足。
	// 别把它提到 Manager 上。
	callCounts := map[string]int{}
	// 前缀稳定性追踪：请求必须是上一次的追加延长（详见 requestShape）
	shape := requestShape{}
	// 上一轮实测的 prompt tokens：这是"现在窗口里装了多少"的唯一可信来源，
	// 压缩判断优先用它，不靠估算。
	lastPromptTokens := 0

	for round := 0; round < maxToolRounds; round++ {
		select {
		case <-ctx.Done():
			m.checkpointLLM(runID, ctxMap, iteration, cache, false)
			em.sealOpenCalls("工单已取消，这次调用没有结果")
			_ = m.store.InterruptRun(runID)
			return
		default:
		}

		iteration++
		// 上下文预算 = 模型窗口（一个数）。到窗口的 compactRatio 就压一次；
		// 压完还塞不进去就停下说清楚，而不是发一个注定 400 的请求。
		window := m.contextWindow()
		used := m.sessionTokens(sessionID, lastPromptTokens)
		if used > int(float64(window)*compactRatio) {
			// 压缩 = 会话日志上的一次显式替换（dsh：能缩小内容的只有压缩，
			// 而且必须留日志）。它必然打断前缀缓存，所以既发事件，
			// 也告诉前缀检查"这次改写有据可依"。
			did, err := m.compactSession(sessionID, runID, turn)
			if err != nil {
				em.notice("压缩失败："+err.Error(), NoticeWarn)
			} else if did {
				if msgs, derr := m.deriveSessionMessages(sessionID); derr == nil {
					messages = msgs // 替换发生在日志上，内存这份重新投影
				}
				shape.reason = "compaction"
				em.emit(EvWAContext, map[string]any{
					"keepRecent":    contextKeepRecent,
					"reason":        "context_window",
					"contextWindow": window,
					"usedTokens":    used,
				})
				em.notice("上下文接近窗口上限，已把中间的工具往返替换成一条摘要。", NoticeInfo)
				used = m.sessionTokens(sessionID, 0)
			}
		}
		if used+outputReserveTokens > window {
			m.failRun(em, ctxMap, iteration, cache, fmt.Sprintf(
				"这段会话已经到 %d token，超过模型窗口 %d（压缩后仍放不下）。开一段新会话继续，或在设置里把窗口填成模型的真实值。",
				used, window))
			return
		}

		// 重建不变量：用会话日志独立投影一遍，和内存里这份逐条比对。
		// 这是"模型可见 ⟺ 有日志"的可执行版本——两者不一致，就说明有一处
		// 内容走了不落日志的旁路，那样的请求是日志解释不了的。
		rebuildOK, rebuildNote := true, ""
		if sessionID != "" {
			switch rebuilt, err := m.deriveSessionMessages(sessionID); {
			case err != nil:
				rebuildOK, rebuildNote = false, "读取日志失败："+err.Error()
			case !messagesEqual(rebuilt, messages):
				rebuildOK = false
				rebuildNote = fmt.Sprintf("日志折出 %d 条、内存里有 %d 条", len(rebuilt), len(messages))
			}
		}
		if !rebuildOK {
			em.notice("上下文与会话日志不一致："+rebuildNote+"（已记录，这属于 bug）", NoticeError)
		}

		// 前缀检查：本次请求必须是上一次的追加延长，否则前缀缓存必然失效。
		// 不依赖 provider 回报缓存用量——vLLM 这类网关根本不报——所以这是
		// "前缀有没有被悄悄改写"在缓存不可观测时唯一的验证手段。
		appendOnly, divergedAt := shape.check(messages)
		reqPayload := map[string]any{
			"step":        iteration,
			"messages":    len(messages),
			"appendOnly":  appendOnly,
			"divergedAt":  divergedAt,
			"shapeReason": shape.reason,
			"rebuildOK":   rebuildOK,
		}
		if headerReason != "" {
			reqPayload["headerReason"] = headerReason
			headerReason = "" // 只随第一条发
		}
		em.request(reqPayload)
		if !appendOnly {
			em.notice(fmt.Sprintf(
				"前缀在第 %d 条消息处被改写且没有记录原因，缓存会失效（已记录，这属于 bug）。", divergedAt), NoticeError)
		}
		shape.prev = append([]llm.Message(nil), messages...)
		shape.seen = true
		shape.reason = ""

		result, streamed, err := m.callModel(ctx, em, client, messages, toolDefs)
		// 这一轮流式出来的那一段话到此为止（下一轮说的是新的一句，是新的 message 项）。
		// 顺手把它记成"上一句"：模型常常一边流式说话、一边又调 answer/narrative
		// 工具把同一句再说一遍，不记这一笔，下面那条权威消息就撞不上去重。
		if said := em.closeMessage(); said != "" {
			lastNarrative = strings.TrimSpace(said)
		}
		if err != nil {
			lastErr = err.Error()
			em.notice("模型调用失败："+lastErr, NoticeError)
			m.checkpointLLM(runID, ctxMap, iteration, cache, wroteContent)
			em.failed("llm_error", lastErr)
			_ = m.store.FailRun(runID, map[string]any{"error": lastErr, "iteration": iteration})
			return
		}

		// 每轮用量。cacheReadTokens 是"前缀到底有没有命中"的生产信号：
		// 网关不报时它是 0（不可观测），报的时候它掉到 0 就说明前缀被改写了。
		if result.Usage.PromptTokens > 0 {
			lastPromptTokens = result.Usage.PromptTokens
		}
		if result.Usage.TotalTokens > 0 {
			em.addUsage(result.Usage)
			em.usageEvent(iteration, result.Usage, m.contextWindow())
		}

		// 模型自己说的那句话：流式时已经边打边出来了（streamed），
		// 非流式（或没流出字）才在这儿补一条完整消息。
		if txt := strings.TrimSpace(result.Content); txt != "" && !streamed {
			if len(result.ToolCalls) == 0 {
				emitNarrative(truncate(txt, 500))
			} else if len(txt) < 300 {
				emitNarrative(truncate(txt, 300))
			}
		}
		if txt := strings.TrimSpace(result.Content); txt != "" && len(result.ToolCalls) == 0 {
			msg := llm.Message{Role: "assistant", Content: txt}
			messages = append(messages, msg)
			logTurn(msg)
			break
		}

		if len(result.ToolCalls) == 0 {
			// no tools, no content — stop
			break
		}

		assistantMsg := llm.Message{
			Role:      "assistant",
			Content:   result.Content,
			ToolCalls: result.ToolCalls,
		}
		messages = append(messages, assistantMsg)
		logTurn(assistantMsg)

		// blockCall 是"没执行就拒绝"的统一收尾：模型照样拿到原因，
		// 界面上是一张失败的工具卡（参数已经流出来了，收尾即可）。
		blockCall := func(tc llm.ToolCall, name, reason string) {
			em.callArgsDone(tc.ID, name, tc.Function.Arguments)
			em.toolResult(tc.ID, name, tc.Function.Arguments, false, map[string]any{
				"outputSummary": reason, "durationMs": 0,
			})
		}

		for i, tc := range result.ToolCalls {
			select {
			case <-ctx.Done():
				m.checkpointLLM(runID, ctxMap, iteration, cache, wroteContent)
				em.sealOpenCalls("工单已取消，这次调用没有结果")
				_ = m.store.InterruptRun(runID)
				return
			default:
			}

			name := tc.Function.Name
			args := tc.Function.Arguments
			toolUsage[name]++

			// 参数不是合法 JSON：模型的花括号没闭合（一次想写太长、被输出上限截断时
			// 最常见）。绝不能拿去执行，也绝不能让原样进下一次请求——网关解析这种
			// function_call 会直接 400，整段会话从此不可用（真实事故：一次
			// write_content 被截断，之后每一轮都 400，工单直接失败）。
			if !json.Valid([]byte(args)) {
				out, _ := json.Marshal(map[string]any{
					"ok": false,
					"message": "这次调用的参数不是合法 JSON（{ } 没闭合，通常是一次想写的太长被截断了）。" +
						"不要原样重发：把内容拆小——先写骨架或前几章，再用 patch_section 逐章补完。",
				})
				pushTool(tc.ID, name, string(out))
				blockCall(tc, name, "参数不是合法 JSON，已拒绝执行")
				continue
			}

			// 检索预算：超限一刀切断。但真正的信号是渐进的——
			// 见下面 withSearchBudgetNote：到 8 次就开始在每次结果里提醒收手。
			if (name == "search_web" && toolUsage[name] > maxWebSearches) ||
				(name == "fetch_url" && toolUsage[name] > maxWebFetches) {
				out, _ := json.Marshal(map[string]any{
					"ok": false,
					"message": fmt.Sprintf("联网检索已达本工单上限（%s 已调用 %d 次）。请停止检索，"+
						"基于已核实的事实开始写作；查不到的细节标「待核实」，不要继续联网。", name, toolUsage[name]-1),
				})
				pushTool(tc.ID, name, string(out))
				blockCall(tc, name, "检索预算已用完，已拦截")
				continue
			}

			// 重复调用（模型绕圈子的典型症状）。三层处理，照 dsh 的 repeat-tool-reminder：
			// **观察并加注，不 veto**——先温和提醒、再具体提醒，最后才硬拦。
			// 只喊"别重复"没用，两条提醒都给出路（换动作 / 换参数 / 收尾）。
			//
			// 判据是**规范化后的参数指纹**：{"a":1,"b":2} 与 {"b":2,"a":1} 是同一个调用。
			// 拿原始串比会漏判——属性顺序一变就被当成新调用（这是我们以前的写法）。
			callKey := name + "|" + canonicalArgs(args)
			callCounts[callKey]++
			repeatReminder := ""
			if !controlTool(name) {
				switch n := callCounts[callKey]; {
				case n > maxRepeatHard:
					out, _ := json.Marshal(map[string]any{
						"ok": false,
						"message": fmt.Sprintf("同一个调用（%s）已经重复 %d 次，不会再生效。换一种做法、换一组参数，"+
							"或者——如果证据已经够了——直接收尾给出结论。", name, n-1),
					})
					pushTool(tc.ID, name, string(out))
					blockCall(tc, name, "重复调用已拦截")
					continue
				case n >= maxRepeatDetailed:
					repeatReminder = detailedRepeatReminder(name, n, callKey)
				case n >= maxRepeatGentle:
					repeatReminder = gentleRepeatReminder
				}
			}

			// 只读模式：写工具一律拒绝（模型仍然可以检索、阅读、回答、播报）
			if docMode == "read" && writeToolNames[name] {
				out, _ := json.Marshal(map[string]any{
					"ok":      false,
					"message": "当前文档处于只读模式：用户只是让 Altas「看」。请用 answer / narrative 给出分析与建议，不要修改正文。",
				})
				pushTool(tc.ID, name, string(out))
				blockCall(tc, name, "只读模式：已阻止写入")
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
						pushTool(tc.ID, name, string(out))
						em.callArgsDone(tc.ID, name, args)
						em.toolResult(tc.ID, name, args, true, map[string]any{
							"outputSummary": "已阻止第 3 次重写（改用待核实并继续）", "durationMs": 0,
						})
						em.notice("「"+heading+"」已改 2 次，转入标注「待核实」，继续后面的章节。", NoticeInfo)
						continue
					}
				}
			}

			// narrative / answer 是"控制工具"：它们不产生工具卡，
			// 要说的话直接是 message 项（流式那一段已经在吐字时画过了）。
			isControl := controlTool(name)
			if !isControl {
				em.callArgsDone(tc.ID, name, args)
			}

			// tool_cache hit for search_web
			if name == "search_web" && cacheGet != nil {
				q := extractJSONString(args, "q")
				if q != "" {
					key := "exa:" + shortHash(q)
					if cached, ok := cacheGet(key); ok {
						em.toolResult(tc.ID, name, args, true, map[string]any{
							"outputSummary": "命中 tool_cache", "durationMs": 0, "cached": true,
						})
						pushTool(tc.ID, name, string(cached))
						continue
					}
				}
			}

			tool, ok := reg.Get(name)
			if !ok {
				// 模型把 search_works 写成 search_work 这类"单复数 / 漏字"打错很常见
				// （真实观察）。报错里直接给出最像的正确名字，省掉一轮猜。
				msg := "unknown tool: " + name + "（未知工具）"
				if near := nearestToolName(reg.Names(), name); near != "" {
					msg += "。你要找的可能是 " + near + "——工具名要照工具定义原样写。"
				}
				out, _ := json.Marshal(map[string]any{"ok": false, "message": msg})
				pushTool(tc.ID, name, string(out))
				blockCall(tc, name, "未知工具："+name)
				continue
			}

			toolStart := time.Now()
			// 控制工具要说的话在流式时已经吐出过：它的 Emit 到这里只是把同一句话
			// 再说一遍，跳过（否则同一句会出现在两个 message 项里）。
			skipNarrative = isControl && em.controlStreamed(i)
			out, err := tool.Execute(ctx, json.RawMessage(args))
			skipNarrative = false
			if err != nil {
				out, _ = json.Marshal(map[string]any{"ok": false, "message": err.Error()})
			}
			// mark write_content
			// 写工具的 diff 统计（工具行显示 +N −M，对齐 MiMo/Codex）
			var (
				diffAdd int
				diffDel int
				hasDiff bool
			)
			if name == "write_content" || name == "patch_section" || name == "edit" {
				var probe struct {
					OK        bool `json:"ok"`
					Additions int  `json:"additions"`
					Deletions int  `json:"deletions"`
				}
				_ = json.Unmarshal(out, &probe)
				if probe.OK {
					wroteContent = true
				}
				if probe.Additions > 0 || probe.Deletions > 0 {
					diffAdd, diffDel, hasDiff = probe.Additions, probe.Deletions, true
				}
			}

			// 任务清单新鲜度：搭在结果里的一句话提醒（不否决）。只在清单已经
			// 建起来之后才开始数——没建清单的小工单不该被催。
			todoReminder := ""
			if isControl && name == "todo_write" {
				var probe struct {
					OK *bool `json:"ok"`
				}
				if json.Unmarshal(out, &probe) == nil && probe.OK != nil && *probe.OK {
					hasTodoList = true
					toolCallsSinceTodo = 0
				}
			} else if !isControl {
				toolCallsSinceTodo++
				if hasTodoList && toolCallsSinceTodo >= todoStaleAfter {
					todoReminder = fmt.Sprintf(
						"任务清单已经 %d 次工具调用没更新了：刚做完的项现在就划掉——把整份新清单和下一个动作一起提交就行，别攒到最后一块打。",
						toolCallsSinceTodo)
				}
			}

			// 重复提醒：并进结果里交给模型（前两级不否决，结果照常给它）
			out = withReminder(out, repeatReminder)
			out = withReminder(out, todoReminder)

			// 这次调用成功没有：前端要拿它区分"失败了"和"被打断"（后者没有结果）。
			// 以前 tool.done 不带这个，前端只能把两种都当成功——工具报错在界面上看不见。
			toolOK := true
			var okProbe struct {
				OK *bool `json:"ok"`
			}
			if err := json.Unmarshal(out, &okProbe); err == nil && okProbe.OK != nil {
				toolOK = *okProbe.OK
			}

			if !isControl {
				extra := map[string]any{
					"outputSummary": summarizeToolOutput(name, out),
					"ok":            toolOK,
					// 实测耗时。以前这里写死 0：一个从没量过、也没人显示的假数据，
					// 留着只会让人以为"工具是瞬时的"。
					"durationMs": time.Since(toolStart).Milliseconds(),
				}
				if hasDiff {
					extra["additions"] = diffAdd
					extra["deletions"] = diffDel
				}
				// 可展开的工具产物（截断后的片段，前端只做只读展示）
				if art := artifactForTool(name, out); art != nil {
					extra["artifact"] = art
					if s := artifactSummary(name, art); s != "" {
						extra["outputSummary"] = extra["outputSummary"].(string) + " · " + s
					}
				}
				em.toolResult(tc.ID, name, args, toolOK, extra)
			}
			// 入口不截断：见文件顶部关于"只有压缩能缩小内容"的说明。
			pushTool(tc.ID, name, string(out))
			m.checkpointLLM(runID, ctxMap, iteration, cache, wroteContent)

			// 「答完就停」不能只写在提示里：问题型工单的交付物就是那一句回答，
			// 答案已经给出去之后还往下走，只能产出重复的答案与一串收尾
			// （真实观测：同一句答案发了两遍，外加三行"已就此回答完毕"）。
			// 决策在这儿做，就在这儿强制——提示词只说它解释得了的规则。
			if name == "answer" && intent == domain.RunIntentAnswer {
				answered = true
			}
		}
		if answered {
			break
		}
	}

	// quality gate before promoting to ready
	if wroteContent && (intent == domain.RunIntentCreateWiki || intent == domain.RunIntentContinueWiki) {
		m.applyQualityGate(em, workID, docID)
	}

	m.checkpointLLM(runID, ctxMap, iteration, cache, wroteContent)

	summary := "工单完成"
	if !wroteContent && intent != domain.RunIntentAnswer {
		summary = "工单完成（未写入正文）"
	}
	if lastErr != "" {
		summary += "；最近错误：" + lastErr
	}
	em.completed(summary, wroteContent)
	_ = m.store.CompleteRun(runID, map[string]any{
		"summary": summary, "intent": string(intent), "wroteContent": wroteContent,
	})
}

func (m *Manager) applyQualityGate(em *respEmitter, workID, docID string) {
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
			em.emit(EvWATree, map[string]any{
				"works": []map[string]any{{"id": targetID, "status": "ready"}},
			})
		}
		em.notice("质量自检通过，已标记为 ready。", NoticeInfo)
		return
	}
	// keep draft; notify
	em.notice("质量自检未完全达标，保留 draft："+strings.Join(q.Issues, "；"), NoticeWarn)
	// annotate latest revision summary via a no-op status ensure draft
	if targetType == "work" {
		st := domain.WorkStatusDraft
		_, _ = m.store.PatchWork(targetID, domain.PatchWorkBody{Status: &st})
	}
}

// checkpointLLM 只存"执行进度"（第几轮、工具缓存、上下文），**不存消息**。
//
// 消息的耐久副本是会话日志（session_messages），它本来就是追加式的所以不会漂移；
// 再在检查点里存一份消息数组，等于给自己造第二个事实源——
// 而两者不一致时，resume 出来的请求就和断之前不是同一个前缀。
func (m *Manager) checkpointLLM(runID string, ctxMap map[string]any, iteration int, cache map[string]any, wrote bool) {
	cp := map[string]any{
		"lastStep":     iteration,
		"iteration":    iteration,
		"context":      ctxMap,
		"wroteContent": wrote,
	}
	_ = m.store.UpdateRunCheckpoint(runID, cp, cache)
}

// failRun 是失败退场的统一出口：播报 + 落检查点 + response.failed + 状态。
func (m *Manager) failRun(em *respEmitter, ctxMap map[string]any, iteration int, cache map[string]any, msg string) {
	em.notice(msg, NoticeError)
	m.checkpointLLM(em.runID, ctxMap, iteration, cache, false)
	em.failed("harness_error", msg)
	_ = m.store.FailRun(em.runID, map[string]any{"error": msg, "iteration": iteration})
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

// buildSystemPrompt 组装本次工单的系统提示：只负责把运行时状态收齐，
// 交给 renderSystemPrompt 按段渲染。段的内容与顺序在 prompt.go 里（一个事实一个 owner）。
func (m *Manager) buildSystemPrompt(docMode string, intent domain.RunIntent, medium domain.Medium, skillFiles []skill.File) string {
	window, userName := defaultContextWindow, ""
	if st, err := m.store.GetSettings(); err == nil {
		if st.LLM.ContextWindow > 0 {
			window = st.LLM.ContextWindow
		}
		// 管理员标识只作显示用：让模型被问「我是谁」时知道在给谁做事
		userName = st.Admin.Username
	}
	return renderSystemPrompt(promptState{
		docMode:         docMode,
		intent:          intent,
		medium:          medium,
		tools:           toolNamesFor(intent, docMode),
		userName:        userName,
		contextWindow:   window,
		maxWebSearches:  maxWebSearches,
		maxWebFetches:   maxWebFetches,
		webBudgetWarnAt: webBudgetWarnAt,
		compactRatio:    compactRatio,
		skillFiles:      skillFiles,
	})
}

// buildSystemPromptFor 是纯函数版本（测试、以及拿不到 store 的场景用）。
func buildSystemPromptFor(window int, docMode string, intent domain.RunIntent, medium domain.Medium, skillFiles []skill.File) string {
	return renderSystemPrompt(promptState{
		docMode:         docMode,
		intent:          intent,
		medium:          medium,
		tools:           toolNamesFor(intent, docMode),
		contextWindow:   window,
		maxWebSearches:  maxWebSearches,
		maxWebFetches:   maxWebFetches,
		webBudgetWarnAt: webBudgetWarnAt,
		compactRatio:    compactRatio,
		skillFiles:      skillFiles,
	})
}

// toolNamesFor 把工具面收成集合，段据此决定自己存不存在。
func toolNamesFor(intent domain.RunIntent, docMode string) map[string]bool {
	tools := map[string]bool{}
	for _, d := range buildToolDefsFor(intent, docMode) {
		tools[d.Function.Name] = true
	}
	return tools
}

// buildUserTurn 拼这一轮的 user 消息：工单信息 + 上下文 brief + 收尾要求。
// 首轮与后续轮内容基本一致，只多一句"这是延续"的说明。
func buildUserTurn(intent domain.RunIntent, goal string, ctxMap map[string]any, medium domain.Medium, workID, docID, docMode, brief string, turn int) string {
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
	if turn > 1 {
		user.WriteString("\n## 这是本会话的延续\n")
		user.WriteString("上文的消息历史就是本会话前几轮的完整经过（含当时的检索结果与判断）；需要细节用工具读，不要凭印象。\n")
	}
	// 这里**不再**写"先 read_skill、再 search_web、最后 write_content 写入正文"这类收尾指令。
	// 工具怎么用归工具的 description，这一单要产出什么归系统提示的 This task 段——
	// 写在这里就会出现"闲聊工单被告知用 write_content"，而那个工具压根没下发
	// （真实观测：模型为此空转了两轮，还专门思考了一遍为什么工具不见了）。
	return user.String()
}

func summarizeToolOutput(name string, out json.RawMessage) string {
	var probe map[string]any
	if err := json.Unmarshal(out, &probe); err != nil {
		return truncate(string(out), 120)
	}
	// 读作品/资料：给《标题》比"ok"有信息量
	if name == "read_work" || name == "read_doc" {
		if w, ok := probe["work"].(map[string]any); ok {
			if t, _ := w["title"].(string); t != "" {
				return "《" + truncate(t, 24) + "》"
			}
		}
		if t, _ := probe["title"].(string); t != "" {
			return "《" + truncate(t, 24) + "》"
		}
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

// clipBytes 已删除：入口不再逐条截断，缩小的动作只归 compactSession
// （带 context.compacted 事件）。工具各自的输出上限由工具自己把关。

// withBudgetNote 已移入 tools 包：预算提醒在构造结果时写进去，
// 执行器不再事后改写工具返回值（改写过 = 模型所见与日志不一致）。
// canonicalArgs 把工具参数规范化，用于"这是不是同一个调用"的指纹。
//
// Go 的 encoding/json 在编码 map 时会**递归按 key 排序**，所以"解出来再编回去"
// 就是规范化：{"a":1,"b":2} 与 {"b":2,"a":1} 得到同一个串。数组顺序保留
// （数组的顺序是语义的一部分）。半截 JSON 解析不了就原样返回——至少同名同串还拦得住。
func canonicalArgs(raw string) string {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return raw
}

// 重复调用的两条提醒（照 dsh 的措辞结构：先温和、再具体）。
// 两条都以**三条出路**收尾——换动作 / 换参数 / 收尾，只喊"别重复"模型不知道往哪走。
const gentleRepeatReminder = "你在用完全相同的参数重复调用同一个工具。先仔细看上一次的返回：" +
	"如果事情还没办完，换一种做法或换一组参数，而不是把这个调用再发一遍。"

func detailedRepeatReminder(name string, count int, args string) string {
	return fmt.Sprintf("检测到重复调用：\n- 工具：%s\n- 连续次数：%d\n- 参数：%s\n"+
		"这些重复没有推进任何东西。不要再拿这组参数调用它：看完最新结果，换一个动作、换一组参数，"+
		"或者——如果证据已经够了——直接收尾。", name, count, truncate(args, 500))
}

// withReminder 把提醒并进工具结果里。
//
// 走工具结果、而不是单独发一条 user 消息：我们没有 dsh 那种 plugin source 区分，
// 单独发一条会进会话日志、被当成一次用户发言（重复计数和"上一轮用户说了什么"都会因此错乱）。
func withReminder(out json.RawMessage, reminder string) json.RawMessage {
	if reminder == "" {
		return out
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		return out
	}
	if prev, ok := m["reminder"].(string); ok && prev != "" {
		reminder = prev + "\n" + reminder
	}
	m["reminder"] = reminder
	b, err := json.Marshal(m)
	if err != nil {
		return out
	}
	return b
}

// nearestToolName 给打错的工具名找最像的那个：前缀包含（单复数 / 漏字），
// 或者编辑距离 ≤2。找不到就返回空串——宁可不猜，别乱指。
func nearestToolName(names []string, attempt string) string {
	best := ""
	bestDist := 3
	for _, n := range names {
		if n == attempt {
			return n
		}
		if strings.HasPrefix(n, attempt) || strings.HasPrefix(attempt, n) {
			if best == "" {
				best = n
			}
			continue
		}
		if d := editDistance(n, attempt); d < bestDist {
			best, bestDist = n, d
		}
	}
	return best
}

// editDistance 是经典 Levenshtein。工具名都很短、工具数量也不多，DP 足够。
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(min(prev[j]+1, cur[j-1]+1), prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
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

// callModel 优先走流式：模型吐字时立刻发 output_text.delta / reasoning.delta /
// function_call_arguments.delta，让面板"边想边打"、工具卡先以运行中出现；
// 不支持流的客户端自动退回 Chat。
//
// 第二个返回值 streamed = 这一轮是否流式（执行器据此决定要不要补一条完整消息）。
func (m *Manager) callModel(
	ctx context.Context,
	em *respEmitter,
	client llm.Client,
	messages []llm.Message,
	tools []llm.ToolDef,
) (llm.ChatResult, bool, error) {
	var lastErr error
	for attempt := 0; attempt <= llmMaxRetries; attempt++ {
		if attempt > 0 {
			wait := llmRetryBackoff[min(attempt-1, len(llmRetryBackoff)-1)]
			em.notice(fmt.Sprintf("模型接口不稳（%s），%s 后重试第 %d 次。", shortReason(lastErr), wait, attempt), NoticeWarn)
			select {
			case <-ctx.Done():
				return llm.ChatResult{}, false, ctx.Err()
			case <-time.After(wait):
			}
		}
		res, err, streaming := m.callModelOnce(ctx, em, client, messages, tools)
		if err == nil {
			return res, streaming, nil
		}
		lastErr = err
		if streaming || !llm.IsRetryable(err) {
			return res, streaming, err
		}
	}
	return llm.ChatResult{}, false, lastErr
}

// callModelOnce 发一次请求（流式优先），返回 (结果, 错误, 这次是否用了流)。
func (m *Manager) callModelOnce(
	ctx context.Context,
	em *respEmitter,
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
		lastEmit time.Time
		// 攒批：节流只影响发送频率，不丢字（早先按帧节流会把窗口里的文本直接扔掉）。
		// 迁移到 Responses 后这一层更重要——每个增量都是一行 run_events，
		// 不攒批的话一次长回答能写进去几千行。
		pendingText      strings.Builder
		pendingReasoning strings.Builder
		fullReasoning    strings.Builder
		toolArgsRaw      = map[int]string{}
		toolNames        = map[int]string{}
		toolIDs          = map[int]string{}
		emitted          = false
		streamedText     = false
	)
	flush := func() {
		if pendingText.Len() > 0 {
			streamedText = true
			em.textDelta(pendingText.String())
			pendingText.Reset()
		}
		if pendingReasoning.Len() > 0 {
			em.reasoningDelta(pendingReasoning.String())
			pendingReasoning.Reset()
		}
	}
	onDelta := func(d llm.Delta) {
		emitted = true
		switch d.Kind {
		case "text":
			pendingText.WriteString(d.Text)
		case "reasoning":
			// 思考按网关的 reasoning 字段流出来：攒批推送 + 留全文（轮末收尾）
			fullReasoning.WriteString(d.Text)
			pendingReasoning.WriteString(d.Text)
		case "tool":
			if d.ToolName != "" {
				toolNames[d.ToolIndex] = d.ToolName
			}
			if d.ToolID != "" {
				toolIDs[d.ToolIndex] = d.ToolID
				em.toolIndex(d.ToolIndex, d.ToolID)
			}
			toolArgsRaw[d.ToolIndex] += d.Text
			// 参数一来，说明这段话已经说完：先把节流缓冲里的文本尾巴落下去。
			// （曾观测到"正文停了 216 秒、末 10 字到最后才补上"——就是它。）
			if pendingText.Len() > 0 || pendingReasoning.Len() > 0 {
				flush()
			}
			// **参数增量必须当场转发**。以前这里只攒着、等流读完才把整个累计值
			// 一次性喂给发射器：界面上是"沉默 216 秒，然后 11752 字符砸下来"，
			// 而空闲看门狗没杀它（字节一直在到）——是我们在缓冲，不是网关。
			// 控制工具（answer/narrative）的参数是要说的话，留给流末的
			// controlText 通道；名字还没到的先攒着，流末的兜底扫描会补上。
			if name := toolNames[d.ToolIndex]; name != "" && !controlTool(name) {
				callID := toolIDs[d.ToolIndex]
				if callID == "" {
					callID = em.callIDFor(d.ToolIndex)
				}
				em.callArgsStream(callID, name, toolArgsRaw[d.ToolIndex])
			}
			return
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
	// 工具参数：把"累计到现在的参数"喂给发射器，只有新增的那一段会成为 delta。
	// 控制工具（answer / narrative）的参数不是参数，是要说的话——按文本流出去。
	for index, raw := range toolArgsRaw {
		name := toolNames[index]
		callID := toolIDs[index]
		if callID == "" {
			callID = em.callIDFor(index)
		}
		if controlTool(name) {
			// 复述抑制：模型常把刚流式说过的一段又走 narrative/answer 说一遍
			// （真实事故：流式说了"自检完成＋总结"，narrative 又把总结原样复述，
			// 同一段在界面上出现两次）。这里挡的是**扫尾通道**，判定用"正在开的
			// 这条消息里已有的文字"；工具执行时的 Emit 通道由 emitNarrative 的
			// 同一套包含判定挡（那边比对的是上一句）。
			if t := partialTextArg(raw); t != "" && len([]rune(t)) >= 8 &&
				strings.Contains(em.openText(), t) {
				continue
			}
			if em.controlText(index, raw) {
				streamedText = true
			}
			continue
		}
		em.callArgsStream(callID, name, raw)
	}
	if fullReasoning.Len() > 0 {
		em.closeReasoning()
	}
	if err != nil && res.Content == "" && len(res.ToolCalls) == 0 {
		if emitted {
			// 已经吐过字了：重放会重复内容，这次不重试
			return res, err, true
		}
		// provider 不支持 stream（或中途断流）：退回一次性请求，功能不受影响
		em.notice("流式中断，已回退普通请求。", NoticeWarn)
		res, err = client.Chat(roundCtx, messages, tools)
		return res, err, false
	}
	return res, err, streamedText
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

// requestShape 追踪"这次请求是不是上一次的追加延长"。
//
// 这是 dsh"每个请求都可从日志重建"的轻量落地：请求由追加式历史投影而来，
// 只要头部（system 提示 + 工具 schema）不变，就该是上一次的前缀延长，
// provider 的前缀缓存才可能命中。任何"没有记录原因的改写"都是 bug。
//
// 为什么不直接看 provider 的缓存用量：自建 vLLM 网关这类后端根本不回
// cached_tokens，命中与否在 API 上是不可观测的，只能自己验。
type requestShape struct {
	seen bool
	prev []llm.Message
	// reason 是上一次请求被重塑的原因（"" = 纯追加）。目前只有压缩会设。
	reason string
}

// check 判断 cur 是否仍是 prev 的追加延长，返回 (是否追加, 首个分歧下标)。
// 首次请求、以及上一次被有记录地重塑过时，重建基线并直接通过。
func (s *requestShape) check(cur []llm.Message) (bool, int) {
	if !s.seen || s.reason != "" {
		return true, -1
	}
	if len(cur) < len(s.prev) {
		return false, len(cur)
	}
	for i := range s.prev {
		if !sameMessage(s.prev[i], cur[i]) {
			return false, i
		}
	}
	return true, -1
}

func sameMessage(a, b llm.Message) bool {
	if a.Role != b.Role || a.Content != b.Content ||
		a.ToolCallID != b.ToolCallID || a.Name != b.Name ||
		len(a.ToolCalls) != len(b.ToolCalls) {
		return false
	}
	for i := range a.ToolCalls {
		if a.ToolCalls[i] != b.ToolCalls[i] {
			return false
		}
	}
	return true
}

// streamEnabled 允许用 WIKIATLAS_LLM_STREAM=0 关掉流式（排障用，默认开）。
func streamEnabled() bool {
	v := strings.TrimSpace(os.Getenv("WIKIATLAS_LLM_STREAM"))
	return !(v == "0" || strings.EqualFold(v, "false") || strings.EqualFold(v, "off"))
}
