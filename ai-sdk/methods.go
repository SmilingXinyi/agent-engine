package ai

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/utils"
)

// TextMessage builds a Message with a single text ContentPart.
func TextMessage(role Role, text string) Message {
	return Message{Role: role, Parts: []ContentPart{{Type: ContentText, Text: text}}}
}

// Text concatenates all text ContentParts in the Message.
func (message Message) Text() string {
	var result strings.Builder
	for _, part := range message.Parts {
		if part.Type == ContentText {
			result.WriteString(part.Text)
		}
	}
	return result.String()
}

// Reasoning concatenates all reasoning ContentParts in the Message.
func (message Message) Reasoning() string {
	var result strings.Builder
	for _, part := range message.Parts {
		if part.Type == ContentReasoning {
			result.WriteString(part.Text)
		}
	}
	return result.String()
}

// ToolCalls extracts all tool call ContentParts from the Message.
func (message Message) ToolCalls() []ToolCall {
	var calls []ToolCall
	for _, part := range message.Parts {
		if part.Type == ContentToolCall && part.ToolCall != nil {
			calls = append(calls, cloneToolCall(*part.ToolCall))
		}
	}
	return calls
}

// ToolResults extracts all tool result ContentParts from the Message.
func (message Message) ToolResults() []ToolResult {
	var results []ToolResult
	for _, part := range message.Parts {
		if part.Type == ContentToolResult && part.ToolResult != nil {
			results = append(results, cloneToolResult(*part.ToolResult))
		}
	}
	return results
}

// TextToolResult builds a ToolResult with a single text ContentPart.
func TextToolResult(text string) ToolResult {
	return ToolResult{Content: []ContentPart{{Type: ContentText, Text: text}}}
}

// Text concatenates all text ContentParts in the ToolResult.
func (result ToolResult) Text() string {
	var text strings.Builder
	for _, part := range result.Content {
		if part.Type == ContentText {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

// OnEvent invokes the underlying function, treating a nil receiver as a no-op.
func (sink EventSinkFunc) OnEvent(ctx context.Context, event StreamEvent) error {
	if sink == nil {
		return nil
	}
	return sink(ctx, event)
}

func cloneToolCall(call ToolCall) ToolCall {
	call.Arguments = append(json.RawMessage(nil), call.Arguments...)
	return call
}

func cloneToolResult(result ToolResult) ToolResult {
	result.Content = cloneContentParts(result.Content)
	result.Metadata = utils.CloneMap(result.Metadata)
	return result
}

func cloneContentParts(parts []ContentPart) []ContentPart {
	result := make([]ContentPart, len(parts))
	for index, part := range parts {
		result[index] = part
		result[index].Metadata = utils.CloneMap(part.Metadata)
		result[index].Data = append(json.RawMessage(nil), part.Data...)
		if part.Media != nil {
			media := *part.Media
			result[index].Media = &media
		}
		if part.ToolCall != nil {
			call := cloneToolCall(*part.ToolCall)
			result[index].ToolCall = &call
		}
		if part.ToolResult != nil {
			toolResult := cloneToolResult(*part.ToolResult)
			result[index].ToolResult = &toolResult
		}
	}
	return result
}
