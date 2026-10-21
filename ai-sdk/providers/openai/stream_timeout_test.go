package openai

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

func TestFirstFrameTimeoutIgnoresCommentsAndBlankData(t *testing.T) {
	provider := testProvider(t, Config{FirstFrameTimeout: 40 * time.Millisecond}, func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &frameBody{ctx: request.Context(), frames: []timedFrame{
			{data: "data:   \n\n"},
			{data: ": keepalive\n\n"},
			{delay: 300 * time.Millisecond, data: "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"},
		}}}, nil
	})
	started := time.Now()
	chunks, stream := consumeStream(t, provider, context.Background(), testRequest())
	failure := requireFailure(t, stream.Err(), protocol.ErrorTimeout)
	if failure.TimeoutKind != protocol.TimeoutFirstFrame || !failure.Retryable || stream.Info().Complete || errors.Is(stream.Err(), context.Canceled) {
		t.Fatalf("failure=%#v info=%#v err=%v", failure, stream.Info(), stream.Err())
	}
	for _, chunk := range chunks {
		if len(bytes.TrimSpace(chunk.Data)) > 0 {
			t.Fatalf("data frame arrived: %q", chunk.Data)
		}
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}

func TestIdleTimeoutStartsAfterRoleChunk(t *testing.T) {
	provider := testProvider(t, Config{FirstFrameTimeout: time.Second, IdleTimeout: 40 * time.Millisecond}, func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &frameBody{ctx: request.Context(), frames: []timedFrame{
			{data: "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"},
			{delay: 300 * time.Millisecond, data: "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"},
		}}}, nil
	})
	started := time.Now()
	chunks, stream := consumeStream(t, provider, context.Background(), testRequest())
	failure := requireFailure(t, stream.Err(), protocol.ErrorTimeout)
	if failure.TimeoutKind != protocol.TimeoutIdle || failure.Retryable || stream.Info().Complete || len(chunks) != 1 || len(bytes.TrimSpace(chunks[0].Data)) == 0 {
		t.Fatalf("chunks=%d failure=%#v info=%#v", len(chunks), failure, stream.Info())
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}

func TestStreamTimeoutOverrideInheritsDisablesAndReplaces(t *testing.T) {
	disabled := time.Duration(0)
	short := 30 * time.Millisecond
	for _, test := range []struct {
		name        string
		config      time.Duration
		override    *time.Duration
		wantTimeout bool
	}{
		{name: "override shorter", config: time.Second, override: &short, wantTimeout: true},
		{name: "zero disables", config: 30 * time.Millisecond, override: &disabled, wantTimeout: false},
		{name: "nil inherits", config: 30 * time.Millisecond, override: nil, wantTimeout: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := testProvider(t, Config{FirstFrameTimeout: test.config}, func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &frameBody{ctx: request.Context(), frames: []timedFrame{
					{delay: 80 * time.Millisecond, data: finalChat},
				}}}, nil
			})
			request := testRequest()
			request.FirstFrameTimeout = test.override
			_, stream := consumeStream(t, provider, context.Background(), request)
			if test.wantTimeout {
				failure := requireFailure(t, stream.Err(), protocol.ErrorTimeout)
				if failure.TimeoutKind != protocol.TimeoutFirstFrame || stream.Info().Complete {
					t.Fatalf("failure=%#v info=%#v", failure, stream.Info())
				}
				return
			}
			if stream.Err() != nil || !stream.Info().Complete {
				t.Fatalf("info=%#v err=%v", stream.Info(), stream.Err())
			}
		})
	}
}

