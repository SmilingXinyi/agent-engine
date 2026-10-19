package protocol

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/utils"
)

func (failure *Error) Error() string {
	if failure.Provider == "" {
		return fmt.Sprintf("%s: %s", failure.Code, failure.Message)
	}
	return fmt.Sprintf("%s %s: %s", failure.Provider, failure.Code, failure.Message)
}

func (failure *Error) Unwrap() error { return failure.Cause }

// Clone copies a message's content and JSON-shaped input extensions.
func (message Message) Clone() Message {
	message.Parts = cloneContentParts(message.Parts)
	message.Metadata = utils.CloneJSONMap(message.Metadata)
	message.ProviderOptions = utils.CloneJSONMap(message.ProviderOptions)
	return message
}

// Clone returns a snapshot including nested extension maps and raw JSON.
func (request ModelRequest) Clone() ModelRequest {
	request.Messages = append([]Message(nil), request.Messages...)
	for index := range request.Messages {
		request.Messages[index] = request.Messages[index].Clone()
	}
	request.Tools = append([]ToolDefinition(nil), request.Tools...)
	for index := range request.Tools {
		request.Tools[index].InputSchema = append(json.RawMessage(nil), request.Tools[index].InputSchema...)
	}
	request.ProviderOptions = utils.CloneJSONMap(request.ProviderOptions)
	request.Metadata = utils.CloneMap(request.Metadata)
	if request.Temperature != nil {
		value := *request.Temperature
		request.Temperature = &value
	}
	if request.MaxOutputTokens != nil {
		value := *request.MaxOutputTokens
		request.MaxOutputTokens = &value
	}
	if request.Reasoning != nil {
		value := *request.Reasoning
		request.Reasoning = &value
	}
	if request.OutputFormat != nil {
		value := *request.OutputFormat
		value.Schema = append(json.RawMessage(nil), value.Schema...)
		request.OutputFormat = &value
	}
	if request.FirstFrameTimeout != nil {
		value := *request.FirstFrameTimeout
		request.FirstFrameTimeout = &value
	}
	if request.IdleTimeout != nil {
		value := *request.IdleTimeout
		request.IdleTimeout = &value
	}
	return request
}

func (response ModelResponse) Clone() ModelResponse {
	response.Message = response.Message.Clone()
	response.Metadata = utils.CloneMap(response.Metadata)
	response.Raw = append(json.RawMessage(nil), response.Raw...)
	response.CallInfo = response.CallInfo.Clone()
	return response
}

func (info CallInfo) Clone() CallInfo {
	info.Headers = utils.CloneMap(info.Headers)
	for name, values := range info.Headers {
		info.Headers[name] = append([]string(nil), values...)
	}
	return info
}

// Clone returns an independent transport summary.
func (info StreamInfo) Clone() StreamInfo {
	info.CallInfo = info.CallInfo.Clone()
	return info
}

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

func cloneToolCall(call ToolCall) ToolCall {
	call.Arguments = append(json.RawMessage(nil), call.Arguments...)
	return call
}

func cloneToolResult(result ToolResult) ToolResult {
	result.Content = cloneContentParts(result.Content)
	result.Metadata = utils.CloneJSONMap(result.Metadata)
	return result
}

func cloneContentParts(parts []ContentPart) []ContentPart {
	result := make([]ContentPart, len(parts))
	for index, part := range parts {
		result[index] = part
		result[index].Metadata = utils.CloneJSONMap(part.Metadata)
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
