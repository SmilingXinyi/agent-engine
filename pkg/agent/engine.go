package agent

import (
	"context"
	"fmt"
)

type defaultEngine struct {
	validator RequestValidator
	factory   ModelClientFactory
	config    engineConfig
}

// NewEngine creates an agent engine with optional configuration.
// Use WithProvider to specify a model client factory; without a factory,
// Execute and Stream return an error at runtime.
func NewEngine(options ...EngineOption) Engine {
	config := newEngineConfig(options...)
	return &defaultEngine{
		validator: defaultRequestValidator{},
		factory:   config.factory,
		config:    config,
	}
}

// NewEngineWithClientFactory creates an engine with an explicit model client factory.
func NewEngineWithClientFactory(factory ModelClientFactory, options ...EngineOption) Engine {
	return &defaultEngine{
		validator: defaultRequestValidator{},
		factory:   factory,
		config:    newEngineConfig(options...),
	}
}

// Execute runs a non-streaming agent request and returns the final response.
func (engine *defaultEngine) Execute(ctx context.Context, request Request) (Response, error) {
	modelRequest, client, err := engine.prepare(ctx, request)
	if err != nil {
		return Response{}, err
	}
	modelResponse, err := client.GenerateText(ctx, modelRequest)
	response := responseFromModelResponse(modelResponse, modelRequest.Metadata)
	if err != nil {
		return response, err
	}
	return response, nil
}

// Stream runs a streaming agent request and returns the final response after the stream ends.
func (engine *defaultEngine) Stream(ctx context.Context, request StreamRequest) (Response, error) {
	modelRequest, client, err := engine.prepare(ctx, request.Request)
	if err != nil {
		return Response{}, err
	}
	modelResponse, err := client.StreamText(ctx, modelRequest, request.StreamHandler)
	response := responseFromModelResponse(modelResponse, modelRequest.Metadata)
	if err != nil {
		return response, err
	}
	return response, nil
}

func (engine *defaultEngine) prepare(ctx context.Context, request Request) (ModelRequest, ModelClient, error) {
	if err := engine.validator.ValidateRequest(ctx, request); err != nil {
		return ModelRequest{}, nil, err
	}
	if engine.factory == nil {
		return ModelRequest{}, nil, fmt.Errorf("model client factory is required: use NewEngineWithClientFactory or NewEngine with WithProvider")
	}
	request.Options = normalizeRunOptions(request.Options)
	client, err := engine.factory.CreateModelClient(ctx, request.ModelConfig)
	if err != nil {
		return ModelRequest{}, nil, err
	}
	return ModelRequest{
		SystemPrompt: request.SystemPrompt,
		UserPrompt:   request.UserPrompt,
		Messages:     append([]Message(nil), request.History...),
		Tools:        append([]Tool(nil), request.Tools...),
		Options:      request.Options,
		Metadata:     copyStringMap(request.Metadata),
		MaxMessages:  engine.config.maxMessages,
		DebugSink:    engine.config.debugSink,
	}, client, nil
}

func normalizeRunOptions(options RunOptions) RunOptions {
	if options.MaxSteps == 0 {
		options.MaxSteps = DefaultMaxSteps
	}
	return options
}

func responseFromModelResponse(response ModelResponse, fallbackMetadata map[string]string) Response {
	metadata := copyStringMap(response.Metadata)
	if len(metadata) == 0 {
		metadata = copyStringMap(fallbackMetadata)
	}
	return Response{
		Text:      response.Text,
		Thinking:  response.Thinking,
		Messages:  append([]Message(nil), response.Messages...),
		ToolCalls: append([]ToolCallRecord(nil), response.ToolCalls...),
		Usage:     response.Usage,
		Metadata:  metadata,
		Model:     response.Model,
	}
}

func copyStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
