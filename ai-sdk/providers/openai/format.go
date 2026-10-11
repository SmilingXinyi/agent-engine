package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	core "github.com/SmilingXinyi/agent-engine/ai-sdk"
)

type chatMessage struct {
	Role             string         `json:"role"`
	Name             string         `json:"name,omitempty"`
	Content          any            `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	Index    int          `json:"index,omitempty"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type responseMessage struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Reasoning        string          `json:"reasoning,omitempty"`
	ToolCalls        []chatToolCall  `json:"tool_calls,omitempty"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Created int64  `json:"created"`
	Choices []struct {
		Index        int             `json:"index"`
		Message      responseMessage `json:"message"`
		FinishReason string          `json:"finish_reason"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	PromptDetails    struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func buildRequest(request core.ModelRequest, stream bool) (map[string]any, error) {
	messages, err := encodeMessages(request.Messages)
	if err != nil {
		return nil, err
	}
	payload := make(map[string]any, len(request.ProviderOptions)+8)
	for key, value := range request.ProviderOptions {
		if key == "reasoning" {
			if reasoning, ok := value.(map[string]any); ok {
				if effort, ok := reasoning["effort"].(string); ok && strings.TrimSpace(effort) != "" {
					payload["reasoning_effort"] = effort
					continue
				}
			}
		}
		if isReservedRequestField(key) {
			return nil, fmt.Errorf("provider option %q must use the common ModelRequest field", key)
		}
		payload[key] = value
	}
	payload["model"] = request.Model
	payload["messages"] = messages
	payload["stream"] = stream
	if stream {
		payload["stream_options"] = map[string]any{"include_usage": true}
	}
	if request.Temperature != nil {
		payload["temperature"] = *request.Temperature
	}
	if request.MaxOutputTokens != nil {
		payload["max_tokens"] = *request.MaxOutputTokens
	}
	if len(request.Tools) > 0 {
		tools := make([]map[string]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			var parameters map[string]any
			if err := json.Unmarshal(tool.InputSchema, &parameters); err != nil {
				return nil, fmt.Errorf("encode tool %q schema: %w", tool.Name, err)
			}
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        tool.Name,
					"description": tool.Description,
					"parameters":  parameters,
				},
			})
		}
		payload["tools"] = tools
	}
	if request.ToolChoice != "" {
		switch request.ToolChoice {
		case core.ToolChoiceAuto, core.ToolChoiceNone, core.ToolChoiceRequired:
			payload["tool_choice"] = string(request.ToolChoice)
		default:
			payload["tool_choice"] = map[string]any{
				"type":     "function",
				"function": map[string]any{"name": string(request.ToolChoice)},
			}
		}
	}
	return payload, nil
}

func isReservedRequestField(key string) bool {
	switch key {
	case "model", "messages", "tools", "stream", "stream_options", "temperature", "max_tokens", "tool_choice":
		return true
	default:
		return false
	}
}

func encodeMessages(messages []core.Message) ([]chatMessage, error) {
	result := make([]chatMessage, 0, len(messages))
	for index, message := range messages {
		if message.Role == core.RoleTool {
			toolMessages, err := encodeToolResults(message)
			if err != nil {
				return nil, fmt.Errorf("encode message %d: %w", index, err)
			}
			result = append(result, toolMessages...)
			continue
		}
		encoded, err := encodeMessage(message)
		if err != nil {
			return nil, fmt.Errorf("encode message %d: %w", index, err)
		}
		result = append(result, encoded)
	}
	return result, nil
}

func encodeMessage(message core.Message) (chatMessage, error) {
	switch message.Role {
	case core.RoleSystem, core.RoleUser, core.RoleAssistant:
	default:
		return chatMessage{}, fmt.Errorf("unsupported message role %q", message.Role)
	}
	encoded := chatMessage{Role: string(message.Role), Name: message.Name}
	var contentParts []map[string]any
	var text strings.Builder
	var reasoning strings.Builder
	for _, part := range message.Parts {
		switch part.Type {
		case core.ContentText:
			text.WriteString(part.Text)
			contentParts = append(contentParts, map[string]any{"type": "text", "text": part.Text})
		case core.ContentReasoning:
			reasoning.WriteString(part.Text)
		case core.ContentImage:
			imagePart, err := encodeImagePart(part.Media)
			if err != nil {
				return chatMessage{}, err
			}
			contentParts = append(contentParts, imagePart)
		case core.ContentFile:
			filePart, err := encodeFilePart(part.Media)
			if err != nil {
				return chatMessage{}, err
			}
			contentParts = append(contentParts, filePart)
		case core.ContentToolCall:
			if part.ToolCall == nil {
				return chatMessage{}, fmt.Errorf("tool call part is missing call data")
			}
			encoded.ToolCalls = append(encoded.ToolCalls, chatToolCall{
				ID: part.ToolCall.ID, Type: "function",
				Function: chatFunction{Name: part.ToolCall.Name, Arguments: string(part.ToolCall.Arguments)},
			})
		case core.ContentAudio:
			return chatMessage{}, fmt.Errorf("content type %q is not supported by Chat Completions", part.Type)
		case core.ContentToolResult:
			return chatMessage{}, fmt.Errorf("tool result must use role %q", core.RoleTool)
		default:
			return chatMessage{}, fmt.Errorf("unsupported content type %q", part.Type)
		}
	}
	encoded.ReasoningContent = reasoning.String()
	if hasMediaContent(contentParts) {
		encoded.Content = contentParts
	} else if text.Len() > 0 || len(encoded.ToolCalls) == 0 {
		encoded.Content = text.String()
	}
	return encoded, nil
}

