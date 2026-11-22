package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/SmilingXinyi/agent-engine/agent"
	"github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	compressionprot "github.com/SmilingXinyi/agent-engine/compression/protocol"
)

func TestExecuteForwardsToolConfirmationAndDenialPolicy(t *testing.T) {
	for _, policy := range []loopprot.ToolDenialPolicy{loopprot.ToolDenialContinue, loopprot.ToolDenialStop} {
		t.Run(string(policy), func(t *testing.T) {
			models, confirmations := 0, 0
			ctx := context.Background()
			call := aiprot.ToolCall{ID: "write-id", Name: "write", Arguments: json.RawMessage(`{"path":"report.txt"}`)}
			provider := testProvider{generate: func(_ context.Context, input aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				models++
				if models == 1 {
					return aiprot.ModelResponse{Message: aiprot.Message{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall, ToolCall: &call}}}}, nil
				}
				result := input.Messages[2].ToolResults()[0]
				if !result.IsError || result.ToolCallID != call.ID {
					t.Fatalf("tool result=%+v", result)
				}
				return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "declined")}, nil
			}}
			request := protocol.Request{
				Model: "model", UserPrompt: "write a report",
				Tools: []loopprot.Tool{{Definition: aiprot.ToolDefinition{Name: "write"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
					t.Fatal("denied tool executed")
					return aiprot.ToolResult{}, nil
				}}},
				Options: loopprot.RunOptions{
					ToolDenialPolicy: policy,
					ConfirmTool: func(callContext context.Context, info loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
						confirmations++
						if callContext != ctx || !reflect.DeepEqual(info.Call, call) || info.Definition.Name != "write" {
							t.Fatalf("confirmation=%+v", info)
						}
						return loopprot.ToolConfirmationDecision{Reason: "user declined"}, nil
					},
				},
			}
			result, err := agent.New(ai.NewClient(provider, aiprot.Hooks{})).Execute(ctx, request)
			if confirmations != 1 || len(result.ToolCalls) != 1 || result.ToolCalls[0].Confirmation == nil || result.ToolCalls[0].Confirmation.Reason != "user declined" {
				t.Fatalf("result=%+v confirmations=%d", result, confirmations)
			}
			if policy == loopprot.ToolDenialStop {
				var denied *loopprot.ToolDeniedError
				if !errors.As(err, &denied) || models != 1 || result.StopReason != loopprot.StopError {
					t.Fatalf("result=%+v error=%v models=%d", result, err, models)
				}
			} else if err != nil || models != 2 || result.StopReason != loopprot.StopNatural {
				t.Fatalf("result=%+v error=%v models=%d", result, err, models)
			}
		})
	}
}

type testProvider struct {
	generate func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error)
}

func (provider testProvider) Name() string { return "test" }
func (provider testProvider) Generate(ctx context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
	return provider.generate(ctx, request)
}

func TestExecuteOrganizesContextAndKeepsRunsIndependent(t *testing.T) {
	history := []aiprot.Message{aiprot.TextMessage(aiprot.RoleUser, "old"), aiprot.TextMessage(aiprot.RoleAssistant, "reply")}
	expected := append([]aiprot.Message{aiprot.TextMessage(aiprot.RoleSystem, " system ")}, history...)
	expected = append(expected, aiprot.TextMessage(aiprot.RoleUser, "next"))
	count := 0
	ctx := context.WithValue(context.Background(), "caller", "context")
	provider := testProvider{generate: func(callContext context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		if callContext != ctx {
			t.Fatal("context was replaced")
		}
		count++
		if !reflect.DeepEqual(request.Messages, expected) {
			t.Fatalf("messages=%#v want=%#v", request.Messages, expected)
		}
		request.Messages[1].Parts[0].Text = "mutated"
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done"), Usage: aiprot.Usage{TotalTokens: 3}}, nil
	}}
	assistant := agent.New(ai.NewClient(provider, aiprot.Hooks{}))
	var previousID string
	for range 2 {
		result, err := assistant.Execute(ctx, protocol.Request{Model: "model", SystemPrompt: " system ", History: history, UserPrompt: "next"})
		if err != nil || result.StopReason != loopprot.StopNatural || result.LastResponse.Message.Text() != "done" || result.Usage.TotalTokens != 3 || len(result.Steps) != 1 || result.RunID == previousID {
			t.Fatalf("result=%#v error=%v", result, err)
		}
		if !reflect.DeepEqual(result.Messages[:len(expected)], expected) {
			t.Fatal("provider contaminated history")
		}
		previousID = result.RunID
	}
	if count != 2 || history[0].Text() != "old" {
		t.Fatalf("calls=%d history=%#v", count, history)
	}
}

