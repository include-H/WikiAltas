package run

import (
	"fmt"
	"strings"
	"time"

	"wikiatlas/backend/internal/llm"
)

// ── 事件类型 ────────────────────────────────────────────────────────────────
//
// 所有事件类型都只在这里定义一次：执行器写、前端读，两边都以本文件为准。
//
//	· EvResp* —— OpenAI Responses 规范的流事件，形状与语义照抄规范；
//	· EvWA*   —— 规范里没有、但产品需要的东西，走 **wikiatlas.* 独立命名空间**。
//
// 这就是"留后手"的位置：以后换协议，只有这一层映射要改，上层与前端只见一种形状。
const (
	EvRespCreated    = "response.created"
	EvRespInProgress = "response.in_progress"
	EvItemAdded      = "response.output_item.added"
	EvItemDone       = "response.output_item.done"
	EvTextDelta      = "response.output_text.delta"
	EvTextDone       = "response.output_text.done"
	EvReasonDelta    = "response.reasoning_summary_text.delta"
	EvReasonDone     = "response.reasoning_summary_text.done"
	EvFnArgsDelta    = "response.function_call_arguments.delta"
	EvFnArgsDone     = "response.function_call_arguments.done"
	EvRespCompleted  = "response.completed"
	EvRespFailed     = "response.failed"

	// ── wikiatlas.* 旁路 ──
	//
	// 任务清单 / 用量与缓存命中 / 宿主播报与护栏提醒 / 会话与工单元信息。
	// 前端把 response.* 喂给 Semi 原生的流式归约器，把这一组交给宿主部件
	// （任务面板、用量环、提醒条），**同流不同类**。
	EvWATodo    = "wikiatlas.todo"
	EvWAUsage   = "wikiatlas.usage"
	EvWANotice  = "wikiatlas.notice"
	EvWAMeta    = "wikiatlas.meta"
	EvWARequest = "wikiatlas.request"
	EvWAContext = "wikiatlas.context"
	EvWATree    = "wikiatlas.tree"
	EvWAStage   = "wikiatlas.content.staging"
	EvWACommit  = "wikiatlas.content.committed"
)

// 播报的语气：前端据此决定提醒条的颜色（不是错误就别画成红的）。
const (
	NoticeInfo  = "info"
	NoticeWarn  = "warn"
	NoticeError = "error"
)

// itemExtra 是挂在输出项上的宿主附加信息（工具结果摘要、耗时、diff 统计、产物）。
//
// 为什么不单开一个 wikiatlas.* 事件：这些都是**某一项的属性**，项自足才好回放——
// 渲染一张工具卡片不该再去别的事件里检索它的结果。规范里 function_call 项本来
// 就有 output/status，我们把宿主那份收在 item 的 `wikiatlas` 键下（项本来就是
// 开放对象，多一个键不影响任何规范消费者）。
const itemExtraKey = "wikiatlas"

// respEmitter 把执行器的产出**统一映射**成 Responses 流事件。
//
// 这是 harness 里唯一知道"线上事件长什么样"的地方：
//   - 模型与工具的产出 → 规范的输出项 + 增量事件；
//   - 宿主自己要说的话 → wikiatlas.* 旁路事件。
//
// 两条由这里保证的性质：
//
//  1. **项自足**：每个输出项在 output_item.done 里带最终形态（完整文本、完整参数、
//     工具结果），所以历史回放丢掉增量也不缺信息，只缺"打字过程"。
//  2. **续跑接续**：output_index 由构造时扫一遍已有事件定起点，resume 出来的项
//     不会覆盖断点前的项。
type respEmitter struct {
	m      *Manager
	runID  string
	model  string
	respID string

	items []map[string]any // 下标 == output_index，顺序即输出顺序

	msgIdx int // 当前打开的 message 项（-1 = 没有）
	msgTxt strings.Builder

	reasonIdx   int
	reasonTxt   strings.Builder
	reasonRunes int

	fcIdx    map[string]int // call_id → output_index
	fcSent   map[string]int // call_id → 已经流出去的参数长度
	fcByTool map[int]string // 流式 tool_index → call_id
	ctrlSent map[int]string // 控制工具的流式文本（tool_index → 已吐出的那句）

	usage llm.Usage // 本次 response 的累计用量
}

