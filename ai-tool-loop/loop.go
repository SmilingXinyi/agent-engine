// Package aitoolloop runs sequential model/tool calls through ai-sdk.
package aitoolloop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

type Loop struct{ client *ai.Client }

func New(client *ai.Client) *Loop { return &Loop{client: client} }

// Run owns one execution. StopMaxSteps is a normal bounded stop with nil error;
// runtime failures return StopError and all available partial records.
func (loop *Loop) Run(ctx context.Context, request protocol.Request) (result protocol.Result, err error) {
	result.StopReason = protocol.StopError
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if loop == nil || loop.client == nil {
		return result, errors.New("ai-tool-loop client is required")
	}
	if strings.TrimSpace(request.Model) == "" || len(request.Messages) == 0 {
		return result, errors.New("model and at least one message are required")
	}
	if request.Options.MaxSteps < 0 {
		return result, errors.New("max steps must not be negative")
	}
	if request.Options.MaxHistoryRounds < 0 {
		return result, errors.New("max history rounds must not be negative")
	}
	switch request.Options.ToolDenialPolicy {
	case "", protocol.ToolDenialContinue, protocol.ToolDenialStop:
	default:
		return result, fmt.Errorf("invalid tool denial policy %q", request.Options.ToolDenialPolicy)
	}
	request = request.Clone()
	tools, err := prepareTools(request.Tools)
	if err != nil {
		return result, err
	}
	for index, tool := range request.Tools {
		request.Tools[index] = tools[strings.TrimSpace(tool.Definition.Name)]
	}
	if request.Options.MaxSteps == 0 {
		request.Options.MaxSteps = protocol.DefaultMaxSteps
	}
	if request.Options.MaxHistoryRounds == 0 {
		request.Options.MaxHistoryRounds = protocol.DefaultMaxHistoryRounds
	}
	if request.Options.ToolDenialPolicy == "" {
		request.Options.ToolDenialPolicy = protocol.ToolDenialContinue
	}
	result.RunID, err = newID("run")
	if err != nil {
		return result, err
	}
	// Elements remain read-only inside the run; a separate slice keeps appends
	// from changing the initial request observed by run hooks.
	result.Messages = slices.Clone(request.Messages)
	startedAt := time.Now()
	defer func() {
		if err != nil {
			result.StopReason = protocol.StopError
		}
		finishedAt := time.Now()
		endErr := notify(ctx, "run end", request.Options.Hooks.OnRunEnd, protocol.RunEndInfo{
			RunID: result.RunID, Request: request, Result: result, Error: err,
			StartedAt: startedAt, FinishedAt: finishedAt, Duration: finishedAt.Sub(startedAt),
		})
		err = withContextError(ctx, errors.Join(err, endErr))
		if err != nil {
			result.StopReason = protocol.StopError
		}
	}()
	if err = notify(ctx, "run start", request.Options.Hooks.OnRunStart, protocol.RunStartInfo{
		RunID: result.RunID, Request: request, StartedAt: startedAt,
	}); err != nil {
		return result, err
	}
	for step := 1; step <= request.Options.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		stop, stepErr := loop.step(ctx, request, tools, &result, step)
		if stepErr != nil || stop {
			return result, stepErr
		}
	}
	result.StopReason = protocol.StopMaxSteps
	return result, nil
}

func (loop *Loop) step(ctx context.Context, request protocol.Request, tools map[string]protocol.Tool, result *protocol.Result, step int) (stop bool, err error) {
	requestID, err := newID("req")
	if err != nil {
		return true, err
	}
	modelRequest := request.ModelCall(result.Messages, result.RunID, requestID, step)
	index := len(result.Steps)
	result.Steps = append(result.Steps, protocol.StepResult{Step: step, RequestID: requestID, StartedAt: time.Now()})
	toolStart := len(result.ToolCalls)
	defer func() {
		if err != nil {
			result.StopReason = protocol.StopError
		}
		result.Steps[index].FinishedAt = time.Now()
		current := result.Steps[index]
		endErr := notify(ctx, "step end", request.Options.Hooks.OnStepEnd, protocol.StepEndInfo{
			RunID: result.RunID, Step: step, RequestID: requestID, Request: modelRequest,
			Result: current, ToolCalls: result.ToolCalls[toolStart:], Error: err,
			StartedAt: current.StartedAt, FinishedAt: current.FinishedAt, Duration: current.FinishedAt.Sub(current.StartedAt),
		})
		err = withContextError(ctx, errors.Join(err, endErr))
	}()
	if err = notify(ctx, "step start", request.Options.Hooks.OnStepStart, protocol.StepStartInfo{
		RunID: result.RunID, Step: step, RequestID: requestID, Request: modelRequest, StartedAt: result.Steps[index].StartedAt,
	}); err != nil {
		return true, err
	}
	if err = ctx.Err(); err != nil {
		return true, err
	}
	// Client.Generate snapshots its input before invoking the provider.
	response, callErr := loop.client.Generate(ctx, modelRequest)
	if response.Message.Role == "" {
		response.Message.Role = aiprot.RoleAssistant
	}
	result.LastResponse = response.Clone()
	result.Steps[index].Response = response.Clone()
	addUsage(&result.Usage, response.Usage)
	if len(response.Message.Parts) > 0 {
		result.Messages = append(result.Messages, response.Message.Clone())
	}
	if callErr != nil || ctx.Err() != nil {
		return true, errors.Join(callErr, ctx.Err())
	}
	if len(response.Message.Parts) == 0 {
		return true, errors.New("model response has no content parts")
	}
	calls, err := toolCalls(response.Message)
	if err != nil {
		return true, fmt.Errorf("validate tool calls: step=%d request=%s finish_reason=%s: %w", step, requestID, response.FinishReason, err)
	}
	if len(calls) == 0 {
		result.StopReason = protocol.StopNatural
		return true, nil
	}
	if step == request.Options.MaxSteps {
		result.StopReason = protocol.StopMaxSteps
		return true, nil
	}
	for _, call := range calls {
		if err = ctx.Err(); err != nil {
			return true, err
		}
		record, toolErr := executeTool(ctx, request.Options, result.RunID, requestID, step, tools, call)
		// executeTool returns an owned record; callbacks have separate snapshots.
		result.ToolCalls = append(result.ToolCalls, record)
		toolResult := record.Result.Clone()
		result.Messages = append(result.Messages, aiprot.Message{Role: aiprot.RoleTool, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolResult, ToolResult: &toolResult}}})
		if toolErr != nil {
			return true, toolErr
		}
	}
	return false, nil
}

func addUsage(total *aiprot.Usage, current aiprot.Usage) {
	total.InputTokens += current.InputTokens
	total.OutputTokens += current.OutputTokens
	total.TotalTokens += current.TotalTokens
	total.ReasoningTokens += current.ReasoningTokens
	total.CacheReadTokens += current.CacheReadTokens
	total.CacheWriteTokens += current.CacheWriteTokens
}

func withContextError(ctx context.Context, err error) error {
	if ctx.Err() != nil && !errors.Is(err, ctx.Err()) {
		return errors.Join(err, ctx.Err())
	}
	return err
}
