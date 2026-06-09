package goai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
	"github.com/zendev-sh/goai/provider"
)

func TestToProviderMessages(t *testing.T) {
	messages, err := toProviderMessages([]agent.Message{
		agent.TextMessage{Role: agent.RoleSystem, Content: "system"},
		agent.TextMessage{Role: agent.RoleUser, Content: "hello"},
		agent.TextMessage{Role: agent.RoleAssistant, Content: "hi"},
		agent.ToolCallMessage{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "read_file", Input: json.RawMessage(`{"path":"a"}`)}}},
		agent.ToolResultMessage{ToolResult: agent.ToolResult{ToolCallID: "call-1", ToolName: "read_file", Output: "ok"}},
	})
	if err != nil {
		t.Fatalf("convert messages: %v", err)
	}
	if len(messages) != 4 {
		t.Fatalf("expected 4 provider messages, got %d", len(messages))
	}
	if messages[0].Role != provider.RoleUser || messages[0].Content[0].Text != "hello" {
		t.Fatalf("unexpected user message: %+v", messages[0])
	}
	if messages[1].Role != provider.RoleAssistant || messages[1].Content[0].Text != "hi" {
		t.Fatalf("unexpected assistant message: %+v", messages[1])
	}
	if messages[2].Role != provider.RoleAssistant || messages[2].Content[0].Type != provider.PartToolCall {
		t.Fatalf("unexpected tool call message: %+v", messages[2])
	}
	if messages[3].Role != provider.RoleTool || messages[3].Content[0].Type != provider.PartToolResult {
		t.Fatalf("unexpected tool result message: %+v", messages[3])
	}
}

func TestToProviderMessagesRejectsUnsupportedRole(t *testing.T) {
	_, err := toProviderMessages([]agent.Message{agent.TextMessage{Role: agent.RoleTool, Content: "bad"}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMarshalToolParameters(t *testing.T) {
	tests := []struct {
		name       string
		parameters any
		want       string
	}{
		{name: "nil", parameters: nil, want: `{"type":"object"}`},
		{name: "raw", parameters: json.RawMessage(`{"type":"object","properties":{}}`), want: `{"type":"object","properties":{}}`},
		{name: "bytes", parameters: []byte(`{"type":"object"}`), want: `{"type":"object"}`},
		{name: "string", parameters: `{"type":"object"}`, want: `{"type":"object"}`},
		{name: "map", parameters: map[string]any{"type": "object"}, want: `{"type":"object"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := marshalToolParameters(test.parameters)
			if err != nil {
				t.Fatalf("marshal parameters: %v", err)
			}
			if string(got) != test.want {
				t.Fatalf("expected %s, got %s", test.want, got)
			}
		})
	}
}

func TestMarshalToolParametersRejectsInvalidJSON(t *testing.T) {
	_, err := marshalToolParameters(`not-json`)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestToGoAIToolsExecutesOriginalHandler(t *testing.T) {
	wantErr := errors.New("failed")
	tools, err := toGoAITools([]agent.Tool{{
		Name:       "read_file",
		Parameters: nil,
		Handler: agent.ToolHandlerFunc(func(ctx context.Context, input json.RawMessage) (string, error) {
			if string(input) != `{"path":"a"}` {
				t.Fatalf("unexpected input: %s", input)
			}
			return "partial", wantErr
		}),
	}})
	if err != nil {
		t.Fatalf("convert tools: %v", err)
	}
	output, err := tools[0].Execute(context.Background(), json.RawMessage(`{"path":"a"}`))
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected handler error, got %v", err)
	}
	if output != "partial" {
		t.Fatalf("expected partial output, got %q", output)
	}
}
