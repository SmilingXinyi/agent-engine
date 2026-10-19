package toolloop_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loop "github.com/SmilingXinyi/agent-engine/tool-loop"
	"github.com/SmilingXinyi/agent-engine/tool-loop/protocol"
)

func TestHookErrorsAndPanicsStopAndPairLifecycle(t *testing.T) {
	for _, stage := range []string{"run start", "run end", "step start", "step end", "tool start", "tool end"} {
		for _, panics := range []bool{false, true} {
			t.Run(stage+map[bool]string{false: " error", true: " panic"}[panics], func(t *testing.T) {
				failure := errors.New("hook failed")
				var events []string
				observe := func(current string) error {
					events = append(events, current)
					if current == stage {
						if panics {
							panic("hook failed")
						}
						return failure
					}
					return nil
				}
				request := initialRequest()
				executions, modelCalls := 0, 0
				request.Tools = []protocol.Tool{{Definition: aiprot.ToolDefinition{Name: "first"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
					executions++
					return aiprot.TextToolResult("done"), nil
				}}, {Definition: aiprot.ToolDefinition{Name: "second"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
					executions++
					return aiprot.TextToolResult("done"), nil
				}}}
				request.Options.Hooks = protocol.Hooks{
					OnRunStart:  func(context.Context, protocol.RunStartInfo) error { return observe("run start") },
					OnRunEnd:    func(context.Context, protocol.RunEndInfo) error { return observe("run end") },
					OnStepStart: func(context.Context, protocol.StepStartInfo) error { return observe("step start") },
					OnStepEnd:   func(context.Context, protocol.StepEndInfo) error { return observe("step end") },
					OnToolStart: func(context.Context, protocol.ToolStartInfo) error { return observe("tool start") },
					OnToolEnd:   func(context.Context, protocol.ToolEndInfo) error { return observe("tool end") },
				}
				provider := scriptedProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
					modelCalls++
					if stage == "run end" {
						return textResponse("done"), nil
					}
					if modelCalls > 1 {
						t.Fatal("failure should prevent another model call")
					}
					return toolResponse(call("first"), call("second")), nil
				}}
				result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), request)
				if err == nil || !strings.Contains(err.Error(), "hook failed") || result.StopReason != protocol.StopError {
					t.Fatalf("result=%#v error=%v", result, err)
				}
				if !panics && !errors.Is(err, failure) {
					t.Fatalf("lost sentinel: %v", err)
				}
				assertPairedEvents(t, events)
				if stage == "tool start" && executions != 0 {
					t.Fatal("start failure invoked executor")
				}
				if stage == "tool end" && (executions != 1 || len(result.ToolCalls) != 1) {
					t.Fatalf("executions=%d records=%d", executions, len(result.ToolCalls))
				}
			})
		}
	}
}

func TestCancellationAtEveryExecutionBoundary(t *testing.T) {
	for _, stage := range []string{"before run", "run start", "step start", "provider", "tool start", "executor", "tool end", "step end", "run end"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var events []string
			observe := func(current string) error {
				events = append(events, current)
				if current == stage {
					cancel()
				}
				return nil
			}
			request := initialRequest()
			request.Options.Hooks = protocol.Hooks{
				OnRunStart:  func(context.Context, protocol.RunStartInfo) error { return observe("run start") },
				OnRunEnd:    func(context.Context, protocol.RunEndInfo) error { return observe("run end") },
				OnStepStart: func(context.Context, protocol.StepStartInfo) error { return observe("step start") },
				OnStepEnd:   func(context.Context, protocol.StepEndInfo) error { return observe("step end") },
				OnToolStart: func(context.Context, protocol.ToolStartInfo) error { return observe("tool start") },
				OnToolEnd:   func(context.Context, protocol.ToolEndInfo) error { return observe("tool end") },
			}
			executions := 0
			request.Tools = []protocol.Tool{{Definition: aiprot.ToolDefinition{Name: "first"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
				executions++
				if stage == "executor" {
					cancel()
				}
				return aiprot.TextToolResult("done"), nil
			}}, {Definition: aiprot.ToolDefinition{Name: "second"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
				executions++
				return aiprot.TextToolResult("done"), nil
			}}}
			models := 0
			provider := scriptedProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				models++
				if stage == "provider" {
					cancel()
				}
				if stage == "run end" || stage == "step end" {
					return textResponse("done"), nil
				}
				return toolResponse(call("first"), call("second")), nil
			}}
			if stage == "before run" {
				cancel()
			}
			result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(ctx, request)
			if !errors.Is(err, context.Canceled) || result.StopReason != protocol.StopError || models > 1 || executions > 1 {
				t.Fatalf("result=%#v error=%v models=%d executions=%d", result, err, models, executions)
			}
			if stage == "before run" {
				if len(events) != 0 {
					t.Fatal(events)
				}
				return
			}
			assertPairedEvents(t, events)
		})
	}
}