// maxReasoningRunes 是思考项的展示上限：CoT 只作展示、不回喂模型，
// 但它是 run_events 里最容易被网关灌爆的一行，所以在这里截一次。
const maxReasoningRunes = 4000

// newRespEmitter 造发射器。resume=true 时接着已有事件往下编号（见类型注释）。
func (m *Manager) newRespEmitter(runID, model string, resume bool) *respEmitter {
	e := &respEmitter{
		m: m, runID: runID, model: model,
		respID:    "resp_" + runID,
		msgIdx:    -1,
		reasonIdx: -1,
		fcIdx:     map[string]int{},
		fcSent:    map[string]int{},
		fcByTool:  map[int]string{},
		ctrlSent:  map[int]string{},
	}
	if resume {
		e.respID = fmt.Sprintf("resp_%s_%d", runID, time.Now().UnixNano())
		e.items = e.loadPriorItems()
	}
	return e
}

// loadPriorItems 续跑时把断点之前的输出项读回来，让新的项接着往后编号。
//
// 读的是 `response.output_item.done` 的 item——项是**自足**的（最终形态都在里面），
// 所以一遍扫描就能把发射器的缓冲还原成"断之前那样"。不这么做的话，
// 续跑结束时那份 response.completed 只会带上续跑之后产生的项，前面的全丢。
func (e *respEmitter) loadPriorItems() []map[string]any {
	events, err := e.m.store.ListRunEvents(e.runID, 0, 10000)
	if err != nil {
		return nil
	}
	byIdx := map[int]map[string]any{}
	max := -1
	for _, ev := range events {
		if ev.Type != EvItemDone {
			continue
		}
		idx, ok := ev.Payload["output_index"].(float64)
		if !ok {
			continue
		}
		item, ok := ev.Payload["item"].(map[string]any)
		if !ok {
			continue
		}
		byIdx[int(idx)] = item
		if int(idx) > max {
			max = int(idx)
		}
	}
	if max < 0 {
		return nil
	}
	out := make([]map[string]any, max+1)
	for i := range out {
		out[i] = byIdx[i] // 缺口留 nil，outputItems() 会跳过
	}
	return out
}

func (e *respEmitter) emit(eventType string, payload map[string]any) {
	e.m.emit(e.runID, eventType, payload)
}

// itemID 给项一个稳定 id（前端把它当 React key）。
func itemID(kind string, index int) string { return fmt.Sprintf("%s_%d", kind, index) }

// ── response 级 ──

// created 只在该 run 的第一次执行时发；续跑发 in_progress，
// 这样归约器里的 response id / 创建时间不被后来的续跑改掉。
func (e *respEmitter) created(fresh bool) {
	apiType := EvRespInProgress
	if fresh {
		e.items = nil
		apiType = EvRespCreated
	}
	e.emit(apiType, map[string]any{
		"response": map[string]any{
			"id":         e.respID,
			"object":     "response",
			"model":      e.model,
			"status":     "in_progress",
			"created_at": time.Now().Unix(),
			// 规范里 response.created 就带一个空的 output（响应对象是完整的，
			// 只是还没有项）。
			"output": []map[string]any{},
		},
	})
}

