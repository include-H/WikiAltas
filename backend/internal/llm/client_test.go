package llm

import (
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
	c := NewOpenAIClient(Config{APIKey: "x", Model: "m", Endpoint: "http://x"})
	if IsEcho(c) {
		t.Fatal("OpenAI client should not be echo")
	}
}

func TestNewFunctionToolSchema(t *testing.T) {
	def := NewFunctionTool("narrative", "desc", ObjectSchema(map[string]any{
		"text": StrProp("line"),
	}, []string{"text"}))
	if def.Type != "function" || def.Function.Name != "narrative" {
		t.Fatalf("def=%+v", def)
	}
	if len(def.Function.Parameters) == 0 {
		t.Fatal("missing parameters")
	}
}
