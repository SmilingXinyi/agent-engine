package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/SmilingXinyi/agent-engine/agent"
	"github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

func TestExecuteReadsToolConfirmationEnvOnEveryRun(t *testing.T) {
	models, executions, confirmations := 0, 0, 0
	call := aiprot.ToolCall{ID: "write-id", Name: "write", Arguments: json.RawMessage(`{}`)}
	provider := testProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		models++
		if models == 1 {
			return aiprot.ModelResponse{Message: aiprot.Message{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall, ToolCall: &call}}}}, nil
		}
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done")}, nil
	}}
	// Reuse the same Agent so environment changes must be read by Execute.
	assistant := agent.New(ai.NewClient(provider, aiprot.Hooks{}))
	for _, scenario := range []struct {
		name       string
		mode       string
		unset      bool
		noCallback bool
		policy     loopprot.ToolDenialPolicy
		wantError  string
	}{
		{name: "unset preserves request", unset: true, policy: loopprot.ToolDenialStop},
		{name: "empty preserves request", policy: loopprot.ToolDenialContinue},
		{name: "off ignores callback", mode: "off", policy: loopprot.ToolDenialStop},
		{name: "continue overrides stop", mode: "continue", policy: loopprot.ToolDenialStop},
		{name: "stop overrides continue", mode: "stop", policy: loopprot.ToolDenialContinue},
		{name: "trim whitespace", mode: " stop ", policy: loopprot.ToolDenialContinue},
		{name: "off without callback", mode: "off", noCallback: true},
		{name: "empty without callback", noCallback: true},
		{name: "continue requires callback", mode: "continue", noCallback: true, wantError: "requires Options.ConfirmTool"},
		{name: "stop requires callback", mode: "stop", noCallback: true, wantError: "requires Options.ConfirmTool"},
		{name: "invalid mode", mode: "invalid", wantError: "invalid AGENT_TOOL_CONFIRMATION"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv("AGENT_TOOL_CONFIRMATION", scenario.mode)
			if scenario.unset {
				if err := os.Unsetenv("AGENT_TOOL_CONFIRMATION"); err != nil {
					t.Fatal(err)
				}
			}
			models, executions, confirmations = 0, 0, 0
			starts, ends := 0, 0
			request := protocol.Request{
				Model: "model", UserPrompt: "write a file",
				Tools: []loopprot.Tool{{Definition: aiprot.ToolDefinition{Name: "write"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
					executions++
					return aiprot.TextToolResult("written"), nil
				}}},
				Options: loopprot.RunOptions{
					ToolDenialPolicy: scenario.policy,
					Hooks: loopprot.Hooks{
						OnRunStart: func(context.Context, loopprot.RunStartInfo) error { starts++; return nil },
						OnRunEnd:   func(context.Context, loopprot.RunEndInfo) error { ends++; return nil },
					},
				},
			}
			if !scenario.noCallback {
				request.Options.ConfirmTool = func(context.Context, loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
					confirmations++
					return loopprot.ToolConfirmationDecision{Reason: "declined"}, nil
				}
			}
			result, err := assistant.Execute(context.Background(), request)
			if request.Options.ToolDenialPolicy != scenario.policy || (request.Options.ConfirmTool == nil) != scenario.noCallback {
				t.Fatal("environment handling mutated the caller's options")
			}
			if scenario.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), scenario.wantError) || result.StopReason != loopprot.StopError || models != 0 || executions != 0 || confirmations != 0 || starts != 0 || ends != 0 || result.RunID != "" {
					t.Fatalf("result=%+v error=%v models=%d executions=%d confirmations=%d hooks=%d/%d", result, err, models, executions, confirmations, starts, ends)
				}
				return
			}
			mode := strings.TrimSpace(scenario.mode)
			enabled := mode != "off" && !scenario.noCallback
			stop := enabled && (mode == "stop" || (mode == "" && scenario.policy == loopprot.ToolDenialStop))
			if starts != 1 || ends != 1 || len(result.ToolCalls) != 1 {
				t.Fatalf("result=%+v hooks=%d/%d", result, starts, ends)
			}
			record := result.ToolCalls[0]
			if enabled {
				if executions != 0 || confirmations != 1 || record.Confirmation == nil || record.Confirmation.Approved || !record.Result.IsError {
					t.Fatalf("record=%+v executions=%d confirmations=%d", record, executions, confirmations)
				}
			} else if executions != 1 || confirmations != 0 || record.Confirmation != nil || record.Result.IsError {
				t.Fatalf("record=%+v executions=%d confirmations=%d", record, executions, confirmations)
			}
			if stop {
				var denied *loopprot.ToolDeniedError
				if !errors.As(err, &denied) || result.StopReason != loopprot.StopError || models != 1 {
					t.Fatalf("result=%+v error=%v models=%d", result, err, models)
				}
			} else if err != nil || result.StopReason != loopprot.StopNatural || models != 2 {
				t.Fatalf("result=%+v error=%v models=%d", result, err, models)
			}
		})
	}
}