// completed 关掉还开着的项，并把**完整响应**发出去。
//
// `response.output` 是规范要求的字段，照发。**`output_text` 不发**：它是官方 SDK
// 在客户端拼出来的便利属性，原始 JSON 里没有它（核对过 platform.openai.com 的
// response.completed 示例——只有 output，没有 output_text）。而且多给这一个字段
// 会把界面弄坏：Semi 的渲染器只要看到 `message.output_text` 非空，就只渲染整段
// 文本、把 content 数组（思考块、工具调用）整个跳过。
//
// 也别指望 response.output 撑起历史回放：Semi 的归约器只把 response.completed
// 当作"把状态置为 completed"，并**不**读它——真正让丢增量后仍能回放成立的，
// 是**每个输出项在 output_item.done 里自足**。
func (e *respEmitter) completed(summary string, wroteContent bool) {
	e.closeMessage()
	e.closeReasoning()
	e.sealOpenCalls("未等到结果（工单收尾时这次调用还没跑完）")
	e.emit(EvRespCompleted, map[string]any{
		"response": map[string]any{
			"id":         e.respID,
			"object":     "response",
			"model":      e.model,
			"status":     "completed",
			"created_at": time.Now().Unix(),
			"output":     e.outputItems(),
			"usage": map[string]any{
				"input_tokens":  e.usage.PromptTokens,
				"output_tokens": e.usage.CompletionTokens,
				"total_tokens":  e.usage.TotalTokens,
				"input_tokens_details": map[string]any{
					"cached_tokens": e.usage.CacheReadTokens,
				},
			},
		},
		"wikiatlas": map[string]any{"summary": summary, "wroteContent": wroteContent},
	})
}

// failed 是失败退场（规范里 response.failed 的载荷）。
func (e *respEmitter) failed(code, msg string) {
	e.closeMessage()
	e.closeReasoning()
	e.sealOpenCalls("未等到结果（这一轮失败了）")
	e.emit(EvRespFailed, map[string]any{
		"response": map[string]any{
			"id":     e.respID,
			"object": "response",
			"model":  e.model,
			"status": "failed",
			"error":  map[string]any{"code": code, "message": msg},
		},
	})
}

// sealOpenCalls 把还挂在 in_progress 上的工具项收尾。
//
// 中断、失败、以及"轮询到一半 ctx 就没了"都会留下这种项：参数已经流出来了，
// 工具却没跑完。不收尾的话 output_item.added 与 .done 就不配对——那既破了
// 规范，前端也会留一张永远在转的卡片。
func (e *respEmitter) sealOpenCalls(reason string) {
	for idx, it := range e.items {
		if it == nil || it["type"] != "function_call" || it["status"] != "in_progress" {
			continue
		}
		extra, _ := it[itemExtraKey].(map[string]any)
		if extra == nil {
			extra = map[string]any{}
		}
		extra["outputSummary"] = reason
		extra["ok"] = false
		it[itemExtraKey] = extra
		it["status"] = "incomplete"
		e.emit(EvItemDone, map[string]any{"output_index": idx, "item": cloneItem(it)})
	}
}

// ── message 项（模型说的话） ──

// ensureMessage 懒开一个 message 项：只有真的要吐字时才开，
// 否则"只调工具的轮次"会留下一串空消息。
func (e *respEmitter) ensureMessage() int {
	if e.msgIdx >= 0 {
		return e.msgIdx
	}
	idx := len(e.items)
	item := map[string]any{
		"type": "message", "id": itemID("msg", idx),
		"role": "assistant", "status": "in_progress",
		"content": []map[string]any{},
	}
	e.items = append(e.items, item)
	e.msgIdx = idx
	e.emit(EvItemAdded, map[string]any{"output_index": idx, "item": cloneItem(item)})
	return idx
}

// textDelta 是模型正在吐出来的一个字/一段话（output_text.delta）。
func (e *respEmitter) textDelta(s string) {
	if s == "" {
		return
	}
	idx := e.ensureMessage()
	e.msgTxt.WriteString(s)
	e.emit(EvTextDelta, map[string]any{
		"output_index": idx, "content_index": 0, "item_id": itemID("msg", idx), "delta": s,
	})
}

