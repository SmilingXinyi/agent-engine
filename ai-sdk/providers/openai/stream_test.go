package openai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

const finalChat = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"

func consumeStream(t *testing.T, provider *Provider, ctx context.Context, request protocol.ModelRequest) ([]protocol.StreamChunk, protocol.Stream) {
	t.Helper()
	stream, err := provider.Stream(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	var chunks []protocol.StreamChunk
	for stream.Next() {
		chunks = append(chunks, stream.Chunk())
	}
	return chunks, stream
}

func joinFrames(chunks []protocol.StreamChunk) string {
	var result strings.Builder
	for _, chunk := range chunks {
		result.Write(chunk.Frame)
	}
	return result.String()
}

func TestSSEFramesRemainExactAcrossReadBoundaries(t *testing.T) {
	frames := []string{
		"\xef\xbb\xbf: hello\r\n\r\n",
		"\n",
		"event: vendor\rid: 7\ndata: {\r\ndata: \"unknown\":true}\r\nretry: 5\r\n\r\n",
		"id: stable\nid: bad\x00id\nevent: first\nevent: second\ndata:  spaced \ndata\ndata:\n\n",
		"data: [DONE]",
	}
	body := strings.Join(frames, "")
	for _, size := range []int{1, 2, 3, 7, 4096} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			provider := testProvider(t, Config{}, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(&fragmentedReader{reader: strings.NewReader(body), size: size})}, nil
			})
			chunks, stream := consumeStream(t, provider, context.Background(), testRequest())
			if stream.Err() != nil || !stream.Info().Complete || len(chunks) != len(frames) || joinFrames(chunks) != body {
				t.Fatalf("chunks=%#v info=%#v err=%v", chunks, stream.Info(), stream.Err())
			}
			for index, frame := range frames {
				if string(chunks[index].Frame) != frame {
					t.Fatalf("frame %d: %q", index, chunks[index].Frame)
				}
			}
			if chunks[2].Event != "vendor" || chunks[2].ID != "7" || string(chunks[2].Data) != "{\n\"unknown\":true}" {
				t.Fatalf("multiline=%#v", chunks[2])
			}
			if chunks[3].Event != "second" || chunks[3].ID != "stable" || string(chunks[3].Data) != " spaced \n\n" || chunks[4].ID != "" {
				t.Fatalf("fields=%#v", chunks[3:])
			}
		})
	}
}

type fragmentedReader struct {
	reader *strings.Reader
	size   int
}

func (reader *fragmentedReader) Read(buffer []byte) (int, error) {
	if len(buffer) > reader.size {
		buffer = buffer[:reader.size]
	}
	return reader.reader.Read(buffer)
}

func TestStreamPreservesEveryPayloadWithoutNormalization(t *testing.T) {
	for _, api := range []APIType{APITypeChatCompletions, APITypeResponsesAPI} {
		t.Run(string(api), func(t *testing.T) {
			frames := []string{
				"data: not JSON\n\n",
				"data: {broken\n\n",
				"data: {\"error\":false,\"vendor\":true}\n\n",
				"data: {\"choices\":\"vendor shape\",\"type\":[true]}\n\n",
				"data: {\"choices\":[{\"delta\":{\"content\":\"text\",\"reasoning_content\":\"reasoning\",\"tool_calls\":[{\"function\":{\"arguments\":\"{\"}}]}}],\"vendor\":42}\n\n",
				"event: vendor\ndata: {\"usage\":{\"custom_tokens\":5}}\n\n",
			}
			if api == APITypeResponsesAPI {
				frames = append(frames, "event: response.completed\ndata: {\"response\":{\"output_text\":\"different final snapshot\"}}\n\n")
			} else {
				frames = append(frames, "data: [DONE]\n\n")
			}
			body := strings.Join(frames, "")
			provider := testProvider(t, Config{APIType: api}, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, body), nil })
			chunks, stream := consumeStream(t, provider, context.Background(), testRequest())
			if stream.Err() != nil || !stream.Info().Complete || len(chunks) != len(frames) || joinFrames(chunks) != body {
				t.Fatalf("chunks=%#v info=%#v err=%v", chunks, stream.Info(), stream.Err())
			}
		})
	}
}

