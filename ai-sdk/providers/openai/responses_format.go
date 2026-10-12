package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	core "github.com/SmilingXinyi/agent-engine/ai-sdk"
)

// responsesUsage is deliberately separate from openAIUsage because the two
// endpoints use different field names for the same accounting values.
type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
	InputDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

type responsesResponse struct {
	ID               string                `json:"id"`
	Model            string                `json:"model"`
	CreatedAt        int64                 `json:"created_at"`
	Status           string                `json:"status"`
	OutputText       string                `json:"output_text"`
	Output           []responsesOutputItem `json:"output"`
	Usage            responsesUsage        `json:"usage"`
	IncompleteDetail struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

type responsesOutputItem struct {
	Type      string                 `json:"type"`
	ID        string                 `json:"id"`
	Status    string                 `json:"status"`
	Role      string                 `json:"role"`
	CallID    string                 `json:"call_id"`
	Name      string                 `json:"name"`
	Arguments string                 `json:"arguments"`
	Content   []responsesContentPart `json:"content"`
	Summary   []responsesContentPart `json:"summary"`
}

type responsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func buildResponsesRequest(request core.ModelRequest, stream bool) (map[string]any, error) {
	payload := make(map[string]any, len(request.ProviderOptions)+8)
	_, hasExplicitReasoning := request.ProviderOptions["reasoning"]
	for key, value := range request.ProviderOptions {
		if key == "reasoning_effort" && !hasExplicitReasoning {
			if effort, ok := value.(string); ok && strings.TrimSpace(effort) != "" {
				payload["reasoning"] = map[string]any{"effort": effort}
				continue
			}
		}
		if key == "thinking" {
			if !hasExplicitReasoning && applyResponsesThinkingOption(payload, value) {
				continue
			}
			if hasExplicitReasoning {
				continue
			}
		}
		if isReservedResponsesRequestField(key) {
			return nil, fmt.Errorf("provider option %q must use the common ModelRequest field", key)
		}
		payload[key] = value
	}
	payload["model"] = request.Model
	payload["stream"] = stream

	var instructions []string
	input := make([]any, 0, len(request.Messages))
	for index, message := range request.Messages {
		if message.Role == core.RoleSystem {
			text, err := responsesMessageText(message)
			if err != nil {
				return nil, fmt.Errorf("encode message %d: %w", index, err)
			}
			if text != "" {
				instructions = append(instructions, text)
			}
			continue
		}
		items, err := encodeResponsesMessage(message)
		if err != nil {
			return nil, fmt.Errorf("encode message %d: %w", index, err)
		}
		for _, item := range items {
			input = append(input, item)
		}
	}
	if len(instructions) > 0 {
		payload["instructions"] = strings.Join(instructions, "\n\n")
	}
	payload["input"] = input

	if request.Temperature != nil {
		payload["temperature"] = *request.Temperature
	}
	if request.MaxOutputTokens != nil {
		payload["max_output_tokens"] = *request.MaxOutputTokens
	}
	if len(request.Tools) > 0 {
		tools := make([]map[string]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			var parameters any
			if err := json.Unmarshal(tool.InputSchema, &parameters); err != nil {
				return nil, fmt.Errorf("encode tool %q schema: %w", tool.Name, err)
			}
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  parameters,
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
				"type": "function", "name": string(request.ToolChoice),
			}
		}
	}
	return payload, nil
}

// applyResponsesThinkingOption translates the adapter's existing thinking
// option into the Responses API reasoning shape. This is especially useful
// for callers that use {"type":"disabled"} with Chat Completions-compatible
// backends: Responses API expects reasoning.effort="none" instead.
func applyResponsesThinkingOption(payload map[string]any, value any) bool {
	thinking, ok := value.(map[string]any)
	if !ok {
		return false
	}
	typeValue, _ := thinking["type"].(string)
	switch strings.ToLower(strings.TrimSpace(typeValue)) {
	case "disabled":
		payload["reasoning"] = map[string]any{"effort": "none"}
		return true
	case "enabled":
		if effort, ok := thinking["effort"].(string); ok && strings.TrimSpace(effort) != "" {
			payload["reasoning"] = map[string]any{"effort": effort}
		} else {
			payload["reasoning"] = map[string]any{"effort": "medium"}
		}
		return true
	default:
		return false
	}
}

