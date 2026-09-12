package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 流式适配：Responses 的流事件要能拼回完整内容与 function_call，并把增量喂给回调。
func TestChatStreamAssemblesDeltas(t *testing.T) {
	frames := []string{
		`{"type":"response.created","response":{"id":"resp_1","model":"m","status":"in_progress"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_0"}}`,
		`{"type":"response.output_text.delta","output_index":0,"delta":"白狼"}`,
		`{"type":"response.output_text.delta","output_index":0,"delta":"崛起"}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"想一想"}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_1","name":"search_works"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"q\":"}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"\"白狼崛起\"}"}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":1000,"output_tokens":20,"total_tokens":1020,"input_tokens_details":{"cached_tokens":640}}}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, f := range frames {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", f)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	c := NewResponsesClient(Config{Endpoint: srv.URL, Model: "m", APIKey: "k"})
	var text, reasoning strings.Builder
	var toolArgs strings.Builder
	res, err := c.ChatStream(context.Background(),
		[]Message{{Role: "user", Content: "写《白狼崛起》"}}, nil,
		func(d Delta) {
			switch d.Kind {
			case "text":
				text.WriteString(d.Text)
			case "reasoning":
				reasoning.WriteString(d.Text)
			case "tool":
				toolArgs.WriteString(d.Text)
			}
		})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if res.Content != "白狼崛起" {
		t.Fatalf("content=%q", res.Content)
	}
	if reasoning.String() != "想一想" || res.Reasoning != "想一想" {
		t.Fatalf("reasoning=%q / %q", reasoning.String(), res.Reasoning)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("toolCalls=%d", len(res.ToolCalls))
	}
	tc := res.ToolCalls[0]
	if tc.Function.Name != "search_works" || tc.ID != "call_1" {
		t.Fatalf("toolCall=%+v", tc)
	}
	if tc.Function.Arguments != `{"q":"白狼崛起"}` {
		t.Fatalf("args=%q", tc.Function.Arguments)
	}
	if toolArgs.String() != `{"q":"白狼崛起"}` {
		t.Fatalf("delta args=%q", toolArgs.String())
	}
	// 缓存命中是"前缀有没有被改写"的唯一生产信号：归一化必须带上它
	if res.Usage.CacheReadTokens != 640 || res.Usage.PromptTokens != 1000 || res.Usage.TotalTokens != 1020 {
		t.Fatalf("usage=%+v", res.Usage)
	}
}

// 非流式回退路径：一次性的 response 也要能折回 ChatResult。
func TestChatDecodesResponseOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"resp_1","status":"completed","output":[
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"白狼崛起"}]},
			{"type":"function_call","call_id":"call_1","name":"search_works","arguments":"{\"q\":\"白狼\"}"}
		],"usage":{"input_tokens":10,"output_tokens":2}}`)
	}))
	defer srv.Close()

	c := NewResponsesClient(Config{Endpoint: srv.URL, Model: "m", APIKey: "k"})
	res, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "写"}}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Content != "白狼崛起" || len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "call_1" {
		t.Fatalf("res=%+v", res)
	}
	// 没给 total 时应由输入+输出推出
	if res.Usage.TotalTokens != 12 {
		t.Fatalf("usage=%+v", res.Usage)
	}
}

