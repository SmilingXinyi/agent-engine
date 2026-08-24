package goai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
	"github.com/zendev-sh/goai"
	"github.com/zendev-sh/goai/provider"
	"github.com/zendev-sh/goai/provider/anthropic"
	"github.com/zendev-sh/goai/provider/compat"
	"github.com/zendev-sh/goai/provider/deepseek"
	"github.com/zendev-sh/goai/provider/google"
	"github.com/zendev-sh/goai/provider/openai"
)

const defaultTemperature = 0.01

var sharedGoAIHTTPClient = &http.Client{Transport: http.DefaultTransport}

type goaiClientFactory struct {
	httpClient *http.Client
}

type goaiClient struct {
	model       provider.LanguageModel
	baseOptions []goai.Option
}

func NewClientFactory() agent.ModelClientFactory {
	return &goaiClientFactory{httpClient: sharedGoAIHTTPClient}
}

// CreateModelClient creates a goai model client based on model name or base URL.
func (factory *goaiClientFactory) CreateModelClient(ctx context.Context, config agent.ModelConfig) (agent.ModelClient, error) {
	_ = ctx
	httpClient := factory.httpClient
	if httpClient == nil {
		httpClient = sharedGoAIHTTPClient
	}

	var model provider.LanguageModel
	modelName := strings.ToLower(config.Name)

	if config.BaseURL != "" {
		options := []compat.Option{
			compat.WithAPIKey(config.APIKey),
			compat.WithBaseURL(config.BaseURL),
			compat.WithHTTPClient(httpClient),
		}
		model = compat.Chat(config.Name, options...)
	} else {
		p := strings.ToLower(config.Provider)
		if p == "" {
			switch {
			case strings.HasPrefix(modelName, "gpt-") || strings.HasPrefix(modelName, "o1-") || strings.HasPrefix(modelName, "o3-"):
				p = "openai"
			case strings.HasPrefix(modelName, "claude-"):
				p = "anthropic"
			case strings.HasPrefix(modelName, "deepseek-"):
				p = "deepseek"
			case strings.HasPrefix(modelName, "gemini-"):
				p = "google"
			default:
				p = "openai"
			}
		}

		switch p {
		case "openai":
			model = openai.Chat(config.Name, openai.WithAPIKey(config.APIKey), openai.WithHTTPClient(httpClient))
		case "anthropic":
			model = anthropic.Chat(config.Name, anthropic.WithAPIKey(config.APIKey), anthropic.WithHTTPClient(httpClient))
		case "deepseek":
			model = deepseek.Chat(config.Name, deepseek.WithAPIKey(config.APIKey), deepseek.WithHTTPClient(httpClient))
		case "google":
			model = google.Chat(config.Name, google.WithAPIKey(config.APIKey), google.WithHTTPClient(httpClient))
		default:
			model = openai.Chat(config.Name, openai.WithAPIKey(config.APIKey), openai.WithHTTPClient(httpClient))
		}
	}

	var baseOptions []goai.Option
	if config.DisableThinking {
		baseOptions = append(baseOptions, goai.WithProviderOptions(map[string]any{
			"thinking": map[string]any{
				"type": "disabled",
			},
		}))
	}
	return &goaiClient{model: model, baseOptions: baseOptions}, nil
}

// GenerateText runs a non-streaming goai text generation request.
func (client *goaiClient) GenerateText(ctx context.Context, request agent.ModelRequest) (agent.ModelResponse, error) {
	options, collector, err := buildGoAIOptions(ctx, request)
	if err != nil {
		return agent.ModelResponse{}, err
	}
	options = append(client.baseOptions, options...)
	result, err := goai.GenerateText(ctx, client.model, options...)
	response := modelResponseFromGenerateResult(result, collector, request.Metadata)
	if err != nil {
		executeErr := fmt.Errorf("llm execute failed: %w", agent.ContextErrorOr(ctx, err))
		return response, errors.Join(executeErr, collector.runtimeError())
	}
	if err := collector.runtimeError(); err != nil {
		return response, err
	}
	return response, nil
}