// closeMessage 收尾当前 message 项：把累计文本写进项的最终形态，
// 并把它返回（执行器用它记住"这一轮已经说过这句"，好挡掉同一句的第二次）。
// 一个增量都没流过就什么都不发（空项是纯噪声）。
func (e *respEmitter) closeMessage() string {
	if e.msgIdx < 0 {
		return ""
	}
	idx, text := e.msgIdx, e.msgTxt.String()
	e.msgIdx, e.msgTxt = -1, strings.Builder{}
	if strings.TrimSpace(text) == "" {
		e.items[idx] = nil // 空项不留输出（output 里会被过滤掉）
		return ""
	}
	item := map[string]any{
		"type": "message", "id": itemID("msg", idx),
		"role": "assistant", "status": "completed",
		// annotations 是规范里 output_text part 的必填项（哪怕为空数组）
		"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []map[string]any{}}},
	}
	e.items[idx] = item
	e.emit(EvTextDone, map[string]any{
		"output_index": idx, "content_index": 0, "item_id": itemID("msg", idx), "text": text,
	})
	e.emit(EvItemDone, map[string]any{"output_index": idx, "item": cloneItem(item)})
	return text
}

// message 发一条完整消息（不必先有增量）：模型收尾的那句话、narrative/answer
// 工具要说的那句话，都走这里。
func (e *respEmitter) message(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	e.closeMessage() // 上一条没收的先收掉
	idx := e.ensureMessage()
	e.msgTxt.WriteString(text)
	e.emit(EvTextDelta, map[string]any{
		"output_index": idx, "content_index": 0, "item_id": itemID("msg", idx), "delta": text,
	})
	e.closeMessage()
}

// ── reasoning 项（网关的思考字段） ──

func (e *respEmitter) ensureReasoning() int {
	if e.reasonIdx >= 0 {
		return e.reasonIdx
	}
	idx := len(e.items)
	item := map[string]any{
		"type": "reasoning", "id": itemID("rs", idx), "status": "in_progress",
		// 规范里 reasoning 项同时有 content 与 summary 两个数组（这里只填 summary）
		"content": []map[string]any{},
		"summary": []map[string]any{},
	}
	e.items = append(e.items, item)
	e.reasonIdx = idx
	e.emit(EvItemAdded, map[string]any{"output_index": idx, "item": cloneItem(item)})
	return idx
}

func (e *respEmitter) reasoningDelta(s string) {
	if s == "" || e.reasonRunes >= maxReasoningRunes {
		return
	}
	if r := []rune(s); len(r) > maxReasoningRunes-e.reasonRunes {
		r = r[:maxReasoningRunes-e.reasonRunes]
		s = string(r)
	}
	if s == "" {
		return
	}
	idx := e.ensureReasoning()
	e.reasonRunes += len([]rune(s))
	e.reasonTxt.WriteString(s)
	e.emit(EvReasonDelta, map[string]any{
		"output_index": idx, "summary_index": 0, "item_id": itemID("rs", idx), "delta": s,
	})
}

// controlText 把**控制工具**（answer / narrative）的流式参数当成"模型正在说的话"。
//
// 参数是半截 JSON（`{"text": "已确认对`），所以只能宽松地抠 text 字段；
// 只有新增的那一段会作为 delta 出去，和模型正文吐字走同一条通道——
// 前端因此不需要再解析工具参数，答案就是逐字长出来的。
// 返回是否真的吐出了字（执行器据此判断"这句话已经画过了"）。
func (e *respEmitter) controlText(toolIndex int, raw string) bool {
	t := partialTextArg(raw)
	prev := e.ctrlSent[toolIndex]
	if t == "" || t == prev || !strings.HasPrefix(t, prev) {
		return false
	}
	e.textDelta(t[len(prev):])
	e.ctrlSent[toolIndex] = t
	return true
}

// controlStreamed 这一路控制工具的文本是否已经在流式时吐出去过。
func (e *respEmitter) controlStreamed(toolIndex int) bool {
	return e.ctrlSent[toolIndex] != ""
}

// openText 是**当前还没收尾**的 message 项里已经攒下的文本。
// 给"这句话刚才是不是已经说过了"的判定用（扫尾查控制工具复述）。
func (e *respEmitter) openText() string {
	return e.msgTxt.String()
}