func isReservedResponsesRequestField(key string) bool {
	switch key {
	case "model", "input", "instructions", "tools", "stream", "temperature", "max_output_tokens", "tool_choice":
		return true
	default:
		return false
	}
}

func encodeResponsesMessage(message core.Message) ([]map[string]any, error) {
	if message.Role == core.RoleTool {
		results := message.ToolResults()
		if len(results) == 0 {
			return nil, fmt.Errorf("tool message has no tool result")
		}
		items := make([]map[string]any, 0, len(results))
		for _, result := range results {
			text, err := responsesToolResultText(result)
			if err != nil {
				return nil, err
			}
			items = append(items, map[string]any{
				"type": "function_call_output", "call_id": result.ToolCallID, "output": text,
			})
		}
		return items, nil
	}
	if message.Role != core.RoleUser && message.Role != core.RoleAssistant {
		return nil, fmt.Errorf("unsupported message role %q", message.Role)
	}
	content := make([]map[string]any, 0, len(message.Parts))
	var calls []map[string]any
	for _, part := range message.Parts {
		switch part.Type {
		case core.ContentText:
			contentType := "input_text"
			if message.Role == core.RoleAssistant {
				contentType = "output_text"
			}
			content = append(content, map[string]any{"type": contentType, "text": part.Text})
		case core.ContentReasoning:
			// Reasoning summaries are output-only in the Responses API and
			// cannot be replayed as ordinary input content.
			continue
		case core.ContentImage:
			if message.Role == core.RoleAssistant {
				return nil, fmt.Errorf("content type %q is not supported in assistant Responses input", part.Type)
			}
			image, err := encodeResponsesImage(part.Media)
			if err != nil {
				return nil, err
			}
			content = append(content, image)
		case core.ContentFile:
			if message.Role == core.RoleAssistant {
				return nil, fmt.Errorf("content type %q is not supported in assistant Responses input", part.Type)
			}
			file, err := encodeResponsesFile(part.Media)
			if err != nil {
				return nil, err
			}
			content = append(content, file)
		case core.ContentAudio:
			return nil, fmt.Errorf("content type %q is not supported by Responses API adapter", part.Type)
		case core.ContentToolCall:
			if message.Role != core.RoleAssistant || part.ToolCall == nil {
				return nil, fmt.Errorf("tool call part is missing call data")
			}
			calls = append(calls, map[string]any{
				"type": "function_call", "call_id": part.ToolCall.ID,
				"name": part.ToolCall.Name, "arguments": string(part.ToolCall.Arguments),
			})
		case core.ContentToolResult:
			return nil, fmt.Errorf("tool result must use role %q", core.RoleTool)
		default:
			return nil, fmt.Errorf("unsupported content type %q", part.Type)
		}
	}
	items := make([]map[string]any, 0, 1+len(calls))
	if len(content) > 0 {
		items = append(items, map[string]any{
			"type": "message", "role": string(message.Role), "content": content,
		})
	}
	items = append(items, calls...)
	return items, nil
}

func responsesMessageText(message core.Message) (string, error) {
	var text strings.Builder
	for _, part := range message.Parts {
		if part.Type != core.ContentText {
			return "", fmt.Errorf("system message content type %q is not supported", part.Type)
		}
		text.WriteString(part.Text)
	}
	return text.String(), nil
}

func responsesToolResultText(result core.ToolResult) (string, error) {
	var text strings.Builder
	for index, part := range result.Content {
		if part.Type != core.ContentText {
			return "", fmt.Errorf("tool %q result content %d type %q is not supported by Responses API", result.ToolName, index, part.Type)
		}
		text.WriteString(part.Text)
	}
	return text.String(), nil
}

