package openai

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

const finalChat = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"

func TestChatCompletionConditions(t *testing.T) {
	for _, test := range []struct {
		name, body       string
		strict, complete bool
	}{
		{name: "finish without done", body: finalChat, complete: true},
		{name: "done alone", body: "data: [DONE]\n\n", complete: true},
		{name: "strict rejects missing done", body: finalChat, strict: true},
		{name: "strict with done", body: finalChat + "data: [DONE]\n\n", strict: true, complete: true},
		{name: "partial EOF", body: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := testProvider(t, Config{RequireDone: test.strict}, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, test.body), nil })
			finishEvents := 0
			response, err := provider.Stream(context.Background(), testRequest(), protocol.EventSinkFunc(func(_ context.Context, event protocol.StreamEvent) error {
				if event.Type == protocol.EventFinish {
					finishEvents++
				}
				return nil
			}))
			if test.complete {
				if err != nil || !response.Complete || finishEvents != 1 {
					t.Fatalf("response=%#v finish=%d err=%v", response, finishEvents, err)
				}
			} else {
				requireFailure(t, err, protocol.ErrorStreamIncomplete)
				if response.Complete || finishEvents != 0 {
					t.Fatalf("response=%#v finish=%d", response, finishEvents)
				}
			}
		})
	}
}

func TestRawSSEPreservesFramesAndMultilineData(t *testing.T) {
	body := ": hello\r\n\r\nevent: vendor\r\nid: 7\r\ndata: {\r\ndata: \"choices\": [], \"vendor\": true}\r\n\r\n" + strings.ReplaceAll(finalChat, "\n", "\r\n") + "data: [DONE]\r\n\r\n"
	var raw []RawEvent
	provider := testProvider(t, Config{OnRawEvent: func(_ context.Context, event RawEvent) error { raw = append(raw, event); return nil }}, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, body), nil })
	response, err := provider.Stream(context.Background(), testRequest(), nil)
	if err != nil || !response.Complete {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	var frames []byte
	for _, event := range raw {
		frames = append(frames, event.Frame...)
	}
	if string(frames) != body || len(raw) != 4 || raw[1].Name != "vendor" || raw[1].ID != "7" || string(raw[1].Data) != "{\n\"choices\": [], \"vendor\": true}" {
		t.Fatalf("frames=%q raw=%#v", frames, raw)
	}
}

func TestRawCallbackCannotMutateNormalization(t *testing.T) {
	provider := testProvider(t, Config{OnRawEvent: func(_ context.Context, event RawEvent) error {
		for index := range event.Data {
			event.Data[index] = 'x'
		}
		for index := range event.Frame {
			event.Frame[index] = 'x'
		}
		return nil
	}}, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, finalChat), nil })
	response, err := provider.Stream(context.Background(), testRequest(), nil)
	if err != nil || response.Message.Text() != "ok" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestStreamLimitsAreIndependent(t *testing.T) {
	for _, test := range []struct {
		name   string
		config Config
		body   string
		failed bool
	}{
		{name: "response limit does not limit stream", config: Config{MaxResponseBytes: 1}, body: finalChat},
		{name: "total includes framing", config: Config{MaxStreamBytes: int64(len(finalChat) - 1)}, body: finalChat, failed: true},
		{name: "event counts all lines", config: Config{MaxEventBytes: 15}, body: "data: {\ndata: \"choices\":[]}\n\n", failed: true},
		{name: "long line is bounded", config: Config{MaxEventBytes: 32}, body: "data: " + strings.Repeat("x", 9000), failed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := testProvider(t, test.config, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, test.body), nil })
			response, err := provider.Stream(context.Background(), testRequest(), nil)
			if test.failed {
				requireFailure(t, err, protocol.ErrorResponseTooLarge)
				if response.Complete {
					t.Fatal("limit failure completed")
				}
			} else if err != nil || !response.Complete {
				t.Fatalf("response=%#v err=%v", response, err)
			}
		})
	}
}

func TestStreamFailureKeepsPartialOutputAndNeverRetries(t *testing.T) {
	prefix := strings.Replace(finalChat, ",\"finish_reason\":\"stop\"", "", 1)
	for _, test := range []struct {
		name, tail string
		code       protocol.ErrorCode
	}{
		{"decode", "data: malformed\n\n", protocol.ErrorDecode},
		{"upstream", "data: {\"error\":{\"message\":\"backend failed\"}}\n\n", protocol.ErrorUpstream},
		{"truncation", "", protocol.ErrorStreamIncomplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			finishes := 0
			provider := testProvider(t, Config{Retry: RetryPolicy{MaxRetries: 3}}, func(*http.Request) (*http.Response, error) {
				attempts++
				return testHTTPResponse(200, prefix+test.tail), nil
			})
			response, err := provider.Stream(context.Background(), testRequest(), protocol.EventSinkFunc(func(_ context.Context, event protocol.StreamEvent) error {
				if event.Type == protocol.EventFinish {
					finishes++
				}
				return nil
			}))
			requireFailure(t, err, test.code)
			if attempts != 1 || response.Complete || response.Message.Text() != "ok" || response.CallInfo.Attempts != 1 || finishes != 0 {
				t.Fatalf("response=%#v attempts=%d finishes=%d", response, attempts, finishes)
			}
		})
	}
}

