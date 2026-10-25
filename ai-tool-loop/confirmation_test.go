package aitoolloop_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loop "github.com/SmilingXinyi/agent-engine/ai-tool-loop"
	"github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

func TestToolConfirmationDecisionsAndDenialPolicies(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		policy protocol.ToolDenialPolicy
		reason string
	}{
		{name: "default", reason: "user declined"},
		{name: "continue", policy: protocol.ToolDenialContinue},
		{name: "stop", policy: protocol.ToolDenialStop, reason: "debug stop"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			calls := []aiprot.ToolCall{
				{ID: "read", Name: "read", Arguments: json.RawMessage(`{}`)},
				{ID: "unsafe-write", Name: "write", Arguments: json.RawMessage(`{"allow":false}`)},
				{ID: "safe-write", Name: "write", Arguments: json.RawMessage(`{"allow":true}`)},
			}
			var events, executed []string
			modelCalls := 0
			request := initialRequest()
			for _, name := range []string{"read", "write"} {
				request.Tools = append(request.Tools, protocol.Tool{
					Definition: aiprot.ToolDefinition{Name: name},
					Execute: func(_ context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
						executed = append(executed, name)
						events = append(events, "execute "+name)
						return aiprot.TextToolResult(name), nil
					},
				})
			}
			request.Options.ToolDenialPolicy = scenario.policy
			request.Options.ConfirmTool = func(_ context.Context, info protocol.ToolConfirmationInfo) (protocol.ToolConfirmationDecision, error) {
				events = append(events, "confirm "+info.Call.ID)
				if info.RunID == "" || info.Step != 1 || info.RequestID == "" || info.Definition.Name != info.Call.Name {
					t.Fatalf("confirmation=%+v", info)
				}
				approved := info.Call.Name == "read" || string(info.Call.Arguments) == `{"allow":true}`
				if approved {
					return protocol.ToolConfirmationDecision{Approved: true}, nil
				}
				return protocol.ToolConfirmationDecision{Reason: scenario.reason}, nil
			}
			request.Options.Hooks.OnToolStart = func(_ context.Context, info protocol.ToolStartInfo) error {
				events = append(events, "start "+info.Call.ID)
				return nil
			}
			request.Options.Hooks.OnToolEnd = func(_ context.Context, info protocol.ToolEndInfo) error {
				events = append(events, "end "+info.Call.ID)
				if info.Confirmation == nil {
					t.Fatal("missing confirmation")
				}
				if info.Call.ID == "unsafe-write" {
					var denied *protocol.ToolDeniedError
					if !errors.As(info.Error, &denied) || denied.ToolName != "write" || denied.ToolCallID != info.Call.ID || denied.Reason != scenario.reason || info.Confirmation.Approved || !info.Result.IsError {
						t.Fatalf("denial=%+v end=%+v", denied, info)
					}
					if info.Result.Text() != denied.Error() {
						t.Fatalf("denial text=%q want=%q", info.Result.Text(), denied.Error())
					}
				} else if info.Error != nil || !info.Confirmation.Approved {
					t.Fatalf("approved end=%+v", info)
				}
				return nil
			}
			provider := scriptedProvider{generate: func(_ context.Context, input aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				modelCalls++
				if modelCalls == 1 {
					return toolResponse(calls...), nil
				}
				if len(input.Messages) != 5 {
					t.Fatalf("incomplete tool batch: %+v", input.Messages)
				}
				for index, call := range calls {
					result := input.Messages[index+2].ToolResults()[0]
					if result.ToolCallID != call.ID || result.ToolName != call.Name || result.IsError != (index == 1) {
						t.Fatalf("result=%+v", result)
					}
				}
				return textResponse("done"), nil
			}}
			result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), request)
			wantEvents := []string{"start read", "confirm read", "execute read", "end read", "start unsafe-write", "confirm unsafe-write", "end unsafe-write"}
			if scenario.policy == protocol.ToolDenialStop {
				var denied *protocol.ToolDeniedError
				if !errors.As(err, &denied) || denied.Reason != scenario.reason || result.StopReason != protocol.StopError || modelCalls != 1 || len(result.ToolCalls) != 2 || len(result.Messages) != 4 || !reflect.DeepEqual(executed, []string{"read"}) {
					t.Fatalf("result=%+v error=%v models=%d executed=%v", result, err, modelCalls, executed)
				}
			} else {
				wantEvents = append(wantEvents, "start safe-write", "confirm safe-write", "execute write", "end safe-write")
				if err != nil || result.StopReason != protocol.StopNatural || modelCalls != 2 || len(result.ToolCalls) != 3 || !reflect.DeepEqual(executed, []string{"read", "write"}) {
					t.Fatalf("result=%+v error=%v models=%d executed=%v", result, err, modelCalls, executed)
				}
			}
			if !reflect.DeepEqual(events, wantEvents) {
				t.Fatalf("events=%v want=%v", events, wantEvents)
			}
			denied := result.ToolCalls[1]
			if denied.Confirmation == nil || denied.Confirmation.Approved || denied.Confirmation.Reason != scenario.reason || denied.Error != denied.Result.Text() || !strings.Contains(denied.Error, "execution denied") {
				t.Fatalf("denied record=%+v", denied)
			}
		})
	}
}

