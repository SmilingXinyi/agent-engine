package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SmilingXinyi/agent-engine/agent"
	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

type confirmationProvider struct {
	generate func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error)
}

func (provider confirmationProvider) Name() string { return "confirmation-test" }
func (provider confirmationProvider) Generate(ctx context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
	return provider.generate(ctx, request)
}

func TestExecutionOptionsConfirmToolsThroughAgent(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		mode          string
		input         string
		wantExecuted  bool
		wantConfirmed bool
		wantStopped   bool
		wantEOF       bool
	}{
		{name: "empty preserves unattended execution", wantExecuted: true},
		{name: "off skips terminal", mode: "off", wantExecuted: true},
		{name: "whitespace off skips terminal", mode: " off ", wantExecuted: true},
		{name: "whitespace empty skips terminal", mode: "  ", wantExecuted: true},
		{name: "trimmed stop", mode: " stop ", input: "n\n", wantConfirmed: true, wantStopped: true},
		{name: "approve y", mode: "continue", input: "y\n", wantConfirmed: true, wantExecuted: true},
		{name: "approve yes with whitespace", mode: "stop", input: " YES \r\n", wantConfirmed: true, wantExecuted: true},
		{name: "deny and continue", mode: "continue", input: "n\n", wantConfirmed: true},
		{name: "deny and stop", mode: "stop", input: "n\n", wantConfirmed: true, wantStopped: true},
		{name: "blank denies", mode: "continue", input: "\n", wantConfirmed: true},
		{name: "other input denies", mode: "stop", input: "unknown\n", wantConfirmed: true, wantStopped: true},
		{name: "EOF stops continue mode", mode: "continue", wantStopped: true, wantEOF: true},
		{name: "unterminated answer never approves", mode: "stop", input: "y", wantStopped: true, wantEOF: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var output bytes.Buffer
			input := io.NopCloser(strings.NewReader(scenario.input))
			defer input.Close()
			modelCalls, executions := 0, 0
			call := aiprot.ToolCall{ID: "click-id", Name: "desktop_click", Arguments: json.RawMessage(`{"x":10,"y":20}`)}
			provider := confirmationProvider{generate: func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				modelCalls++
				if modelCalls == 1 {
					return aiprot.ModelResponse{Message: aiprot.Message{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall, ToolCall: &call}}}}, nil
				}
				if modelCalls > 2 {
					t.Fatal("unexpected repeated model call")
				}
				result := request.Messages[2].ToolResults()[0]
				if result.ToolCallID != call.ID || result.IsError == scenario.wantExecuted {
					t.Fatalf("tool result=%+v", result)
				}
				return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done")}, nil
			}}
			runOptions, err := executionOptions(scenario.mode, input, &output, 3)
			if err != nil {
				t.Fatal(err)
			}
			// Changes to process environment must not override already resolved options.
			t.Setenv("AGENT_TOOL_CONFIRMATION", "off")
			request := agentprot.Request{
				Model: "test-model", UserPrompt: "click",
				Tools: []loopprot.Tool{{Definition: aiprot.ToolDefinition{Name: call.Name}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
					executions++
					return aiprot.TextToolResult("clicked"), nil
				}}},
				Options: runOptions,
			}
			result, err := agent.New(ai.NewClient(provider, aiprot.Hooks{})).Execute(context.Background(), request)
			wantExecutions := 0
			if scenario.wantExecuted {
				wantExecutions = 1
			}
			if executions != wantExecutions || len(result.ToolCalls) != 1 {
				t.Fatalf("executions=%d result=%+v error=%v", executions, result, err)
			}
			if scenario.wantStopped {
				if err == nil || result.StopReason != loopprot.StopError || modelCalls != 1 {
					t.Fatalf("result=%+v error=%v modelCalls=%d", result, err, modelCalls)
				}
				if scenario.wantEOF {
					if !errors.Is(err, io.EOF) {
						t.Fatal(err)
					}
				} else {
					var denied *loopprot.ToolDeniedError
					if !errors.As(err, &denied) {
						t.Fatal(err)
					}
				}
			} else if err != nil || result.StopReason != loopprot.StopNatural || modelCalls != 2 {
				t.Fatalf("result=%+v error=%v modelCalls=%d", result, err, modelCalls)
			}
			decision := result.ToolCalls[0].Confirmation
			if (decision != nil) != scenario.wantConfirmed {
				t.Fatalf("confirmation=%+v", decision)
			}
			if decision != nil && (decision.Approved != scenario.wantExecuted || (!decision.Approved && decision.Reason == "")) {
				t.Fatalf("decision=%+v", decision)
			}
			mode := strings.TrimSpace(scenario.mode)
			prompted := mode != "" && mode != "off"
			if strings.Contains(output.String(), "[tool confirmation]") != prompted || strings.Contains(output.String(), "[tool decision]") != scenario.wantConfirmed {
				t.Fatalf("log=%s", &output)
			}
			if prompted && (!strings.Contains(output.String(), call.Name) || !strings.Contains(output.String(), `"x": 10`)) {
				t.Fatalf("prompt missing tool details: %s", &output)
			}
		})
	}
}

