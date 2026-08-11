package goai

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
	"github.com/zendev-sh/goai"
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
}

func TestToProviderMessagesSkipsToolRoleTextMessage(t *testing.T) {
	messages, err := toProviderMessages([]agent.Message{
		agent.TextMessage{Role: agent.RoleTool, Content: "bad"},
		agent.TextMessage{Role: agent.RoleUser, Content: "real"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 || messages[0].Content[0].Text != "real" {
		t.Fatalf("expected only real user message, got %+v", messages)
	}
}

func TestToProviderMessagesRejectsUnknownRole(t *testing.T) {
	_, err := toProviderMessages([]agent.Message{agent.TextMessage{Role: agent.Role("unknown"), Content: "bad"}})
	if err == nil {
		t.Fatal("expected error for unknown role")
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

func TestMarshalToolParametersRejectsNull(t *testing.T) {
	_, err := marshalToolParameters(`null`)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestToGoAIToolsExecutesOriginalHandler(t *testing.T) {
	wantErr := errors.New("failed")
	tools, err := toGoAITools(context.Background(), []agent.Tool{{
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

func TestToGoAIToolsInjectsEnvFromContext(t *testing.T) {
	var capturedInput json.RawMessage
	tools, err := toGoAITools(context.Background(), []agent.Tool{{
		Name: "search",
		Handler: agent.ToolHandlerFunc(func(ctx context.Context, input json.RawMessage) (string, error) {
			capturedInput = input
			return "ok", nil
		}),
	}})
	if err != nil {
		t.Fatalf("convert tools: %v", err)
	}
	ctx := agent.WithToolEnv(context.Background(), map[string]map[string]any{"search": {"user_id": "u999"}})
	if _, err := tools[0].Execute(ctx, json.RawMessage(`{"query":"test"}`)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(capturedInput, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["user_id"] != "u999" || got["query"] != "test" {
		t.Fatalf("unexpected injected params: %v", got)
	}
}

type providerOptionsModel struct {
	params         provider.GenerateParams
	generateResult *provider.GenerateResult
	generate       func(context.Context, provider.GenerateParams) (*provider.GenerateResult, error)
	streamChunks   []provider.StreamChunk
}

type debugSinkFunc func(string, int, []agent.DebugMessage) error

func (sink debugSinkFunc) DumpRequest(model string, toolCount int, messages []agent.DebugMessage) error {
	return sink(model, toolCount, messages)
}

func (model *providerOptionsModel) ModelID() string { return "provider-options-model" }

func (model *providerOptionsModel) DoGenerate(ctx context.Context, params provider.GenerateParams) (*provider.GenerateResult, error) {
	model.params = params
	if model.generate != nil {
		return model.generate(ctx, params)
	}
	if model.generateResult != nil {
		return model.generateResult, nil
	}
	return &provider.GenerateResult{Text: "ok"}, nil
}

func (model *providerOptionsModel) DoStream(ctx context.Context, params provider.GenerateParams) (*provider.StreamResult, error) {
	_ = ctx
	model.params = params
	stream := make(chan provider.StreamChunk, len(model.streamChunks))
	for _, chunk := range model.streamChunks {
		stream <- chunk
	}
	close(stream)
	return &provider.StreamResult{Stream: stream}, nil
}

func TestGoAIClientExposesOnBeforeToolExecute(t *testing.T) {
	var callCount int
	var hookCalled bool
	var toolExecuted bool
	model := &providerOptionsModel{
		generate: func(_ context.Context, params provider.GenerateParams) (*provider.GenerateResult, error) {
			callCount++
			if callCount == 1 {
				return &provider.GenerateResult{
					ToolCalls:    []provider.ToolCall{{ID: "call-1", Name: "shell", Input: json.RawMessage(`{"command":"unsafe"}`)}},
					FinishReason: provider.FinishToolCalls,
				}, nil
			}
			for _, message := range params.Messages {
				for _, part := range message.Content {
					if part.Type == provider.PartToolResult && part.ToolOutput == "blocked by policy" {
						return &provider.GenerateResult{Text: "blocked", FinishReason: provider.FinishStop}, nil
					}
				}
			}
			return &provider.GenerateResult{Text: "missing hook result", FinishReason: provider.FinishStop}, nil
		},
	}
	client := &goaiClient{model: model}

	response, err := client.GenerateText(context.Background(), agent.ModelRequest{
		UserPrompt: "run a command",
		Tools: []agent.Tool{{
			Name: "shell",
			Handler: agent.ToolHandlerFunc(func(context.Context, json.RawMessage) (string, error) {
				toolExecuted = true
				return "executed", nil
			}),
		}},
		Options: agent.RunOptions{
			MaxSteps: 3,
			OnBeforeToolExecute: func(info agent.BeforeToolExecuteInfo) agent.BeforeToolExecuteResult {
				hookCalled = true
				return agent.BeforeToolExecuteResult{Skip: true, Result: "blocked by policy"}
			},
		},
	})
	if err != nil {
		t.Fatalf("generate text: %v", err)
	}
	if !hookCalled || toolExecuted || response.Text != "blocked" {
		t.Fatalf("hookCalled=%v toolExecuted=%v text=%q", hookCalled, toolExecuted, response.Text)
	}
}

func TestGoAIClientReturnsEventHandlerPanics(t *testing.T) {
	callCount := 0
	toolExecuted := false
	model := &providerOptionsModel{
		generate: func(context.Context, provider.GenerateParams) (*provider.GenerateResult, error) {
			callCount++
			if callCount == 1 {
				return &provider.GenerateResult{
					ToolCalls:    []provider.ToolCall{{ID: "call-1", Name: "read_file", Input: json.RawMessage(`{}`)}},
					FinishReason: provider.FinishToolCalls,
				}, nil
			}
			return &provider.GenerateResult{Text: "done", FinishReason: provider.FinishStop}, nil
		},
	}
	client := &goaiClient{model: model}

	response, err := client.GenerateText(context.Background(), agent.ModelRequest{
		UserPrompt: "read",
		Tools: []agent.Tool{{
			Name: "read_file",
			Handler: agent.ToolHandlerFunc(func(context.Context, json.RawMessage) (string, error) {
				toolExecuted = true
				return "content", nil
			}),
		}},
		Options: agent.RunOptions{
			MaxSteps: 3,
			EventHandler: agent.EventHandlerFunc{
				ToolCallStart: func(context.Context, agent.ToolCallStartEvent) error {
					panic("start persistence panic")
				},
				ToolCallFinish: func(context.Context, agent.ToolCallFinishEvent) error {
					panic(errors.New("finish persistence panic"))
				},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "start persistence panic") || !strings.Contains(err.Error(), "finish persistence panic") {
		t.Fatalf("expected both handler panic errors, got %v", err)
	}
	if !toolExecuted || response.Text != "done" || len(response.ToolCalls) != 1 {
		t.Fatalf("unexpected response: toolExecuted=%v %+v", toolExecuted, response)
	}
}

func TestGoAIClientReturnsThinkingFromGenerateResult(t *testing.T) {
	model := &providerOptionsModel{generateResult: &provider.GenerateResult{Text: "answer", Reasoning: "thinking"}}
	client := &goaiClient{model: model}

	response, err := client.GenerateText(context.Background(), agent.ModelRequest{
		UserPrompt: "hello",
		Options:    agent.RunOptions{MaxSteps: 1},
	})
	if err != nil {
		t.Fatalf("generate text: %v", err)
	}
	if response.Text != "answer" || response.Thinking != "thinking" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestGoAIClientReturnsSeparateTextAndThinkingFromStream(t *testing.T) {
	model := &providerOptionsModel{streamChunks: []provider.StreamChunk{
		{Type: provider.ChunkReasoning, Text: "thinking"},
		{Type: provider.ChunkText, Text: "answer"},
		{Type: provider.ChunkFinish},
	}}
	client := &goaiClient{model: model}
	var chunks string

	response, err := client.StreamText(context.Background(), agent.ModelRequest{
		UserPrompt: "hello",
		Options:    agent.RunOptions{MaxSteps: 1},
	}, agent.StreamHandlerFunc(func(ctx context.Context, chunk string) error {
		_ = ctx
		chunks += chunk
		return nil
	}))
	if err != nil {
		t.Fatalf("stream text: %v", err)
	}
	if chunks != "answer" || response.Text != "answer" || response.Thinking != "thinking" {
		t.Fatalf("unexpected stream result chunks=%q response=%+v", chunks, response)
	}
}

func TestGoAIClientDisablesThinkingProviderOption(t *testing.T) {
	model := &providerOptionsModel{}
	client := &goaiClient{
		model: model,
		baseOptions: []goai.Option{goai.WithProviderOptions(map[string]any{
			"thinking": map[string]any{"type": "disabled"},
		})},
	}

	_, err := client.GenerateText(context.Background(), agent.ModelRequest{
		UserPrompt: "hello",
		Options:    agent.RunOptions{MaxSteps: 1},
	})
	if err != nil {
		t.Fatalf("generate text: %v", err)
	}
	thinking, ok := model.params.ProviderOptions["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("expected disabled thinking, got %#v", model.params.ProviderOptions)
	}
}

func TestGoAIClientLimitsMessagesPassedToProvider(t *testing.T) {
	model := &providerOptionsModel{}
	client := &goaiClient{model: model}

	_, err := client.GenerateText(context.Background(), agent.ModelRequest{
		Messages: []agent.Message{
			agent.TextMessage{Role: agent.RoleUser, Content: "old"},
			agent.TextMessage{Role: agent.RoleAssistant, Content: "previous"},
		},
		UserPrompt:  "current",
		MaxMessages: 2,
		Options:     agent.RunOptions{MaxSteps: 1},
	})
	if err != nil {
		t.Fatalf("generate text: %v", err)
	}
	if len(model.params.Messages) != 3 {
		t.Fatalf("expected 2 history messages plus current prompt, got %+v", model.params.Messages)
	}
}

func TestLimitProviderMessagesPreservesToolRound(t *testing.T) {
	messages := []provider.Message{
		goai.UserMessage("old"),
		{Role: provider.RoleAssistant, Content: []provider.Part{{Type: provider.PartToolCall, ToolCallID: "call-1", ToolName: "search"}}},
		goai.ToolMessage("call-1", "search", "result"),
		goai.AssistantMessage("done"),
	}
	limited := limitProviderMessages(messages, 2)
	if len(limited) != 3 {
		t.Fatalf("expected complete tool round plus newest message, got %+v", limited)
	}
}

func TestValidateProviderToolSequenceRejectsCallFollowedByUser(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.RoleAssistant, Content: []provider.Part{{Type: provider.PartToolCall, ToolCallID: "ask-1", ToolName: "ask_user"}}},
		goai.UserMessage("answer"),
	}
	err := validateProviderToolSequence(messages)
	if err == nil || !strings.Contains(err.Error(), "must be followed by tool results") {
		t.Fatalf("expected missing result error, got %v", err)
	}
}

func TestGoAIClientAutomaticallyStopsExternalTools(t *testing.T) {
	callCount := 0
	model := &providerOptionsModel{
		generate: func(context.Context, provider.GenerateParams) (*provider.GenerateResult, error) {
			callCount++
			return &provider.GenerateResult{
				ToolCalls:    []provider.ToolCall{{ID: "call-1", Name: "external", Input: json.RawMessage(`{}`)}},
				FinishReason: provider.FinishToolCalls,
			}, nil
		},
	}
	client := &goaiClient{model: model}

	response, err := client.GenerateText(context.Background(), agent.ModelRequest{
		UserPrompt: "call external",
		Tools:      []agent.Tool{{Name: "external"}},
		Options:    agent.RunOptions{MaxSteps: 3},
	})
	if err != nil {
		t.Fatalf("generate text: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected external tool to stop before another model call, got %d calls", callCount)
	}
	if len(response.Messages) != 2 {
		t.Fatalf("expected replayable tool call and marker result, got %+v", response.Messages)
	}
}

type cancelAwareStreamModel struct {
	canceled chan struct{}
}

func (model *cancelAwareStreamModel) ModelID() string { return "cancel-aware" }

func (model *cancelAwareStreamModel) DoGenerate(context.Context, provider.GenerateParams) (*provider.GenerateResult, error) {
	return &provider.GenerateResult{}, nil
}

func (model *cancelAwareStreamModel) DoStream(ctx context.Context, _ provider.GenerateParams) (*provider.StreamResult, error) {
	stream := make(chan provider.StreamChunk)
	go func() {
		defer close(stream)
		select {
		case stream <- provider.StreamChunk{Type: provider.ChunkText, Text: "rejected"}:
		case <-ctx.Done():
			close(model.canceled)
			return
		}
		<-ctx.Done()
		close(model.canceled)
	}()
	return &provider.StreamResult{Stream: stream}, nil
}

func TestGoAIClientCancelsStreamOnHandlerError(t *testing.T) {
	wantErr := errors.New("consumer failed")
	model := &cancelAwareStreamModel{canceled: make(chan struct{})}
	client := &goaiClient{model: model}

	response, err := client.StreamText(context.Background(), agent.ModelRequest{
		UserPrompt: "hello",
		Options:    agent.RunOptions{MaxSteps: 1},
	}, agent.StreamHandlerFunc(func(context.Context, string) error {
		return wantErr
	}))
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected handler error, got %v", err)
	}
	if response.Text != "" {
		t.Fatalf("rejected chunk must not be included in partial response: %+v", response)
	}
	select {
	case <-model.canceled:
	case <-time.After(time.Second):
		t.Fatal("provider stream was not canceled")
	}
}

func TestYAMLFileSinkReturnsDirectoryError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := (YAMLFileSink{Dir: filepath.Join(blocker, "requests")}).DumpRequest("model", 0, nil)
	if err == nil || !strings.Contains(err.Error(), "create debug request directory") {
		t.Fatalf("expected directory error, got %v", err)
	}
}

func TestGoAIToolReturnsContextCancellation(t *testing.T) {
	goaiTools, err := toGoAITools(context.Background(), []agent.Tool{{
		Name: "slow_tool",
		Handler: agent.ToolHandlerFunc(func(ctx context.Context, input json.RawMessage) (string, error) {
			_ = input
			<-ctx.Done()
			return "", ctx.Err()
		}),
	}})
	if err != nil {
		t.Fatalf("convert tools: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = goaiTools[0].Execute(ctx, json.RawMessage(`{}`))
	if !agent.IsExecutionCanceled(err) {
		t.Fatalf("expected tool cancellation, got %v", err)
	}
}

func float64Ptr(f float64) *float64 { return &f }