func TestChatCompletionConditions(t *testing.T) {
	for _, test := range []struct {
		name, body       string
		strict, complete bool
	}{
		{name: "finish without done", body: finalChat, complete: true},
		{name: "done alone", body: "data: [DONE]\n\n", complete: true},
		{name: "strict rejects missing done", body: finalChat, strict: true},
		{name: "strict with done", body: finalChat + "data: [DONE]\n\n", strict: true, complete: true},
		{name: "usage after finish", body: finalChat + "data: {\"usage\":{\"total_tokens\":5}}\n\ndata: [DONE]\n\n", complete: true},
		{name: "tail without delimiter", body: strings.TrimSuffix(finalChat, "\n\n"), complete: true},
		{name: "CR only", body: "data: hello\r\rdata: [DONE]\r\r", complete: true},
		{name: "partial EOF", body: "data: partial\n\n"},
		{name: "empty stream"},
		{name: "only comments", body: ": heartbeat\n\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := testProvider(t, Config{RequireDone: test.strict}, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, test.body), nil })
			chunks, stream := consumeStream(t, provider, context.Background(), testRequest())
			if joinFrames(chunks) != test.body || stream.Info().Complete != test.complete {
				t.Fatalf("frames=%q info=%#v", joinFrames(chunks), stream.Info())
			}
			if test.complete {
				if stream.Err() != nil {
					t.Fatal(stream.Err())
				}
			} else {
				requireFailure(t, stream.Err(), protocol.ErrorStreamIncomplete)
			}
		})
	}
}

func TestResponsesTerminalConditions(t *testing.T) {
	for _, test := range []struct {
		name, body string
		code       protocol.ErrorCode
		complete   bool
	}{
		{name: "completed by type", body: "data: {\"type\":\"response.completed\",\"response\":{}}\n\n", complete: true},
		{name: "incomplete by event", body: "event: response.incomplete\ndata: vendor payload\n\n", complete: true},
		{name: "bare done", body: "data: [DONE]\n\n", code: protocol.ErrorStreamIncomplete},
		{name: "no terminal", body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"text\"}\n\n", code: protocol.ErrorStreamIncomplete},
		{name: "failed by event", body: "event: response.failed\ndata: {\"response\":{\"error\":{\"message\":\"failed\"}}}\n\n", code: protocol.ErrorUpstream},
		{name: "error event", body: "event: error\ndata: opaque error\n\n", code: protocol.ErrorUpstream},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := testProvider(t, Config{APIType: APITypeResponsesAPI}, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, test.body), nil })
			chunks, stream := consumeStream(t, provider, context.Background(), testRequest())
			if joinFrames(chunks) != test.body || stream.Info().Complete != test.complete {
				t.Fatalf("frames=%q info=%#v", joinFrames(chunks), stream.Info())
			}
			if test.code != "" {
				requireFailure(t, stream.Err(), test.code)
			} else if stream.Err() != nil {
				t.Fatal(stream.Err())
			}
		})
	}
}

func TestStreamStopsAfterDeliveringTerminalFrame(t *testing.T) {
	terminal := "data: [DONE]\n\n"
	provider := testProvider(t, Config{}, func(*http.Request) (*http.Response, error) {
		return testHTTPResponse(200, terminal+"data: never delivered\n\n"), nil
	})
	chunks, stream := consumeStream(t, provider, context.Background(), testRequest())
	if len(chunks) != 1 || joinFrames(chunks) != terminal || stream.Err() != nil || !stream.Info().Complete || stream.Next() {
		t.Fatalf("chunks=%#v info=%#v err=%v", chunks, stream.Info(), stream.Err())
	}
}

func TestStreamOpenIsLazyAndChunkBuffersAreRetainable(t *testing.T) {
	body := &trackedBody{reader: strings.NewReader(finalChat + "data: extra\n\n")}
	provider := testProvider(t, Config{}, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Test": {"original"}}, Body: body}, nil
	})
	stream, err := provider.Stream(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if body.reads.Load() != 0 || stream.Info().Complete {
		t.Fatal("Stream consumed body eagerly")
	}
	if !stream.Next() {
		t.Fatal(stream.Err())
	}
	first := stream.Chunk()
	original := bytes.Clone(first.Frame)
	// State was inspected before publication; mutation cannot erase finish_reason.
	for index := range first.Data {
		first.Data[index] = 'x'
	}
	if !stream.Next() || string(stream.Chunk().Data) != "extra" {
		t.Fatal("one Next did not deliver one frame")
	}
	if !bytes.Equal(first.Frame, original) {
		t.Fatal("next overwrote retained frame")
	}
	if stream.Next() || stream.Err() != nil || !stream.Info().Complete {
		t.Fatalf("info=%#v err=%v", stream.Info(), stream.Err())
	}
	snapshot := stream.Info()
	snapshot.CallInfo.Headers["X-Test"][0] = "changed"
	if stream.Info().CallInfo.Headers["X-Test"][0] != "original" {
		t.Fatal("Info headers were shared")
	}
	if err := stream.Close(); err != nil || body.closes.Load() != 1 {
		t.Fatalf("close=%v count=%d", err, body.closes.Load())
	}
}

