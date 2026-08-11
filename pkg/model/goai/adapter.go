package goai

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
	"github.com/zendev-sh/goai"
	"github.com/zendev-sh/goai/provider"
)

var defaultToolSchema = json.RawMessage(`{"type":"object"}`)

func buildGoAIOptions(ctx context.Context, request agent.ModelRequest) ([]goai.Option, *toolCallCollector, error) {
	systemPrompt, history := extractSystemPrompt(request.SystemPrompt, request.Messages)
	messages, err := toProviderMessages(history)
	if err != nil {
		return nil, nil, err
	}
	tools, err := toGoAITools(ctx, request.Tools)
	if err != nil {
		return nil, nil, err
	}
	collector := newToolCallCollector()
	temperature := defaultTemperature
	if request.Options.Temperature != nil {
		temperature = *request.Options.Temperature
	}
	options := []goai.Option{goai.WithTemperature(temperature)}
	if systemPrompt != "" {
		options = append(options, goai.WithSystem(systemPrompt))
	}

	messages = limitProviderMessages(messages, request.MaxMessages)
	if request.UserPrompt != "" {
		messages = append(messages, goai.UserMessage(request.UserPrompt))
	} else if systemPrompt != "" && len(messages) == 0 {
		messages = append(messages, goai.UserMessage(" "))
	}
	if err := validateProviderToolSequence(messages); err != nil {
		return nil, nil, fmt.Errorf("invalid model message history: %w", err)
	}
	if len(messages) > 0 {
		options = append(options, goai.WithMessages(messages...))
	}
	if len(tools) > 0 {
		options = append(options, goai.WithTools(tools...))
	}
	if request.Options.MaxSteps > 0 {
		options = append(options, goai.WithMaxSteps(request.Options.MaxSteps))
	}
	if request.Options.MaxOutputTokens != nil {
		options = append(options, goai.WithMaxOutputTokens(*request.Options.MaxOutputTokens))
	}
	if request.Options.OnBeforeToolExecute != nil {
		hook := request.Options.OnBeforeToolExecute
		options = append(options, goai.WithOnBeforeToolExecute(func(info goai.BeforeToolExecuteInfo) goai.BeforeToolExecuteResult {
			result := hook(agent.BeforeToolExecuteInfo{
				Ctx:        info.Ctx,
				ToolCallID: info.ToolCallID,
				ToolName:   info.ToolName,
				Step:       info.Step,
				Input:      copyRawMessage(info.Input),
			})
			return goai.BeforeToolExecuteResult{
				Skip:   result.Skip,
				Result: result.Result,
				Error:  result.Error,
				Ctx:    result.Ctx,
				Input:  copyRawMessage(result.Input),
			}
		}))
	}
	stopNames := make(map[string]struct{}, len(request.Options.StopOnToolNames))
	for _, tool := range request.Tools {
		if tool.Handler == nil {
			stopNames[tool.Name] = struct{}{}
		}
	}
	if len(request.Options.StopOnToolNames) > 0 {
		for _, name := range request.Options.StopOnToolNames {
			stopNames[name] = struct{}{}
		}
	}
	if len(stopNames) > 0 {
		options = append(options, goai.WithStopWhen(func(steps []goai.StepResult) bool {
			if len(steps) == 0 {
				return false
			}
			last := steps[len(steps)-1]
			for _, tc := range last.ToolCalls {
				if _, ok := stopNames[tc.Name]; ok {
					return true
				}
			}
			return false
		}))
	}
	options = append(options, buildToolEventOptions(ctx, request.Options.EventHandler, collector)...)
	if request.DebugSink != nil {
		sink := request.DebugSink
		options = append(options, goai.WithOnRequest(func(info goai.RequestInfo) {
			if err := dumpToSink(sink, info); err != nil {
				collector.recordRuntimeError(fmt.Errorf("debug sink failed: %w", err))
			}
		}))
	}
	return options, collector, nil
}

func limitProviderMessages(messages []provider.Message, maxMessages int) []provider.Message {
	if maxMessages <= 0 || len(messages) <= maxMessages {
		return messages
	}
	start := len(messages) - maxMessages
	for start > 0 && messages[start].Role == provider.RoleTool {
		start--
	}
	return messages[start:]
}

