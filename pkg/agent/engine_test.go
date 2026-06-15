package agent

import (
	"context"
	"errors"
	"testing"
)

type fakeModelClientFactory struct {
	client        ModelClient
	receivedModel ModelConfig
}

func (factory *fakeModelClientFactory) CreateModelClient(ctx context.Context, config ModelConfig) (ModelClient, error) {
	_ = ctx
	factory.receivedModel = config
	return factory.client, nil
}

type fakeModelClient struct {
	generateRequest ModelRequest
	streamRequest   ModelRequest
	generateError   error
	streamError     error
}

func (client *fakeModelClient) GenerateText(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	_ = ctx
	client.generateRequest = request
	return ModelResponse{Text: "done", Thinking: "thought", Usage: &Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}}, client.generateError
}

func (client *fakeModelClient) StreamText(ctx context.Context, request ModelRequest, handler StreamHandler) (ModelResponse, error) {
	client.streamRequest = request
	text := ""
	thinking := ""
	for _, chunk := range []string{"a", "b", "c"} {
		if err := ctx.Err(); err != nil {
			return ModelResponse{Text: text, Thinking: thinking}, err
		}
		if handler != nil {
			if err := handler.OnText(ctx, chunk); err != nil {
				return ModelResponse{Text: text, Thinking: thinking}, err
			}
		}
		text += chunk
	}
	if thinkingHandler, ok := handler.(ThinkingStreamHandler); ok {
		for _, chunk := range []string{"t", "h"} {
			if err := thinkingHandler.OnThinking(ctx, chunk); err != nil {
				return ModelResponse{Text: text, Thinking: thinking}, err
			}
			thinking += chunk
		}
	}
	return ModelResponse{Text: text, Thinking: thinking}, client.streamError
}

