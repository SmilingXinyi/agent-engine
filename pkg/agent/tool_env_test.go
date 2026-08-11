package agent

import (
	"context"
	"testing"
)

func TestApplyToolEnvWildcard(t *testing.T) {
	env := map[string]map[string]any{
		"*": {"user_id": "u123"},
	}
	params := map[string]any{"query": "hello"}
	ApplyToolEnv("any_tool", params, env)
	if params["user_id"] != "u123" {
		t.Fatalf("expected user_id injected, got %v", params["user_id"])
	}
	if params["query"] != "hello" {
		t.Fatal("model param overwritten unexpectedly")
	}
}

func TestApplyToolEnvToolSpecificFillsAfterWildcard(t *testing.T) {
	env := map[string]map[string]any{
		"*":         {"workspace": "/default", "timeout": 30},
		"read_file": {"workspace": "/specific", "encoding": "utf-8"},
	}
	params := map[string]any{}
	ApplyToolEnv("read_file", params, env)
	if params["timeout"] != 30 {
		t.Fatalf("expected wildcard timeout=30, got %v", params["timeout"])
	}
	if params["workspace"] != "/default" {
		t.Fatalf("expected wildcard workspace=/default (set first), got %v", params["workspace"])
	}
	if params["encoding"] != "utf-8" {
		t.Fatalf("expected tool-specific encoding=utf-8, got %v", params["encoding"])
	}
}

func TestApplyToolEnvDoesNotOverrideModelParams(t *testing.T) {
	env := map[string]map[string]any{
		"my_tool": {"mobile": "13810320124", "timeout": 30},
	}
	params := map[string]any{"mobile": "model_value", "query": "hello"}
	ApplyToolEnv("my_tool", params, env)
	if params["mobile"] != "model_value" {
		t.Fatalf("expected model-supplied mobile preserved, got %v", params["mobile"])
	}
	if params["timeout"] != 30 {
		t.Fatalf("expected env-injected timeout=30, got %v", params["timeout"])
	}
}

func TestWithToolEnvRoundtrip(t *testing.T) {
	env := map[string]map[string]any{
		"*": {"k": "v"},
	}
	ctx := WithToolEnv(context.Background(), env)
	got := ToolEnvFromContext(ctx)
	if got["*"]["k"] != "v" {
		t.Fatalf("env not preserved in context: %v", got)
	}
}

func TestWithToolEnvCopiesInputMaps(t *testing.T) {
	env := map[string]map[string]any{"*": {"k": "original"}}
	ctx := WithToolEnv(context.Background(), env)
	env["*"]["k"] = "changed"

	if got := ToolEnvFromContext(ctx)["*"]["k"]; got != "original" {
		t.Fatalf("expected context snapshot, got %v", got)
	}
}

func TestWithToolEnvEmptyIsNoop(t *testing.T) {
	ctx := WithToolEnv(context.Background(), nil)
	if got := ToolEnvFromContext(ctx); got != nil {
		t.Fatalf("expected nil env from empty WithToolEnv, got %v", got)
	}
}

func TestHasToolEnv(t *testing.T) {
	env := map[string]map[string]any{"search": {"user_id": "u1"}}
	if !HasToolEnv("search", env) {
		t.Fatal("expected HasToolEnv true for matching tool")
	}
	if HasToolEnv("other", env) {
		t.Fatal("expected HasToolEnv false for unrelated tool")
	}
}