// encodeImagePart converts a provider-neutral image MediaContent into an
// OpenAI-compatible "image_url" content block. A Files API FileID takes
// precedence over URL/Data since it requires no re-encoding.
func encodeImagePart(media *core.MediaContent) (map[string]any, error) {
	if media == nil {
		return nil, fmt.Errorf("image part is missing media")
	}
	if media.FileID != "" {
		return map[string]any{"type": "file", "file_id": media.FileID}, nil
	}
	url := media.URL
	if url == "" && media.Data != "" {
		mimeType := media.MIMEType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		data := media.Data
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			return nil, fmt.Errorf("image data is not base64: %w", err)
		}
		url = "data:" + mimeType + ";base64," + data
	}
	if url == "" {
		return nil, fmt.Errorf("image part requires URL, data, or file ID")
	}
	imageURL := map[string]any{"url": url}
	if media.Detail != "" {
		imageURL["detail"] = media.Detail
	}
	return map[string]any{"type": "image_url", "image_url": imageURL}, nil
}

// encodeFilePart converts a provider-neutral file MediaContent into an
// OpenAI/DeepSeek-compatible "file" content block, referencing a Files API
// FileID or carrying inline base64 data via file_data.
func encodeFilePart(media *core.MediaContent) (map[string]any, error) {
	if media == nil {
		return nil, fmt.Errorf("file part is missing media")
	}
	if media.FileID != "" {
		return map[string]any{"type": "file", "file_id": media.FileID}, nil
	}
	if media.Data == "" {
		return nil, fmt.Errorf("file part requires a file ID or data")
	}
	mimeType := media.MIMEType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	data := media.Data
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return nil, fmt.Errorf("file data is not base64: %w", err)
	}
	filePart := map[string]any{
		"type": "file",
		"file": map[string]any{
			"file_data": "data:" + mimeType + ";base64," + data,
			"filename":  media.Filename,
		},
	}
	return filePart, nil
}

func encodeToolResults(message core.Message) ([]chatMessage, error) {
	results := message.ToolResults()
	if len(results) == 0 {
		return nil, fmt.Errorf("tool message has no tool result")
	}
	messages := make([]chatMessage, 0, len(results))
	for _, result := range results {
		var content strings.Builder
		for index, part := range result.Content {
			if part.Type != core.ContentText {
				return nil, fmt.Errorf(
					"tool %q result content %d type %q is not supported by Chat Completions",
					result.ToolName, index, part.Type,
				)
			}
			content.WriteString(part.Text)
		}
		messages = append(messages, chatMessage{
			Role: "tool", ToolCallID: result.ToolCallID, Content: content.String(),
		})
	}
	return messages, nil
}

func hasMediaContent(parts []map[string]any) bool {
	for _, part := range parts {
		if part["type"] != "text" {
			return true
		}
	}
	return false
}

func convertResponse(response chatResponse, providerName string) (core.ModelResponse, error) {
	if len(response.Choices) == 0 {
		return core.ModelResponse{}, fmt.Errorf("OpenAI response has no choices")
	}
	choice := response.Choices[0]
	message, err := convertMessage(choice.Message)
	if err != nil {
		return core.ModelResponse{}, err
	}
	return core.ModelResponse{
		Message:      message,
		FinishReason: mapFinishReason(choice.FinishReason),
		Usage:        convertUsage(response.Usage),
		Metadata:     responseMetadata(providerName, response.ID, response.Model, response.Created),
	}, nil
}

func convertMessage(message responseMessage) (core.Message, error) {
	result := core.Message{Role: core.RoleAssistant}
	text, err := decodeResponseContent(message.Content)
	if err != nil {
		return core.Message{}, err
	}
	if text != "" {
		result.Parts = append(result.Parts, core.ContentPart{Type: core.ContentText, Text: text})
	}
	reasoning := message.ReasoningContent
	if reasoning == "" {
		reasoning = message.Reasoning
	}
	if reasoning != "" {
		result.Parts = append(result.Parts, core.ContentPart{Type: core.ContentReasoning, Text: reasoning})
	}
	for _, call := range message.ToolCalls {
		toolCall := core.ToolCall{
			ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments),
		}
		result.Parts = append(result.Parts, core.ContentPart{Type: core.ContentToolCall, ToolCall: &toolCall})
	}
	return result, nil
}

func decodeResponseContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", fmt.Errorf("decode OpenAI message content: %w", err)
	}
	var result strings.Builder
	for _, part := range parts {
		if part.Type == "text" || part.Type == "output_text" {
			result.WriteString(part.Text)
		}
	}
	return result.String(), nil
}

func convertUsage(usage openAIUsage) core.Usage {
	return core.Usage{
		InputTokens:     usage.PromptTokens,
		OutputTokens:    usage.CompletionTokens,
		TotalTokens:     usage.TotalTokens,
		ReasoningTokens: usage.CompletionDetails.ReasoningTokens,
		CacheReadTokens: usage.PromptDetails.CachedTokens,
	}
}

func mapFinishReason(reason string) core.FinishReason {
	switch reason {
	case "stop":
		return core.FinishStop
	case "tool_calls", "function_call":
		return core.FinishToolCalls
	case "length":
		return core.FinishLength
	case "content_filter":
		return core.FinishContentFilter
	case "error":
		return core.FinishError
	case "":
		return ""
	default:
		return core.FinishOther
	}
}

func responseMetadata(providerName, id, model string, created int64) map[string]string {
	if providerName == "" {
		providerName = "openai"
	}
	metadata := map[string]string{"provider": providerName}
	if id != "" {
		metadata["request_id"] = id
	}
	if model != "" {
		metadata["model"] = model
	}
	if created != 0 {
		metadata["created"] = fmt.Sprintf("%d", created)
	}
	return metadata
}