func TestEngineExecuteReturnsContextErrorWhenAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	engine := NewEngineWithClientFactory(&fakeModelClientFactory{client: &fakeModelClient{}})
	_, err := engine.Execute(ctx, Request{
		ModelConfig: ModelConfig{Provider: "openai", APIKey: "token", BaseURL: "https://example.com", Name: "model"},
		UserPrompt:  "hello",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestEngineStreamReturnsContextErrorWhenAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	engine := NewEngineWithClientFactory(&fakeModelClientFactory{client: &fakeModelClient{}})
	_, err := engine.Stream(ctx, StreamRequest{
		Request: Request{
			ModelConfig: ModelConfig{Provider: "openai", APIKey: "token", BaseURL: "https://example.com", Name: "model"},
			UserPrompt:  "hello",
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestEngineExecuteUsesClientFactoryAndReturnsResponse(t *testing.T) {
	client := &fakeModelClient{}
	factory := &fakeModelClientFactory{client: client}
	engine := NewEngineWithClientFactory(factory)
	response, err := engine.Execute(context.Background(), Request{
		ModelConfig: ModelConfig{Provider: "openai", APIKey: "token", BaseURL: "https://example.com", Name: "model"},
		UserPrompt:  "hello",
		Options:     RunOptions{MaxSteps: 0},
		Metadata:    map[string]string{"trace": "1"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if factory.receivedModel.Name != "model" {
		t.Fatalf("unexpected model config: %+v", factory.receivedModel)
	}
	if client.generateRequest.Options.MaxSteps != DefaultMaxSteps {
		t.Fatalf("expected default max steps, got %d", client.generateRequest.Options.MaxSteps)
	}
	if response.Text != "done" || response.Thinking != "thought" || response.Usage.TotalTokens != 3 || response.Metadata["trace"] != "1" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestNewEngineWithClientFactoryUsesDefaultWhenFactoryIsNil(t *testing.T) {
	engine := NewEngineWithClientFactory(nil)
	defaultEngineInstance, ok := engine.(*defaultEngine)
	if !ok {
		t.Fatalf("expected default engine, got %T", engine)
	}
	if defaultEngineInstance.factory != nil {
		t.Fatal("expected nil model client factory by default in core package")
	}
	if defaultEngineInstance.validator == nil {
		t.Fatal("expected default request validator")
	}
}

func TestEngineExecutePropagatesClientError(t *testing.T) {
	wantErr := errors.New("model failed")
	engine := NewEngineWithClientFactory(&fakeModelClientFactory{client: &fakeModelClient{generateError: wantErr}})
	_, err := engine.Execute(context.Background(), Request{
		ModelConfig: ModelConfig{Provider: "openai", APIKey: "token", BaseURL: "https://example.com", Name: "model"},
		UserPrompt:  "hello",
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}

func TestEngineStreamSendsChunksAndReturnsResponse(t *testing.T) {
	client := &fakeModelClient{}
	engine := NewEngineWithClientFactory(&fakeModelClientFactory{client: client})
	chunks := ""
	response, err := engine.Stream(context.Background(), StreamRequest{
		Request: Request{
			ModelConfig: ModelConfig{Provider: "openai", APIKey: "token", BaseURL: "https://example.com", Name: "model"},
			UserPrompt:  "hello",
		},
		StreamHandler: StreamHandlerFunc(func(ctx context.Context, chunk string) error {
			_ = ctx
			chunks += chunk
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if chunks != "abc" || response.Text != "abc" {
		t.Fatalf("unexpected stream result chunks=%q response=%+v", chunks, response)
	}
	if client.streamRequest.Options.MaxSteps != DefaultMaxSteps {
		t.Fatalf("expected default max steps, got %d", client.streamRequest.Options.MaxSteps)
	}
}

func TestEngineStreamReturnsPartialResponseOnHandlerError(t *testing.T) {
	wantErr := errors.New("handler failed")
	engine := NewEngineWithClientFactory(&fakeModelClientFactory{client: &fakeModelClient{}})
	response, err := engine.Stream(context.Background(), StreamRequest{
		Request: Request{
			ModelConfig: ModelConfig{Provider: "openai", APIKey: "token", BaseURL: "https://example.com", Name: "model"},
			UserPrompt:  "hello",
		},
		StreamHandler: StreamHandlerFunc(func(ctx context.Context, chunk string) error {
			_ = ctx
			if chunk == "b" {
				return wantErr
			}
			return nil
		}),
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
	if response.Text != "a" {
		t.Fatalf("expected partial response, got %+v", response)
	}
}

func TestEngineStreamPropagatesThinkingChunks(t *testing.T) {
	engine := NewEngineWithClientFactory(&fakeModelClientFactory{client: &fakeModelClient{}})
	textChunks := ""
	thinkingChunks := ""
	response, err := engine.Stream(context.Background(), StreamRequest{
		Request: Request{
			ModelConfig: ModelConfig{Provider: "openai", APIKey: "token", BaseURL: "https://example.com", Name: "model"},
			UserPrompt:  "hello",
		},
		StreamHandler: streamHandlerWithThinking{
			text: StreamHandlerFunc(func(ctx context.Context, chunk string) error {
				_ = ctx
				textChunks += chunk
				return nil
			}),
			thinking: ThinkingStreamHandlerFunc(func(ctx context.Context, chunk string) error {
				_ = ctx
				thinkingChunks += chunk
				return nil
			}),
		},
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if textChunks != "abc" || thinkingChunks != "th" {
		t.Fatalf("unexpected chunks text=%q thinking=%q", textChunks, thinkingChunks)
	}
	if response.Text != "abc" || response.Thinking != "th" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

type streamHandlerWithThinking struct {
	text     StreamHandler
	thinking ThinkingStreamHandler
}

func (handler streamHandlerWithThinking) OnText(ctx context.Context, chunk string) error {
	return handler.text.OnText(ctx, chunk)
}

func (handler streamHandlerWithThinking) OnThinking(ctx context.Context, chunk string) error {
	return handler.thinking.OnThinking(ctx, chunk)
}
