// Package ai provides a provider-neutral model call contract without an
// agent loop. It is independent from the archived pkg/ai-temp runtime.
package ai

import (
	"context"
	"encoding/json"
)

// Role identifies who produced a Message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ContentType identifies the kind of data carried by a ContentPart.
type ContentType string

const (
	ContentText       ContentType = "text"
	ContentReasoning  ContentType = "reasoning"
	ContentImage      ContentType = "image"
	ContentAudio      ContentType = "audio"
	ContentFile       ContentType = "file"
	ContentToolCall   ContentType = "tool_call"
	ContentToolResult ContentType = "tool_result"
)

// ContentPart is the provider-neutral unit of message content. Providers may
// reject content types that their upstream API cannot represent.
type ContentPart struct {
	Type       ContentType     `json:"type"`
	Text       string          `json:"text,omitempty"`
	Media      *MediaContent   `json:"media,omitempty"`
	ToolCall   *ToolCall       `json:"tool_call,omitempty"`
	ToolResult *ToolResult     `json:"tool_result,omitempty"`
	Metadata   map[string]any  `json:"metadata,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
}

// MediaContent carries image/audio/file payloads via a URL, inline base64
// Data, or a Files API FileID.
type MediaContent struct {
	URL      string `json:"url,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	Filename string `json:"filename,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// Message is one turn in a model conversation, made of ContentParts.
type Message struct {
	Role     Role           `json:"role"`
	Name     string         `json:"name,omitempty"`
	Parts    []ContentPart  `json:"parts"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ToolCall is a model-issued request to invoke a tool. It carries no
// execution behavior; callers decide how to fulfill it.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolResult is a caller-supplied outcome for a ToolCall, fed back to the
// model as a RoleTool Message.
type ToolResult struct {
	ToolCallID string         `json:"tool_call_id"`
	ToolName   string         `json:"tool_name"`
	Content    []ContentPart  `json:"content"`
	IsError    bool           `json:"is_error,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// ToolDefinition describes a tool available to the model. It carries no
// handler; this package never executes tools.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolChoice controls how the model selects among available tools.
type ToolChoice string

const (
	ToolChoiceAuto     ToolChoice = "auto"
	ToolChoiceNone     ToolChoice = "none"
	ToolChoiceRequired ToolChoice = "required"
)

// FinishReason is the provider-neutral reason a model call stopped.
type FinishReason string

const (
	FinishStop          FinishReason = "stop"
	FinishToolCalls     FinishReason = "tool_calls"
	FinishLength        FinishReason = "length"
	FinishContentFilter FinishReason = "content_filter"
	FinishError         FinishReason = "error"
	FinishOther         FinishReason = "other"
)

// Usage reports token accounting for one model call.
type Usage struct {
	InputTokens      int `json:"input_tokens,omitempty"`
	OutputTokens     int `json:"output_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// ModelRequest is the provider-neutral request for one model call.
type ModelRequest struct {
	Model           string            `json:"model"`
	Messages        []Message         `json:"messages"`
	Tools           []ToolDefinition  `json:"tools,omitempty"`
	Temperature     *float64          `json:"temperature,omitempty"`
	MaxOutputTokens *int              `json:"max_output_tokens,omitempty"`
	ToolChoice      ToolChoice        `json:"tool_choice,omitempty"`
	ProviderOptions map[string]any    `json:"provider_options,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// ModelResponse is the provider-neutral result of one model call.
type ModelResponse struct {
	Message      Message           `json:"message"`
	FinishReason FinishReason      `json:"finish_reason"`
	Usage        Usage             `json:"usage"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// StreamEventType identifies the kind of incremental data in a StreamEvent.
type StreamEventType string

const (
	EventTextDelta      StreamEventType = "text_delta"
	EventReasoningDelta StreamEventType = "reasoning_delta"
	EventToolCallDelta  StreamEventType = "tool_call_delta"
	EventUsage          StreamEventType = "usage"
	EventFinish         StreamEventType = "finish"
)

// ToolCallDelta is an incremental fragment of a streamed tool call.
type ToolCallDelta struct {
	Index          int    `json:"index"`
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgumentsDelta string `json:"arguments_delta,omitempty"`
}

// StreamEvent is one incremental unit emitted while a model call streams.
type StreamEvent struct {
	Type          StreamEventType `json:"type"`
	Delta         string          `json:"delta,omitempty"`
	ToolCallDelta *ToolCallDelta  `json:"tool_call_delta,omitempty"`
	Usage         *Usage          `json:"usage,omitempty"`
	FinishReason  FinishReason    `json:"finish_reason,omitempty"`
}

// EventSink receives StreamEvents as a model call streams.
type EventSink interface {
	OnEvent(ctx context.Context, event StreamEvent) error
}

// EventSinkFunc adapts a function to the EventSink interface.
type EventSinkFunc func(context.Context, StreamEvent) error

// Provider maps the common ModelRequest and ModelResponse format to one AI
// service. It performs one complete call via Generate.
type Provider interface {
	Name() string
	Generate(ctx context.Context, request ModelRequest) (ModelResponse, error)
}

// StreamingProvider is a Provider that delivers incremental output through
// Stream. Leave it unimplemented when the upstream API has no stream.
type StreamingProvider interface {
	Provider
	Stream(ctx context.Context, request ModelRequest, sink EventSink) (ModelResponse, error)
}