func TestExecuteAcceptsHistoryWithoutUserPrompt(t *testing.T) {
	history := []aiprot.Message{aiprot.TextMessage(aiprot.RoleUser, "hello")}
	assistant := agent.New(ai.NewClient(testProvider{generate: func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		if !reflect.DeepEqual(request.Messages, history) {
			t.Fatalf("messages=%#v", request.Messages)
		}
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done")}, nil
	}}, aiprot.Hooks{}))
	if _, err := assistant.Execute(context.Background(), protocol.Request{Model: "model", History: history}); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteForwardsGenerationSettings(t *testing.T) {
	temperature := 0.4
	limit := 16
	history := []aiprot.Message{aiprot.TextMessage(aiprot.RoleDeveloper, "rules")}
	history[0].ProviderOptions = map[string]any{"vendor_extension": true}
	request := protocol.Request{
		Model: "model", History: history, UserPrompt: "hello",
		Temperature: &temperature, MaxOutputTokens: &limit, ToolChoice: aiprot.ToolChoiceNone,
		Reasoning:       &aiprot.ReasoningOptions{Effort: "low"},
		OutputFormat:    &aiprot.OutputFormat{Type: aiprot.OutputJSON, Name: "answer"},
		ProviderOptions: map[string]any{"thinking": map[string]any{"type": "disabled"}},
		Metadata:        map[string]string{"trace": "abc", loopprot.MetadataRunID: "caller"},
	}
	assistant := agent.New(ai.NewClient(testProvider{generate: func(_ context.Context, modelRequest aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		if modelRequest.Temperature == nil || *modelRequest.Temperature != temperature || modelRequest.MaxOutputTokens == nil || *modelRequest.MaxOutputTokens != limit || modelRequest.ToolChoice != aiprot.ToolChoiceNone {
			t.Fatalf("scalars=%#v", modelRequest)
		}
		if modelRequest.Reasoning == nil || modelRequest.Reasoning.Effort != "low" || modelRequest.OutputFormat == nil || modelRequest.OutputFormat.Type != aiprot.OutputJSON || modelRequest.ProviderOptions["thinking"].(map[string]any)["type"] != "disabled" {
			t.Fatalf("generation=%#v", modelRequest)
		}
		if modelRequest.Metadata["trace"] != "abc" || modelRequest.Metadata[loopprot.MetadataRunID] == "caller" || modelRequest.Messages[0].Role != aiprot.RoleDeveloper || modelRequest.Messages[0].ProviderOptions["vendor_extension"] != true {
			t.Fatalf("metadata=%v messages=%#v", modelRequest.Metadata, modelRequest.Messages)
		}
		modelRequest.ProviderOptions["thinking"].(map[string]any)["type"] = "changed"
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done")}, nil
	}}, aiprot.Hooks{}))
	if _, err := assistant.Execute(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if request.ProviderOptions["thinking"].(map[string]any)["type"] != "disabled" {
		t.Fatal("caller options were shared with the provider")
	}
}

func TestExecuteReturnsPartialLoopResult(t *testing.T) {
	failure := errors.New("provider failure")
	assistant := agent.New(ai.NewClient(testProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "partial")}, failure
	}}, aiprot.Hooks{}))
	result, err := assistant.Execute(context.Background(), protocol.Request{Model: "model", UserPrompt: "hello"})
	if !errors.Is(err, failure) || result.StopReason != loopprot.StopError || result.LastResponse.Message.Text() != "partial" || len(result.Steps) != 1 {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestExecuteRejectsInvalidInput(t *testing.T) {
	assistant := agent.New(ai.NewClient(testProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		t.Fatal("unexpected call")
		return aiprot.ModelResponse{}, nil
	}}, aiprot.Hooks{}))
	for _, request := range []protocol.Request{{Model: " ", UserPrompt: "hello"}, {Model: "model"}, {Model: "model", History: []aiprot.Message{{Role: aiprot.RoleUser}}}, {Model: "model", UserPrompt: "hello", Options: loopprot.RunOptions{MaxSteps: -1}}, {Model: "model", UserPrompt: "next", History: []aiprot.Message{{Role: aiprot.RoleTool}}}} {
		result, err := assistant.Execute(context.Background(), request)
		if err == nil || result.StopReason != loopprot.StopError {
			t.Fatalf("result=%#v error=%v", result, err)
		}
	}
	for _, missing := range []*agent.Agent{nil, agent.New(nil)} {
		if _, err := missing.Execute(context.Background(), protocol.Request{Model: "model", UserPrompt: "hello"}); err == nil {
			t.Fatal("nil agent/client should fail")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := assistant.Execute(ctx, protocol.Request{Model: "model", UserPrompt: "hello"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCompressionRespectsExplicitOptInAndProviderOptions(t *testing.T) {
	history := []aiprot.Message{
		aiprot.TextMessage(aiprot.RoleUser, "old question"),
		aiprot.TextMessage(aiprot.RoleAssistant, "old answer"),
	}
	request := protocol.Request{
		Model: "model", History: history, UserPrompt: "new question",
		Options:         loopprot.RunOptions{MaxHistoryRounds: 1},
		ProviderOptions: map[string]any{"store": false, "previous_response_id": "ordinary"},
	}
	summaries := 0
	assistant := agent.New(ai.NewClient(testProvider{generate: func(_ context.Context, input aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		if input.Metadata[compressionprot.MetadataPurpose] == compressionprot.PurposeSummary {
			summaries++
			if !reflect.DeepEqual(input.ProviderOptions, map[string]any{"store": false, "service_tier": "default"}) {
				t.Fatalf("summary provider options=%v", input.ProviderOptions)
			}
			return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant,
				`{"user_goal":"summarized","decisions":"","key_facts":"","open_items":"","agreements":"","keep_recent_rounds":1}`)}, nil
		}
		if !reflect.DeepEqual(input.ProviderOptions, request.ProviderOptions) {
			t.Fatalf("conversation provider options=%v", input.ProviderOptions)
		}
		if summaries == 0 && (len(input.Messages) != 1 || input.Messages[0].Text() != "new question") {
			t.Fatalf("default history exceeded the hard limit: %+v", input.Messages)
		}
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done")}, nil
	}}, aiprot.Hooks{}))
	if _, err := assistant.Execute(t.Context(), request); err != nil || summaries != 0 {
		t.Fatalf("default run produced %d summaries: %v", summaries, err)
	}
	keep := 1
	request.Options.Compression = &compressionprot.Config{
		KeepRecentRounds: &keep, SummaryProviderOptions: map[string]any{"store": true, "service_tier": "default"},
	}
	if _, err := assistant.Execute(t.Context(), request); err != nil || summaries != 1 {
		t.Fatalf("opt-in run produced %d summaries: %v", summaries, err)
	}
	if request.Options.Compression.SummaryProviderOptions["store"] != true {
		t.Fatal("summary mutated caller configuration")
	}
}

func TestCompressedContextResumesAfterIncompleteToolBatch(t *testing.T) {
	for _, scenario := range []string{"max steps", "tool hook failure"} {
		t.Run(scenario, func(t *testing.T) {
			first := aiprot.ToolCall{ID: "first", Name: "test", Arguments: json.RawMessage("{}")}
			second := aiprot.ToolCall{ID: "second", Name: "test", Arguments: json.RawMessage("{}")}
			assistant := agent.New(ai.NewClient(testProvider{generate: func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				if request.Messages[len(request.Messages)-1].Text() == "continue" {
					return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "resumed")}, nil
				}
				return aiprot.ModelResponse{Message: aiprot.Message{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{
					{Type: aiprot.ContentToolCall, ToolCall: &first}, {Type: aiprot.ContentToolCall, ToolCall: &second},
				}}}, nil
			}}, aiprot.Hooks{}))
			hookError := errors.New("stop after one tool")
			options := loopprot.RunOptions{MaxSteps: 1, Compression: &compressionprot.Config{}}
			if scenario == "tool hook failure" {
				options.MaxSteps = 2
				options.Hooks.OnToolEnd = func(context.Context, loopprot.ToolEndInfo) error { return hookError }
			}
			result, err := assistant.Execute(t.Context(), protocol.Request{
				Model: "model", UserPrompt: "start", Options: options,
				Tools: []loopprot.Tool{{Definition: aiprot.ToolDefinition{Name: "test"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
					return aiprot.TextToolResult("done"), nil
				}}},
			})
			if scenario == "max steps" && (err != nil || result.StopReason != loopprot.StopMaxSteps || len(result.ToolCalls) != 0) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if scenario == "tool hook failure" && (!errors.Is(err, hookError) || len(result.ToolCalls) != 1) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if len(result.ContextMessages) != 1 || len(result.Messages[1].ToolCalls()) != 2 {
				t.Fatalf("invalid history or execution record: %+v", result)
			}
			resumed, err := assistant.Execute(t.Context(), protocol.Request{
				Model: "model", History: result.ContextMessages, UserPrompt: "continue",
			})
			if err != nil || resumed.LastResponse.Message.Text() != "resumed" {
				t.Fatalf("could not resume: %v", err)
			}
		})
	}
}
