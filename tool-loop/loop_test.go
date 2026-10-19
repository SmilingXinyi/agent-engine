package toolloop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loop "github.com/SmilingXinyi/agent-engine/tool-loop"
	"github.com/SmilingXinyi/agent-engine/tool-loop/protocol"
)

type scriptedProvider struct {
	generate func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error)
}

func (provider scriptedProvider) Name() string { return "scripted" }
func (provider scriptedProvider) Generate(ctx context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
	return provider.generate(ctx, request)
}

func initialRequest() protocol.Request {
	return protocol.Request{Model: "model", Messages: []aiprot.Message{aiprot.TextMessage(aiprot.RoleUser, "hello")}}
}

func call(name string) aiprot.ToolCall {
	return aiprot.ToolCall{ID: name + "-id", Name: name, Arguments: json.RawMessage(`{}`)}
}

func toolResponse(calls ...aiprot.ToolCall) aiprot.ModelResponse {
	message := aiprot.Message{Role: aiprot.RoleAssistant}
	for _, call := range calls {
		message.Parts = append(message.Parts, aiprot.ContentPart{Type: aiprot.ContentToolCall, ToolCall: &call})
	}
	return aiprot.ModelResponse{Message: message, FinishReason: aiprot.FinishToolCalls}
}

func textResponse(text string) aiprot.ModelResponse {
	return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, text), FinishReason: aiprot.FinishStop}
}

func TestRunSequentialLifecycleRecordsAndUsage(t *testing.T) {
	var lifecycle []string
	var sdkRequests []aiprot.RequestInfo
	first := toolResponse(call("first"), call("second"))
	first.Usage = aiprot.Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5, ReasoningTokens: 1, CacheReadTokens: 1, CacheWriteTokens: 2}
	last := textResponse("final")
	last.Usage = aiprot.Usage{InputTokens: 7, OutputTokens: 2, TotalTokens: 9, ReasoningTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4}
	modelCalls := 0
	provider := scriptedProvider{generate: func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		modelCalls++
		lifecycle = append(lifecycle, "model")
		if modelCalls == 1 {
			return first, nil
		}
		if modelCalls != 2 || len(request.Messages) != 4 {
			t.Fatalf("request=%#v", request)
		}
		for index, name := range []string{"first", "second"} {
			result := request.Messages[index+2].ToolResults()[0]
			if result.ToolName != name || result.ToolCallID != name+"-id" || result.Text() != name {
				t.Fatalf("tool result=%#v", result)
			}
		}
		return last, nil
	}}
	client := ai.NewClient(provider, aiprot.Hooks{
		OnRequest: []aiprot.RequestHook{func(_ context.Context, info aiprot.RequestInfo) error {
			sdkRequests = append(sdkRequests, info)
			return nil
		}},
	})
	request := initialRequest()
	for _, name := range []string{"first", "second"} {
		request.Tools = append(request.Tools, protocol.Tool{
			Definition: aiprot.ToolDefinition{Name: name},
			Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
				lifecycle = append(lifecycle, name)
				return aiprot.TextToolResult(name), nil
			},
		})
	}
	request.Options.Hooks = protocol.Hooks{
		OnRunStart: func(context.Context, protocol.RunStartInfo) error {
			lifecycle = append(lifecycle, "run start")
			return nil
		},
		OnRunEnd: func(_ context.Context, info protocol.RunEndInfo) error {
			lifecycle = append(lifecycle, "run end")
			if info.Result.StopReason != protocol.StopNatural || info.Error != nil || len(info.Result.Steps) != 2 {
				t.Fatalf("run end=%#v", info)
			}
			return nil
		},
		OnStepStart: func(context.Context, protocol.StepStartInfo) error {
			lifecycle = append(lifecycle, "step start")
			return nil
		},
		OnStepEnd: func(_ context.Context, info protocol.StepEndInfo) error {
			lifecycle = append(lifecycle, "step end")
			if info.RequestID != sdkRequests[info.Step-1].RequestID || info.FinishedAt.Before(info.StartedAt) {
				t.Fatalf("step=%#v", info)
			}
			return nil
		},
		OnToolStart: func(context.Context, protocol.ToolStartInfo) error {
			lifecycle = append(lifecycle, "tool start")
			return nil
		},
		OnToolEnd: func(_ context.Context, info protocol.ToolEndInfo) error {
			lifecycle = append(lifecycle, "tool end")
			if info.RequestID != sdkRequests[0].RequestID || info.Result.ToolCallID != info.Call.ID {
				t.Fatal("tool correlation lost")
			}
			return nil
		},
	}
	result, err := loop.New(client).Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	wantLifecycle := []string{"run start", "step start", "model", "tool start", "first", "tool end", "tool start", "second", "tool end", "step end", "step start", "model", "step end", "run end"}
	if !reflect.DeepEqual(lifecycle, wantLifecycle) {
		t.Fatalf("lifecycle=%v", lifecycle)
	}
	wantUsage := aiprot.Usage{InputTokens: 9, OutputTokens: 5, TotalTokens: 14, ReasoningTokens: 3, CacheReadTokens: 4, CacheWriteTokens: 6}
	if result.Usage != wantUsage || !reflect.DeepEqual(result.LastResponse, last) || !reflect.DeepEqual(result.Steps[0].Response, first) || len(result.Messages) != 5 || len(result.ToolCalls) != 2 {
		t.Fatalf("result=%#v", result)
	}
	if result.RunID == "" || sdkRequests[0].RequestID == sdkRequests[1].RequestID {
		t.Fatal("ids missing or reused")
	}
	for index, info := range sdkRequests {
		if info.Request.Metadata["run_id"] != result.RunID || info.Request.Metadata["step"] != fmt.Sprint(index+1) {
			t.Fatalf("metadata=%v", info.Request.Metadata)
		}
	}
	for _, record := range result.ToolCalls {
		if record.Step != 1 || record.StartedAt.IsZero() || record.FinishedAt.Before(record.StartedAt) {
			t.Fatalf("record=%#v", record)
		}
	}
}