func assertPairedEvents(t *testing.T, events []string) {
	t.Helper()
	counts := map[string]int{}
	for _, event := range events {
		counts[event]++
	}
	for _, prefix := range []string{"run", "step", "tool"} {
		if counts[prefix+" start"] != counts[prefix+" end"] {
			t.Fatalf("unpaired hooks: %v", events)
		}
	}
	if counts["run end"] != 1 {
		t.Fatalf("run end count: %v", events)
	}
}

func TestToolStartAndEndErrorsAreJoinedWithoutExecuting(t *testing.T) {
	startErr, endErr := errors.New("start failure"), errors.New("end failure")
	request := initialRequest()
	request.Tools = []protocol.Tool{{Definition: aiprot.ToolDefinition{Name: "first"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
		t.Fatal("start failure invoked executor")
		return aiprot.ToolResult{}, nil
	}}}
	request.Options.Hooks.OnToolStart = func(context.Context, protocol.ToolStartInfo) error { return startErr }
	request.Options.Hooks.OnToolEnd = func(_ context.Context, info protocol.ToolEndInfo) error {
		if !errors.Is(info.Error, startErr) || !info.Result.IsError {
			t.Fatalf("tool end=%#v", info)
		}
		return endErr
	}
	provider := scriptedProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		return toolResponse(call("first")), nil
	}}
	result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), request)
	if !errors.Is(err, startErr) || !errors.Is(err, endErr) || result.StopReason != protocol.StopError || len(result.ToolCalls) != 1 {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestSnapshotsIsolateCallerProviderExecutorsAndHooks(t *testing.T) {
	request := initialRequest()
	request.Messages[0].Metadata = map[string]any{"nested": map[string]any{"value": "safe"}}
	request.Messages[0].Parts[0].Data = json.RawMessage(`{"safe":true}`)
	first := toolResponse(aiprot.ToolCall{ID: "first-id", Name: "first", Arguments: json.RawMessage(`{"value":"safe"}`)})
	first.Message.Metadata = map[string]any{"nested": map[string]any{"value": "safe"}}
	shared := aiprot.TextToolResult("safe result")
	shared.Metadata = map[string]any{"nested": map[string]any{"value": "safe"}}
	request.Tools = []protocol.Tool{{Definition: aiprot.ToolDefinition{Name: "first", InputSchema: json.RawMessage(`{"type":"object"}`)}, Execute: func(_ context.Context, input json.RawMessage) (aiprot.ToolResult, error) {
		input[0] = '['
		return shared, nil
	}}}
	original := request.Clone()
	providerCalls := 0
	provider := scriptedProvider{generate: func(_ context.Context, input aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		providerCalls++
		if input.Messages[0].Text() != "hello" || input.Messages[0].Metadata["nested"].(map[string]any)["value"] != "safe" || string(input.Messages[0].Parts[0].Data) != `{"safe":true}` || string(input.Tools[0].InputSchema) != `{"type":"object"}` {
			t.Fatal("provider observed corrupted snapshot")
		}
		if providerCalls == 2 {
			if input.Messages[1].ToolCalls()[0].ID != "first-id" || string(input.Messages[1].ToolCalls()[0].Arguments) != `{"value":"safe"}` || input.Messages[2].ToolResults()[0].Text() != "safe result" || input.Messages[2].ToolResults()[0].Metadata["nested"].(map[string]any)["value"] != "safe" {
				t.Fatal("history was contaminated")
			}
		}
		input.Messages[0].Parts[0].Text = "provider mutation"
		input.Messages[0].Parts[0].Data[0] = '['
		input.Messages[0].Metadata["nested"].(map[string]any)["value"] = "provider mutation"
		input.Tools[0].InputSchema[0] = '['
		input.Metadata["request_id"] = "provider mutation"
		if providerCalls == 1 {
			return first, nil
		}
		return textResponse("final"), nil
	}}
	request.Options.Hooks = protocol.Hooks{
		OnRunStart: func(_ context.Context, info protocol.RunStartInfo) error {
			info.Request.Messages[0].Parts[0].Text = "run mutation"
			info.Request.Tools[0].Definition.InputSchema[0] = '['
			info.Request.Options.Hooks.OnToolEnd = nil
			return nil
		},
		OnStepStart: func(_ context.Context, info protocol.StepStartInfo) error {
			info.Request.Messages[0].Metadata["nested"].(map[string]any)["value"] = "step mutation"
			info.Request.Tools[0].InputSchema[0] = '['
			info.Request.Metadata["request_id"] = "step mutation"
			return nil
		},
		OnToolStart: func(_ context.Context, info protocol.ToolStartInfo) error { info.Call.Arguments[0] = '['; return nil },
		OnToolEnd: func(_ context.Context, info protocol.ToolEndInfo) error {
			info.Result.Content[0].Text = "tool end mutation"
			info.Result.Metadata["nested"].(map[string]any)["value"] = "tool end mutation"
			return nil
		},
		OnStepEnd: func(_ context.Context, info protocol.StepEndInfo) error {
			info.Result.Response.Message.Parts[0].Text = "step end mutation"
			if len(info.ToolCalls) > 0 {
				info.ToolCalls[0].Call.Arguments[0] = '['
				info.ToolCalls[0].Result.Content[0].Text = "step end mutation"
			}
			return nil
		},
		OnRunEnd: func(_ context.Context, info protocol.RunEndInfo) error {
			info.Result.Messages[0].Parts[0].Data[0] = '['
			info.Result.LastResponse.Message.Parts[0].Text = "run end mutation"
			return nil
		},
	}
	result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), request)
	if err != nil || result.LastResponse.Message.Text() != "final" || providerCalls != 2 {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if !reflect.DeepEqual(request.Messages, original.Messages) || !reflect.DeepEqual(request.Tools[0].Definition, original.Tools[0].Definition) || shared.Text() != "safe result" {
		t.Fatal("caller data changed")
	}
	first.Message.Parts[0].ToolCall.Arguments[0] = '['
	shared.Content[0].Text = "source mutation"
	if string(result.ToolCalls[0].Call.Arguments) != `{"value":"safe"}` || result.ToolCalls[0].Result.Text() != "safe result" || result.Messages[2].ToolResults()[0].Text() != "safe result" {
		t.Fatal("result still aliases source data")
	}
	result.LastResponse.Message.Parts[0].Text = "result mutation"
	if result.Steps[1].Response.Message.Text() != "final" || result.Messages[3].Text() != "final" {
		t.Fatal("result fields alias each other")
	}
	copy := result.Clone()
	copy.Messages[0].Metadata["nested"].(map[string]any)["value"] = "copy mutation"
	if result.Messages[0].Metadata["nested"].(map[string]any)["value"] != "safe" {
		t.Fatal("result clone is shallow")
	}
}
