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
	return ModelResponse{Text: "done", Usage: &Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}}, client.generateError
}

func (client *fakeModelClient) StreamText(ctx context.Context, request ModelRequest, handler StreamHandler) (ModelResponse, error) {
	client.streamRequest = request
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
	if client.generateRequest.Options.MaxMessages != DefaultMaxMessages {
		t.Fatalf("expected default max messages, got %d", client.generateRequest.Options.MaxMessages)
	}
	if len(client.generateRequest.Messages) != 0 {
		t.Fatalf("expected empty history, got %d messages", len(client.generateRequest.Messages))
	}
	if response.Text != "done" || response.Usage.TotalTokens != 3 || response.Metadata["trace"] != "1" {
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

func TestEngineExecuteNormalizesHistoryMessages(t *testing.T) {
	client := &fakeModelClient{}
	engine := NewEngineWithClientFactory(&fakeModelClientFactory{client: client})
	history := make([]Message, DefaultMaxMessages+2)
	for index := range history {
		history[index] = TextMessage{Role: RoleUser, Content: "history"}
	}

	_, err := engine.Execute(context.Background(), Request{
		ModelConfig: ModelConfig{Provider: "openai", APIKey: "token", BaseURL: "https://example.com", Name: "model"},
		UserPrompt:  "hello",
		History:     history,
		Options:     RunOptions{MaxMessages: DefaultMaxMessages},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(client.generateRequest.Messages) != DefaultMaxMessages {
		t.Fatalf("expected %d history messages, got %d", DefaultMaxMessages, len(client.generateRequest.Messages))
	}
	for index, message := range client.generateRequest.Messages {
		if messageID(message) == "" {
			t.Fatalf("expected message id at index %d", index)
		}
	}
}

type fakeModelClientWithMessages struct {
	fakeModelClient
	responseMessages []Message
}

func (client *fakeModelClientWithMessages) GenerateText(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	response, err := client.fakeModelClient.GenerateText(ctx, request)
	if err != nil {
		return response, err
	}
	response.Messages = append([]Message(nil), client.responseMessages...)
	return response, nil
}

func TestEngineExecuteAssignsMessageIDsInResponse(t *testing.T) {
	client := &fakeModelClientWithMessages{
		responseMessages: []Message{
			TextMessage{Role: RoleAssistant, Content: "done"},
		},
	}
	engine := NewEngineWithClientFactory(&fakeModelClientFactory{client: client})
	response, err := engine.Execute(context.Background(), Request{
		ModelConfig: ModelConfig{Provider: "openai", APIKey: "token", BaseURL: "https://example.com", Name: "model"},
		UserPrompt:  "hello",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(response.Messages) != 1 {
		t.Fatalf("expected 1 response message, got %d", len(response.Messages))
	}
	if messageID(response.Messages[0]) == "" {
		t.Fatalf("expected response message id, got empty")
	}
}