func TestRunFeedsOrdinaryToolFailuresToModel(t *testing.T) {
	for _, mode := range []string{"unknown", "error", "panic", "isError"} {
		t.Run(mode, func(t *testing.T) {
			request := initialRequest()
			name := "test"
			if mode == "unknown" {
				name = "unknown"
			}
			request.Tools = []protocol.Tool{{Definition: aiprot.ToolDefinition{Name: "test"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
				switch mode {
				case "unknown":
					t.Fatal("unknown tool should not invoke executor")
				case "error":
					return aiprot.TextToolResult("partial"), errors.New("executor failed")
				case "panic":
					panic("executor panic")
				}
				result := aiprot.TextToolResult("tool failure")
				result.IsError = true
				return result, nil
			}}}
			modelCalls := 0
			provider := scriptedProvider{generate: func(_ context.Context, modelRequest aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				modelCalls++
				if modelCalls == 1 {
					return toolResponse(call(name)), nil
				}
				result := modelRequest.Messages[2].ToolResults()[0]
				if !result.IsError || result.Text() == "" || result.ToolCallID != name+"-id" {
					t.Fatalf("result=%#v", result)
				}
				return textResponse("recovered"), nil
			}}
			result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), request)
			if err != nil || result.StopReason != protocol.StopNatural || modelCalls != 2 || len(result.ToolCalls) != 1 {
				t.Fatalf("result=%#v error=%v", result, err)
			}
			if mode != "isError" && result.ToolCalls[0].Error == "" {
				t.Fatal("execution error was not recorded")
			}
		})
	}
}

func TestMalformedToolBatchHasNoSideEffects(t *testing.T) {
	badArguments := []json.RawMessage{nil, json.RawMessage(`{`), json.RawMessage(`null`), json.RawMessage(`[]`)}
	responses := []aiprot.ModelResponse{
		toolResponse(call("test"), call("test")),
		toolResponse(call("test"), aiprot.ToolCall{Name: "test", Arguments: json.RawMessage(`{}`)}),
		{Message: aiprot.Message{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall}}}},
	}
	for _, arguments := range badArguments {
		invalid := call("test")
		invalid.ID = "invalid-id"
		invalid.Arguments = arguments
		responses = append(responses, toolResponse(call("test"), invalid))
	}
	for _, response := range responses {
		request := initialRequest()
		request.Tools = []protocol.Tool{{Definition: aiprot.ToolDefinition{Name: "test"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
			t.Fatal("malformed batch executed a tool")
			return aiprot.ToolResult{}, nil
		}}}
		provider := scriptedProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) { return response, nil }}
		result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), request)
		if err == nil || result.StopReason != protocol.StopError || len(result.ToolCalls) != 0 || len(result.Steps) != 1 {
			t.Fatalf("result=%#v error=%v", result, err)
		}
	}
}

