package agent

import (
	"context"
	"encoding/json"
	"time"
)

// DefaultMaxSteps is used when RunOptions.MaxSteps is not specified.
const DefaultMaxSteps = 20

// ModelConfig describes the model endpoint used by an agent run.
type ModelConfig struct {
	Provider string
	APIKey   string
	BaseURL  string
	Name     string
}

// Engine executes portable agent requests without owning session or workspace state.
type Engine interface {
	// Execute runs a non-streaming agent request and returns the final response.
	Execute(ctx context.Context, request Request) (Response, error)
	// Stream runs a streaming agent request and may return a partial response with an error.
	Stream(ctx context.Context, request StreamRequest) (Response, error)
}

// Request describes a single portable agent run.
type Request struct {
	ModelConfig  ModelConfig
	SystemPrompt string
	UserPrompt   string
	History      []Message
	Tools        []Tool
	Options      RunOptions
	Metadata     map[string]string
}

// StreamRequest describes a streaming agent run.
type StreamRequest struct {
	Request
	StreamHandler StreamHandler
}

// Response describes the final result of an agent run.
type Response struct {
	Text      string
	Thinking  string
	Messages  []Message
	ToolCalls []ToolCallRecord
	Usage     *Usage
	Metadata  map[string]string
}

// ThinkingConfig controls extended thinking/reasoning mode for supported models.
type ThinkingConfig struct {
	Enabled      bool
	BudgetTokens *int
}

// RunOptions describes stable runtime controls for an agent run.
type RunOptions struct {
	MaxSteps        int
	Temperature     *float64
	MaxOutputTokens *int
	Thinking        *ThinkingConfig
	EventHandler    EventHandler
}

// Role identifies the source or purpose of a message.
type Role string

const (
	// RoleSystem represents platform-level instructions.
	RoleSystem Role = "system"
	// RoleUser represents user input.
	RoleUser Role = "user"
	// RoleAssistant represents assistant output.
	RoleAssistant Role = "assistant"
	// RoleToolCall represents an assistant tool call request.
	RoleToolCall Role = "tool_call"
	// RoleTool represents a tool execution result.
	RoleTool Role = "tool"
)

// ContentType identifies the storage or audit category of a message record.
type ContentType string

const (
	// ContentTypeText represents a text message record.
	ContentTypeText ContentType = "text"
	// ContentTypeToolCall represents a tool call message record.
	ContentTypeToolCall ContentType = "tool_call"
	// ContentTypeToolResult represents a tool result message record.
	ContentTypeToolResult ContentType = "tool_result"
)

// Message describes an item that can be replayed into an agent conversation.
type Message interface {
	message()
}

// TextMessage describes a system, user, or assistant text message.
type TextMessage struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

// message marks TextMessage as a conversation message.
func (TextMessage) message() {}

// ToolCallMessage describes assistant tool call requests.
type ToolCallMessage struct {
	ToolCalls []ToolCall `json:"tool_calls"`
}

// message marks ToolCallMessage as a conversation message.
func (ToolCallMessage) message() {}

// ToolResultMessage describes a tool execution result.
type ToolResultMessage struct {
	ToolResult ToolResult `json:"tool_result"`
}

// message marks ToolResultMessage as a conversation message.
func (ToolResultMessage) message() {}

// MessageRecord describes a message with persistence and audit metadata.
type MessageRecord struct {
	ContentType ContentType
	Message     Message
	Usage       *Usage
	Metadata    map[string]any
	CreatedAt   time.Time
}

// Tool describes a callable capability that can be used by the agent.
type Tool struct {
	Name        string
	Description string
	Parameters  any
	Handler     ToolHandler
}

// ToolHandler executes a tool call with raw JSON input.
type ToolHandler interface {
	Handle(ctx context.Context, input json.RawMessage) (string, error)
}

// ToolHandlerFunc adapts a function into a ToolHandler.
type ToolHandlerFunc func(context.Context, json.RawMessage) (string, error)

// Handle executes a tool call with raw JSON input.
func (handler ToolHandlerFunc) Handle(ctx context.Context, input json.RawMessage) (string, error) {
	return handler(ctx, input)
}

// ToolCall describes the input requested by the assistant for a tool.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult describes the output returned by a tool.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Output     string `json:"output"`
}

// ToolCallRecord stores a completed tool call for callers that need auditing or history persistence.
type ToolCallRecord struct {
	ToolCallID string
	ToolName   string
	Input      json.RawMessage
	Output     string
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

// StreamHandler receives text chunks from a streaming agent run.
type StreamHandler interface {
	OnText(ctx context.Context, chunk string) error
}

// StreamHandlerFunc adapts a function into a StreamHandler.
type StreamHandlerFunc func(context.Context, string) error

// OnText handles a streamed text chunk.
func (handler StreamHandlerFunc) OnText(ctx context.Context, chunk string) error {
	if handler == nil {
		return nil
	}
	return handler(ctx, chunk)
}

// ThinkingStreamHandler receives thinking/reasoning chunks from a streaming agent run.
type ThinkingStreamHandler interface {
	OnThinking(ctx context.Context, chunk string) error
}

// ThinkingStreamHandlerFunc adapts a function into a ThinkingStreamHandler.
type ThinkingStreamHandlerFunc func(context.Context, string) error

// OnThinking handles a streamed thinking chunk.
func (handler ThinkingStreamHandlerFunc) OnThinking(ctx context.Context, chunk string) error {
	if handler == nil {
		return nil
	}
	return handler(ctx, chunk)
}

// EventHandler receives low-level agent runtime events.
type EventHandler interface {
	OnToolCallStart(ctx context.Context, event ToolCallStartEvent) error
	OnToolCallFinish(ctx context.Context, event ToolCallFinishEvent) error
}

// EventHandlerFunc adapts functions into an EventHandler.
type EventHandlerFunc struct {
	ToolCallStart  func(context.Context, ToolCallStartEvent) error
	ToolCallFinish func(context.Context, ToolCallFinishEvent) error
}

// OnToolCallStart handles the start of a tool call.
func (handler EventHandlerFunc) OnToolCallStart(ctx context.Context, event ToolCallStartEvent) error {
	if handler.ToolCallStart == nil {
		return nil
	}
	return handler.ToolCallStart(ctx, event)
}

// OnToolCallFinish handles the completion of a tool call.
func (handler EventHandlerFunc) OnToolCallFinish(ctx context.Context, event ToolCallFinishEvent) error {
	if handler.ToolCallFinish == nil {
		return nil
	}
	return handler.ToolCallFinish(ctx, event)
}

// ToolCallStartEvent describes the beginning of a tool call.
type ToolCallStartEvent struct {
	ToolCallID string
	ToolName   string
	Input      json.RawMessage
	StartedAt  time.Time
}

// ToolCallFinishEvent describes the completion of a tool call.
type ToolCallFinishEvent struct {
	ToolCallID string
	ToolName   string
	Output     string
	Error      error
	StartedAt  time.Time
	FinishedAt time.Time
}

// Usage describes token or cost information returned by a model provider when available.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}