func TestToolConfirmationFailuresStopAndJoinEndErrors(t *testing.T) {
	for _, mode := range []string{"error", "panic", "cancel waiting", "cancel approved", "denied"} {
		for _, joinEndErrors := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "", true: " with end errors"}[joinEndErrors], func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				failure, toolEndErr := errors.New("confirmation unavailable"), errors.New("tool end failed")
				stepEndErr, runEndErr := errors.New("step end failed"), errors.New("run end failed")
				var events []string
				request := initialRequest()
				// Also verify a continued denial becomes fatal if an end hook fails.
				if mode == "denied" && !joinEndErrors {
					request.Options.ToolDenialPolicy = protocol.ToolDenialStop
				}
				request.Tools = []protocol.Tool{{Definition: aiprot.ToolDefinition{Name: "first"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
					t.Fatal("confirmation failure invoked executor")
					return aiprot.ToolResult{}, nil
				}}}
				request.Options.ConfirmTool = func(callContext context.Context, _ protocol.ToolConfirmationInfo) (protocol.ToolConfirmationDecision, error) {
					events = append(events, "confirm")
					if callContext != ctx {
						t.Fatal("context was replaced")
					}
					switch mode {
					case "panic":
						panic("confirmation panic")
					case "cancel waiting":
						go cancel()
						<-callContext.Done()
						return protocol.ToolConfirmationDecision{}, callContext.Err()
					case "cancel approved":
						cancel()
						return protocol.ToolConfirmationDecision{Approved: true}, nil
					case "denied":
						return protocol.ToolConfirmationDecision{}, nil
					default:
						return protocol.ToolConfirmationDecision{Approved: true}, failure
					}
				}
				request.Options.Hooks = protocol.Hooks{
					OnRunStart:  func(context.Context, protocol.RunStartInfo) error { events = append(events, "run start"); return nil },
					OnStepStart: func(context.Context, protocol.StepStartInfo) error { events = append(events, "step start"); return nil },
					OnToolStart: func(context.Context, protocol.ToolStartInfo) error { events = append(events, "tool start"); return nil },
					OnToolEnd: func(_ context.Context, info protocol.ToolEndInfo) error {
						events = append(events, "tool end")
						if info.Error == nil || !info.Result.IsError || (info.Confirmation != nil) != (mode == "cancel approved" || mode == "denied") {
							t.Fatalf("end=%+v", info)
						}
						if joinEndErrors {
							return toolEndErr
						}
						return nil
					},
					OnStepEnd: func(context.Context, protocol.StepEndInfo) error {
						events = append(events, "step end")
						if joinEndErrors {
							return stepEndErr
						}
						return nil
					},
					OnRunEnd: func(context.Context, protocol.RunEndInfo) error {
						events = append(events, "run end")
						if joinEndErrors {
							return runEndErr
						}
						return nil
					},
				}
				models := 0
				provider := scriptedProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
					models++
					if models > 1 {
						t.Fatal("confirmation failure invoked another model call")
					}
					return toolResponse(call("first"), call("first-again")), nil
				}}
				result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(ctx, request)
				if err == nil || errors.Is(err, toolEndErr) != joinEndErrors || errors.Is(err, stepEndErr) != joinEndErrors || errors.Is(err, runEndErr) != joinEndErrors || result.StopReason != protocol.StopError || len(result.ToolCalls) != 1 || len(result.Messages) != 3 {
					t.Fatalf("result=%+v error=%v", result, err)
				}
				switch mode {
				case "error":
					if !errors.Is(err, failure) {
						t.Fatal(err)
					}
				case "panic":
					if !strings.Contains(err.Error(), "tool confirmation panicked: confirmation panic") {
						t.Fatal(err)
					}
				case "denied":
					var denied *protocol.ToolDeniedError
					if !errors.As(err, &denied) {
						t.Fatal(err)
					}
				default:
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				}
				assertPairedEvents(t, events)
				if want := []string{"run start", "step start", "tool start", "confirm", "tool end", "step end", "run end"}; !reflect.DeepEqual(events, want) {
					t.Fatalf("events=%v", events)
				}
				if strings.Contains(result.ToolCalls[0].Error, toolEndErr.Error()) != joinEndErrors || !result.ToolCalls[0].Result.IsError {
					t.Fatalf("record=%+v", result.ToolCalls[0])
				}
			})
		}
	}
}