func TestMaxStepsStopsBeforeLastToolBatch(t *testing.T) {
	for _, limit := range []int{0, 1, 2} {
		request := initialRequest()
		request.Options.MaxSteps = limit
		executions := 0
		request.Tools = []protocol.Tool{{Definition: aiprot.ToolDefinition{Name: "test"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
			executions++
			return aiprot.TextToolResult("done"), nil
		}}}
		provider := scriptedProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
			response := toolResponse(call("test"))
			response.Usage.TotalTokens = 3
			return response, nil
		}}
		result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), request)
		if limit == 0 {
			limit = protocol.DefaultMaxSteps
		}
		if err != nil || result.StopReason != protocol.StopMaxSteps || len(result.Steps) != limit || executions != limit-1 || result.Usage.TotalTokens != limit*3 || result.LastResponse.Usage.TotalTokens != 3 {
			t.Fatalf("result=%#v error=%v executions=%d", result, err, executions)
		}
	}
}

func TestProviderFailureAndEndHookErrorsAreJoined(t *testing.T) {
	providerErr, stepEndErr, runEndErr := errors.New("provider"), errors.New("step end"), errors.New("run end")
	request := initialRequest()
	request.Options.Hooks.OnStepEnd = func(_ context.Context, info protocol.StepEndInfo) error {
		if !errors.Is(info.Error, providerErr) {
			t.Fatalf("observed error=%v", info.Error)
		}
		return stepEndErr
	}
	request.Options.Hooks.OnRunEnd = func(_ context.Context, info protocol.RunEndInfo) error {
		if !errors.Is(info.Error, providerErr) || !errors.Is(info.Error, stepEndErr) {
			t.Fatalf("observed error=%v", info.Error)
		}
		return runEndErr
	}
	provider := scriptedProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		response := textResponse("partial")
		response.Usage.TotalTokens = 4
		return response, providerErr
	}}
	result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), request)
	if !errors.Is(err, providerErr) || !errors.Is(err, stepEndErr) || !errors.Is(err, runEndErr) || result.StopReason != protocol.StopError || result.LastResponse.Message.Text() != "partial" || result.Usage.TotalTokens != 4 || len(result.Messages) != 2 {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestInvalidRequestsDoNotEnterLifecycle(t *testing.T) {
	requests := []protocol.Request{{}, {Model: "model"}, {Model: " ", Messages: initialRequest().Messages}}
	negative := initialRequest()
	negative.Options.MaxSteps = -1
	requests = append(requests, negative)
	for _, tools := range [][]protocol.Tool{
		{{Definition: aiprot.ToolDefinition{Name: "test"}}},
		{{Definition: aiprot.ToolDefinition{Name: " ", InputSchema: json.RawMessage(`{}`)}, Execute: noopTool}},
		{{Definition: aiprot.ToolDefinition{Name: "test", InputSchema: json.RawMessage(`null`)}, Execute: noopTool}},
		{{Definition: aiprot.ToolDefinition{Name: "test", InputSchema: json.RawMessage(`{"type":"string"}`)}, Execute: noopTool}},
		{{Definition: aiprot.ToolDefinition{Name: "test"}, Execute: noopTool}, {Definition: aiprot.ToolDefinition{Name: " test "}, Execute: noopTool}},
	} {
		request := initialRequest()
		request.Tools = tools
		requests = append(requests, request)
	}
	provider := scriptedProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		t.Fatal("invalid request reached provider")
		return aiprot.ModelResponse{}, nil
	}}
	for _, request := range requests {
		request.Options.Hooks.OnRunStart = func(context.Context, protocol.RunStartInfo) error {
			t.Fatal("invalid request entered lifecycle")
			return nil
		}
		if result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), request); err == nil || result.StopReason != protocol.StopError {
			t.Fatalf("result=%#v error=%v", result, err)
		}
	}
	for _, missing := range []*loop.Loop{nil, loop.New(nil)} {
		if _, err := missing.Run(context.Background(), initialRequest()); err == nil {
			t.Fatal("missing client should fail")
		}
	}
	empty := scriptedProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		return aiprot.ModelResponse{}, nil
	}}
	if _, err := loop.New(ai.NewClient(empty, aiprot.Hooks{})).Run(context.Background(), initialRequest()); err == nil || !strings.Contains(err.Error(), "no content") {
		t.Fatalf("error=%v", err)
	}
}

