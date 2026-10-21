// Package protocol is the provider-neutral contract for one model call.
// It has no client, no hooks, and no agent loop.
package protocol

import (
	"context"
	"encoding/json"
	"time"
)

// Role identifies who produced a Message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
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
	// ContentProvider carries an opaque native content block in Data. It is
	// intentionally tied to the selected provider's wire format.
	ContentProvider ContentType = "provider"
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
	// ProviderOptions are native input fields on the corresponding message
	// object, not on its content blocks or the top-level request. Providers must
	// reject common-field collisions and mappings without an unambiguous target.
	// Native response fields remain in ModelResponse.Raw or StreamChunk;
	// they are never automatically turned into these input options.
	ProviderOptions map[string]any `json:"provider_options,omitempty"`
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
	Reasoning       *ReasoningOptions `json:"reasoning,omitempty"`
	OutputFormat    *OutputFormat     `json:"output_format,omitempty"`
	// ProviderOptions are native top-level request fields. Providers must reject
	// collisions with common fields; they must not guess or translate aliases.
	ProviderOptions map[string]any    `json:"provider_options,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	// FirstFrameTimeout and IdleTimeout override a streaming provider's defaults.
	// Nil inherits the provider value. Zero disables that timer for this call.
	// The first-frame clock starts on the first Next and keeps running across
	// comments and empty data. Idle applies after the first non-empty data frame,
	// to each later wait inside Next. Generate ignores both.
	FirstFrameTimeout *time.Duration `json:"-"`
	IdleTimeout       *time.Duration `json:"-"`
}

// ModelResponse is the provider-neutral result of one model call.
type ModelResponse struct {
	Message      Message           `json:"message"`
	FinishReason FinishReason      `json:"finish_reason"`
	Usage        Usage             `json:"usage"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	// Complete means the provider read the complete non-streaming result. A
	// length limit still completes a call. Lifecycle hook errors do not change
	// this state; callers must also check the returned error.
	Complete bool     `json:"complete"`
	CallInfo CallInfo `json:"call_info"`
	// Raw retains the original non-streaming response JSON, when available.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// ReasoningOptions covers the common effort setting. Other reasoning controls
// belong to ProviderOptions and are not portable between providers.
type ReasoningOptions struct {
	Effort string `json:"effort"`
}

type OutputFormatType string

const (
	OutputText       OutputFormatType = "text"
	OutputJSON       OutputFormatType = "json"
	OutputJSONSchema OutputFormatType = "json_schema"
)

type OutputFormat struct {
	Type   OutputFormatType `json:"type"`
	Name   string           `json:"name,omitempty"`
	Schema json.RawMessage  `json:"schema,omitempty"`
	Strict bool             `json:"strict,omitempty"`
}

// CallInfo describes the final transport attempt. Non-HTTP providers leave
// StatusCode and Headers empty. Attempts counts actual application attempts,
// excluding any transparent retries inside an injected transport.
type CallInfo struct {
	Attempts   int                 `json:"attempts"`
	StatusCode int                 `json:"status_code,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
}

type ErrorCode string

const (
	ErrorInvalidRequest   ErrorCode = "invalid_request"
	ErrorUnsupported      ErrorCode = "unsupported"
	ErrorAuthentication   ErrorCode = "authentication"
	ErrorRateLimit        ErrorCode = "rate_limit"
	ErrorUpstream         ErrorCode = "upstream"
	ErrorTransport        ErrorCode = "transport"
	ErrorCanceled         ErrorCode = "canceled"
	ErrorTimeout          ErrorCode = "timeout"
	ErrorDecode           ErrorCode = "decode"
	ErrorStreamIncomplete ErrorCode = "stream_incomplete"
	ErrorResponseTooLarge ErrorCode = "response_too_large"
	ErrorSink             ErrorCode = "sink"
)

// TimeoutKind distinguishes a stream stall from a caller deadline. It is empty
// for every error that is not a first-frame or idle timeout.
type TimeoutKind string

const (
	TimeoutFirstFrame TimeoutKind = "first_frame"
	TimeoutIdle       TimeoutKind = "idle"
)

// Error preserves classification and the underlying error chain. Retryable
// identifies a transient failure; it never authorizes replaying emitted output.
// A first-frame timeout is retryable. An idle timeout is not, because frames
// may already have been delivered.
type Error struct {
	Code        ErrorCode
	Provider    string
	Message     string
	Retryable   bool
	TimeoutKind TimeoutKind
	CallInfo    CallInfo
	Body        []byte
	Cause       error
}

// Capabilities describes an adapter's encodings, not what every upstream model
// accepts. Model-specific restrictions can still be returned by the service.
type Capabilities struct {
	Streaming         bool
	InputContent      []ContentType
	ToolResultContent []ContentType
	Tools             bool
	Reasoning         bool
	StructuredOutput  bool
}

type CapabilityProvider interface {
	Capabilities() Capabilities
}

const RequestIDMetadataKey = "request_id"

type RequestInfo struct {
	Provider  string
	Request   ModelRequest
	RequestID string
	Timestamp time.Time
}

type ResponseInfo struct {
	Provider  string
	Request   ModelRequest
	RequestID string
	Response  ModelResponse
	// Stream is non-nil for a streaming call; Response is then unused. The
	// summary contains no accumulated model output.
	Stream   *StreamInfo
	Duration time.Duration
	Error    error
}

// Hooks observe independent snapshots. A request hook error prevents the call;
// response hook errors are joined with the call error. Panics propagate.
type Hooks struct {
	OnRequest  []RequestHook
	OnResponse []ResponseHook
}

type RequestHook func(context.Context, RequestInfo) error
type ResponseHook func(context.Context, ResponseInfo) error

// Stream is a synchronous pull reader. Each successful Next delivers one
// upstream frame, including comments, empty frames and terminal frames. No
// model output is accumulated. Always Close the stream, including on early exit.
// Next and Chunk are serial operations; Close may interrupt a blocked Next.
type Stream interface {
	Next() bool
	Chunk() StreamChunk
	Err() error
	Close() error
	Info() StreamInfo
}

// StreamChunk preserves one SSE frame. Frame retains its exact bytes and line
// endings. Event and ID describe this frame only (no inherited last-event ID).
// Data joins data lines with LF and need not be JSON. Buffers remain valid after
// subsequent Next calls; callers may retain or modify them.
type StreamChunk struct {
	Frame []byte
	Event string
	ID    string
	Data  []byte
}

// StreamInfo is a snapshot of transport and terminal state, never accumulated
// content. Complete reports normal upstream termination, not answer quality.
// Hook or body-close errors do not undo observed normal termination; check Err.
type StreamInfo struct {
	CallInfo CallInfo
	Complete bool
}

// Provider maps the common ModelRequest and ModelResponse format to one AI
// service. It performs one complete call via Generate.
type Provider interface {
	Name() string
	Generate(ctx context.Context, request ModelRequest) (ModelResponse, error)
}

// StreamingProvider opens and validates a streaming response without consuming
// its body. The returned reader owns the response until termination or Close.
type StreamingProvider interface {
	Provider
	Stream(ctx context.Context, request ModelRequest) (Stream, error)
}
