package goai

import (
	"context"
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

type goaiClientFactory struct{}

type goaiClient struct {
	model provider.LanguageModel
}

func NewClientFactory() agent.ModelClientFactory {
	return goaiClientFactory{}
}

// CreateModelClient creates a goai model client based on model name or base URL.
func (goaiClientFactory) CreateModelClient(ctx context.Context, config agent.ModelConfig) (agent.ModelClient, error) {
	_ = ctx
	httpClient := &http.Client{
		Transport: &http.Transport{Proxy: nil},
	}

	var model provider.LanguageModel
	modelName := strings.ToLower(config.Name)

	// 1. If BaseURL is provided, we use the compat (OpenAI-compatible) driver.
	if config.BaseURL != "" {
		options := []compat.Option{
			compat.WithAPIKey(config.APIKey),
			compat.WithBaseURL(config.BaseURL),
			compat.WithHTTPClient(httpClient),
		}
		model = compat.Chat(config.Name, options...)
		return &goaiClient{model: model}, nil
	}

	// 2. If no BaseURL, try to route based on model name prefix or explicit provider.
	p := strings.ToLower(config.Provider)
	if p == "" {
		// Auto-detect provider from model name
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
			// Default to openai if unknown
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
		// Last resort: try openai driver
		model = openai.Chat(config.Name, openai.WithAPIKey(config.APIKey), openai.WithHTTPClient(httpClient))
	}

	return &goaiClient{model: model}, nil
}

// GenerateText runs a non-streaming goai text generation request.
func (client *goaiClient) GenerateText(ctx context.Context, request agent.ModelRequest) (agent.ModelResponse, error) {
	options, collector, err := buildGoAIOptions(ctx, request)
	if err != nil {
		return agent.ModelResponse{}, err
	}
	result, err := goai.GenerateText(ctx, client.model, options...)
	if err != nil {
		return agent.ModelResponse{}, fmt.Errorf("llm execute failed: %w", err)
	}
	return agent.ModelResponse{
		Text:      result.Text,
		Messages:  messagesFromProvider(result.ResponseMessages),
		ToolCalls: collector.recordsSnapshot(),
		Usage:     usageFromProvider(result.TotalUsage),
		Metadata:  copyStringMap(request.Metadata),
	}, nil
}

// StreamText runs a streaming goai text generation request.
func (client *goaiClient) StreamText(ctx context.Context, request agent.ModelRequest, handler agent.StreamHandler) (agent.ModelResponse, error) {
	options, collector, err := buildGoAIOptions(ctx, request)
	if err != nil {
		return agent.ModelResponse{}, err
	}
	stream, err := goai.StreamText(ctx, client.model, options...)
	if err != nil {
		return agent.ModelResponse{}, fmt.Errorf("llm stream request failed: %w", err)
	}
	for chunk := range stream.TextStream() {
		if handler != nil {
			if err := handler.OnText(ctx, chunk); err != nil {
				result := stream.Result()
				return modelResponseFromStreamResult(result, collector, request.Metadata), err
			}
		}
	}
	result := stream.Result()
	if err := stream.Err(); err != nil {
		return modelResponseFromStreamResult(result, collector, request.Metadata), err
	}
	return modelResponseFromStreamResult(result, collector, request.Metadata), nil
}

func modelResponseFromStreamResult(result *goai.TextResult, collector *toolCallCollector, metadata map[string]string) agent.ModelResponse {
	if result == nil {
		return agent.ModelResponse{Metadata: copyStringMap(metadata)}
	}
	return agent.ModelResponse{
		Text:      result.Text,
		Messages:  messagesFromProvider(result.ResponseMessages),
		ToolCalls: collector.recordsSnapshot(),
		Usage:     usageFromProvider(result.TotalUsage),
		Metadata:  copyStringMap(metadata),
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