func validateProviderToolSequence(messages []provider.Message) error {
	pending := make(map[string]struct{})
	pendingMessage := -1

	for index, message := range messages {
		if len(pending) > 0 {
			if message.Role != provider.RoleTool {
				return fmt.Errorf(
					"assistant message %d tool calls %v must be followed by tool results before message %d (%s)",
					pendingMessage, sortedStringSet(pending), index, message.Role,
				)
			}
			resultCount := 0
			for _, part := range message.Content {
				if part.Type != provider.PartToolResult {
					continue
				}
				resultCount++
				if part.ToolCallID == "" {
					return fmt.Errorf("tool result in message %d has an empty tool_call_id", index)
				}
				if _, expected := pending[part.ToolCallID]; !expected {
					return fmt.Errorf("tool result %q in message %d has no pending tool call", part.ToolCallID, index)
				}
				delete(pending, part.ToolCallID)
			}
			if resultCount == 0 {
				return fmt.Errorf("tool message %d contains no tool results", index)
			}
			continue
		}

		if message.Role == provider.RoleTool {
			return fmt.Errorf("tool message %d has no preceding assistant tool call", index)
		}
		for _, part := range message.Content {
			if part.Type == provider.PartToolResult {
				return fmt.Errorf("tool result %q in non-tool message %d", part.ToolCallID, index)
			}
			if part.Type != provider.PartToolCall {
				continue
			}
			if message.Role != provider.RoleAssistant {
				return fmt.Errorf("tool call %q in non-assistant message %d", part.ToolCallID, index)
			}
			if part.ToolCallID == "" {
				return fmt.Errorf("tool call in assistant message %d has an empty id", index)
			}
			if _, duplicate := pending[part.ToolCallID]; duplicate {
				return fmt.Errorf("duplicate tool call id %q in assistant message %d", part.ToolCallID, index)
			}
			pending[part.ToolCallID] = struct{}{}
			pendingMessage = index
		}
	}

	if len(pending) > 0 {
		return fmt.Errorf("assistant message %d has tool calls without results: %v", pendingMessage, sortedStringSet(pending))
	}
	return nil
}

func sortedStringSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func extractSystemPrompt(fallback string, messages []agent.Message) (string, []agent.Message) {
	var prompts []string
	history := make([]agent.Message, 0, len(messages))
	for _, message := range messages {
		text, ok := message.(agent.TextMessage)
		if ok && text.Role == agent.RoleSystem {
			if content := strings.TrimSpace(text.Content); content != "" {
				prompts = append(prompts, content)
			}
			continue
		}
		history = append(history, message)
	}
	if len(prompts) == 0 {
		return fallback, history
	}
	return strings.Join(prompts, "\n\n"), history
}

func toProviderMessages(messages []agent.Message) ([]provider.Message, error) {
	result := make([]provider.Message, 0, len(messages))
	for index, message := range messages {
		converted, include, err := encodeMessageToProvider(message)
		if err != nil {
			return nil, fmt.Errorf("convert message at index %d: %w", index, err)
		}
		if include {
			result = append(result, converted)
		}
	}
	return result, nil
}

func toGoAITools(ctx context.Context, tools []agent.Tool) ([]goai.Tool, error) {
	result := make([]goai.Tool, 0, len(tools))
	for index, tool := range tools {
		schema, err := marshalToolParameters(tool.Parameters)
		if err != nil {
			return nil, fmt.Errorf("marshal tool parameters at index %d: %w", index, err)
		}
		goaiTool := goai.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
		}
		handler := tool.Handler
		toolName := tool.Name
		goaiTool.Execute = func(ctx context.Context, input json.RawMessage) (string, error) {
			if env := agent.ToolEnvFromContext(ctx); agent.HasToolEnv(toolName, env) {
				var params map[string]any
				if len(input) == 0 {
					params = make(map[string]any)
				} else {
					if err := json.Unmarshal(input, &params); err != nil {
						return "", fmt.Errorf("inject tool environment for %s: input must be a JSON object: %w", toolName, err)
					}
					if params == nil {
						return "", fmt.Errorf("inject tool environment for %s: input must be a JSON object", toolName)
					}
				}
				agent.ApplyToolEnv(toolName, params, env)
				modified, err := json.Marshal(params)
				if err != nil {
					return "", fmt.Errorf("inject tool environment for %s: %w", toolName, err)
				}
				input = modified
			}
			if handler == nil {
				return "", nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			output, err := handler.Handle(ctx, input)
			if err != nil {
				return output, agent.ContextErrorOr(ctx, err)
			}
			if err := ctx.Err(); err != nil {
				return output, err
			}
			return output, nil
		}
		result = append(result, goaiTool)
	}
	return result, nil
}

