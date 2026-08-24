package agent

import "context"

// RequestValidator checks whether an agent run request can be executed.
type RequestValidator interface {
	ValidateRequest(ctx context.Context, request Request) error
}

// DebugSink receives raw request payloads for diagnostic output.
// A nil DebugSink disables request dumping entirely.
// DumpRequest is called once per outbound LLM request; toolCount is the number
// of tools registered for that request. Returned errors are surfaced with the
// final or partial agent response.
type DebugSink interface {
	DumpRequest(model string, toolCount int, messages []DebugMessage) error
}

// DebugMessage is a provider-agnostic snapshot of one message sent to the model.
type DebugMessage struct {
	Role  string
	Parts []DebugPart
}

// DebugPart is a single content part within a DebugMessage.
type DebugPart struct {
	Type       string
	Text       string
	ToolCallID string
	ToolName   string
	ToolInput  string
	ToolOutput string
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
	// MaxMessages caps the number of history messages forwarded to the provider.
	// Zero means no limit.
	MaxMessages int
	DebugSink   DebugSink
}

// ModelResponse describes the standard result returned by a model client.
type ModelResponse struct {
	Text      string
	Thinking  string
	Messages  []Message
	ToolCalls []ToolCallRecord
	Usage     *Usage
	Metadata  map[string]string
	Model     *ModelInfo
}
