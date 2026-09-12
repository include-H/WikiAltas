package llm

import (
	"strings"
	"testing"
)

func TestConfigFromEnvBaseURLOverridesEndpoint(t *testing.T) {
	t.Setenv("WIKIATLAS_LLM_API_KEY", "k")
	t.Setenv("WIKIATLAS_LLM_BASE_URL", "https://example.com/v1")
	t.Setenv("WIKIATLAS_LLM_ENDPOINT", "https://legacy.example/v1")
	t.Setenv("WIKIATLAS_LLM_MODEL", "m1")
	cfg, ok := ConfigFromEnv()
	if !ok {
		t.Fatal("expected ok")
	}
	if cfg.Endpoint != "https://example.com/v1" {
		t.Fatalf("endpoint=%s", cfg.Endpoint)
	}
	if cfg.Model != "m1" {
		t.Fatalf("model=%s", cfg.Model)
	}
}

func TestConfigFromEnvLegacyEndpoint(t *testing.T) {
	t.Setenv("WIKIATLAS_LLM_API_KEY", "k")
	t.Setenv("WIKIATLAS_LLM_BASE_URL", "")
	t.Setenv("WIKIATLAS_LLM_ENDPOINT", "https://legacy.example/v1")
	t.Setenv("WIKIATLAS_LLM_MODEL", "")
	cfg, ok := ConfigFromEnv()
	if !ok {
		t.Fatal("expected ok")
	}
	if cfg.Endpoint != "https://legacy.example/v1" {
		t.Fatalf("endpoint=%s", cfg.Endpoint)
	}
	if cfg.Model != "gpt-4o-mini" {
		t.Fatalf("default model=%s", cfg.Model)
	}
}

func TestConfigFromEnvMissingKey(t *testing.T) {
	t.Setenv("WIKIATLAS_LLM_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	if _, ok := ConfigFromEnv(); ok {
		t.Fatal("expected not configured")
	}
}

func TestIsEcho(t *testing.T) {
	if !IsEcho(nil) {
		t.Fatal("nil should be echo")
	}
	if !IsEcho(EchoClient{}) {
		t.Fatal("EchoClient should be echo")
	}
	c, err := NewClient(Config{APIKey: "x", Model: "m", Endpoint: "http://x"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if IsEcho(c) {
		t.Fatal("Responses client should not be echo")
	}
}

// 协议分派：空 = 默认 responses；认不出的协议要明确报错，不静默退回默认。
func TestNewClientProtocolDispatch(t *testing.T) {
	for _, p := range []string{"", "responses", "Responses"} {
		c, err := NewClient(Config{APIKey: "x", Model: "m", Protocol: p})
		if err != nil {
			t.Fatalf("protocol %q should be accepted: %v", p, err)
		}
		if _, ok := c.(*ResponsesClient); !ok {
			t.Fatalf("protocol %q built %T", p, c)
		}
	}
	if _, err := NewClient(Config{APIKey: "x", Protocol: "chat"}); err == nil {
		t.Fatal("不认识的协议必须报错，不能静默退回默认")
	} else if !strings.Contains(err.Error(), "chat") {
		t.Fatalf("错误里应带上那个不认识的协议：%v", err)
	}
}

func TestNewFunctionToolSchema(t *testing.T) {
	def := NewFunctionTool("narrative", "desc", ObjectSchema(map[string]any{
		"text": map[string]any{"type": "string", "description": "line"},
	}, []string{"text"}))
	if def.Type != "function" || def.Function.Name != "narrative" {
		t.Fatalf("def=%+v", def)
	}
	if len(def.Function.Parameters) == 0 {
		t.Fatal("missing parameters")
	}
}
