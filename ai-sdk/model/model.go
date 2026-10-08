package model

import (
	"context"
	"encoding/json"
	"io"
)

// Role represents the role of a message sender.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ContentPartType represents the type of content part.
type ContentPartType string

const (
	ContentTypeText       ContentPartType = "text"
	ContentTypeReasoning  ContentPartType = "reasoning"
	ContentTypeToolCall   ContentPartType = "tool_call"
	ContentTypeToolResult ContentPartType = "tool_result"
)

// ContentPart represents an individual part of a message.
type ContentPart struct {
	Type       ContentPartType `json:"type"`
	Text       string          `json:"text,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	ToolInput  string          `json:"tool_input,omitempty"`
	ToolOutput string          `json:"tool_output,omitempty"`
}

// Message represents a chat message.
type Message struct {
	Role    Role          `json:"role"`
	Content string        `json:"content,omitempty"`
	Parts   []ContentPart `json:"parts,omitempty"`
}

// UserMessage creates a simple user text message.
func UserMessage(text string) Message {
	return Message{
		Role:    RoleUser,
		Content: text,
		Parts: []ContentPart{
			{Type: ContentTypeText, Text: text},
		},
	}
}

// AssistantMessage creates a simple assistant text message.
func AssistantMessage(text string) Message {
	return Message{
		Role:    RoleAssistant,
		Content: text,
		Parts: []ContentPart{
			{Type: ContentTypeText, Text: text},
		},
	}
}

// SystemMessage creates a simple system message.
func SystemMessage(text string) Message {
	return Message{
		Role:    RoleSystem,
		Content: text,
		Parts: []ContentPart{
			{Type: ContentTypeText, Text: text},
		},
	}
}

// ToolResultMessage creates a tool result message.
func ToolResultMessage(toolCallID, name, output string) Message {
	return Message{
		Role: RoleTool,
		Parts: []ContentPart{
			{
				Type:       ContentTypeToolResult,
				ToolCallID: toolCallID,
				ToolName:   name,
				ToolOutput: output,
			},
		},
	}
}

// ToolCall represents a model-requested tool execution.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDefinition defines a callable tool for language models.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Usage captures token usage statistics.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// RequestOptions configures generation parameters.
type RequestOptions struct {
	Temperature     *float64          `json:"temperature,omitempty"`
	MaxTokens       *int              `json:"max_tokens,omitempty"`
	TopP            *float64          `json:"top_p,omitempty"`
	Stop            []string          `json:"stop,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	DisableThinking bool              `json:"disable_thinking,omitempty"`
}

// GenerateRequest defines input for generation.
type GenerateRequest struct {
	SystemPrompt string           `json:"system_prompt,omitempty"`
	Messages     []Message        `json:"messages"`
	Tools        []ToolDefinition `json:"tools,omitempty"`
	Options      RequestOptions   `json:"options"`
}

// GenerateResponse defines output from generation.
type GenerateResponse struct {
	Text         string     `json:"text"`
	Reasoning    string     `json:"reasoning,omitempty"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	Usage        Usage      `json:"usage"`
}

// StreamChunkType represents the nature of a streaming event.
type StreamChunkType string

const (
	ChunkTypeText      StreamChunkType = "text"
	ChunkTypeReasoning StreamChunkType = "reasoning"
	ChunkTypeToolCall  StreamChunkType = "tool_call"
	ChunkTypeFinish    StreamChunkType = "finish"
)

// StreamChunk represents a delta during streaming.
type StreamChunk struct {
	Type         StreamChunkType `json:"type"`
	Text         string          `json:"text,omitempty"`
	Reasoning    string          `json:"reasoning,omitempty"`
	ToolCall     *ToolCall       `json:"tool_call,omitempty"`
	FinishReason string          `json:"finish_reason,omitempty"`
	Usage        *Usage          `json:"usage,omitempty"`
}

// StreamHandler receives stream chunks in real-time.
type StreamHandler func(ctx context.Context, chunk StreamChunk) error

// Model defines standard generation and streaming operations for language models.
type Model interface {
	// Name returns the identifier of the model.
	Name() string
	// Generate produces a complete response for the given request.
	Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)
	// Stream produces chunks of response for the given request.
	Stream(ctx context.Context, req GenerateRequest, handler StreamHandler) (*GenerateResponse, error)
}

// StreamIterator allows reading chunks sequentially as an alternative to callbacks.
type StreamIterator interface {
	Next() (*StreamChunk, error)
	io.Closer
}