func noopTool(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
	return aiprot.TextToolResult("done"), nil
}

func TestRunForwardsGenerationSettingsOnEveryStep(t *testing.T) {
	temperature := 0.2
	maxTokens := 32
	message := aiprot.TextMessage(aiprot.RoleDeveloper, "rules")
	message.ProviderOptions = map[string]any{"vendor_extension": map[string]any{"enabled": true}}
	request := initialRequest()
	request.Messages = []aiprot.Message{message}
	request.Temperature = &temperature
	request.MaxOutputTokens = &maxTokens
	request.ToolChoice = aiprot.ToolChoiceRequired
	request.Reasoning = &aiprot.ReasoningOptions{Effort: "low"}
	request.OutputFormat = &aiprot.OutputFormat{Type: aiprot.OutputJSONSchema, Name: "answer", Schema: json.RawMessage(`{"type":"object"}`), Strict: true}
	request.ProviderOptions = map[string]any{"thinking": map[string]any{"type": "disabled"}}
	request.Metadata = map[string]string{"trace": "abc", protocol.MetadataRunID: "caller", aiprot.RequestIDMetadataKey: "caller-request", protocol.MetadataStep: "9"}
	request.Tools = []protocol.Tool{{Definition: aiprot.ToolDefinition{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
		return aiprot.TextToolResult("found"), nil
	}}}
	modelCalls := 0
	provider := scriptedProvider{generate: func(_ context.Context, modelRequest aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		modelCalls++
		if modelRequest.Temperature == nil || *modelRequest.Temperature != temperature || modelRequest.MaxOutputTokens == nil || *modelRequest.MaxOutputTokens != maxTokens {
			t.Fatalf("scalars=%#v", modelRequest)
		}
		if modelRequest.ToolChoice != aiprot.ToolChoiceRequired || modelRequest.Reasoning == nil || modelRequest.Reasoning.Effort != "low" || modelRequest.OutputFormat == nil || modelRequest.OutputFormat.Type != aiprot.OutputJSONSchema || !modelRequest.OutputFormat.Strict || string(modelRequest.OutputFormat.Schema) != `{"type":"object"}` {
			t.Fatalf("generation=%#v", modelRequest)
		}
		if modelRequest.ProviderOptions["thinking"].(map[string]any)["type"] != "disabled" || modelRequest.Messages[0].ProviderOptions["vendor_extension"].(map[string]any)["enabled"] != true {
			t.Fatalf("extensions=%#v message=%#v", modelRequest.ProviderOptions, modelRequest.Messages[0].ProviderOptions)
		}
		if len(modelRequest.Tools) != 1 || modelRequest.Tools[0].Name != "lookup" || modelRequest.Metadata["trace"] != "abc" || modelRequest.Metadata[protocol.MetadataRunID] == "caller" || modelRequest.Metadata[aiprot.RequestIDMetadataKey] == "caller-request" || modelRequest.Metadata[protocol.MetadataStep] != fmt.Sprint(modelCalls) {
			t.Fatalf("metadata=%v tools=%#v", modelRequest.Metadata, modelRequest.Tools)
		}
		modelRequest.ProviderOptions["thinking"].(map[string]any)["type"] = "changed"
		modelRequest.OutputFormat.Schema[0] = 'x'
		modelRequest.Messages[0].ProviderOptions["vendor_extension"].(map[string]any)["enabled"] = false
		*modelRequest.Temperature = 1
		if modelCalls == 1 {
			return toolResponse(call("lookup")), nil
		}
		if len(modelRequest.Messages) != 3 || modelRequest.Messages[2].Role != aiprot.RoleTool {
			t.Fatalf("follow-up=%#v", modelRequest.Messages)
		}
		return textResponse("done"), nil
	}}
	result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), request)
	if err != nil || result.StopReason != protocol.StopNatural || modelCalls != 2 {
		t.Fatalf("result=%#v error=%v calls=%d", result, err, modelCalls)
	}
	if request.ProviderOptions["thinking"].(map[string]any)["type"] != "disabled" || string(request.OutputFormat.Schema) != `{"type":"object"}` || request.Messages[0].ProviderOptions["vendor_extension"].(map[string]any)["enabled"] != true || *request.Temperature != temperature {
		t.Fatal("caller request was shared with the provider")
	}
}
