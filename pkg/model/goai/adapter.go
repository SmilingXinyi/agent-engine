package goai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
	"github.com/zendev-sh/goai"
	"github.com/zendev-sh/goai/provider"
)

var defaultToolSchema = json.RawMessage(`{"type":"object"}`)

func buildGoAIOptions(ctx context.Context, request agent.ModelRequest) ([]goai.Option, *toolCallCollector, error) {
	messages, err := toProviderMessages(request.Messages)
	if err != nil {
		return nil, nil, err
	}
	tools, err := toGoAITools(request.Tools)
	if err != nil {
		return nil, nil, err
	}
	collector := newToolCallCollector()
	temperature := defaultTemperature
	if request.Options.Temperature != nil {
		temperature = *request.Options.Temperature
	}
	options := []goai.Option{goai.WithTemperature(temperature)}
	if request.SystemPrompt != "" {
		options = append(options, goai.WithSystem(request.SystemPrompt))
	}
	if request.UserPrompt != "" {
		messages = append(messages, goai.UserMessage(request.UserPrompt))
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
	if providerOptions := buildThinkingProviderOptions(request.Options); providerOptions != nil {
		options = append(options, goai.WithProviderOptions(providerOptions))
	}
	options = append(options, buildToolEventOptions(ctx, request.Options.EventHandler, collector)...)
	return options, collector, nil
}

func buildThinkingProviderOptions(options agent.RunOptions) map[string]any {
	if options.Thinking == nil || !options.Thinking.Enabled {
		return nil
	}
	thinking := map[string]any{"type": "enabled"}
	if options.Thinking.BudgetTokens != nil {
		thinking["budget_tokens"] = *options.Thinking.BudgetTokens
	}
	return map[string]any{"thinking": thinking}
}

func buildToolEventOptions(ctx context.Context, handler agent.EventHandler, collector *toolCallCollector) []goai.Option {
	return []goai.Option{
		goai.WithOnToolCallStart(func(info goai.ToolCallStartInfo) {
			event := agent.ToolCallStartEvent{
				ToolCallID: info.ToolCallID,
				ToolName:   info.ToolName,
				Input:      copyRawMessage(info.Input),
				StartedAt:  time.Now(),
			}
			collector.recordStart(event)
			if handler != nil {
				_ = handler.OnToolCallStart(ctx, event)
			}
		}),
		goai.WithOnToolCall(func(info goai.ToolCallInfo) {
			startedAt := info.StartTime
			finishedAt := time.Now()
			if !startedAt.IsZero() && info.Duration > 0 {
				finishedAt = startedAt.Add(info.Duration)
			}
			event := agent.ToolCallFinishEvent{
				ToolCallID: info.ToolCallID,
				ToolName:   info.ToolName,
				Output:     info.Output,
				Error:      info.Error,
				StartedAt:  startedAt,
				FinishedAt: finishedAt,
			}
			collector.recordFinish(event)
			if handler != nil {
				_ = handler.OnToolCallFinish(ctx, event)
			}
		}),
	}
}

func toProviderMessages(messages []agent.Message) ([]provider.Message, error) {
	result := make([]provider.Message, 0, len(messages))
	for index, message := range messages {
		switch typedMessage := message.(type) {
		case agent.TextMessage:
			converted, ok, err := textMessageToProvider(typedMessage)
			if err != nil {
				return nil, fmt.Errorf("convert text message at index %d: %w", index, err)
			}
			if ok {
				result = append(result, converted)
			}
		case *agent.TextMessage:
			if typedMessage == nil {
				return nil, fmt.Errorf("nil text message at index %d", index)
			}
			converted, ok, err := textMessageToProvider(*typedMessage)
			if err != nil {
				return nil, fmt.Errorf("convert text message at index %d: %w", index, err)
			}
			if ok {
				result = append(result, converted)
			}
		case agent.ToolCallMessage:
			result = append(result, toolCallMessageToProvider(typedMessage))
		case *agent.ToolCallMessage:
			if typedMessage == nil {
				return nil, fmt.Errorf("nil tool call message at index %d", index)
			}
			result = append(result, toolCallMessageToProvider(*typedMessage))
		case agent.ToolResultMessage:
			result = append(result, toolResultMessageToProvider(typedMessage))
		case *agent.ToolResultMessage:
			if typedMessage == nil {
				return nil, fmt.Errorf("nil tool result message at index %d", index)
			}
			result = append(result, toolResultMessageToProvider(*typedMessage))
		default:
			return nil, fmt.Errorf("unsupported message type at index %d", index)
		}
	}
	return result, nil
}

func textMessageToProvider(message agent.TextMessage) (provider.Message, bool, error) {
	switch message.Role {
	case agent.RoleUser:
		return goai.UserMessage(message.Content), true, nil
	case agent.RoleAssistant:
		return goai.AssistantMessage(message.Content), true, nil
	case agent.RoleSystem:
		return provider.Message{}, false, nil
	default:
		return provider.Message{}, false, fmt.Errorf("unsupported text message role: %s", message.Role)
	}
}

func toolCallMessageToProvider(message agent.ToolCallMessage) provider.Message {
	parts := make([]provider.Part, 0, len(message.ToolCalls))
	for _, toolCall := range message.ToolCalls {
		parts = append(parts, provider.Part{
			Type:       provider.PartToolCall,
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			ToolInput:  copyRawMessage(toolCall.Input),
		})
	}
	return provider.Message{Role: provider.RoleAssistant, Content: parts}
}

func toolResultMessageToProvider(message agent.ToolResultMessage) provider.Message {
	return goai.ToolMessage(message.ToolResult.ToolCallID, message.ToolResult.ToolName, message.ToolResult.Output)
}

func toGoAITools(tools []agent.Tool) ([]goai.Tool, error) {
	result := make([]goai.Tool, 0, len(tools))
	for index, tool := range tools {
		schema, err := marshalToolParameters(tool.Parameters)
		if err != nil {
			return nil, fmt.Errorf("marshal tool parameters at index %d: %w", index, err)
		}
		handler := tool.Handler
		result = append(result, goai.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
			Execute: func(ctx context.Context, input json.RawMessage) (string, error) {
				return handler.Handle(ctx, input)
			},
		})
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

// validateJSONObjectSchema accepts schemas that are valid JSON objects.
func validateJSONObjectSchema(schema json.RawMessage) (json.RawMessage, error) {
	if len(schema) == 0 {
		return copyRawMessage(defaultToolSchema), nil
	}
	var object map[string]any
	if err := json.Unmarshal(schema, &object); err != nil {
		return nil, err
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
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
	}
}