func TestTerminalConfirmationPreservesBufferedAnswers(t *testing.T) {
	confirm := terminalConfirmation(io.NopCloser(strings.NewReader("y\nn\n")), io.Discard)
	for _, approved := range []bool{true, false} {
		decision, err := confirm(context.Background(), loopprot.ToolConfirmationInfo{})
		if err != nil || decision.Approved != approved {
			t.Fatalf("decision=%+v error=%v", decision, err)
		}
	}
}

type confirmationWriter func([]byte) (int, error)

func (write confirmationWriter) Write(data []byte) (int, error) { return write(data) }

func TestTerminalConfirmationCancellationClosesPendingInput(t *testing.T) {
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	prompted := make(chan struct{}, 1)
	output := confirmationWriter(func(data []byte) (int, error) {
		prompted <- struct{}{}
		return len(data), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		decision, err := terminalConfirmation(input, output)(ctx, loopprot.ToolConfirmationInfo{})
		if decision.Approved {
			done <- errors.New("cancelled confirmation approved")
			return
		}
		done <- err
	}()
	select {
	case <-prompted:
	case <-time.After(3 * time.Second):
		t.Fatal("confirmation did not prompt")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("confirmation did not cancel")
	}
	writeDone := make(chan error, 1)
	go func() { _, err := writer.Write([]byte("late answer\n")); writeDone <- err }()
	select {
	case err := <-writeDone:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("pending input was not closed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pending input read was not released")
	}
}

func TestTerminalConfirmationDoesNotReadAfterCancellationOrPromptFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	decision, err := terminalConfirmation(io.NopCloser(strings.NewReader("y\n")), &output)(ctx, loopprot.ToolConfirmationInfo{})
	if !errors.Is(err, context.Canceled) || decision.Approved || output.Len() != 0 {
		t.Fatalf("decision=%+v error=%v output=%s", decision, err, &output)
	}
	failure := errors.New("prompt write failed")
	brokenOutput := confirmationWriter(func([]byte) (int, error) { return 0, failure })
	decision, err = terminalConfirmation(io.NopCloser(strings.NewReader("y\n")), brokenOutput)(context.Background(), loopprot.ToolConfirmationInfo{})
	if !errors.Is(err, failure) || decision.Approved {
		t.Fatalf("decision=%+v error=%v", decision, err)
	}
}

func TestExecutionOptionsRejectInvalidModeBeforeExecution(t *testing.T) {
	var output bytes.Buffer
	options, err := executionOptions("invalid", io.NopCloser(strings.NewReader("y\n")), &output, 3)
	if err == nil || !strings.Contains(err.Error(), "invalid AGENT_TOOL_CONFIRMATION") || options.ConfirmTool != nil || output.Len() != 0 {
		t.Fatalf("options=%+v error=%v output=%s", options, err, &output)
	}
}

func TestRunRejectsInvalidConfirmationBeforeConnecting(t *testing.T) {
	directory := t.TempDir()
	urlPath := filepath.Join(directory, "download-url.txt")
	if err := os.WriteFile(urlPath, []byte("https://example.com/file.zip"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_TOOL_CONFIRMATION", "invalid")
	// Missing model and MCP configuration must never be used for this invalid mode.
	t.Setenv("AGENT_LLM_MODEL", "")
	t.Setenv("AGENT_MODEL", "")
	err := run(context.Background(), options{
		urlPath: urlPath, envPath: filepath.Join(directory, ".env"),
		configPath: filepath.Join(directory, "missing-mcp.json"),
		maxSteps:   3, historyMessages: 10, timeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid AGENT_TOOL_CONFIRMATION") {
		t.Fatalf("error=%v", err)
	}
}