// partialTextArg 从半截 JSON 里宽松地抠出 "text" 字段（未闭合也能拿到已有的部分）。
// 与前端同一套思路：流式参数随时可能断在半个字符串上，JSON.parse 会直接失败。
func partialTextArg(raw string) string {
	const key = `"text"`
	i := strings.Index(raw, key)
	if i < 0 {
		return ""
	}
	i += len(key)
	for i < len(raw) && (raw[i] == ' ' || raw[i] == ':' || raw[i] == '\t') {
		i++
	}
	if i >= len(raw) || raw[i] != '"' {
		return ""
	}
	i++
	var b strings.Builder
	for i < len(raw) {
		c := raw[i]
		if c == '\\' && i+1 < len(raw) {
			switch raw[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte(raw[i+1])
			}
			i += 2
			continue
		}
		if c == '"' {
			break
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// closeReasoning 收尾思考项。full 非空时以它为准（轮末的权威全文）。
func (e *respEmitter) closeReasoning() {
	if e.reasonIdx < 0 {
		return
	}
	idx := e.reasonIdx
	text := e.reasonTxt.String()
	e.reasonIdx, e.reasonTxt = -1, strings.Builder{}
	if strings.TrimSpace(text) == "" {
		e.items[idx] = nil
		return
	}
	item := map[string]any{
		"type": "reasoning", "id": itemID("rs", idx), "status": "completed",
		"content": []map[string]any{},
		"summary": []map[string]any{{"type": "summary_text", "text": text}},
	}
	e.items[idx] = item
	e.emit(EvReasonDone, map[string]any{
		"output_index": idx, "summary_index": 0, "item_id": itemID("rs", idx), "text": text,
	})
	e.emit(EvItemDone, map[string]any{"output_index": idx, "item": cloneItem(item)})
}

// ── function_call 项（工具调用） ──

// ensureCall 懒开一个 function_call 项。
func (e *respEmitter) ensureCall(callID, name string) int {
	if idx, ok := e.fcIdx[callID]; ok {
		return idx
	}
	idx := len(e.items)
	item := map[string]any{
		"type": "function_call", "id": itemID("fc", idx),
		"call_id": callID, "name": name,
		"arguments": "", "status": "in_progress",
	}
	e.items = append(e.items, item)
	e.fcIdx[callID] = idx
	e.emit(EvItemAdded, map[string]any{"output_index": idx, "item": cloneItem(item)})
	return idx
}

// callArgsStream 把"累计到现在的参数"喂进来，只有新增的那一段会作为 delta 出去。
// 流式与非流式共用：非流式就是一次性把整串给过来。
func (e *respEmitter) callArgsStream(callID, name, cumulative string) {
	idx := e.ensureCall(callID, name)
	if name != "" {
		if it := e.items[idx]; it != nil {
			it["name"] = name
		}
	}
	sent := e.fcSent[callID]
	if len(cumulative) <= sent {
		return
	}
	delta := cumulative[sent:]
	e.fcSent[callID] = len(cumulative)
	e.emit(EvFnArgsDelta, map[string]any{
		"output_index": idx, "item_id": itemID("fc", idx), "delta": delta,
	})
}

// callArgsDone 参数定稿（`function_call_arguments.done`）。
func (e *respEmitter) callArgsDone(callID, name, args string) {
	idx := e.ensureCall(callID, name)
	e.fcSent[callID] = len(args)
	e.emit(EvFnArgsDone, map[string]any{
		"output_index": idx, "item_id": itemID("fc", idx),
		"call_id": callID, "name": name, "arguments": args,
	})
}

// toolResult 把一项工具调用的结果收尾成 output_item.done。
// extra 里的东西（结果摘要 / 耗时 / diff / 产物）挂在项的 wikiatlas 键下。
func (e *respEmitter) toolResult(callID, name, args string, ok bool, extra map[string]any) {
	idx := e.ensureCall(callID, name)
	e.fcSent[callID] = len(args)
	status := "completed"
	if !ok {
		status = "failed"
	}
	item := map[string]any{
		"type": "function_call", "id": itemID("fc", idx),
		"call_id": callID, "name": name,
		"arguments": args, "status": status,
		itemExtraKey: extra,
	}
	e.items[idx] = item
	e.emit(EvItemDone, map[string]any{"output_index": idx, "item": cloneItem(item)})
}

// toolIndex 登记"流式 tool_index ↔ call_id"的对应，供增量归位。
func (e *respEmitter) toolIndex(toolIndex int, callID string) {
	if callID != "" {
		e.fcByTool[toolIndex] = callID
	}
}

// callIDFor 由流式 tool_index 找回 call_id（增量先于 call 元数据到达时用）。
func (e *respEmitter) callIDFor(toolIndex int) string {
	if id, ok := e.fcByTool[toolIndex]; ok {
		return id
	}
	return fmt.Sprintf("call_%d", toolIndex)
}

// ── 汇总 ──

func (e *respEmitter) addUsage(u llm.Usage) {
	e.usage.PromptTokens += u.PromptTokens
	e.usage.CompletionTokens += u.CompletionTokens
	e.usage.TotalTokens += u.TotalTokens
	e.usage.CacheReadTokens += u.CacheReadTokens
	e.usage.CacheWriteTokens += u.CacheWriteTokens
}

// outputItems 是 response.output 的取值：不带空位（续跑留下的空洞）。
func (e *respEmitter) outputItems() []map[string]any {
	out := make([]map[string]any, 0, len(e.items))
	for _, it := range e.items {
		if it != nil {
			out = append(out, it)
		}
	}
	return out
}

// contentParts 取一个项的 content 数组，两种形态都认：内存里新造的是
// []map[string]any，从事件日志读回来的是 JSON 解出来的 []any（续跑时会遇到）。
func contentParts(v any) []map[string]any {
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, p := range t {
			if m, ok := p.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

// cloneItem 浅拷贝一层（嵌套的 content/summary 也拷）——发出去的事件不能被
// 后续的写操作改动，否则订阅方可能读到正在被改的 map。
func cloneItem(it map[string]any) map[string]any {
	out := make(map[string]any, len(it))
	for k, v := range it {
		switch t := v.(type) {
		case []map[string]any:
			cp := make([]map[string]any, len(t))
			for i, m := range t {
				inner := make(map[string]any, len(m))
				for kk, vv := range m {
					inner[kk] = vv
				}
				cp[i] = inner
			}
			out[k] = cp
		case map[string]any:
			inner := make(map[string]any, len(t))
			for kk, vv := range t {
				inner[kk] = vv
			}
			out[k] = inner
		default:
			out[k] = v
		}
	}
	return out
}

// ── wikiatlas.* 旁路 ──

// notice 是宿主播报 / 护栏提醒：模型没说这句话，是 harness 说的。
// 它们和模型输出分开走，前端就能"只把模型的输出画成对话，把宿主的话画成提醒条"。
func (e *respEmitter) notice(text, tone string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	e.emit(EvWANotice, map[string]any{"text": text, "tone": tone})
}

// noticeRun 是发射器之外（建单/续跑/取消这些生命周期动作）播报的入口。
func (m *Manager) noticeRun(runID, text, tone string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	m.emit(runID, EvWANotice, map[string]any{"text": text, "tone": tone})
}

// messageItemText 从一个（已反序列化的）输出项里取它说的话；不是 message 项就是空。
func messageItemText(raw any) string {
	item, ok := raw.(map[string]any)
	if !ok || item["type"] != "message" {
		return ""
	}
	var b strings.Builder
	for _, p := range contentParts(item["content"]) {
		if s, ok := p["text"].(string); ok {
			b.WriteString(s)
		}
	}
	return strings.TrimSpace(b.String())
}

func (e *respEmitter) todo(tasks any) {
	e.emit(EvWATodo, map[string]any{"tasks": tasks})
}

func (e *respEmitter) usageEvent(step int, u llm.Usage, contextWindow int) {
	e.emit(EvWAUsage, map[string]any{
		"step":             step,
		"promptTokens":     u.PromptTokens,
		"completionTokens": u.CompletionTokens,
		"totalTokens":      u.TotalTokens,
		"cacheReadTokens":  u.CacheReadTokens,
		"cacheWriteTokens": u.CacheWriteTokens,
		"contextWindow":    contextWindow,
	})
}

func (e *respEmitter) request(payload map[string]any) {
	e.emit(EvWARequest, payload)
}