func marshalToolParameters(parameters any) (json.RawMessage, error) {
	if parameters == nil {
		return copyRawMessage(defaultToolSchema), nil
	}
	switch typedParameters := parameters.(type) {
	case json.RawMessage:
		return validateJSONObjectSchema(typedParameters)
	case []byte:
		return validateJSONObjectSchema(json.RawMessage(typedParameters))
	case string:
		return validateJSONObjectSchema(json.RawMessage(typedParameters))
	default:
		bytes, err := json.Marshal(typedParameters)
		if err != nil {
			return nil, err
		}
		return validateJSONObjectSchema(bytes)
	}
}

func validateJSONObjectSchema(schema json.RawMessage) (json.RawMessage, error) {
	if len(schema) == 0 {
		return copyRawMessage(defaultToolSchema), nil
	}
	var object map[string]any
	if err := json.Unmarshal(schema, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("tool schema must be a JSON object")
	}
	return copyRawMessage(schema), nil
}

func messagesFromProvider(messages []provider.Message) []agent.Message {
	result := make([]agent.Message, 0, len(messages))
	for _, message := range messages {
		text, hasText := textFromProviderParts(message.Content)
		if hasText {
			result = append(result, agent.TextMessage{Role: roleFromProvider(message.Role), Content: text})
		}
		toolCalls := toolCallsFromProviderParts(message.Content)
		if len(toolCalls) > 0 {
			result = append(result, agent.ToolCallMessage{ToolCalls: toolCalls})
		}
		toolResults := toolResultsFromProviderParts(message.Content)
		for _, toolResult := range toolResults {
			result = append(result, agent.ToolResultMessage{ToolResult: toolResult})
		}
	}
	return result
}

func textFromProviderParts(parts []provider.Part) (string, bool) {
	text := ""
	for _, part := range parts {
		if part.Type == provider.PartText && part.Text != "" {
			text += part.Text
		}
	}
	return text, text != ""
}

func toolCallsFromProviderParts(parts []provider.Part) []agent.ToolCall {
	result := make([]agent.ToolCall, 0)
	for _, part := range parts {
		if part.Type == provider.PartToolCall {
			result = append(result, agent.ToolCall{ID: part.ToolCallID, Name: part.ToolName, Input: copyRawMessage(part.ToolInput)})
		}
	}
	return result
}

func toolResultsFromProviderParts(parts []provider.Part) []agent.ToolResult {
	result := make([]agent.ToolResult, 0)
	for _, part := range parts {
		if part.Type == provider.PartToolResult {
			result = append(result, agent.ToolResult{ToolCallID: part.ToolCallID, ToolName: part.ToolName, Output: part.ToolOutput})
		}
	}
	return result
}

func roleFromProvider(role provider.Role) agent.Role {
	switch role {
	case provider.RoleSystem:
		return agent.RoleSystem
	case provider.RoleUser:
		return agent.RoleUser
	case provider.RoleAssistant:
		return agent.RoleAssistant
	case provider.RoleTool:
		return agent.RoleTool
	default:
		return agent.Role(role)
	}
}

func usageFromProvider(usage provider.Usage) *agent.Usage {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0 {
		return nil
	}
	return &agent.Usage{
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		TotalTokens:      usage.TotalTokens,
		ReasoningTokens:  usage.ReasoningTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
	}
}

func modelInfoFromProvider(meta provider.ResponseMetadata) *agent.ModelInfo {
	if meta.Model == "" && meta.ID == "" {
		return nil
	}
	return &agent.ModelInfo{ModelID: meta.Model, RequestID: meta.ID}
}
