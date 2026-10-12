package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	protocol "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestProviderGenerateMapsRequestAndResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("authorization = %q", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if payload["model"] != "model-1" || payload["stream"] != false || payload["reasoning_effort"] != "high" {
			t.Errorf("payload = %#v", payload)
		}
		if tools, ok := payload["tools"].([]any); !ok || len(tools) != 1 {
			t.Errorf("tools = %#v", payload["tools"])
		}
		body := `{
            "id":"req-1","model":"model-1","created":123,
            "choices":[{"index":0,"message":{"role":"assistant","content":"hello","reasoning_content":"think"},"finish_reason":"stop"}],
            "usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"completion_tokens_details":{"reasoning_tokens":1}}
        }`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	provider, err := New(Config{BaseURL: "https://example.com/v1", APIKey: "token", HTTPClient: client})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	response, err := provider.Generate(context.Background(), protocol.ModelRequest{
		Model:    "model-1",
		Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")},
		Tools: []protocol.ToolDefinition{{
			Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		ProviderOptions: map[string]any{"reasoning_effort": "high"},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if response.Message.Text() != "hello" || response.Message.Reasoning() != "think" {
		t.Fatalf("message = %#v", response.Message)
	}
	if response.Usage.TotalTokens != 5 || response.Usage.ReasoningTokens != 1 {
		t.Fatalf("usage = %#v", response.Usage)
	}
	if response.Metadata["request_id"] != "req-1" || response.FinishReason != protocol.FinishStop {
		t.Fatalf("response = %#v", response)
	}
}

func TestResponsesProviderGenerateMapsRequestAndResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/responses" {
			t.Errorf("path = %q", request.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if payload["model"] != "model-1" || payload["stream"] != false || payload["instructions"] != "be concise" {
			t.Errorf("payload = %#v", payload)
		}
		input, ok := payload["input"].([]any)
		if !ok || len(input) != 1 {
			t.Fatalf("input = %#v", payload["input"])
		}
		tools, ok := payload["tools"].([]any)
		if !ok || len(tools) != 1 || tools[0].(map[string]any)["type"] != "function" {
			t.Fatalf("tools = %#v", payload["tools"])
		}
		body := `{
            "id":"resp-1","model":"model-1","created_at":123,"status":"completed",
            "output":[
              {"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]},
              {"type":"reasoning","summary":[{"type":"summary_text","text":"think"}]}
            ],
            "usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5,"output_tokens_details":{"reasoning_tokens":1}}
        }`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	provider, err := New(Config{BaseURL: "https://example.com/v1", APIKey: "token", APIType: ResponsesAPI, HTTPClient: client})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	response, err := provider.Generate(context.Background(), protocol.ModelRequest{
		Model: "model-1",
		Messages: []protocol.Message{
			protocol.TextMessage(protocol.RoleSystem, "be concise"),
			protocol.TextMessage(protocol.RoleUser, "hi"),
		},
		MaxOutputTokens: func() *int { value := 12; return &value }(),
		Tools:           []protocol.ToolDefinition{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if response.Message.Text() != "hello" || response.Message.Reasoning() != "think" {
		t.Fatalf("message = %#v", response.Message)
	}
	if response.Usage.TotalTokens != 5 || response.Usage.ReasoningTokens != 1 {
		t.Fatalf("usage = %#v", response.Usage)
	}
	if response.Metadata["request_id"] != "resp-1" || response.FinishReason != protocol.FinishStop {
		t.Fatalf("response = %#v", response)
	}
}

func TestResponsesRequestMapsDisabledThinkingToNoReasoning(t *testing.T) {
	payload, err := buildResponsesRequest(protocol.ModelRequest{
		Model:    "model-1",
		Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")},
		ProviderOptions: map[string]any{
			"thinking": map[string]any{"type": "disabled"},
		},
	}, false)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "none" {
		t.Fatalf("reasoning = %#v", payload["reasoning"])
	}
	if _, exists := payload["thinking"]; exists {
		t.Fatalf("thinking should be translated, payload = %#v", payload)
	}
}

func TestResponsesProviderStreamAccumulatesTypedEvents(t *testing.T) {
	streamBody := strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"Hel"}`,
		"",
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc-1","call_id":"call-1","name":"lookup","arguments":""}}`,
		"",
		`event: response.function_call_arguments.delta`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc-1","delta":"{\"city\":"}`,
		"",
		`event: response.function_call_arguments.delta`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc-1","delta":"\"BJ\"}"}`,
		"",
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp-stream","model":"model-1","created_at":123,"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Hello"}]},{"type":"function_call","id":"fc-1","call_id":"call-1","name":"lookup","arguments":"{\"city\":\"BJ\"}"}],"usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}}`,
		"",
	}, "\n")
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/responses" {
			t.Errorf("path = %q", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(streamBody)),
		}, nil
	})}

	provider, err := New(Config{BaseURL: "https://example.com/v1", APIType: ResponsesAPI, HTTPClient: client})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	var events []protocol.StreamEventType
	response, err := provider.Stream(context.Background(), protocol.ModelRequest{
		Model: "model-1", Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")},
	}, protocol.EventSinkFunc(func(_ context.Context, event protocol.StreamEvent) error {
		events = append(events, event.Type)
		return nil
	}))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if response.Message.Text() != "Hel" {
		t.Fatalf("message text = %q", response.Message.Text())
	}
	calls := response.Message.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "call-1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"city":"BJ"}` {
		t.Fatalf("calls = %#v", calls)
	}
	if response.FinishReason != protocol.FinishToolCalls || response.Usage.TotalTokens != 7 {
		t.Fatalf("response = %#v", response)
	}
	wantEvents := []protocol.StreamEventType{
		protocol.EventTextDelta, protocol.EventToolCallDelta, protocol.EventToolCallDelta,
		protocol.EventToolCallDelta, protocol.EventUsage,
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
}

