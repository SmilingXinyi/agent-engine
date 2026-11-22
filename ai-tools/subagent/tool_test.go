package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SmilingXinyi/agent-engine/agent"
	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

type testProvider func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error)

func (testProvider) Name() string { return "test" }
func (provider testProvider) Generate(ctx context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
	return provider(ctx, request)
}

func TestInputAndResult(t *testing.T) {
	requests := 0
	child := agent.New(ai.NewClient(testProvider(func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		requests++
		if request.Model != "child" || len(request.Messages) != 2 || request.Messages[0].Text() != "role" ||
			request.Messages[1].Text() != "Task:\ninspect\n\nContext:\nrepo" {
			t.Fatalf("unexpected child request: %+v", request)
		}
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, strings.Repeat("好", maxResultBytes)),
			Usage: aiprot.Usage{TotalTokens: 21}}, nil
	}), aiprot.Hooks{}))
	tool, err := NewTool(child, "subagent_test", "Inspect", agentprot.Request{
		Model: "child", SystemPrompt: "role", UserPrompt: "parent",
		History: []aiprot.Message{aiprot.TextMessage(aiprot.RoleUser, "private history")},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`null`, `[]`, `{}`, `{"task":null}`, `{"task":" "}`, `{"task":"x","context":null}`, `{"task":"x","context":1}`, `{"task":"x","tools":[]}`, `{"task":"x"}{}`, `{"task":"` + strings.Repeat("x", maxArgumentBytes) + `"}`} {
		result, err := tool.Execute(t.Context(), json.RawMessage(input))
		if err != nil || !result.IsError || !strings.Contains(result.Text(), "invalid_arguments") {
			t.Fatalf("invalid input accepted, length=%d: %+v, %v", len(input), result, err)
		}
	}
	result, err := tool.Execute(t.Context(), json.RawMessage(`{"task":"inspect","context":"repo"}`))
	if err != nil || requests != 1 || !result.IsError || !strings.HasPrefix(result.Text(), "Status: truncated\n") ||
		len(result.Text()) > maxResultBytes || !utf8.ValidString(result.Text()) {
		t.Fatalf("unexpected result: calls=%d bytes=%d valid=%t err=%v", requests, len(result.Text()), utf8.ValidString(result.Text()), err)
	}
	if result.Metadata["subagent_run_id"] == "" || result.Metadata["subagent_usage"].(map[string]any)["total_tokens"] != 21 {
		t.Fatalf("missing run metadata: %+v", result.Metadata)
	}
}

func TestRunStops(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		provider testProvider
	}{
		{"timeout", func(ctx context.Context, _ aiprot.ModelRequest) (aiprot.ModelResponse, error) {
			<-ctx.Done()
			return aiprot.ModelResponse{}, ctx.Err()
		}},
		{"max_steps", func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
			return aiprot.ModelResponse{Message: aiprot.Message{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{
				Type: aiprot.ContentToolCall, ToolCall: &aiprot.ToolCall{ID: "call", Name: "lookup", Arguments: json.RawMessage(`{}`)},
			}}}}, nil
		}},
		{"incomplete", func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
			return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "partial"), FinishReason: aiprot.FinishLength}, nil
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			timeout := time.Second
			if scenario.name == "timeout" {
				timeout = 15 * time.Millisecond
			}
			tool, err := NewTool(agent.New(ai.NewClient(scenario.provider, aiprot.Hooks{})), "subagent_test", "Inspect",
				agentprot.Request{Model: "test", Options: loopprot.RunOptions{MaxSteps: 1}}, timeout)
			if err != nil {
				t.Fatal(err)
			}
			result, err := tool.Execute(t.Context(), json.RawMessage(`{"task":"inspect"}`))
			if err != nil || !result.IsError || !strings.HasPrefix(result.Text(), "Status: "+scenario.name+"\n") {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}
