package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	protocol "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

// ChatRequest is one Chat Completions JSON body. Messages are native objects
// so provider-specific message fields stay on the object instead of a fixed struct.
type ChatRequest struct {
	Model    string
	Messages []map[string]any
	Stream   bool
	// Options holds the other top-level fields. model, messages, and stream
	// always come from the struct fields above.
	Options map[string]any
	// FirstFrameTimeout and IdleTimeout override Config for StreamChat.
	// Nil inherits the provider value. Zero disables that timer for this call.
	// Chat ignores them. Payload does not send them upstream.
	FirstFrameTimeout *time.Duration
	IdleTimeout       *time.Duration
}

// Payload copies Options and then sets model, messages, and stream.
func (request ChatRequest) Payload() map[string]any {
	payload := make(map[string]any, len(request.Options)+3)
	for key, value := range request.Options {
		payload[key] = value
	}
	payload["model"] = request.Model
	payload["messages"] = request.Messages
	payload["stream"] = request.Stream
	return payload
}

func validateChatMessageOptions(options map[string]any) error {
	return validateMessageOptions(options, "role", "name", "content", "reasoning_content", "tool_calls", "tool_call_id")
}

func validateMessageOptions(options map[string]any, reserved ...string) error {
	for key := range options {
		for _, common := range reserved {
			if key == common {
				return &protocol.Error{Code: protocol.ErrorInvalidRequest, Message: fmt.Sprintf("message provider option %q must use the common message field", key)}
			}
		}
	}
	return nil
}

func buildRequest(request protocol.ModelRequest, stream bool) (map[string]any, error) {
	chatRequest, err := newChatRequest(request, stream)
	if err != nil {
		return nil, err
	}
	return chatRequest.Payload(), nil
}