func TestProviderStreamAccumulatesTextReasoningAndToolCallDeltas(t *testing.T) {
	streamBody := strings.Join([]string{
		`data: {"id":"req-stream","model":"model-1","choices":[{"index":0,"delta":{"content":"Hel","reasoning_content":"th"}}]}`,
		"",
		`data: {"choices":[{"index":0,"delta":{"content":"lo","reasoning_content":"ink","tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"look","arguments":"{\"city\":"}}]}}]}`,
		"",
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"up","arguments":"\"BJ\"}"}}]},"finish_reason":"tool_calls"}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(streamBody)),
		}, nil
	})}

	provider, err := New(Config{BaseURL: "https://example.com/v1", Name: "deepseek", HTTPClient: client})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	var events []protocol.StreamEventType
	response, err := provider.Stream(context.Background(), protocol.ModelRequest{
		Model: "model-1", Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")},
	}, protocol.EventSinkFunc(func(_ context.Context, event protocol.StreamEvent) error {
		events = append(events, event.Type)
		return nil
	}))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if response.Message.Text() != "Hello" || response.Message.Reasoning() != "think" {
		t.Fatalf("message = %#v", response.Message)
	}
	calls := response.Message.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "call-1" || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"city":"BJ"}` {
		t.Fatalf("calls = %#v", calls)
	}
	if response.FinishReason != protocol.FinishToolCalls || response.Usage.TotalTokens != 7 {
		t.Fatalf("response = %#v", response)
	}
	if got := response.Metadata["provider"]; got != "deepseek" {
		t.Fatalf("provider metadata = %q", got)
	}
	wantEvents := []protocol.StreamEventType{
		protocol.EventTextDelta, protocol.EventReasoningDelta,
		protocol.EventTextDelta, protocol.EventReasoningDelta, protocol.EventToolCallDelta,
		protocol.EventToolCallDelta, protocol.EventUsage,
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
}

func TestBuildRequestRejectsProviderOptionThatOverridesCommonField(t *testing.T) {
	_, err := buildRequest(protocol.ModelRequest{
		Model:           "model-1",
		Messages:        []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")},
		ProviderOptions: map[string]any{"stream": false},
	}, true)
	if err == nil || !strings.Contains(err.Error(), `provider option "stream"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRequestPreservesExtraBodyThinkingOption(t *testing.T) {
	extraBody := map[string]any{"thinking": map[string]any{"type": "enabled"}}
	payload, err := buildRequest(protocol.ModelRequest{
		Model:           "model-1",
		Messages:        []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")},
		ProviderOptions: map[string]any{"extra_body": extraBody},
	}, false)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	got, ok := payload["extra_body"].(map[string]any)
	if !ok || got["thinking"] == nil {
		t.Fatalf("extra_body = %#v", payload["extra_body"])
	}
}

