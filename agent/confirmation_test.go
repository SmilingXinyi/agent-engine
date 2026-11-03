package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/SmilingXinyi/agent-engine/agent"
	"github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

func TestExecuteIgnoresToolConfirmationEnv(t *testing.T) {
	models, executions, confirmations := 0, 0, 0
	call := aiprot.ToolCall{ID: "write-id", Name: "write", Arguments: json.RawMessage(`{}`)}
	provider := testProvider{generate: func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		models++
		if models == 1 {
			return aiprot.ModelResponse{Message: aiprot.Message{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall, ToolCall: &call}}}}, nil
		}
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done")}, nil
	}}
	// The same Agent must honor request options even as the process environment changes.
	assistant := agent.New(ai.NewClient(provider, aiprot.Hooks{}))
	for _, mode := range []string{"", "off", "continue", "stop", " stop ", "invalid"} {
		for _, policy := range []loopprot.ToolDenialPolicy{loopprot.ToolDenialContinue, loopprot.ToolDenialStop} {
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("env=%q/policy=%s/enabled=%t", mode, policy, enabled), func(t *testing.T) {
					t.Setenv("AGENT_TOOL_CONFIRMATION", mode)
					models, executions, confirmations = 0, 0, 0
					request := protocol.Request{
						Model: "model", UserPrompt: "write a file",
						Tools: []loopprot.Tool{{Definition: aiprot.ToolDefinition{Name: "write"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
							executions++
							return aiprot.TextToolResult("written"), nil
						}}},
						Options: loopprot.RunOptions{ToolDenialPolicy: policy},
					}
					if enabled {
						request.Options.ConfirmTool = func(context.Context, loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error) {
							confirmations++
							return loopprot.ToolConfirmationDecision{Reason: "declined"}, nil
						}
					}
					result, err := assistant.Execute(context.Background(), request)
					if request.Options.ToolDenialPolicy != policy || (request.Options.ConfirmTool != nil) != enabled {
						t.Fatal("execution mutated caller options")
					}
					if len(result.ToolCalls) != 1 {
						t.Fatalf("result=%+v error=%v", result, err)
					}
					record := result.ToolCalls[0]
					if enabled {
						if confirmations != 1 || executions != 0 || record.Confirmation == nil || record.Confirmation.Approved || !record.Result.IsError {
							t.Fatalf("confirmation bypassed: record=%+v confirmations=%d executions=%d", record, confirmations, executions)
						}
					} else if confirmations != 0 || executions != 1 || record.Confirmation != nil {
						t.Fatalf("environment enabled confirmation: record=%+v confirmations=%d executions=%d", record, confirmations, executions)
					}
					if enabled && policy == loopprot.ToolDenialStop {
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
	}
}