type trackedBody struct {
	reader   io.Reader
	reads    atomic.Int32
	closes   atomic.Int32
	closeErr error
}

func (body *trackedBody) Read(buffer []byte) (int, error) {
	body.reads.Add(1)
	return body.reader.Read(buffer)
}
func (body *trackedBody) Close() error { body.closes.Add(1); return body.closeErr }

func TestStreamLimitsIncludeFramingAndRemainIndependent(t *testing.T) {
	for _, test := range []struct {
		name   string
		config Config
		body   string
		failed bool
	}{
		{name: "response limit does not limit stream", config: Config{MaxResponseBytes: 1}, body: finalChat},
		{name: "exact event limit", config: Config{MaxEventBytes: int64(len(finalChat))}, body: finalChat},
		{name: "event includes delimiter", config: Config{MaxEventBytes: int64(len(finalChat) - 1)}, body: finalChat, failed: true},
		{name: "exact stream limit", config: Config{MaxStreamBytes: int64(len(finalChat))}, body: finalChat},
		{name: "total includes framing", config: Config{MaxStreamBytes: int64(len(finalChat) - 1)}, body: finalChat, failed: true},
		{name: "event includes all lines", config: Config{MaxEventBytes: 15}, body: "data: {\ndata: \"choices\":[]}\n\n", failed: true},
		{name: "long line bounded", config: Config{MaxEventBytes: 32}, body: "data: " + strings.Repeat("x", 9000), failed: true},
		{name: "CRLF counts both bytes", config: Config{MaxEventBytes: 15}, body: "data: [DONE]\r\n\r\n", failed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := testProvider(t, test.config, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, test.body), nil })
			_, stream := consumeStream(t, provider, context.Background(), testRequest())
			if test.failed {
				requireFailure(t, stream.Err(), protocol.ErrorResponseTooLarge)
				if stream.Info().Complete {
					t.Fatal("limit failure completed")
				}
			} else if stream.Err() != nil || !stream.Info().Complete {
				t.Fatalf("info=%#v err=%v", stream.Info(), stream.Err())
			}
		})
	}
}

func TestFailuresPreserveFramesAndNeverReplay(t *testing.T) {
	for _, test := range []struct {
		name, tail string
		code       protocol.ErrorCode
	}{
		{"upstream", "data: {\"error\":{\"message\":\"backend failed\"}}\n\n", protocol.ErrorUpstream},
		{"finish error", "data: {\"choices\":[{\"finish_reason\":\"error\"}]}\n\n", protocol.ErrorUpstream},
		{"truncation", "", protocol.ErrorStreamIncomplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			prefix := "data: partial\n\n"
			body := &trackedBody{reader: strings.NewReader(prefix + test.tail)}
			provider := testProvider(t, Config{Retry: RetryPolicy{MaxRetries: 3}}, func(*http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{StatusCode: 200, Body: body}, nil
			})
			chunks, stream := consumeStream(t, provider, context.Background(), testRequest())
			requireFailure(t, stream.Err(), test.code)
			if joinFrames(chunks) != prefix+test.tail || attempts != 1 || body.closes.Load() != 1 || stream.Info().Complete || stream.Info().CallInfo.Attempts != 1 {
				t.Fatalf("frames=%q attempts=%d closes=%d info=%#v", joinFrames(chunks), attempts, body.closes.Load(), stream.Info())
			}
		})
	}
}