func TestProviderStreamRequiresDoneMarker(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}`))}, nil
	})}
	provider, err := New(Config{BaseURL: "https://example.com/v1", HTTPClient: client, MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Stream(context.Background(), protocol.ModelRequest{Model: "m", Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")}}, nil)
	if err == nil || !strings.Contains(err.Error(), "without [DONE]") {
		t.Fatalf("err=%v response=%#v", err, response)
	}
}

func TestProviderRetriesTransientStatus(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		attempts++
		if attempts < 3 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("busy"))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))}, nil
	})}
	provider, err := New(Config{BaseURL: "https://example.com/v1", HTTPClient: client, MaxRetries: 2, RetryBackoff: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Generate(context.Background(), protocol.ModelRequest{Model: "m", Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")}})
	if err != nil || response.Message.Text() != "ok" || attempts != 3 {
		t.Fatalf("attempts=%d response=%#v err=%v", attempts, response, err)
	}
}

func TestProviderRejectsOversizedResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 100)))}, nil
	})}
	provider, err := New(Config{BaseURL: "https://example.com/v1", HTTPClient: client, MaxResponseBytes: 32, MaxRetries: -1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Generate(context.Background(), protocol.ModelRequest{Model: "m", Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")}})
	if err == nil {
		t.Fatal("expected oversized response error")
	}
}

func TestProviderNameDefaultsToOpenAI(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://example.com/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := provider.Name(); got != "openai" {
		t.Fatalf("name = %q", got)
	}
}

func TestProviderRejectsUnknownAPIType(t *testing.T) {
	if _, err := New(Config{BaseURL: "https://example.com/v1", APIType: APIType("unknown")}); err == nil {
		t.Fatal("expected unknown API type error")
	}
}

func TestProviderNameUsesConfiguredOverride(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://api.deepseek.com", Name: "deepseek"})
	if err != nil {
		t.Fatal(err)
	}
	if got := provider.Name(); got != "deepseek" {
		t.Fatalf("name = %q", got)
	}
}

func TestProviderGenerateMetadataUsesConfiguredName(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"id":"req-1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`,
			)),
		}, nil
	})}
	provider, err := New(Config{BaseURL: "https://api.deepseek.com", Name: "deepseek", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Generate(context.Background(), protocol.ModelRequest{
		Model: "model", Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := response.Metadata["provider"]; got != "deepseek" {
		t.Fatalf("provider metadata = %q", got)
	}
}

func TestEncodeImagePartPrefersFileIDOverURL(t *testing.T) {
	part, err := encodeImagePart(&protocol.MediaContent{FileID: "file-abc", URL: "https://example.com/x.png"})
	if err != nil {
		t.Fatal(err)
	}
	if part["type"] != "file" || part["file_id"] != "file-abc" {
		t.Fatalf("part = %#v", part)
	}
}

func TestEncodeImagePartIncludesDetailWhenSet(t *testing.T) {
	part, err := encodeImagePart(&protocol.MediaContent{URL: "https://example.com/x.png", Detail: "low"})
	if err != nil {
		t.Fatal(err)
	}
	imageURL, ok := part["image_url"].(map[string]any)
	if !ok || imageURL["detail"] != "low" || imageURL["url"] != "https://example.com/x.png" {
		t.Fatalf("part = %#v", part)
	}
}

func TestEncodeImagePartOmitsDetailWhenEmpty(t *testing.T) {
	part, err := encodeImagePart(&protocol.MediaContent{URL: "https://example.com/x.png"})
	if err != nil {
		t.Fatal(err)
	}
	imageURL, ok := part["image_url"].(map[string]any)
	if !ok {
		t.Fatalf("part = %#v", part)
	}
	if _, exists := imageURL["detail"]; exists {
		t.Fatalf("detail should be omitted, got %#v", imageURL)
	}
}

func TestEncodeFilePartUsesFileID(t *testing.T) {
	part, err := encodeFilePart(&protocol.MediaContent{FileID: "file-abc"})
	if err != nil {
		t.Fatal(err)
	}
	if part["type"] != "file" || part["file_id"] != "file-abc" {
		t.Fatalf("part = %#v", part)
	}
}

func TestEncodeFilePartInlinesBase64Data(t *testing.T) {
	part, err := encodeFilePart(&protocol.MediaContent{Data: "aGVsbG8=", MIMEType: "text/plain", Filename: "hello.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if part["type"] != "file" {
		t.Fatalf("part = %#v", part)
	}
	filePayload, ok := part["file"].(map[string]any)
	if !ok || filePayload["file_data"] != "data:text/plain;base64,aGVsbG8=" || filePayload["filename"] != "hello.txt" {
		t.Fatalf("file payload = %#v", filePayload)
	}
}

func TestEncodeFilePartRejectsMissingIDAndData(t *testing.T) {
	_, err := encodeFilePart(&protocol.MediaContent{})
	if err == nil {
		t.Fatal("expected error for empty file media")
	}
}

func TestEncodeToolResultsRejectsUnsupportedRichContent(t *testing.T) {
	result := protocol.ToolResult{
		ToolCallID: "call-1",
		ToolName:   "lookup",
		Content: []protocol.ContentPart{
			{Type: protocol.ContentText, Text: "caption"},
			{Type: protocol.ContentImage, Media: &protocol.MediaContent{URL: "https://example.com/image.png"}},
		},
	}
	_, err := encodeToolResults(protocol.Message{Role: protocol.RoleTool, Parts: []protocol.ContentPart{{
		Type: protocol.ContentToolResult, ToolResult: &result,
	}}})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("err=%v", err)
	}
}

func TestEncodeToolResultsConcatenatesTextParts(t *testing.T) {
	result := protocol.ToolResult{
		ToolCallID: "call-1",
		ToolName:   "lookup",
		Content: []protocol.ContentPart{
			{Type: protocol.ContentText, Text: "first"},
			{Type: protocol.ContentText, Text: "second"},
		},
	}
	messages, err := encodeToolResults(protocol.Message{Role: protocol.RoleTool, Parts: []protocol.ContentPart{{
		Type: protocol.ContentToolResult, ToolResult: &result,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "firstsecond" {
		t.Fatalf("messages=%#v", messages)
	}
}
