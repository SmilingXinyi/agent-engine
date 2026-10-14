//go:build integration

package ai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/providers/openai"
)

// These tests use real HTTP/1.1 chunked responses on loopback. They are explicit
// so ordinary SDK tests do not require permission to bind a listening socket.
func TestStreamHTTPFramesMatchUpstreamForBothAPIs(t *testing.T) {
	for _, apiType := range []openai.APIType{openai.APITypeChatCompletions, openai.APITypeResponsesAPI} {
		t.Run(string(apiType), func(t *testing.T) {
			frames := []string{
				": heartbeat\r\n\r\n",
				"\n",
				"event: vendor\rid: 7\ndata: {\r\ndata: \"text\":\"你好\",\"vendor\":true}\r\n\r\n",
				"data: not JSON\n\n",
			}
			endpoint := "/v1/chat/completions"
			if apiType == openai.APITypeResponsesAPI {
				endpoint = "/v1/responses"
				frames = append(frames, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n")
			} else {
				frames = append(frames,
					"data: {\"choices\":[{\"delta\":{\"content\":\"answer\",\"reasoning_content\":\"reasoning\"},\"finish_reason\":\"stop\"}]}\n\n",
					"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n",
					"data: [DONE]\n\n",
				)
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if request.Method != http.MethodPost || request.URL.Path != endpoint || request.Header.Get("Authorization") != "Bearer integration-token" || request.Header.Get("Accept") != "text/event-stream" {
					t.Errorf("request: %s %s headers=%v", request.Method, request.URL.Path, request.Header)
				}
				var payload map[string]any
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Error(err)
					writer.WriteHeader(http.StatusBadRequest)
					return
				}
				if payload["stream"] != true || payload["model"] != "integration-model" || payload["thinking"].(map[string]any)["type"] != "disabled" {
					t.Errorf("payload=%#v", payload)
				}
				messageField := "messages"
				if apiType == openai.APITypeResponsesAPI {
					messageField = "input"
					if payload["reasoning"].(map[string]any)["effort"] != "low" {
						t.Errorf("reasoning=%#v", payload)
					}
				} else if payload["reasoning_effort"] != "low" {
					t.Errorf("reasoning=%#v", payload)
				}
				message := payload[messageField].([]any)[0].(map[string]any)
				if message["vendor_extension"] != "kept" {
					t.Errorf("message=%#v", message)
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				writer.Header().Set("X-Request-Id", "integration-request")
				writer.WriteHeader(http.StatusOK)
				writer.(http.Flusher).Flush()
				// Split both Unicode bytes and SSE line endings across writes.
				for _, frame := range frames {
					for start := 0; start < len(frame); {
						end := start + 3
						if end > len(frame) {
							end = len(frame)
						}
						if _, err := io.WriteString(writer, frame[start:end]); err != nil {
							return
						}
						writer.(http.Flusher).Flush()
						start = end
					}
				}
			}))
			defer server.Close()
			var hooks atomic.Int32
			client := integrationClient(t, server.URL, apiType, openai.RetryPolicy{}, protocol.Hooks{OnResponse: []protocol.ResponseHook{func(_ context.Context, info protocol.ResponseInfo) error {
				hooks.Add(1)
				if info.Stream == nil || !info.Stream.Complete || info.Stream.CallInfo.StatusCode != 200 || info.Stream.CallInfo.Attempts != 1 || info.Error != nil || len(info.Response.Message.Parts) != 0 {
					t.Errorf("response observation=%#v", info)
				}
				return nil
			}}})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			message := protocol.TextMessage(protocol.RoleUser, "hello")
			message.ProviderOptions = map[string]any{"vendor_extension": "kept"}
			stream, err := client.Stream(ctx, protocol.ModelRequest{
				Model: "integration-model", Messages: []protocol.Message{message},
				Reasoning:       &protocol.ReasoningOptions{Effort: "low"},
				ProviderOptions: map[string]any{"thinking": map[string]any{"type": "disabled"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if stream.Info().Complete || stream.Info().CallInfo.Headers["X-Request-Id"][0] != "integration-request" {
				t.Fatalf("opening info=%#v", stream.Info())
			}
			var received []protocol.StreamChunk
			for stream.Next() {
				received = append(received, stream.Chunk())
			}
			var receivedFrames []string
			for _, chunk := range received {
				receivedFrames = append(receivedFrames, string(chunk.Frame))
			}
			if !reflect.DeepEqual(receivedFrames, frames) || stream.Err() != nil || !stream.Info().Complete || requests.Load() != 1 || hooks.Load() != 1 {
				t.Fatalf("frames=%q want=%q info=%#v err=%v requests=%d hooks=%d", receivedFrames, frames, stream.Info(), stream.Err(), requests.Load(), hooks.Load())
			}
			if received[2].Event != "vendor" || received[2].ID != "7" || string(received[2].Data) != "{\n\"text\":\"你好\",\"vendor\":true}" {
				t.Fatalf("parsed frame=%#v", received[2])
			}
		})
	}
}

func TestStreamHTTPCancelAndCloseReachServer(t *testing.T) {
	for _, mode := range []string{"context cancellation", "explicit close"} {
		t.Run(mode, func(t *testing.T) {
			serverCanceled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				writer.WriteHeader(http.StatusOK)
				writer.(http.Flusher).Flush()
				<-request.Context().Done()
				close(serverCanceled)
			}))
			defer server.Close()
			var hooks atomic.Int32
			client := integrationClient(t, server.URL, openai.APITypeChatCompletions, openai.RetryPolicy{}, protocol.Hooks{OnResponse: []protocol.ResponseHook{func(_ context.Context, info protocol.ResponseInfo) error {
				hooks.Add(1)
				if info.Stream == nil || info.Stream.Complete || !errors.Is(info.Error, context.Canceled) {
					t.Errorf("observation=%#v", info)
				}
				return nil
			}}})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := client.Stream(ctx, protocol.ModelRequest{Model: "integration-model"})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			started := make(chan struct{})
			done := make(chan bool, 1)
			go func() { close(started); done <- stream.Next() }()
			<-started
			if mode == "context cancellation" {
				cancel()
			} else if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case next := <-done:
				if next {
					t.Fatal("canceled read yielded a frame")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Next did not unblock")
			}
			select {
			case <-serverCanceled:
			case <-time.After(2 * time.Second):
				t.Fatal("HTTP cancellation did not reach server")
			}
			if !errors.Is(stream.Err(), context.Canceled) || stream.Info().Complete || hooks.Load() != 1 {
				t.Fatalf("info=%#v err=%v hooks=%d", stream.Info(), stream.Err(), hooks.Load())
			}
			if err := stream.Close(); err != nil || hooks.Load() != 1 {
				t.Fatalf("repeat close=%v hooks=%d", err, hooks.Load())
			}
		})
	}
}