func TestReaderFailureAfterFinishIsNotCleanEOF(t *testing.T) {
	body := &trackedBody{reader: io.MultiReader(strings.NewReader(finalChat+"data: broken"), failingReader{})}
	provider := testProvider(t, Config{Retry: RetryPolicy{MaxRetries: 2}}, func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: 200, Body: body}, nil })
	chunks, stream := consumeStream(t, provider, context.Background(), testRequest())
	requireFailure(t, stream.Err(), protocol.ErrorTransport)
	if !errors.Is(stream.Err(), io.ErrUnexpectedEOF) || stream.Info().Complete || joinFrames(chunks) != finalChat || body.closes.Load() != 1 {
		t.Fatalf("frames=%q info=%#v err=%v", joinFrames(chunks), stream.Info(), stream.Err())
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestCloseFailureDoesNotUndoNormalTermination(t *testing.T) {
	sentinel := errors.New("close failed")
	body := &trackedBody{reader: strings.NewReader("data: [DONE]\n\n"), closeErr: sentinel}
	provider := testProvider(t, Config{}, func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: 200, Body: body}, nil })
	_, stream := consumeStream(t, provider, context.Background(), testRequest())
	if !errors.Is(stream.Err(), sentinel) || !stream.Info().Complete || !errors.Is(stream.Close(), sentinel) || body.closes.Load() != 1 {
		t.Fatalf("info=%#v err=%v closes=%d", stream.Info(), stream.Err(), body.closes.Load())
	}
}

func TestCloseAndCancellationInterruptBlockedNext(t *testing.T) {
	for _, cancelContext := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelContext), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := &blockingBody{started: make(chan struct{}), closed: make(chan struct{})}
			provider := testProvider(t, Config{}, func(request *http.Request) (*http.Response, error) {
				body.ctx = request.Context()
				return &http.Response{StatusCode: 200, Body: body}, nil
			})
			stream, err := provider.Stream(ctx, testRequest())
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			done := make(chan bool, 1)
			go func() { done <- stream.Next() }()
			select {
			case <-body.started:
			case <-time.After(2 * time.Second):
				t.Fatal("read did not start")
			}
			if cancelContext {
				cancel()
			} else if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case next := <-done:
				if next {
					t.Fatal("canceled read produced a chunk")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Next did not unblock")
			}
			requireFailure(t, stream.Err(), protocol.ErrorCanceled)
			if !errors.Is(stream.Err(), context.Canceled) || stream.Info().Complete || body.closes.Load() != 1 {
				t.Fatalf("info=%#v err=%v closes=%d", stream.Info(), stream.Err(), body.closes.Load())
			}
		})
	}
}

type blockingBody struct {
	ctx       context.Context
	started   chan struct{}
	closed    chan struct{}
	readOnce  sync.Once
	closeOnce sync.Once
	closes    atomic.Int32
}

func (body *blockingBody) Read([]byte) (int, error) {
	body.readOnce.Do(func() { close(body.started) })
	select {
	case <-body.ctx.Done():
		return 0, body.ctx.Err()
	case <-body.closed:
		return 0, io.ErrClosedPipe
	}
}
func (body *blockingBody) Close() error {
	body.closeOnce.Do(func() { body.closes.Add(1); close(body.closed) })
	return nil
}

func TestStreamOpenFailuresRetainClassificationAndCloseBody(t *testing.T) {
	for _, status := range []int{401, 429, 503} {
		body := &trackedBody{reader: strings.NewReader("error details")}
		provider := testProvider(t, Config{}, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{"X-Request-Id": {"failed"}}, Body: body}, nil
		})
		stream, err := provider.Stream(context.Background(), testRequest())
		var failure *protocol.Error
		if stream != nil || !errors.As(err, &failure) || failure.CallInfo.StatusCode != status || failure.CallInfo.Attempts != 1 || body.closes.Load() != 1 {
			t.Fatalf("stream=%v err=%v closes=%d", stream, err, body.closes.Load())
		}
	}
	provider := testProvider(t, Config{}, func(*http.Request) (*http.Response, error) { t.Fatal("invalid request sent"); return nil, nil })
	stream, err := provider.Stream(context.Background(), protocol.ModelRequest{})
	requireFailure(t, err, protocol.ErrorInvalidRequest)
	if stream != nil {
		t.Fatal("invalid request returned a stream")
	}
}

func TestConcurrentStreamsHaveIndependentFramesAndSnapshots(t *testing.T) {
	provider := testProvider(t, Config{}, func(*http.Request) (*http.Response, error) {
		return testHTTPResponse(200, "data: first\n\ndata: [DONE]\n\n"), nil
	})
	var workers sync.WaitGroup
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			stream, err := provider.Stream(context.Background(), testRequest())
			if err != nil {
				t.Error(err)
				return
			}
			defer stream.Close()
			var payloads []string
			for stream.Next() {
				payloads = append(payloads, string(stream.Chunk().Data))
			}
			if !reflect.DeepEqual(payloads, []string{"first", "[DONE]"}) || stream.Err() != nil || !stream.Info().Complete {
				t.Errorf("data=%v info=%#v err=%v", payloads, stream.Info(), stream.Err())
			}
		}()
	}
	workers.Wait()
}
