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

func TestChatRequestMessagesAreNativeObjects(t *testing.T) {
	request, err := newChatRequest(protocol.ModelRequest{
		Model: "model-1",
		Messages: []protocol.Message{{
			Role: protocol.RoleUser, Name: "ada",
			Parts:           []protocol.ContentPart{{Type: protocol.ContentText, Text: "hi"}},
			ProviderOptions: map[string]any{"vendor_extension": map[string]any{"enabled": true}},
		}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 1 {
		t.Fatalf("messages = %#v", request.Messages)
	}
	message := request.Messages[0]
	if message["role"] != "user" || message["content"] != "hi" || message["name"] != "ada" {
		t.Fatalf("message = %#v", message)
	}
	extension, ok := message["vendor_extension"].(map[string]any)
	if !ok || extension["enabled"] != true {
		t.Fatalf("extension = %#v", message["vendor_extension"])
	}
	payload := request.Payload()
	encoded, err := json.Marshal(payload["messages"])
	if err != nil {
		t.Fatal(err)
	}
	var messages []map[string]any
	if err := json.Unmarshal(encoded, &messages); err != nil || messages[0]["content"] != "hi" {
		t.Fatalf("messages = %s err=%v", encoded, err)
	}
}

func TestConvertChatCompletionKeepsTextToolCallsAndUsage(t *testing.T) {
	response := map[string]any{
		"id": "chatcmpl-1", "model": "model-1", "created": float64(123), "object": "chat.completion",
		"choices": []any{map[string]any{
			"finish_reason": "tool_calls",
			"message": map[string]any{
				"content":           []any{map[string]any{"type": "text", "text": "hello"}},
				"reasoning_content": "think",
				"tool_calls": []any{map[string]any{
					"id": "call-1", "type": "function",
					"function": map[string]any{"name": "lookup", "arguments": `{"q":"x"}`},
				}},
			},
		}},
		"usage": map[string]any{
			"prompt_tokens": float64(3), "completion_tokens": float64(2), "total_tokens": float64(5),
			"completion_tokens_details": map[string]any{"reasoning_tokens": float64(1)},
		},
	}
	converted, err := convertChatCompletion(response, "openai")
	if err != nil {
		t.Fatal(err)
	}
	calls := converted.Message.ToolCalls()
	if converted.Message.Text() != "hello" || converted.Message.Reasoning() != "think" || converted.FinishReason != protocol.FinishToolCalls || len(calls) != 1 || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"x"}` || converted.Usage.ReasoningTokens != 1 || converted.Metadata["request_id"] != "chatcmpl-1" {
		t.Fatalf("converted = %#v", converted)
	}
	if _, err := normalizeChatCompletion(map[string]any{"object": "chat.completion.chunk", "choices": []any{}}, true); err == nil {
		t.Fatal("chunk object accepted as a non-streaming completion")
	}
}

func TestChatReturnsChatCompletionObject(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload struct {
			Stream   bool             `json:"stream"`
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if payload.Stream || payload.Messages[0]["content"] != "hi" {
			t.Errorf("payload = %#v", payload)
		}
		body := `{
			"id":"chatcmpl-1","model":"model-1","created":123,
			"choices":[{"index":0,"message":{"role":"assistant","content":[{"type":"text","text":"hello"}]},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"prompt_tokens_details":{"cached_tokens":1}},
			"vendor":{"signed":"opaque"}
		}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	provider, err := New(Config{BaseURL: "https://example.com/v1", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Chat(context.Background(), ChatRequest{
		Model:    "model-1",
		Messages: []map[string]any{{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response["object"] != "chat.completion" || response["id"] != "chatcmpl-1" {
		t.Fatalf("response = %#v", response)
	}
	if response["vendor"].(map[string]any)["signed"] != "opaque" {
		t.Fatalf("vendor dropped: %#v", response)
	}
	choices := response["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	if message["content"].([]any)[0].(map[string]any)["text"] != "hello" {
		t.Fatalf("message = %#v", message)
	}
	_, err = provider.Chat(context.Background(), ChatRequest{Model: "model-1", Messages: []map[string]any{{"role": "user"}}, Stream: true})
	if err == nil {
		t.Fatal("streaming Chat was accepted")
	}
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
		Reasoning: &protocol.ReasoningOptions{Effort: "high"},
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

	provider, err := New(Config{BaseURL: "https://example.com/v1", APIKey: "token", APIType: APITypeResponsesAPI, HTTPClient: client})
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

func TestResponsesRequestMapsExplicitReasoning(t *testing.T) {
	payload, err := buildResponsesRequest(protocol.ModelRequest{Model: "m", Reasoning: &protocol.ReasoningOptions{Effort: "none"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "none" {
		t.Fatalf("payload=%#v", payload)
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

func TestBuildRequestPreservesNativeThinkingOption(t *testing.T) {
	thinking := map[string]any{"type": "enabled"}
	payload, err := buildRequest(protocol.ModelRequest{Model: "m", ProviderOptions: map[string]any{"thinking": thinking}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(payload["thinking"], thinking) {
		t.Fatalf("payload=%#v", payload)
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
	provider, err := New(Config{BaseURL: "https://example.com/v1", HTTPClient: client, Retry: RetryPolicy{MaxRetries: 2, Backoff: time.Nanosecond}})
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
	provider, err := New(Config{BaseURL: "https://example.com/v1", HTTPClient: client, MaxResponseBytes: 32})
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
	if len(messages) != 1 || messages[0]["role"] != "tool" || messages[0]["tool_call_id"] != "call-1" || messages[0]["content"] != "firstsecond" {
		t.Fatalf("messages=%#v", messages)
	}
}
