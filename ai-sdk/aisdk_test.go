package aisdk_test

import (
	"context"
	"encoding/json"
	"testing"

	aisdk "github.com/SmilingXinyi/agent-engine/ai-sdk"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/agent"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/memory"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/model"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/prompt"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/provider"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/schema"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/tool"
)

func TestGenerateAndStreamText(t *testing.T) {
	ctx := context.Background()
	mockProvider := provider.NewMockProvider()
	mockProvider.RegisterResponse("test-model", &model.GenerateResponse{
		Text: "Hello from test model",
	})

	m, err := mockProvider.Model("test-model")
	if err != nil {
		t.Fatalf("failed to get model: %v", err)
	}

	text, err := aisdk.GenerateText(ctx, m, "Hi")
	if err != nil {
		t.Fatalf("GenerateText failed: %v", err)
	}
	if text != "Hello from test model" {
		t.Errorf("expected 'Hello from test model', got %q", text)
	}

	var streamedText string
	_, err = aisdk.StreamText(ctx, m, "Hi", func(chunk string) error {
		streamedText += chunk
		return nil
	})
	if err != nil {
		t.Fatalf("StreamText failed: %v", err)
	}
	if streamedText != "Hello from test model" {
		t.Errorf("expected streamed 'Hello from test model', got %q", streamedText)
	}
}

type calculatorInput struct {
	A int `json:"a" description:"first number"`
	B int `json:"b" description:"second number"`
}

func TestAgentWithTool(t *testing.T) {
	ctx := context.Background()

	calcTool, err := aisdk.NewTool("add", "Add two numbers", calculatorInput{}, func(ctx context.Context, input json.RawMessage) (any, error) {
		var args calculatorInput
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return args.A + args.B, nil
	})
	if err != nil {
		t.Fatalf("NewTool failed: %v", err)
	}

	mockProvider := provider.NewMockProvider()
	mockProvider.RegisterResponse("agent-model", &model.GenerateResponse{
		ToolCalls: []model.ToolCall{
			{
				ID:        "call_1",
				Name:      "add",
				Arguments: `{"a": 2, "b": 3}`,
			},
		},
	})

	m, err := mockProvider.Model("agent-model")
	if err != nil {
		t.Fatalf("failed to get model: %v", err)
	}

	ag := aisdk.NewAgent(m,
		agent.WithTools(calcTool),
		agent.WithMaxSteps(2),
	)

	// Since mock returns same response, step 1 executes tool call, step 2 returns same tool call and reaches max steps.
	_, err = ag.Run(ctx, []model.Message{aisdk.UserMessage("Calculate 2 + 3")})
	if err == nil {
		t.Errorf("expected max execution steps reached error, got nil")
	}
}

func TestSchemaGeneration(t *testing.T) {
	raw, err := schema.GenerateJSONSchema(calculatorInput{})
	if err != nil {
		t.Fatalf("GenerateJSONSchema failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("failed to unmarshal schema: %v", err)
	}

	if parsed["type"] != "object" {
		t.Errorf("expected type object, got %v", parsed["type"])
	}
}

func TestPromptTemplateAndBuilder(t *testing.T) {
	tmpl, err := prompt.New("greeting", "Hello {{.Name}}!")
	if err != nil {
		t.Fatalf("prompt.New failed: %v", err)
	}

	out, err := tmpl.Format(map[string]string{"Name": "World"})
	if err != nil {
		t.Fatalf("tmpl.Format failed: %v", err)
	}
	if out != "Hello World!" {
		t.Errorf("expected 'Hello World!', got %q", out)
	}

	builder := prompt.NewBuilder()
	builder.AddSection("Role", "Assistant").AddSection("Rules", "Be concise")
	combined := builder.Build()
	if combined == "" {
		t.Errorf("expected non-empty prompt")
	}
}

func TestBufferMemory(t *testing.T) {
	mem := memory.NewBufferMemory(2)
	mem.Add(aisdk.UserMessage("msg 1"))
	mem.Add(aisdk.AssistantMessage("msg 2"))
	mem.Add(aisdk.UserMessage("msg 3"))

	msgs := mem.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "msg 2" || msgs[1].Content != "msg 3" {
		t.Errorf("unexpected messages in buffer: %+v", msgs)
	}
}

func TestToolRegistry(t *testing.T) {
	reg := tool.NewRegistry()
	t1 := tool.New("test", "test description", nil, nil)
	if err := reg.Register(t1); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	got, ok := reg.Get("test")
	if !ok || got.Definition().Name != "test" {
		t.Errorf("expected to find tool 'test'")
	}
}
