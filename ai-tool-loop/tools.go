package aitoolloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tool-loop/utils"
)

func prepareTools(tools []protocol.Tool) (map[string]protocol.Tool, error) {
	byName := make(map[string]protocol.Tool, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Definition.Name)
		if name == "" || tool.Execute == nil {
			return nil, fmt.Errorf("tool %q requires a name and executor", name)
		}
		if _, exists := byName[name]; exists {
			return nil, fmt.Errorf("duplicate tool %q", name)
		}
		tool.Definition.Name = name
		if len(tool.Definition.InputSchema) == 0 {
			tool.Definition.InputSchema = json.RawMessage(`{"type":"object"}`)
		}
		var schema map[string]any
		if json.Unmarshal(tool.Definition.InputSchema, &schema) != nil || schema == nil {
			return nil, fmt.Errorf("tool %q input schema must be a JSON object", name)
		}
		if schemaType, exists := schema["type"]; exists && schemaType != "object" {
			return nil, fmt.Errorf("tool %q input schema type must be object", name)
		}
		byName[name] = tool
	}
	return byName, nil
}

func toolCalls(message aiprot.Message) ([]aiprot.ToolCall, error) {
	seen := make(map[string]bool)
	for _, part := range message.Parts {
		if part.Type != aiprot.ContentToolCall {
			continue
		}
		call := part.ToolCall
		if call == nil || strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" {
			return nil, errors.New("model returned a tool call without an id or name")
		}
		if seen[call.ID] {
			return nil, fmt.Errorf("model returned duplicate tool call id %q", call.ID)
		}
		seen[call.ID] = true
		var arguments map[string]any
		decodeErr := json.Unmarshal(call.Arguments, &arguments)
		if decodeErr != nil || arguments == nil {
			return nil, errors.Join(fmt.Errorf("tool %q arguments must be a JSON object; call_id=%q raw_arguments=%q", call.Name, call.ID, string(call.Arguments)), decodeErr)
		}
	}
	return message.ToolCalls(), nil
}

func executeTool(ctx context.Context, options protocol.RunOptions, runID, requestID string, step int, tools map[string]protocol.Tool, call aiprot.ToolCall) (record protocol.ToolCallRecord, err error) {
	record = protocol.ToolCallRecord{Step: step, Call: utils.Clone(call), StartedAt: time.Now()}
	var executionErr error
	defer func() {
		observedErr := errors.Join(executionErr, err)
		for _, failure := range []error{executionErr, err} {
			if failure != nil {
				record.Result.IsError = true
				record.Result.Content = append(record.Result.Content, aiprot.ContentPart{Type: aiprot.ContentText, Text: failure.Error()})
			}
		}
		record.Result.ToolCallID, record.Result.ToolName = call.ID, call.Name
		record.Result = utils.Clone(record.Result)
		record.FinishedAt = time.Now()
		endErr := notify(ctx, "tool end", options.Hooks.OnToolEnd, protocol.ToolEndInfo{
			RunID: runID, Step: step, RequestID: requestID, Call: record.Call, Result: record.Result,
			Confirmation: record.Confirmation, Error: observedErr, StartedAt: record.StartedAt, FinishedAt: record.FinishedAt,
			Duration: record.FinishedAt.Sub(record.StartedAt),
		})
		if endErr != nil {
			err = errors.Join(err, executionErr, endErr)
		}
		err = withContextError(ctx, err)
		if combined := withContextError(ctx, errors.Join(observedErr, endErr)); combined != nil {
			record.Error = combined.Error()
		}
	}()
	if err = notify(ctx, "tool start", options.Hooks.OnToolStart, protocol.ToolStartInfo{
		RunID: runID, Step: step, RequestID: requestID, Call: record.Call, StartedAt: record.StartedAt,
	}); err != nil {
		return record, err
	}
	if err = ctx.Err(); err != nil {
		return record, err
	}
	if tool, exists := tools[call.Name]; exists {
		if options.ConfirmTool != nil {
			decision, confirmationErr := confirmTool(ctx, options.ConfirmTool, protocol.ToolConfirmationInfo{
				RunID: runID, Step: step, RequestID: requestID, Call: call, Definition: tool.Definition,
			})
			if confirmationErr != nil {
				return record, confirmationErr
			}
			record.Confirmation = &decision
			if err = ctx.Err(); err != nil {
				return record, err
			}
			if !decision.Approved {
				denied := &protocol.ToolDeniedError{ToolName: call.Name, ToolCallID: call.ID, Reason: decision.Reason}
				if options.ToolDenialPolicy == protocol.ToolDenialStop {
					return record, denied
				}
				executionErr = denied
				return record, nil
			}
		}
		record.Result, executionErr = safeExecute(ctx, tool.Execute, utils.Clone(call.Arguments))
	} else {
		executionErr = fmt.Errorf("unknown tool %q", call.Name)
	}
	return record, ctx.Err()
}

func confirmTool(ctx context.Context, confirm func(context.Context, protocol.ToolConfirmationInfo) (protocol.ToolConfirmationDecision, error), info protocol.ToolConfirmationInfo) (decision protocol.ToolConfirmationDecision, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tool confirmation panicked: %v", recovered)
		}
	}()
	decision, err = confirm(ctx, utils.Clone(info))
	if err != nil {
		return decision, fmt.Errorf("tool confirmation: %w", err)
	}
	return decision, nil
}

func safeExecute(ctx context.Context, execute func(context.Context, json.RawMessage) (aiprot.ToolResult, error), arguments json.RawMessage) (result aiprot.ToolResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tool executor panicked: %v", recovered)
		}
	}()
	return execute(ctx, arguments)
}