func TestStreamChatUsesRequestTimeout(t *testing.T) {
	limit := 30 * time.Millisecond
	provider := testProvider(t, Config{FirstFrameTimeout: time.Second}, func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &frameBody{ctx: request.Context(), frames: []timedFrame{
			{delay: 80 * time.Millisecond, data: finalChat},
		}}}, nil
	})
	stream, err := provider.StreamChat(context.Background(), ChatRequest{
		Model: "m", Messages: []map[string]any{{"role": "user", "content": "hi"}}, FirstFrameTimeout: &limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	for stream.Next() {
	}
	failure := requireFailure(t, stream.Err(), protocol.ErrorTimeout)
	if failure.TimeoutKind != protocol.TimeoutFirstFrame || !failure.Retryable {
		t.Fatalf("failure=%#v", failure)
	}
}

func TestNegativeStreamTimeoutIsRejected(t *testing.T) {
	if _, err := New(Config{BaseURL: "https://example.com/v1", IdleTimeout: -time.Millisecond}); err == nil {
		t.Fatal("negative config was accepted")
	}
	attempts := 0
	provider := testProvider(t, Config{}, func(*http.Request) (*http.Response, error) {
		attempts++
		return testHTTPResponse(200, finalChat), nil
	})
	negative := -time.Millisecond
	request := testRequest()
	request.IdleTimeout = &negative
	_, err := provider.Stream(context.Background(), request)
	requireFailure(t, err, protocol.ErrorInvalidRequest)
	if attempts != 0 {
		t.Fatalf("negative timeout sent %d requests", attempts)
	}
}

func TestCallerCancelWinsOverStreamTimer(t *testing.T) {
	provider := testProvider(t, Config{FirstFrameTimeout: time.Second}, func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &frameBody{ctx: request.Context(), frames: []timedFrame{
			{delay: 5 * time.Second, data: finalChat},
		}}}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	_, stream := consumeStream(t, provider, ctx, testRequest())
	failure := requireFailure(t, stream.Err(), protocol.ErrorCanceled)
	if failure.TimeoutKind != "" || !errors.Is(stream.Err(), context.Canceled) {
		t.Fatalf("failure=%#v err=%v", failure, stream.Err())
	}
}

func TestGenerateAndChatIgnoreStreamTimeouts(t *testing.T) {
	body := `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`
	limit := 20 * time.Millisecond
	provider := testProvider(t, Config{FirstFrameTimeout: limit, IdleTimeout: limit}, func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &frameBody{ctx: request.Context(), frames: []timedFrame{
			{delay: 80 * time.Millisecond, data: body},
		}}}, nil
	})
	request := testRequest()
	request.FirstFrameTimeout = &limit
	started := time.Now()
	response, err := provider.Generate(context.Background(), request)
	if err != nil || !response.Complete || time.Since(started) < 50*time.Millisecond {
		t.Fatalf("response=%#v err=%v elapsed=%s", response, err, time.Since(started))
	}
	chat, err := provider.Chat(context.Background(), ChatRequest{
		Model: "m", Messages: []map[string]any{{"role": "user", "content": "hi"}}, FirstFrameTimeout: &limit, IdleTimeout: &limit,
	})
	if err != nil || chat["object"] != "chat.completion" {
		t.Fatalf("chat=%#v err=%v", chat, err)
	}
}

type timedFrame struct {
	delay time.Duration
	data  string
}

// frameBody emits scripted frames. A delay waits on the request context so a
// stream timer can unblock the read by cancelling that context.
type frameBody struct {
	ctx    context.Context
	frames []timedFrame
	index  int
	buf    []byte
}

func (body *frameBody) Read(buffer []byte) (int, error) {
	if len(body.buf) == 0 {
		if body.index >= len(body.frames) {
			return 0, io.EOF
		}
		frame := body.frames[body.index]
		body.index++
		if frame.delay > 0 {
			timer := time.NewTimer(frame.delay)
			select {
			case <-body.ctx.Done():
				timer.Stop()
				return 0, body.ctx.Err()
			case <-timer.C:
			}
		}
		body.buf = []byte(frame.data)
	}
	copied := copy(buffer, body.buf)
	body.buf = body.buf[copied:]
	return copied, nil
}

func (*frameBody) Close() error { return nil }