// provider 不支持 stream（返回普通 JSON / 报错）时不能让整条工单失败：执行器会回退 Chat。
func TestChatStreamPropagatesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"stream not supported"}}`)
	}))
	defer srv.Close()

	c := NewResponsesClient(Config{Endpoint: srv.URL, Model: "m", APIKey: "k"})
	if _, err := c.ChatStream(context.Background(), nil, nil, nil); err == nil {
		t.Fatal("期望报错以便执行器回退 Chat")
	}
}

// sseServer 把一串 Responses 帧做成一个流式端点。
func sseServer(t *testing.T, frames []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, f := range frames {
			fmt.Fprintf(w, "event: x\ndata: %s\n\n", f)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 有的网关**不发** function_call_arguments.delta，只在 done 里给全量参数。
// 只认增量就会拿到空参数，工具被空参数调用——静默地错，所以钉住它。
func TestChatStreamTakesArgumentsFromDoneWhenNoDeltas(t *testing.T) {
	srv := sseServer(t, []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_9","name":"search_web"}}`,
		`{"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc_0","name":"search_web","arguments":"{\"q\":\"龙与虎\"}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call_9","name":"search_web","arguments":"{\"q\":\"龙与虎\"}"}}`,
		`{"type":"response.completed","response":{"status":"completed"}}`,
	})
	c := NewResponsesClient(Config{Endpoint: srv.URL, Model: "m", APIKey: "k"})
	var streamed strings.Builder
	res, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "查"}}, nil,
		func(d Delta) {
			if d.Kind == "tool" {
				streamed.WriteString(d.Text)
			}
		})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("toolCalls=%d", len(res.ToolCalls))
	}
	if got := res.ToolCalls[0].Function.Arguments; got != `{"q":"龙与虎"}` {
		t.Fatalf("done-only 参数丢了: %q", got)
	}
	// 流出去的增量与最终值必须一致（不能空、也不能把 done 的整份重复加一遍）
	if streamed.String() != `{"q":"龙与虎"}` {
		t.Fatalf("流出的参数=%q", streamed.String())
	}
}

// 增量给了前缀、done 给全量时，只该把缺的尾巴补出去——重复拼接会让参数成为非法 JSON。
func TestChatStreamDoneDoesNotDuplicateDeltaPrefix(t *testing.T) {
	srv := sseServer(t, []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"search_web"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"q\":"}`,
		`{"type":"response.function_call_arguments.done","output_index":0,"name":"search_web","arguments":"{\"q\":\"龙与虎\"}"}`,
		`{"type":"response.completed","response":{"status":"completed"}}`,
	})
	c := NewResponsesClient(Config{Endpoint: srv.URL, Model: "m", APIKey: "k"})
	var streamed strings.Builder
	res, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "查"}}, nil,
		func(d Delta) {
			if d.Kind == "tool" {
				streamed.WriteString(d.Text)
			}
		})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if got := res.ToolCalls[0].Function.Arguments; got != `{"q":"龙与虎"}` {
		t.Fatalf("参数被重复拼接: %q", got)
	}
	if got := streamed.String(); got != `{"q":"龙与虎"}` {
		t.Fatalf("流出的参数被重复拼接: %q", got)
	}
}

// 历史里存着一份没闭合的参数时，**绝不能原样发给网关**：Responses 网关会解析
// function_call.arguments，解析不了就整段 400——一次截断就让会话从此不可用
// （真实事故：write_content 参数被截断，之后每一轮请求都 400，工单直接失败）。
func TestBuildBodyNeverSendsMalformedToolArguments(t *testing.T) {
	c := NewResponsesClient(Config{Endpoint: "http://127.0.0.1:1", Model: "m"})
	msgs := []Message{
		{Role: "user", Content: "写《X》"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_bad", Type: "function", Function: FunctionCall{
				Name: "write_content", Arguments: `{"contentMd": "没闭合`,
			}},
			{ID: "call_ok", Type: "function", Function: FunctionCall{
				Name: "search_web", Arguments: `{"q":"正常"}`,
			}},
		}},
		{Role: "tool", ToolCallID: "call_bad", Name: "write_content", Content: `{"ok":false}`},
		{Role: "tool", ToolCallID: "call_ok", Name: "search_web", Content: `{"ok":true}`},
	}
	body := c.buildBody(msgs, nil, false)
	items, _ := body["input"].([]map[string]any)
	seen := 0
	for _, it := range items {
		if it["type"] != "function_call" {
			continue
		}
		seen++
		args, _ := it["arguments"].(string)
		if !json.Valid([]byte(args)) {
			t.Fatalf("%v 的非法参数被原样发了出去：%q", it["name"], args)
		}
	}
	if seen != 2 {
		t.Fatalf("function_call 项数 = %d，want 2（两个调用都要在，配对不能断）", seen)
	}
	// 合法的那个不许被动过
	for _, it := range items {
		if it["name"] == "search_web" && it["arguments"] != `{"q":"正常"}` {
			t.Fatalf("合法参数被改了：%v", it["arguments"])
		}
	}
}
