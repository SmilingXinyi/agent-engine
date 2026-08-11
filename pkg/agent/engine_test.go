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
	waitForCancel   bool
}

func (client *fakeModelClient) GenerateText(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	client.generateRequest = request
	if client.waitForCancel {
		<-ctx.Done()
		return ModelResponse{}, ctx.Err()
	}
	return ModelResponse{Text: "done", Thinking: "thoughts", Usage: &Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}}, client.generateError
}

func (client *fakeModelClient) StreamText(ctx context.Context, request ModelRequest, handler StreamHandler) (ModelResponse, error) {
	client.streamRequest = request
	if client.waitForCancel {
		<-ctx.Done()
		return ModelResponse{}, ctx.Err()
	}
	text := ""
	for _, chunk := range []string{"a", "b", "c"} {
		if handler != nil {
			if err := handler.OnText(ctx, chunk); err != nil {
				return ModelResponse{Text: text}, err
			}
		}
		text += chunk
	}
	return ModelResponse{Text: text}, client.streamError
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
	if response.Text != "done" || response.Thinking != "thoughts" || response.Usage.TotalTokens != 3 || response.Metadata["trace"] != "1" {
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

func TestEngineExecuteForwardsMaxMessages(t *testing.T) {
	client := &fakeModelClient{}
	engine := NewEngineWithClientFactory(&fakeModelClientFactory{client: client}, WithMaxMessages(2))

	_, err := engine.Execute(context.Background(), Request{
		ModelConfig: ModelConfig{Provider: "openai", APIKey: "token", BaseURL: "https://example.com", Name: "model"},
		UserPrompt:  "current",
		History: []Message{
			TextMessage{Role: RoleUser, Content: "old"},
			TextMessage{Role: RoleAssistant, Content: "previous"},
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if client.generateRequest.MaxMessages != 2 {
		t.Fatalf("expected max messages to be forwarded, got %d", client.generateRequest.MaxMessages)
	}
}

func TestEngineExecutePreservesResponseWhenClientReturnsError(t *testing.T) {
	wantErr := errors.New("model failed")
	engine := NewEngineWithClientFactory(&fakeModelClientFactory{client: &fakeModelClient{generateError: wantErr}})
	response, err := engine.Execute(context.Background(), Request{
		ModelConfig: ModelConfig{Provider: "openai", APIKey: "token", BaseURL: "https://example.com", Name: "model"},
		UserPrompt:  "hello",
		Metadata:    map[string]string{"request_id": "request-1"},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
	if response.Text != "done" || response.Thinking != "thoughts" {
		t.Fatalf("expected partial response text and thinking, got %+v", response)
	}
	if response.Usage == nil || response.Usage.TotalTokens != 3 {
		t.Fatalf("expected partial response usage, got %+v", response.Usage)
	}
	if response.Metadata["request_id"] != "request-1" {
		t.Fatalf("expected response metadata, got %+v", response.Metadata)
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

func TestEngineExecuteRequiresFactory(t *testing.T) {
	engine := NewEngine()
	_, err := engine.Execute(context.Background(), Request{
		ModelConfig: ModelConfig{APIKey: "token", Name: "model"},
		UserPrompt:  "hello",
	})
	if err == nil {
		t.Fatal("expected factory required error")
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

func TestEngineExecuteStopsWhenContextCanceled(t *testing.T) {
	client := &fakeModelClient{waitForCancel: true}
	engine := NewEngineWithClientFactory(&fakeModelClientFactory{client: client})
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, executeErr := engine.Execute(ctx, Request{
			ModelConfig: ModelConfig{Provider: "openai", APIKey: "token", Name: "model"},
			UserPrompt:  "hello",
		})
		errCh <- executeErr
	}()

	cancel()
	if err := <-errCh; !IsExecutionCanceled(err) {
		t.Fatalf("expected execution canceled, got %v", err)
	}
}