// StreamText runs a streaming goai text generation request.
func (client *goaiClient) StreamText(ctx context.Context, request agent.ModelRequest, handler agent.StreamHandler) (agent.ModelResponse, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	options, collector, err := buildGoAIOptions(streamCtx, request)
	if err != nil {
		return agent.ModelResponse{}, err
	}
	options = append(client.baseOptions, options...)
	stream, err := goai.StreamText(streamCtx, client.model, options...)
	if err != nil {
		requestErr := fmt.Errorf("llm stream request failed: %w", agent.ContextErrorOr(ctx, err))
		return partialModelResponse(collector, request.Metadata, "", ""), errors.Join(requestErr, collector.runtimeError())
	}
	var text strings.Builder
	var thinking strings.Builder
	for chunk := range stream.Stream() {
		if err := streamCtx.Err(); err != nil {
			return partialModelResponse(collector, request.Metadata, text.String(), thinking.String()), errors.Join(err, collector.runtimeError())
		}
		if err := handleStreamChunk(streamCtx, handler, chunk, &text, &thinking); err != nil {
			cancel()
			handlerErr := agent.ContextErrorOr(streamCtx, err)
			return partialModelResponse(collector, request.Metadata, text.String(), thinking.String()), errors.Join(handlerErr, collector.runtimeError())
		}
	}
	result := stream.Result()
	response := modelResponseFromGenerateResult(result, collector, request.Metadata)
	if err := stream.Err(); err != nil {
		return response, errors.Join(agent.ContextErrorOr(ctx, err), collector.runtimeError())
	}
	if err := collector.runtimeError(); err != nil {
		return response, err
	}
	return response, nil
}

func handleStreamChunk(ctx context.Context, handler agent.StreamHandler, chunk provider.StreamChunk, text *strings.Builder, thinking *strings.Builder) error {
	switch chunk.Type {
	case provider.ChunkText:
		if handler != nil {
			if err := handler.OnText(ctx, chunk.Text); err != nil {
				return err
			}
		}
		text.WriteString(chunk.Text)
	case provider.ChunkReasoning:
		if thinkingHandler, ok := handler.(agent.ThinkingStreamHandler); ok {
			if err := thinkingHandler.OnThinking(ctx, chunk.Text); err != nil {
				return err
			}
		}
		thinking.WriteString(chunk.Text)
	}
	return nil
}

func partialModelResponse(collector *toolCallCollector, metadata map[string]string, text string, thinking string) agent.ModelResponse {
	return agent.ModelResponse{
		Text:      text,
		Thinking:  thinking,
		ToolCalls: collector.recordsSnapshot(),
		Metadata:  copyStringMap(metadata),
	}
}

func textFromGoAIResult(result *goai.TextResult) string {
	if len(result.Steps) == 0 {
		return result.Text
	}
	var text strings.Builder
	for _, step := range result.Steps {
		text.WriteString(step.Text)
	}
	return text.String()
}

func thinkingFromGoAIResult(result *goai.TextResult) string {
	if len(result.Steps) == 0 {
		return result.Reasoning
	}
	var thinking strings.Builder
	for _, step := range result.Steps {
		thinking.WriteString(step.Reasoning)
	}
	return thinking.String()
}

func modelResponseFromGenerateResult(result *goai.TextResult, collector *toolCallCollector, metadata map[string]string) agent.ModelResponse {
	if result == nil {
		return partialModelResponse(collector, metadata, "", "")
	}
	return agent.ModelResponse{
		Text:      textFromGoAIResult(result),
		Thinking:  thinkingFromGoAIResult(result),
		Messages:  messagesFromProvider(result.ResponseMessages),
		ToolCalls: collector.recordsSnapshot(),
		Usage:     usageFromProvider(result.TotalUsage),
		Metadata:  copyStringMap(metadata),
		Model:     modelInfoFromProvider(result.Response),
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
