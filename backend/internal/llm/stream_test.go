package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 流式适配：SSE 增量要能拼回完整内容与 tool_calls，并把增量喂给回调。
func TestChatStreamAssemblesDeltas(t *testing.T) {
	frames := []string{
		`{"choices":[{"delta":{"reasoning_content":"想一想"}}]}`,
		`{"choices":[{"delta":{"content":"白狼"}}]}`,
		`{"choices":[{"delta":{"content":"崛起"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search_works","arguments":"{\"q\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"白狼崛起\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewOpenAIClient(Config{Endpoint: srv.URL, Model: "m", APIKey: "k"})
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
	if reasoning.String() != "想一想" {
		t.Fatalf("reasoning=%q", reasoning.String())
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
}

// provider 不支持 stream（返回普通 JSON）时不能让整条工单失败：执行器会回退 Chat。
func TestChatStreamPropagatesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"stream not supported"}}`)
	}))
	defer srv.Close()

	c := NewOpenAIClient(Config{Endpoint: srv.URL, Model: "m", APIKey: "k"})
	if _, err := c.ChatStream(context.Background(), nil, nil, nil); err == nil {
		t.Fatal("期望报错以便执行器回退 Chat")
	}
}