func TestToolConfirmationSnapshotsAndRepeatedCalls(t *testing.T) {
	request := initialRequest()
	arguments := json.RawMessage(`{"value":"safe"}`)
	request.Tools = []protocol.Tool{{Definition: aiprot.ToolDefinition{Name: "test", Description: "safe", InputSchema: json.RawMessage(`{"type":"object"}`)}, Execute: func(_ context.Context, input json.RawMessage) (aiprot.ToolResult, error) {
		if string(input) != string(arguments) {
			t.Fatalf("arguments=%s", input)
		}
		return aiprot.TextToolResult("done"), nil
	}}}
	confirmations := 0
	request.Options.ConfirmTool = func(_ context.Context, info protocol.ToolConfirmationInfo) (protocol.ToolConfirmationDecision, error) {
		confirmations++
		if info.Call.Name != "test" || string(info.Call.Arguments) != string(arguments) || info.Definition.Description != "safe" || string(info.Definition.InputSchema) != `{"type":"object"}` {
			t.Fatalf("confirmation snapshot=%+v", info)
		}
		info.Call.Arguments[0] = '['
		info.Definition.InputSchema[0] = '['
		return protocol.ToolConfirmationDecision{Approved: true, Reason: "approved"}, nil
	}
	request.Options.Hooks.OnToolEnd = func(_ context.Context, info protocol.ToolEndInfo) error {
		if info.Confirmation == nil || !info.Confirmation.Approved {
			t.Fatal("missing approval")
		}
		info.Confirmation.Approved = false
		info.Confirmation.Reason = "mutated"
		return nil
	}
	models := 0
	provider := scriptedProvider{generate: func(_ context.Context, input aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		models++
		if string(input.Tools[0].InputSchema) != `{"type":"object"}` {
			t.Fatal("tool schema mutated")
		}
		if models <= 2 {
			return toolResponse(aiprot.ToolCall{ID: "repeated", Name: "test", Arguments: arguments}), nil
		}
		return textResponse("done"), nil
	}}
	result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), request)
	if err != nil || confirmations != 2 || len(result.ToolCalls) != 2 {
		t.Fatalf("result=%+v confirmations=%d error=%v", result, confirmations, err)
	}
	for _, record := range result.ToolCalls {
		if !record.Confirmation.Approved || record.Confirmation.Reason != "approved" || string(record.Call.Arguments) != string(arguments) {
			t.Fatalf("record=%+v", record)
		}
	}
	cloned := result.Clone()
	cloned.ToolCalls[0].Confirmation.Approved = false
	if !result.ToolCalls[0].Confirmation.Approved {
		t.Fatal("result clone shared confirmation")
	}
}