func TestCallbacksAbortAndPreserveCause(t *testing.T) {
	sentinel := errors.New("downstream closed")
	for _, raw := range []bool{false, true} {
		t.Run(map[bool]string{false: "normalized", true: "raw"}[raw], func(t *testing.T) {
			config := Config{}
			if raw {
				config.OnRawEvent = func(context.Context, RawEvent) error { return sentinel }
			}
			provider := testProvider(t, config, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, finalChat), nil })
			sink := protocol.EventSinkFunc(func(context.Context, protocol.StreamEvent) error { return sentinel })
			response, err := provider.Stream(context.Background(), testRequest(), sink)
			failure := requireFailure(t, err, protocol.ErrorSink)
			if !errors.Is(err, sentinel) || failure.Retryable || response.Complete {
				t.Fatalf("response=%#v err=%v", response, err)
			}
		})
	}
}

func TestResponsesRequiresTerminalEvent(t *testing.T) {
	for _, body := range []string{"data: [DONE]\n\n", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"} {
		provider := testProvider(t, Config{APIType: APITypeResponsesAPI}, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, body), nil })
		response, err := provider.Stream(context.Background(), testRequest(), nil)
		requireFailure(t, err, protocol.ErrorStreamIncomplete)
		if response.Complete {
			t.Fatal("unterminated Responses stream completed")
		}
	}
}

func TestResponsesFinalOnlyMultipleToolCalls(t *testing.T) {
	body := `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","id":"fc-a","call_id":"call-a","name":"a","arguments":"{}"},{"type":"function_call","id":"fc-b","call_id":"call-b","name":"b","arguments":"{}"}]}}` + "\n\n"
	provider := testProvider(t, Config{APIType: APITypeResponsesAPI}, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, body), nil })
	var ids []string
	var finishes int
	response, err := provider.Stream(context.Background(), testRequest(), protocol.EventSinkFunc(func(_ context.Context, event protocol.StreamEvent) error {
		if event.ToolCallDelta != nil && event.ToolCallDelta.Name != "" {
			ids = append(ids, event.ToolCallDelta.ID)
		}
		if event.Type == protocol.EventFinish {
			finishes++
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	calls := response.Message.ToolCalls()
	if len(calls) != 2 || calls[0].ID != "call-a" || calls[1].ID != "call-b" || finishes != 1 || !reflect.DeepEqual(ids, []string{"call-a", "call-b"}) {
		t.Fatalf("calls=%#v ids=%#v finishes=%d", calls, ids, finishes)
	}
}

func TestReaderFailuresPreserveCauseEvenAfterFinishReason(t *testing.T) {
	provider := testProvider(t, Config{}, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(io.MultiReader(bytes.NewBufferString(finalChat), failingReader{}))}, nil
	})
	response, err := provider.Stream(context.Background(), testRequest(), nil)
	requireFailure(t, err, protocol.ErrorTransport)
	if !errors.Is(err, io.ErrUnexpectedEOF) || response.Complete || response.Message.Text() != "ok" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestResponsesRejectsInconsistentFinalContent(t *testing.T) {
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"original\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output_text\":\"different\"}}\n\n"
	provider := testProvider(t, Config{APIType: APITypeResponsesAPI}, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, body), nil })
	response, err := provider.Stream(context.Background(), testRequest(), nil)
	requireFailure(t, err, protocol.ErrorDecode)
	if response.Complete || response.Message.Text() != "original" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestResponsesRejectsInconsistentToolMetadata(t *testing.T) {
	body := "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc\",\"call_id\":\"call\",\"name\":\"original\"}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc\",\"call_id\":\"different\",\"name\":\"original\",\"arguments\":\"{}\"}}\n\n"
	provider := testProvider(t, Config{APIType: APITypeResponsesAPI}, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, body), nil })
	response, err := provider.Stream(context.Background(), testRequest(), nil)
	requireFailure(t, err, protocol.ErrorDecode)
	if response.Complete {
		t.Fatal("inconsistent tool metadata completed")
	}
}