func newChatRequest(request protocol.ModelRequest, stream bool) (ChatRequest, error) {
	messages, err := encodeMessages(request.Messages)
	if err != nil {
		return ChatRequest{}, err
	}
	options := make(map[string]any, len(request.ProviderOptions)+8)
	for key, value := range request.ProviderOptions {
		if isReservedRequestField(key) {
			return ChatRequest{}, fmt.Errorf("provider option %q must use the common ModelRequest field", key)
		}
		if key == "extra_body" {
			return ChatRequest{}, fmt.Errorf("extra_body is an SDK wrapper; pass native fields directly in ProviderOptions")
		}
		options[key] = value
	}
	if stream {
		if _, exists := options["stream_options"]; !exists {
			options["stream_options"] = map[string]any{"include_usage": true}
		}
	}
	if request.Reasoning != nil {
		options["reasoning_effort"] = request.Reasoning.Effort
	}
	if request.OutputFormat != nil {
		format, err := encodeOutputFormat(request.OutputFormat, false)
		if err != nil {
			return ChatRequest{}, err
		}
		options["response_format"] = format
	}
	if request.Temperature != nil {
		options["temperature"] = *request.Temperature
	}
	if request.MaxOutputTokens != nil {
		options["max_tokens"] = *request.MaxOutputTokens
	}
	if len(request.Tools) > 0 {
		tools := make([]map[string]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			var parameters map[string]any
			if err := json.Unmarshal(tool.InputSchema, &parameters); err != nil {
				return ChatRequest{}, fmt.Errorf("encode tool %q schema: %w", tool.Name, err)
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
		options["tools"] = tools
	}
	if request.ToolChoice != "" {
		switch request.ToolChoice {
		case protocol.ToolChoiceAuto, protocol.ToolChoiceNone, protocol.ToolChoiceRequired:
			options["tool_choice"] = string(request.ToolChoice)
		default:
			options["tool_choice"] = map[string]any{
				"type":     "function",
				"function": map[string]any{"name": string(request.ToolChoice)},
			}
		}
	}
	return ChatRequest{Model: request.Model, Messages: messages, Stream: stream, Options: options}, nil
}

func isReservedRequestField(key string) bool {
	switch key {
	case "model", "messages", "tools", "stream", "temperature", "max_tokens", "max_completion_tokens", "tool_choice", "reasoning_effort", "response_format":
		return true
	default:
		return false
	}
}

func encodeMessages(messages []protocol.Message) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(messages))
	for index, message := range messages {
		if message.Role == protocol.RoleTool {
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

func encodeMessage(message protocol.Message) (map[string]any, error) {
	if err := validateChatMessageOptions(message.ProviderOptions); err != nil {
		return nil, err
	}
	switch message.Role {
	case protocol.RoleSystem, protocol.RoleDeveloper, protocol.RoleUser, protocol.RoleAssistant:
	default:
		return nil, fmt.Errorf("unsupported message role %q", message.Role)
	}
	encoded := map[string]any{"role": string(message.Role)}
	if message.Name != "" {
		encoded["name"] = message.Name
	}
	var contentParts []map[string]any
	var text strings.Builder
	var reasoning strings.Builder
	var toolCalls []map[string]any
	for _, part := range message.Parts {
		switch part.Type {
		case protocol.ContentText:
			text.WriteString(part.Text)
			contentParts = append(contentParts, map[string]any{"type": "text", "text": part.Text})
		case protocol.ContentReasoning:
			reasoning.WriteString(part.Text)
		case protocol.ContentImage:
			imagePart, err := encodeImagePart(part.Media)
			if err != nil {
				return nil, err
			}
			contentParts = append(contentParts, imagePart)
		case protocol.ContentFile:
			filePart, err := encodeFilePart(part.Media)
			if err != nil {
				return nil, err
			}
			contentParts = append(contentParts, filePart)
		case protocol.ContentToolCall:
			if message.Role != protocol.RoleAssistant || part.ToolCall == nil {
				return nil, fmt.Errorf("tool call part is missing call data")
			}
			toolCalls = append(toolCalls, toolCallObject(*part.ToolCall))
		case protocol.ContentAudio:
			if message.Role != protocol.RoleUser {
				return nil, fmt.Errorf("audio input requires user role")
			}
			audio, err := encodeAudioPart(part.Media)
			if err != nil {
				return nil, err
			}
			contentParts = append(contentParts, audio)
		case protocol.ContentProvider:
			block, err := decodeNativeBlock(part.Data)
			if err != nil {
				return nil, err
			}
			contentParts = append(contentParts, block)
		case protocol.ContentToolResult:
			return nil, fmt.Errorf("tool result must use role %q", protocol.RoleTool)
		default:
			return nil, fmt.Errorf("unsupported content type %q", part.Type)
		}
	}
	if reasoning.Len() > 0 {
		encoded["reasoning_content"] = reasoning.String()
	}
	if hasMediaContent(contentParts) {
		encoded["content"] = contentParts
	} else if text.Len() > 0 || len(toolCalls) == 0 {
		encoded["content"] = text.String()
	}
	if len(toolCalls) > 0 {
		encoded["tool_calls"] = toolCalls
	}
	for key, value := range message.ProviderOptions {
		encoded[key] = value
	}
	return encoded, nil
}

func toolCallObject(call protocol.ToolCall) map[string]any {
	function := map[string]any{"arguments": string(call.Arguments)}
	if call.Name != "" {
		function["name"] = call.Name
	}
	object := map[string]any{"type": "function", "function": function}
	if call.ID != "" {
		object["id"] = call.ID
	}
	return object
}

// encodeImagePart converts a provider-neutral image MediaContent into an
// OpenAI-compatible "image_url" content block. A Files API FileID takes
// precedence over URL/Data since it requires no re-encoding.
func encodeImagePart(media *protocol.MediaContent) (map[string]any, error) {
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
func encodeFilePart(media *protocol.MediaContent) (map[string]any, error) {
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

func encodeToolResults(message protocol.Message) ([]map[string]any, error) {
	if err := validateChatMessageOptions(message.ProviderOptions); err != nil {
		return nil, err
	}
	results := message.ToolResults()
	if len(results) == 0 {
		return nil, fmt.Errorf("tool message has no tool result")
	}
	if len(message.ProviderOptions) > 0 && len(results) != 1 {
		return nil, &protocol.Error{Code: protocol.ErrorUnsupported, Message: "message provider options require exactly one tool result; split the message"}
	}
	messages := make([]map[string]any, 0, len(results))
	for _, result := range results {
		var content strings.Builder
		for index, part := range result.Content {
			if part.Type != protocol.ContentText {
				return nil, fmt.Errorf(
					"tool %q result content %d type %q is not supported by Chat Completions",
					result.ToolName, index, part.Type,
				)
			}
			content.WriteString(part.Text)
		}
		encoded := map[string]any{"role": "tool", "content": content.String()}
		if result.ToolCallID != "" {
			encoded["tool_call_id"] = result.ToolCallID
		}
		for key, value := range message.ProviderOptions {
			encoded[key] = value
		}
		messages = append(messages, encoded)
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

// normalizeChatCompletion returns the non-streaming response object.
// A missing object field becomes "chat.completion". Unknown fields stay.
// strict rejects any object value other than chat.completion.
func normalizeChatCompletion(response map[string]any, strict bool) (map[string]any, error) {
	if response == nil {
		return nil, fmt.Errorf("OpenAI response must be a JSON object")
	}
	switch object := response["object"].(type) {
	case nil:
		response["object"] = "chat.completion"
	case string:
		if object == "" {
			response["object"] = "chat.completion"
		} else if strict && object != "chat.completion" {
			return nil, fmt.Errorf("non-streaming response object %q is not chat.completion", object)
		}
	default:
		return nil, fmt.Errorf("non-streaming response object must be a string")
	}
	choices, exists := response["choices"]
	if !exists || choices == nil {
		return nil, fmt.Errorf("OpenAI response has no choices")
	}
	if _, ok := choices.([]any); !ok {
		return nil, fmt.Errorf("OpenAI response choices must be an array")
	}
	return response, nil
}

func decodeJSONObject(body []byte) (map[string]any, error) {
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("OpenAI response must be a JSON object")
	}
	return response, nil
}

func convertChatCompletion(response map[string]any, providerName string) (protocol.ModelResponse, error) {
	choices, _ := response["choices"].([]any)
	if len(choices) == 0 {
		return protocol.ModelResponse{}, fmt.Errorf("OpenAI response has no choices")
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return protocol.ModelResponse{}, fmt.Errorf("OpenAI choice must be an object")
	}
	messageValue, _ := choice["message"].(map[string]any)
	message, err := convertChatMessage(messageValue)
	if err != nil {
		return protocol.ModelResponse{}, err
	}
	finishReason, err := optionalString(choice, "finish_reason")
	if err != nil {
		return protocol.ModelResponse{}, err
	}
	created, err := optionalInt(response, "created")
	if err != nil {
		return protocol.ModelResponse{}, err
	}
	identifier, err := optionalString(response, "id")
	if err != nil {
		return protocol.ModelResponse{}, err
	}
	model, err := optionalString(response, "model")
	if err != nil {
		return protocol.ModelResponse{}, err
	}
	usage, err := convertChatUsage(response["usage"])
	if err != nil {
		return protocol.ModelResponse{}, err
	}
	return protocol.ModelResponse{
		Message:      message,
		FinishReason: mapFinishReason(finishReason),
		Usage:        usage,
		Metadata:     responseMetadata(providerName, identifier, model, int64(created)),
	}, nil
}

func convertChatMessage(message map[string]any) (protocol.Message, error) {
	result := protocol.Message{Role: protocol.RoleAssistant}
	if message == nil {
		return result, nil
	}
	text, err := chatMessageText(message["content"])
	if err != nil {
		return protocol.Message{}, err
	}
	if text != "" {
		result.Parts = append(result.Parts, protocol.ContentPart{Type: protocol.ContentText, Text: text})
	}
	reasoning, err := firstString(message, "reasoning_content", "reasoning")
	if err != nil {
		return protocol.Message{}, err
	}
	if reasoning != "" {
		result.Parts = append(result.Parts, protocol.ContentPart{Type: protocol.ContentReasoning, Text: reasoning})
	}
	calls, exists := message["tool_calls"]
	if !exists || calls == nil {
		return result, nil
	}
	toolCalls, ok := calls.([]any)
	if !ok {
		return protocol.Message{}, fmt.Errorf("tool_calls must be an array")
	}
	for _, item := range toolCalls {
		call, ok := item.(map[string]any)
		if !ok {
			return protocol.Message{}, fmt.Errorf("tool call must be an object")
		}
		function, ok := call["function"].(map[string]any)
		if !ok {
			return protocol.Message{}, fmt.Errorf("tool call is missing function")
		}
		identifier, err := optionalString(call, "id")
		if err != nil {
			return protocol.Message{}, err
		}
		name, err := optionalString(function, "name")
		if err != nil {
			return protocol.Message{}, err
		}
		arguments, err := optionalString(function, "arguments")
		if err != nil {
			return protocol.Message{}, err
		}
		toolCall := protocol.ToolCall{ID: identifier, Name: name, Arguments: json.RawMessage(arguments)}
		result.Parts = append(result.Parts, protocol.ContentPart{Type: protocol.ContentToolCall, ToolCall: &toolCall})
	}
	return result, nil
}

func chatMessageText(content any) (string, error) {
	switch typed := content.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case []any:
		var result strings.Builder
		for _, item := range typed {
			part, ok := item.(map[string]any)
			if !ok {
				return "", fmt.Errorf("decode OpenAI message content: content part must be an object")
			}
			partType, _ := part["type"].(string)
			if partType == "text" || partType == "output_text" {
				text, _ := part["text"].(string)
				result.WriteString(text)
			}
		}
		return result.String(), nil
	default:
		return "", fmt.Errorf("decode OpenAI message content: unsupported content type %T", content)
	}
}

func convertChatUsage(value any) (protocol.Usage, error) {
	if value == nil {
		return protocol.Usage{}, nil
	}
	usage, ok := value.(map[string]any)
	if !ok {
		return protocol.Usage{}, fmt.Errorf("usage must be an object")
	}
	inputTokens, err := optionalInt(usage, "prompt_tokens")
	if err != nil {
		return protocol.Usage{}, err
	}
	outputTokens, err := optionalInt(usage, "completion_tokens")
	if err != nil {
		return protocol.Usage{}, err
	}
	totalTokens, err := optionalInt(usage, "total_tokens")
	if err != nil {
		return protocol.Usage{}, err
	}
	reasoningTokens, err := nestedInt(usage, "completion_tokens_details", "reasoning_tokens")
	if err != nil {
		return protocol.Usage{}, err
	}
	cacheReadTokens, err := nestedInt(usage, "prompt_tokens_details", "cached_tokens")
	if err != nil {
		return protocol.Usage{}, err
	}
	return protocol.Usage{
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		TotalTokens:     totalTokens,
		ReasoningTokens: reasoningTokens,
		CacheReadTokens: cacheReadTokens,
	}, nil
}

func firstString(object map[string]any, keys ...string) (string, error) {
	for _, key := range keys {
		value, exists := object[key]
		if !exists || value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("%s must be a string", key)
		}
		if text != "" {
			return text, nil
		}
	}
	return "", nil
}

func optionalString(object map[string]any, key string) (string, error) {
	value, exists := object[key]
	if !exists || value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return text, nil
}

func optionalInt(object map[string]any, key string) (int, error) {
	value, exists := object[key]
	if !exists || value == nil {
		return 0, nil
	}
	number, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("%s must be a number", key)
	}
	return int(number), nil
}

func nestedInt(object map[string]any, parent, key string) (int, error) {
	value, exists := object[parent]
	if !exists || value == nil {
		return 0, nil
	}
	nested, ok := value.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("%s must be an object", parent)
	}
	return optionalInt(nested, key)
}

func mapFinishReason(reason string) protocol.FinishReason {
	switch reason {
	case "stop":
		return protocol.FinishStop
	case "tool_calls", "function_call":
		return protocol.FinishToolCalls
	case "length":
		return protocol.FinishLength
	case "content_filter":
		return protocol.FinishContentFilter
	case "error":
		return protocol.FinishError
	case "":
		return ""
	default:
		return protocol.FinishOther
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

// encodeOutputFormat maps only explicit common options; ProviderOptions never
// undergoes heuristic alias translation.
func encodeOutputFormat(format *protocol.OutputFormat, responses bool) (map[string]any, error) {
	switch format.Type {
	case protocol.OutputText:
		return map[string]any{"type": "text"}, nil
	case protocol.OutputJSON:
		return map[string]any{"type": "json_object"}, nil
	case protocol.OutputJSONSchema:
		if strings.TrimSpace(format.Name) == "" {
			return nil, fmt.Errorf("JSON schema output requires a name")
		}
		schema, err := decodeNativeBlock(format.Schema)
		if err != nil {
			return nil, fmt.Errorf("output schema: %w", err)
		}
		definition := map[string]any{"name": format.Name, "schema": schema, "strict": format.Strict}
		if responses {
			definition["type"] = "json_schema"
			return definition, nil
		}
		return map[string]any{"type": "json_schema", "json_schema": definition}, nil
	default:
		return nil, fmt.Errorf("unsupported output format %q", format.Type)
	}
}

func decodeNativeBlock(raw json.RawMessage) (map[string]any, error) {
	var block map[string]any
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil, err
	}
	if block == nil {
		return nil, fmt.Errorf("native block must be a JSON object")
	}
	return block, nil
}

func encodeAudioPart(media *protocol.MediaContent) (map[string]any, error) {
	if media == nil || media.Data == "" {
		return nil, fmt.Errorf("audio input requires inline base64 data")
	}
	if _, err := base64.StdEncoding.DecodeString(media.Data); err != nil {
		return nil, fmt.Errorf("invalid audio base64: %w", err)
	}
	format := ""
	switch media.MIMEType {
	case "audio/wav", "audio/x-wav":
		format = "wav"
	case "audio/mpeg", "audio/mp3":
		format = "mp3"
	default:
		return nil, fmt.Errorf("audio MIME type %q is unsupported; use wav or mp3", media.MIMEType)
	}
	return map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": media.Data, "format": format}}, nil
}