func encodeResponsesImage(media *core.MediaContent) (map[string]any, error) {
	if media == nil {
		return nil, fmt.Errorf("image part is missing media")
	}
	part := map[string]any{"type": "input_image"}
	if media.FileID != "" {
		part["file_id"] = media.FileID
	} else {
		url := media.URL
		if url == "" && media.Data != "" {
			mimeType := media.MIMEType
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			if _, err := base64.StdEncoding.DecodeString(media.Data); err != nil {
				return nil, fmt.Errorf("image data is not base64: %w", err)
			}
			url = "data:" + mimeType + ";base64," + media.Data
		}
		if url == "" {
			return nil, fmt.Errorf("image part requires URL, data, or file ID")
		}
		part["image_url"] = url
	}
	if media.Detail != "" {
		part["detail"] = media.Detail
	}
	return part, nil
}

func encodeResponsesFile(media *core.MediaContent) (map[string]any, error) {
	if media == nil {
		return nil, fmt.Errorf("file part is missing media")
	}
	part := map[string]any{"type": "input_file"}
	if media.FileID != "" {
		part["file_id"] = media.FileID
		return part, nil
	}
	if media.Data == "" {
		return nil, fmt.Errorf("file part requires a file ID or data")
	}
	if _, err := base64.StdEncoding.DecodeString(media.Data); err != nil {
		return nil, fmt.Errorf("file data is not base64: %w", err)
	}
	mimeType := media.MIMEType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	part["file_data"] = "data:" + mimeType + ";base64," + media.Data
	if media.Filename != "" {
		part["filename"] = media.Filename
	}
	return part, nil
}

func convertResponsesResponse(response responsesResponse, providerName string) (core.ModelResponse, error) {
	message := core.Message{Role: core.RoleAssistant}
	seenText := false
	toolCalls := 0
	for _, item := range response.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" || part.Type == "refusal" {
					if part.Text != "" {
						message.Parts = append(message.Parts, core.ContentPart{Type: core.ContentText, Text: part.Text})
						seenText = true
					}
				}
			}
		case "reasoning":
			for _, part := range item.Summary {
				if part.Text != "" {
					message.Parts = append(message.Parts, core.ContentPart{Type: core.ContentReasoning, Text: part.Text})
				}
			}
		case "function_call":
			call := core.ToolCall{ID: item.CallID, Name: item.Name, Arguments: json.RawMessage(item.Arguments)}
			message.Parts = append(message.Parts, core.ContentPart{Type: core.ContentToolCall, ToolCall: &call})
			toolCalls++
		}
	}
	if response.OutputText != "" && !seenText {
		message.Parts = append(message.Parts, core.ContentPart{Type: core.ContentText, Text: response.OutputText})
	}
	finishReason := responsesFinishReason(response.Status, response.IncompleteDetail.Reason, toolCalls > 0)
	return core.ModelResponse{
		Message:      message,
		FinishReason: finishReason,
		Usage:        convertResponsesUsage(response.Usage),
		Metadata:     responseMetadata(providerName, response.ID, response.Model, response.CreatedAt),
	}, nil
}

func convertResponsesUsage(usage responsesUsage) core.Usage {
	return core.Usage{
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		TotalTokens: usage.TotalTokens, ReasoningTokens: usage.OutputDetails.ReasoningTokens,
		CacheReadTokens: usage.InputDetails.CachedTokens,
	}
}

func responsesFinishReason(status, incompleteReason string, hasToolCalls bool) core.FinishReason {
	if hasToolCalls {
		return core.FinishToolCalls
	}
	switch status {
	case "completed":
		return core.FinishStop
	case "incomplete":
		switch incompleteReason {
		case "max_output_tokens", "length":
			return core.FinishLength
		case "content_filter":
			return core.FinishContentFilter
		default:
			return core.FinishOther
		}
	case "failed", "cancelled":
		return core.FinishError
	case "":
		return ""
	default:
		return core.FinishOther
	}
}
