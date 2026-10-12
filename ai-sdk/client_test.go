package ai

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

type scriptedProvider struct {
	responses []protocol.ModelResponse
	requests  []protocol.ModelRequest
}

func (provider *scriptedProvider) Name() string { return "scripted" }

func (provider *scriptedProvider) Generate(_ context.Context, request protocol.ModelRequest) (protocol.ModelResponse, error) {
	provider.requests = append(provider.requests, request)
	if len(provider.responses) == 0 {
		return protocol.ModelResponse{}, errors.New("no scripted response")
	}
	response := provider.responses[0]
	provider.responses = provider.responses[1:]
	return response, nil
}

func TestClientGenerateHooksSurroundProviderCall(t *testing.T) {
	var lifecycle []string
	provider := &scriptedProvider{responses: []protocol.ModelResponse{{Message: protocol.TextMessage(protocol.RoleAssistant, "ok")}}}
	client := NewClient(provider, Hooks{
		OnRequest: []RequestHook{func(_ context.Context, _ RequestInfo) error {
			lifecycle = append(lifecycle, "request")
			return nil
		}},
		OnResponse: []ResponseHook{func(_ context.Context, info ResponseInfo) error {
			if info.Error != nil {
				t.Fatalf("unexpected response error: %v", info.Error)
			}
			lifecycle = append(lifecycle, "response")
			return nil
		}},
	})

	response, err := client.Generate(context.Background(), protocol.ModelRequest{
		Model:    "model",
		Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("generate() error = %v", err)
	}
	if response.Message.Text() != "ok" {
		t.Fatalf("response text = %q, want ok", response.Message.Text())
	}
	if got, want := strings.Join(lifecycle, ","), "request,response"; got != want {
		t.Fatalf("hook order = %q, want %q", got, want)
	}
}

func TestClientHooksShareRequestAndRequestID(t *testing.T) {
	provider := &scriptedProvider{responses: []protocol.ModelResponse{{
		Message: protocol.TextMessage(protocol.RoleAssistant, "ok"),
	}}}
	var requestIDFromRequest, requestIDFromResponse string
	var responseRequest protocol.ModelRequest
	client := NewClient(provider, Hooks{
		OnRequest: []RequestHook{func(_ context.Context, info RequestInfo) error {
			requestIDFromRequest = info.RequestID
			return nil
		}},
		OnResponse: []ResponseHook{func(_ context.Context, info ResponseInfo) error {
			requestIDFromResponse = info.RequestID
			responseRequest = info.Request
			return nil
		}},
	})
	request := protocol.ModelRequest{
		Model:    "model",
		Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hello")},
		Metadata: map[string]string{RequestIDMetadataKey: "req_test_123"},
	}
	if _, err := client.Generate(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if requestIDFromRequest != "req_test_123" || requestIDFromResponse != requestIDFromRequest {
		t.Fatalf("request IDs request=%q response=%q", requestIDFromRequest, requestIDFromResponse)
	}
	if responseRequest.Model != request.Model || len(responseRequest.Messages) != 1 || responseRequest.Metadata[RequestIDMetadataKey] != "req_test_123" {
		t.Fatalf("response request=%#v", responseRequest)
	}
}

func TestClientGenerateRequestHookErrorSkipsProviderCall(t *testing.T) {
	provider := &scriptedProvider{responses: []protocol.ModelResponse{{Message: protocol.TextMessage(protocol.RoleAssistant, "ok")}}}
	client := NewClient(provider, Hooks{
		OnRequest: []RequestHook{func(_ context.Context, _ RequestInfo) error {
			return errors.New("blocked")
		}},
	})

	_, err := client.Generate(context.Background(), protocol.ModelRequest{
		Model:    "model",
		Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")},
	})
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("err = %v, want blocked", err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("provider should not have been called, got %d requests", len(provider.requests))
	}
}

func TestClientGenerateResponseHookObservesProviderError(t *testing.T) {
	provider := &scriptedProvider{}
	var observedErr error
	client := NewClient(provider, Hooks{
		OnResponse: []ResponseHook{func(_ context.Context, info ResponseInfo) error {
			observedErr = info.Error
			return nil
		}},
	})

	_, err := client.Generate(context.Background(), protocol.ModelRequest{
		Model:    "model",
		Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")},
	})
	if err == nil {
		t.Fatalf("expected provider error")
	}
	if observedErr == nil || observedErr.Error() != err.Error() {
		t.Fatalf("observed error = %v, want %v", observedErr, err)
	}
}

func TestClientStreamRejectsProviderWithoutStreaming(t *testing.T) {
	provider := &scriptedProvider{responses: []protocol.ModelResponse{{Message: protocol.TextMessage(protocol.RoleAssistant, "ok")}}}
	hookCalled := false
	client := NewClient(provider, Hooks{
		OnRequest: []RequestHook{func(_ context.Context, _ RequestInfo) error {
			hookCalled = true
			return nil
		}},
	})

	_, err := client.Stream(context.Background(), protocol.ModelRequest{
		Model: "model", Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")},
	}, protocol.EventSinkFunc(func(_ context.Context, _ protocol.StreamEvent) error {
		t.Fatal("sink should not receive events")
		return nil
	}))
	if !errors.Is(err, ErrStreamingNotSupported) {
		t.Fatalf("err = %v, want ErrStreamingNotSupported", err)
	}
	if hookCalled {
		t.Fatal("request hook should not run when streaming is unsupported")
	}
	if len(provider.requests) != 0 {
		t.Fatalf("provider should not have been called, got %d requests", len(provider.requests))
	}
}

func TestClientStreamUsesProviderStreamMethod(t *testing.T) {
	streamingCalled := false
	provider := &streamingScriptedProvider{
		scriptedProvider: scriptedProvider{},
		streamFunc: func(_ context.Context, _ protocol.ModelRequest, sink protocol.EventSink) (protocol.ModelResponse, error) {
			streamingCalled = true
			_ = sink.OnEvent(context.Background(), protocol.StreamEvent{Type: protocol.EventTextDelta, Delta: "hi"})
			return protocol.ModelResponse{Message: protocol.TextMessage(protocol.RoleAssistant, "hi")}, nil
		},
	}
	client := NewClient(provider, Hooks{})

	var deltas []string
	_, err := client.Stream(context.Background(), protocol.ModelRequest{
		Model: "model", Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")},
	}, protocol.EventSinkFunc(func(_ context.Context, event protocol.StreamEvent) error {
		if event.Type == protocol.EventTextDelta {
			deltas = append(deltas, event.Delta)
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if !streamingCalled {
		t.Fatalf("expected provider Stream method to be used")
	}
	if len(deltas) != 1 || deltas[0] != "hi" {
		t.Fatalf("deltas = %#v", deltas)
	}
}

type streamingScriptedProvider struct {
	scriptedProvider
	streamFunc func(context.Context, protocol.ModelRequest, protocol.EventSink) (protocol.ModelResponse, error)
}

func (provider *streamingScriptedProvider) Stream(ctx context.Context, request protocol.ModelRequest, sink protocol.EventSink) (protocol.ModelResponse, error) {
	return provider.streamFunc(ctx, request, sink)
}

func TestClientGenerateRequiresProvider(t *testing.T) {
	client := NewClient(nil, Hooks{})
	_, err := client.Generate(context.Background(), protocol.ModelRequest{Model: "model"})
	if err == nil {
		t.Fatalf("expected error for nil provider")
	}
}

func TestClientGenerateHookPanicPropagates(t *testing.T) {
	provider := &scriptedProvider{responses: []protocol.ModelResponse{{Message: protocol.TextMessage(protocol.RoleAssistant, "ok")}}}
	client := NewClient(provider, Hooks{
		OnRequest: []RequestHook{func(_ context.Context, _ RequestInfo) error {
			panic("boom")
		}},
	})
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected hook panic to propagate")
		}
		if recovered != "boom" {
			t.Fatalf("panic = %v, want boom", recovered)
		}
		if len(provider.requests) != 0 {
			t.Fatalf("provider should not have been called, got %d requests", len(provider.requests))
		}
	}()
	_, _ = client.Generate(context.Background(), protocol.ModelRequest{
		Model:    "model",
		Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")},
	})
	t.Fatal("expected hook panic to propagate")
}
