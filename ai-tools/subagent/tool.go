// Package subagent exposes a preconfigured Agent as a synchronous tool.
package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SmilingXinyi/agent-engine/agent"
	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/utils"
)

const (
	maxArgumentBytes = 16 << 10
	maxResultBytes   = 2 << 10
)

// NewTool delegates to child using template's configuration. Each invocation
// replaces the user prompt and starts with empty history. The caller keeps the
// template immutable and owns approval and resource policy.
func NewTool(child *agent.Agent, name, description string, template agentprot.Request, timeout time.Duration) (loopprot.Tool, error) {
	if child == nil || strings.TrimSpace(name) == "" || strings.TrimSpace(description) == "" ||
		strings.TrimSpace(template.Model) == "" || timeout <= 0 {
		return loopprot.Tool{}, errors.New("subagent requires an agent, name, description, model and positive timeout")
	}
	template.History = nil
	return loopprot.Tool{
		Definition: aiprot.ToolDefinition{
			Name: name, Description: description,
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"task": {"type": "string", "description": "A self-contained task and expected deliverable"},
					"context": {"type": "string", "description": "Relevant paths, constraints, and facts needed to do the task"}
				},
				"required": ["task"],
				"additionalProperties": false
			}`),
		},
		Execute: func(ctx context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
			if err := ctx.Err(); err != nil {
				return aiprot.ToolResult{}, err
			}
			var input struct {
				Task    string          `json:"task"`
				Context json.RawMessage `json:"context"`
			}
			if len(arguments) > maxArgumentBytes {
				return toolResult("invalid_arguments", "Delegation arguments exceed 16 KiB."), nil
			}
			if err := utils.DecodeObject(arguments, &input); err != nil || strings.TrimSpace(input.Task) == "" {
				return toolResult("invalid_arguments", "Provide a nonblank task and optional context string."), nil
			}
			var background *string
			if input.Context != nil {
				if err := json.Unmarshal(input.Context, &background); err != nil || background == nil {
					return toolResult("invalid_arguments", "Context must be a string when supplied."), nil
				}
			}
			request := template
			request.UserPrompt = "Task:\n" + input.Task
			if background != nil && strings.TrimSpace(*background) != "" {
				request.UserPrompt += "\n\nContext:\n" + *background
			}
			childCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			run, err := child.Execute(childCtx, request)
			if parentErr := ctx.Err(); parentErr != nil {
				return withRunMetadata(toolResult("cancelled", "The parent run was cancelled."), run), parentErr
			}
			return withRunMetadata(runResult(childCtx, run, err), run), nil
		},
	}, nil
}

func runResult(ctx context.Context, run loopprot.Result, err error) aiprot.ToolResult {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return toolResult("timeout", "The subagent reached its deadline.")
	}
	if err != nil {
		return toolResult("failed", "The subagent failed during execution.")
	}
	if run.StopReason == loopprot.StopMaxSteps {
		return toolResult("max_steps", "The subagent reached its model step limit.")
	}
	text := strings.TrimSpace(run.LastResponse.Message.Text())
	switch run.LastResponse.FinishReason {
	case aiprot.FinishLength:
		return toolResult("incomplete", text)
	case aiprot.FinishContentFilter, aiprot.FinishError, aiprot.FinishToolCalls:
		return toolResult("incomplete", "The subagent did not produce a final answer.")
	}
	if run.StopReason != loopprot.StopNatural || text == "" {
		return toolResult("incomplete", "The subagent did not produce a final answer.")
	}
	return toolResult("completed", text)
}

func toolResult(status, text string) aiprot.ToolResult {
	header := "Status: " + status + "\n"
	if len(header)+len(text) > maxResultBytes {
		status = "truncated"
		header = "Status: truncated\n"
		end := min(len(text), maxResultBytes-len(header))
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end]
	}
	result := aiprot.TextToolResult(header + text)
	result.IsError = status != "completed"
	return result
}

func withRunMetadata(result aiprot.ToolResult, run loopprot.Result) aiprot.ToolResult {
	if run.RunID == "" {
		return result
	}
	result.Metadata = map[string]any{
		"subagent_run_id": run.RunID,
		"subagent_usage": map[string]any{
			"input_tokens":       run.Usage.InputTokens,
			"output_tokens":      run.Usage.OutputTokens,
			"total_tokens":       run.Usage.TotalTokens,
			"reasoning_tokens":   run.Usage.ReasoningTokens,
			"cache_read_tokens":  run.Usage.CacheReadTokens,
			"cache_write_tokens": run.Usage.CacheWriteTokens,
		},
	}
	return result
}
