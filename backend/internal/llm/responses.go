package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ResponsesClient 走 OpenAI 的 Responses API（/v1/responses）。
//
// 差异全在 wire 层，对上层同构（产出 ChatResult / Delta）：
//
//   - 输入是扁平的 input 项。工具往返用 function_call / function_call_output
//     两项表达，配对靠 call_id —— 我们把它放在 ToolCall.ID 里，正好对上。
//   - 工具 schema 是**扁平**的 {type,name,description,parameters}。
//   - 系统提示走顶层 instructions，不混在 input 里。
//   - 流事件是 response.output_text.delta / response.function_call_arguments.delta /
//     response.completed；用量在 completed 的 response.usage 上。
type ResponsesClient struct {
	cfg    Config
	client *http.Client
	stream *http.Client
}

// defaultMaxOutputTokens 是"设置页没填单次输出上限"时用的值。
//
// 必须和设置页上写的默认值一致（那儿写的就是 65536）。曾经这里是 8192，
// 而界面写 65536——模型写一整篇条目时**正好顶死在 8192 上**，arguments 的 JSON
// 被从中间截断（右引号都没了），非法参数进了会话历史，之后每一轮请求都被网关
// 400 掉，工单直接失败。输出上限不是"调小更省"，它是**功能上限**：
// 顶到它，模型就没有"写完整"这个选项了。
const defaultMaxOutputTokens = 65536

// NewResponsesClient 造一个 Responses 协议的客户端。
func NewResponsesClient(cfg Config) *ResponsesClient {
	if cfg.MaxTokens == nil {
		def := defaultMaxOutputTokens
		cfg.MaxTokens = &def
	}
	return &ResponsesClient{
		cfg:    cfg,
		client: &http.Client{Timeout: 300 * time.Second},
		stream: &http.Client{},
	}
}

func (c *ResponsesClient) Model() string { return c.cfg.Model }

func (c *ResponsesClient) maxTokens() int {
	if c.cfg.MaxTokens != nil {
		return *c.cfg.MaxTokens
	}
	return 8192
}

type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

// normalize 归一化用量。注意 Responses 的 input_tokens 已经**含**缓存命中
// （cached_tokens 是它的明细），不需要再补回来。
func (u *responsesUsage) normalize() Usage {
	if u == nil {
		return Usage{}
	}
	cached := 0
	if u.InputTokensDetails != nil {
		cached = u.InputTokensDetails.CachedTokens
	}
	total := u.TotalTokens
	if total == 0 {
		total = u.InputTokens + u.OutputTokens
	}
	return Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      total,
		CacheReadTokens:  cached,
	}
}

