package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

func TestCommonReasoningAndStructuredOutputAcrossAPIs(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)
	for _, api := range []APIType{APITypeChatCompletions, APITypeResponsesAPI} {
		t.Run(string(api), func(t *testing.T) {
			var payload map[string]any
			provider := testProvider(t, Config{APIType: api}, func(request *http.Request) (*http.Response, error) {
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if api == APITypeResponsesAPI {
					return testHTTPResponse(200, `{"status":"completed","output":[]}`), nil
				}
				return testHTTPResponse(200, `{"choices":[{"message":{"content":"{}"},"finish_reason":"stop"}]}`), nil
			})
			request := testRequest()
			request.Reasoning = &protocol.ReasoningOptions{Effort: "low"}
			request.OutputFormat = &protocol.OutputFormat{Type: protocol.OutputJSONSchema, Name: "answer", Schema: schema, Strict: true}
			response, err := provider.Generate(context.Background(), request)
			if err != nil || !response.Complete {
				t.Fatalf("response=%#v err=%v", response, err)
			}
			var format map[string]any
			if api == APITypeResponsesAPI {
				if payload["reasoning"].(map[string]any)["effort"] != "low" {
					t.Fatalf("payload=%#v", payload)
				}
				format = payload["text"].(map[string]any)["format"].(map[string]any)
			} else {
				if payload["reasoning_effort"] != "low" {
					t.Fatalf("payload=%#v", payload)
				}
				format = payload["response_format"].(map[string]any)["json_schema"].(map[string]any)
			}
			if format["name"] != "answer" || format["strict"] != true || format["schema"].(map[string]any)["type"] != "object" {
				t.Fatalf("format=%#v", format)
			}
		})
	}
}

func TestProviderOptionsAreNativeAndDoNotOverrideCommonFields(t *testing.T) {
	for _, api := range []APIType{APITypeChatCompletions, APITypeResponsesAPI} {
		provider, err := New(Config{BaseURL: "https://example.com/v1", APIType: api})
		if err != nil {
			t.Fatal(err)
		}
		request := testRequest()
		native := map[string]any{"type": "disabled", "vendor": true}
		request.ProviderOptions = map[string]any{"thinking": native}
		payload, err := provider.buildRequest(request, false)
		if err != nil || !reflect.DeepEqual(payload["thinking"], native) {
			t.Fatalf("payload=%#v err=%v", payload, err)
		}
		for _, key := range []string{"model", "stream", "tools", "extra_body"} {
			request.ProviderOptions = map[string]any{key: "override"}
			if _, err := provider.buildRequest(request, false); err == nil {
				t.Fatalf("%s allowed collision with %s", api, key)
			}
		}
	}
}

func TestNativeContentBlocksAndStreamOptionsArePreserved(t *testing.T) {
	native := json.RawMessage(`{"type":"vendor_image","url":"asset://id","vendor":{"signature":"signed"}}`)
	for _, api := range []APIType{APITypeChatCompletions, APITypeResponsesAPI} {
		provider, err := New(Config{BaseURL: "https://example.com/v1", APIType: api})
		if err != nil {
			t.Fatal(err)
		}
		request := protocol.ModelRequest{Model: "m", Messages: []protocol.Message{{Role: protocol.RoleUser, Parts: []protocol.ContentPart{{Type: protocol.ContentProvider, Data: native}}}}}
		payload, err := provider.buildRequest(request, true)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		key := "messages"
		if api == APITypeResponsesAPI {
			key = "input"
		}
		block := decoded[key].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
		if block["type"] != "vendor_image" || block["vendor"].(map[string]any)["signature"] != "signed" {
			t.Fatalf("block=%#v", block)
		}
	}
	options := map[string]any{"include_usage": false, "vendor": true}
	payload, err := buildRequest(protocol.ModelRequest{Model: "m", ProviderOptions: map[string]any{"stream_options": options}}, true)
	if err != nil || !reflect.DeepEqual(payload["stream_options"], options) {
		t.Fatalf("options lost: %#v err=%v", payload, err)
	}
}

func TestAudioEncodingAndCapabilitiesMatch(t *testing.T) {
	chat, err := New(Config{BaseURL: "https://example.com/v1"})
	if err != nil {
		t.Fatal(err)
	}
	request := protocol.ModelRequest{Model: "m", Messages: []protocol.Message{{Role: protocol.RoleUser, Parts: []protocol.ContentPart{{Type: protocol.ContentAudio, Media: &protocol.MediaContent{MIMEType: "audio/wav", Data: "aGk="}}}}}}
	payload, err := chat.buildRequest(request, false)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(payload)
	var decoded map[string]any
	json.Unmarshal(encoded, &decoded)
	block := decoded["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if block["input_audio"].(map[string]any)["format"] != "wav" {
		t.Fatalf("block=%#v", block)
	}
	responses, err := New(Config{BaseURL: "https://example.com/v1", APIType: APITypeResponsesAPI})
	if err != nil {
		t.Fatal(err)
	}
	_, err = responses.Generate(context.Background(), request)
	requireFailure(t, err, protocol.ErrorUnsupported)
	for _, content := range responses.Capabilities().InputContent {
		if content == protocol.ContentAudio {
			t.Fatal("Responses advertises audio input")
		}
	}
}

func TestRawNonStreamingResponseKeepsUnknownFields(t *testing.T) {
	body := `{ "choices":[{"message":{"content":"ok"},"finish_reason":"stop"}], "vendor":{"signed":"opaque"} }`
	provider := testProvider(t, Config{}, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, body), nil })
	response, err := provider.Generate(context.Background(), testRequest())
	if err != nil || string(response.Raw) != body || response.CallInfo.Attempts != 1 || !response.Complete {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestInvalidRequestsAndPendingResultsRemainIncomplete(t *testing.T) {
	provider := testProvider(t, Config{}, func(*http.Request) (*http.Response, error) { t.Fatal("invalid request sent"); return nil, nil })
	for _, request := range []protocol.ModelRequest{
		{Messages: testRequest().Messages},
		{Model: "m", ProviderOptions: map[string]any{"model": "override"}},
		{Model: "m", OutputFormat: &protocol.OutputFormat{Type: protocol.OutputJSONSchema, Name: "schema", Schema: json.RawMessage(`null`)}},
	} {
		response, err := provider.Generate(context.Background(), request)
		requireFailure(t, err, protocol.ErrorInvalidRequest)
		if response.Complete || response.CallInfo.Attempts != 0 {
			t.Fatalf("response=%#v", response)
		}
	}
	request := testRequest()
	request.ProviderOptions = map[string]any{"n": 2}
	_, err := provider.Generate(context.Background(), request)
	requireFailure(t, err, protocol.ErrorUnsupported)
	for _, status := range []string{"queued", "in_progress", ""} {
		provider := testProvider(t, Config{APIType: APITypeResponsesAPI}, func(*http.Request) (*http.Response, error) {
			return testHTTPResponse(200, `{"status":"`+status+`","output":[]}`), nil
		})
		response, err := provider.Generate(context.Background(), testRequest())
		requireFailure(t, err, protocol.ErrorStreamIncomplete)
		if response.Complete {
			t.Fatalf("pending response completed: %#v", response)
		}
	}
}