func TestToolConfirmationBoundaries(t *testing.T) {
	for _, mode := range []string{"absent", "unknown", "invalid arguments", "max steps", "start error", "invalid policy"} {
		t.Run(mode, func(t *testing.T) {
			request := initialRequest()
			executions, starts, ends, modelCalls := 0, 0, 0, 0
			request.Tools = []protocol.Tool{{Definition: aiprot.ToolDefinition{Name: "first"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
				executions++
				return aiprot.TextToolResult("done"), nil
			}}}
			if mode != "absent" {
				request.Options.ConfirmTool = func(context.Context, protocol.ToolConfirmationInfo) (protocol.ToolConfirmationDecision, error) {
					t.Fatal("unexpected confirmation")
					return protocol.ToolConfirmationDecision{}, nil
				}
			}
			startErr := errors.New("start failed")
			request.Options.Hooks.OnToolStart = func(context.Context, protocol.ToolStartInfo) error {
				starts++
				if mode == "start error" {
					return startErr
				}
				return nil
			}
			request.Options.Hooks.OnToolEnd = func(_ context.Context, info protocol.ToolEndInfo) error {
				ends++
				if info.Confirmation != nil {
					t.Fatal("unexpected decision")
				}
				return nil
			}
			if mode == "max steps" {
				request.Options.MaxSteps = 1
			}
			if mode == "invalid policy" {
				request.Options.ToolDenialPolicy = "invalid"
				request.Options.Hooks.OnRunStart = func(context.Context, protocol.RunStartInfo) error {
					t.Fatal("invalid policy entered lifecycle")
					return nil
				}
				request.Options.Hooks.OnRunEnd = func(context.Context, protocol.RunEndInfo) error {
					t.Fatal("invalid policy entered lifecycle")
					return nil
				}
			}
			provider := scriptedProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				modelCalls++
				if modelCalls > 1 {
					return textResponse("done"), nil
				}
				first := call("first")
				switch mode {
				case "unknown":
					first.Name = "unknown"
				case "invalid arguments":
					invalid := call("invalid")
					invalid.Arguments = json.RawMessage(`[]`)
					return toolResponse(first, invalid), nil
				}
				return toolResponse(first), nil
			}}
			result, err := loop.New(ai.NewClient(provider, aiprot.Hooks{})).Run(context.Background(), request)
			if starts != ends {
				t.Fatalf("unpaired tool hooks: %d/%d", starts, ends)
			}
			for _, record := range result.ToolCalls {
				if record.Confirmation != nil {
					t.Fatal("unexpected confirmation record")
				}
			}
			switch mode {
			case "absent", "unknown":
				if err != nil || result.StopReason != protocol.StopNatural || modelCalls != 2 || starts != 1 {
					t.Fatalf("result=%+v error=%v", result, err)
				}
				if (executions == 1) != (mode == "absent") || result.ToolCalls[0].Result.IsError != (mode == "unknown") {
					t.Fatal("unexpected execution/result")
				}
			case "max steps":
				if err != nil || result.StopReason != protocol.StopMaxSteps || starts != 0 || executions != 0 {
					t.Fatalf("result=%+v error=%v", result, err)
				}
			case "start error":
				if !errors.Is(err, startErr) || starts != 1 || executions != 0 {
					t.Fatal(err)
				}
			default:
				if err == nil || result.StopReason != protocol.StopError || starts != 0 || executions != 0 || (mode == "invalid policy" && modelCalls != 0) {
					t.Fatalf("result=%+v error=%v", result, err)
				}
			}
		})
	}
}