// responsesItem 是 output 里的一项：message 或 function_call。
type responsesItem struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type responsesResponse struct {
	Output []responsesItem `json:"output"`
	Usage  *responsesUsage `json:"usage"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// safeArguments 保证发出去的 function_call.arguments 一定是合法 JSON。
//
// 模型偶尔会吐一份花括号没闭合的参数（一次想写太长、被输出上限截断时最常见）。
// 这种调用**已经进了会话日志**（模型可见 ⟺ 有日志），而 Responses 网关会去解析
// arguments——原样重放就是 400，整段会话从此不可用：真实事故里一次 write_content
// 被截断，之后每一轮请求都 400，工单直接失败。
// 降级成空对象只是让请求过得去；配对的结果项里已经写明了这次调用为什么没成。
func safeArguments(args string) string {
	if json.Valid([]byte(args)) {
		return args
	}
	return "{}"
}

// buildBody 把对话折成 Responses 的 input 项。
func (c *ResponsesClient) buildBody(messages []Message, tools []ToolDef, stream bool) map[string]any {
	var instructions []string
	input := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case "system":
			if strings.TrimSpace(m.Content) != "" {
				instructions = append(instructions, m.Content)
			}
		case "tool":
			// 工具结果：靠 call_id 和前面那条 function_call 配对
			input = append(input, map[string]any{
				"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content,
			})
		case "assistant":
			if strings.TrimSpace(m.Content) != "" {
				input = append(input, map[string]any{"role": "assistant", "content": m.Content})
			}
			for _, tc := range m.ToolCalls {
				input = append(input, map[string]any{
					"type": "function_call", "call_id": tc.ID,
					"name": tc.Function.Name, "arguments": safeArguments(tc.Function.Arguments),
				})
			}
		default: // user
			input = append(input, map[string]any{"role": "user", "content": m.Content})
		}
	}

	body := map[string]any{
		"model":             c.cfg.Model,
		"input":             input,
		"max_output_tokens": c.maxTokens(),
		// 历史由会话日志自己管（那是事实源），不依赖服务端存响应
		"store": false,
	}
	if len(instructions) > 0 {
		body["instructions"] = strings.Join(instructions, "\n\n")
	}
	if stream {
		body["stream"] = true
	}
	if c.cfg.Temperature != nil {
		body["temperature"] = *c.cfg.Temperature
	}
	if e := strings.TrimSpace(c.cfg.ReasoningEffort); e != "" {
		body["reasoning"] = map[string]any{"effort": e}
	}
	if len(tools) > 0 {
		ft := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			var params any = map[string]any{"type": "object", "properties": map[string]any{}}
			if len(t.Function.Parameters) > 0 {
				_ = json.Unmarshal(t.Function.Parameters, &params)
			}
			ft = append(ft, map[string]any{
				"type":        "function",
				"name":        t.Function.Name,
				"description": t.Function.Description,
				"parameters":  params,
			})
		}
		body["tools"] = ft
	}
	return body
}

func (c *ResponsesClient) do(ctx context.Context, hc *http.Client, body map[string]any, stream bool) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(c.cfg.Endpoint, "/") + "/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	return hc.Do(req)
}

// Chat 是非流式请求（执行器的回退路径用）。
func (c *ResponsesClient) Chat(ctx context.Context, messages []Message, tools []ToolDef) (ChatResult, error) {
	resp, err := c.do(ctx, c.client, c.buildBody(messages, tools, false), false)
	if err != nil {
		return ChatResult{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResult{}, fmt.Errorf("llm read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ChatResult{}, HTTPError{Status: resp.StatusCode, Body: truncateForErr(string(raw), 400)}
	}
	var out responsesResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return ChatResult{}, fmt.Errorf("llm decode: %w", err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return ChatResult{}, fmt.Errorf("llm error: %s", out.Error.Message)
	}
	res := ChatResult{Usage: out.Usage.normalize()}
	for _, item := range out.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					res.Content += c.Text
				}
			}
		case "function_call":
			res.ToolCalls = append(res.ToolCalls, ToolCall{
				ID: item.CallID, Type: "function",
				Function: FunctionCall{Name: item.Name, Arguments: item.Arguments},
			})
		}
	}
	return res, nil
}

// ChatStream 实现 StreamingClient。
func (c *ResponsesClient) ChatStream(
	ctx context.Context,
	messages []Message,
	tools []ToolDef,
	onDelta func(Delta),
) (ChatResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var lastRead atomicInt64
	lastRead.Store(time.Now().UnixNano())
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if time.Since(time.Unix(0, lastRead.Load())) > streamIdleTimeout {
					cancel()
					return
				}
			}
		}
	}()

	resp, err := c.do(ctx, c.stream, c.buildBody(messages, tools, true), true)
	if err != nil {
		return ChatResult{}, fmt.Errorf("llm request: %w", err)
	}
	body := &activityReader{rc: resp.Body, last: &lastRead}
	defer body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(body, 4096))
		return ChatResult{}, HTTPError{Status: resp.StatusCode, Body: truncateForErr(string(raw), 400)}
	}

	var (
		content   strings.Builder
		reasoning strings.Builder
		order     []int
		byIndex   = map[int]*ToolCall{}
		usage     Usage
	)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue // event: 行、空行、心跳都跳过
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev struct {
			Type        string `json:"type"`
			Delta       string `json:"delta"`
			OutputIndex int    `json:"output_index"`
			// function_call_arguments.done 把最终参数放在**顶层**（不是 item 里）
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Item      struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"item"`
			Response struct {
				Usage *responsesUsage `json:"usage"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta != "" {
				content.WriteString(ev.Delta)
				if onDelta != nil {
					onDelta(Delta{Kind: "text", Text: ev.Delta})
				}
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if ev.Delta != "" {
				reasoning.WriteString(ev.Delta)
				if onDelta != nil {
					onDelta(Delta{Kind: "reasoning", Text: ev.Delta})
				}
			}
		case "response.output_item.added":
			if ev.Item.Type == "function_call" {
				call := &ToolCall{ID: ev.Item.CallID, Type: "function"}
				call.Function.Name = ev.Item.Name
				call.Function.Arguments = ev.Item.Arguments
				if _, seen := byIndex[ev.OutputIndex]; !seen {
					order = append(order, ev.OutputIndex)
				}
				byIndex[ev.OutputIndex] = call
			}
		case "response.function_call_arguments.delta":
			call := byIndex[ev.OutputIndex]
			if call == nil {
				continue
			}
			call.Function.Arguments += ev.Delta
			if onDelta != nil {
				onDelta(Delta{
					Kind: "tool", Text: ev.Delta, ToolIndex: ev.OutputIndex,
					ToolID: call.ID, ToolName: call.Function.Name,
				})
			}
		case "response.function_call_arguments.done":
			// 参数定稿，**这是权威值**。有的网关不发增量、只在 done 里给全量；
			// 只认 delta 就会拿到空参数——工具于是被空参数调用（静默地错）。
			// 只补不覆盖：增量已经把前缀写好了，这里只把缺的尾巴补出去。
			call := byIndex[ev.OutputIndex]
			if call == nil {
				call = &ToolCall{ID: ev.Item.CallID, Type: "function"}
				byIndex[ev.OutputIndex] = call
				order = append(order, ev.OutputIndex)
			}
			if ev.Name != "" {
				call.Function.Name = ev.Name
			}
			if prev := len(call.Function.Arguments); len(ev.Arguments) > prev {
				call.Function.Arguments = ev.Arguments
				if onDelta != nil {
					onDelta(Delta{
						Kind: "tool", Text: ev.Arguments[prev:], ToolIndex: ev.OutputIndex,
						ToolID: call.ID, ToolName: call.Function.Name,
					})
				}
			}
		case "response.output_item.done":
			// 兜底：项完成时带的最终形态里同样有 name/arguments（规范如此）。与上面一样只补不覆盖。
			if ev.Item.Type == "function_call" {
				call := byIndex[ev.OutputIndex]
				if call == nil {
					call = &ToolCall{ID: ev.Item.CallID, Type: "function"}
					byIndex[ev.OutputIndex] = call
					order = append(order, ev.OutputIndex)
				}
				if ev.Item.Name != "" {
					call.Function.Name = ev.Item.Name
				}
				if prev := len(call.Function.Arguments); len(ev.Item.Arguments) > prev {
					call.Function.Arguments = ev.Item.Arguments
					if onDelta != nil {
						onDelta(Delta{
							Kind: "tool", Text: ev.Item.Arguments[prev:], ToolIndex: ev.OutputIndex,
							ToolID: call.ID, ToolName: call.Function.Name,
						})
					}
				}
			}
		case "response.completed":
			usage = ev.Response.Usage.normalize()
		case "error":
			if ev.Error != nil {
				return ChatResult{}, fmt.Errorf("llm error: %s", ev.Error.Message)
			}
		case "response.failed", "response.incomplete":
			msg := ev.Type
			if ev.Response.Error != nil && ev.Response.Error.Message != "" {
				msg = ev.Response.Error.Message
			}
			return ChatResult{}, fmt.Errorf("llm error: %s", msg)
		}
	}
	if err := scanner.Err(); err != nil && content.Len() == 0 && len(order) == 0 {
		return ChatResult{}, fmt.Errorf("llm stream: %w", err)
	}

	out := ChatResult{Content: content.String(), Reasoning: reasoning.String(), Usage: usage}
	for _, idx := range order {
		if call := byIndex[idx]; call != nil {
			out.ToolCalls = append(out.ToolCalls, *call)
		}
	}
	return out, nil
}
