package toolloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/tool-loop/utils"
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
		if json.Unmarshal(call.Arguments, &arguments) != nil || arguments == nil {
			return nil, fmt.Errorf("tool %q arguments must be a JSON object", call.Name)
		}
	}
	return message.ToolCalls(), nil
}

func executeTool(ctx context.Context, hooks protocol.Hooks, runID, requestID string, step int, tools map[string]protocol.Tool, call aiprot.ToolCall) (record protocol.ToolCallRecord, err error) {
	record = protocol.ToolCallRecord{Step: step, Call: utils.Clone(call), StartedAt: time.Now()}
	var executionErr error
	defer func() {
		if err != nil {
			record.Result.IsError = true
			record.Result.Content = append(record.Result.Content, aiprot.ContentPart{Type: aiprot.ContentText, Text: err.Error()})
		}
		record.Result.ToolCallID, record.Result.ToolName = call.ID, call.Name
		record.Result = utils.Clone(record.Result)
		record.FinishedAt = time.Now()
		observedErr := errors.Join(executionErr, err)
		endErr := notify(ctx, "tool end", hooks.OnToolEnd, protocol.ToolEndInfo{
			RunID: runID, Step: step, RequestID: requestID, Call: record.Call, Result: record.Result,
			Error: observedErr, StartedAt: record.StartedAt, FinishedAt: record.FinishedAt,
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
	if err = notify(ctx, "tool start", hooks.OnToolStart, protocol.ToolStartInfo{
		RunID: runID, Step: step, RequestID: requestID, Call: record.Call, StartedAt: record.StartedAt,
	}); err != nil {
		return record, err
	}
	if err = ctx.Err(); err != nil {
		return record, err
	}
	if tool, exists := tools[call.Name]; exists {
		record.Result, executionErr = safeExecute(ctx, tool.Execute, utils.Clone(call.Arguments))
	} else {
		executionErr = fmt.Errorf("unknown tool %q", call.Name)
	}
	if executionErr != nil {
		record.Result.IsError = true
		record.Result.Content = append(record.Result.Content, aiprot.ContentPart{Type: aiprot.ContentText, Text: executionErr.Error()})
	}
	return record, ctx.Err()
}

func safeExecute(ctx context.Context, execute func(context.Context, json.RawMessage) (aiprot.ToolResult, error), arguments json.RawMessage) (result aiprot.ToolResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tool executor panicked: %v", recovered)
		}
	}()
	return execute(ctx, arguments)
}