func TestStreamHTTPBrokenConnectionIsNotReplayed(t *testing.T) {
	frame := "data: partial answer\n\n"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, frame)
		writer.(http.Flusher).Flush()
		connection, _, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close() // No final HTTP chunk and no SSE terminal marker.
	}))
	defer server.Close()
	client := integrationClient(t, server.URL, openai.APITypeChatCompletions, openai.RetryPolicy{MaxRetries: 2, Backoff: time.Millisecond}, protocol.Hooks{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, protocol.ModelRequest{Model: "integration-model"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var received strings.Builder
	for stream.Next() {
		received.Write(stream.Chunk().Frame)
	}
	var failure *protocol.Error
	if received.String() != frame || !errors.As(stream.Err(), &failure) || failure.Code != protocol.ErrorTransport || !errors.Is(stream.Err(), io.ErrUnexpectedEOF) || stream.Info().Complete || requests.Load() != 1 {
		t.Fatalf("frames=%q info=%#v err=%v requests=%d", received.String(), stream.Info(), stream.Err(), requests.Load())
	}
}

func integrationClient(t *testing.T, serverURL string, apiType openai.APIType, retry openai.RetryPolicy, hooks protocol.Hooks) *ai.Client {
	t.Helper()
	provider, err := openai.New(openai.Config{BaseURL: serverURL + "/v1", APIType: apiType, APIKey: "integration-token", Retry: retry})
	if err != nil {
		t.Fatal(err)
	}
	return ai.NewClient(provider, hooks)
}
