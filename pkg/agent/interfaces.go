package agent

import "context"

// RequestValidator checks whether an agent run request can be executed.
type RequestValidator interface {
	ValidateRequest(ctx context.Context, request Request) error
}

// ModelClientFactory creates model clients from model configuration.
type ModelClientFactory interface {
	CreateModelClient(ctx context.Context, config ModelConfig) (ModelClient, error)
}

// ModelClient wraps non-streaming and streaming text generation.
type ModelClient interface {
	GenerateText(ctx context.Context, request ModelRequest) (ModelResponse, error)
	StreamText(ctx context.Context, request ModelRequest, handler StreamHandler) (ModelResponse, error)
}

// ModelRequest describes the standard request passed to a model client.
type ModelRequest struct {
	SystemPrompt string
	UserPrompt   string
	Messages     []Message
	Tools        []Tool
	Options      RunOptions
	Metadata     map[string]string
}

// ModelResponse describes the standard result returned by a model client.
type ModelResponse struct {
	Text      string
	Messages  []Message
	ToolCalls []ToolCallRecord
	Usage     *Usage
	Metadata  map[string]string
}
