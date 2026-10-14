package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/providers/openai"
)

type streamTransport func(*http.Request) (*http.Response, error)

func (transport streamTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type clientStreamBody struct {
	reader   io.Reader
	reads    atomic.Int32
	closes   atomic.Int32
	closeErr error
}

func (body *clientStreamBody) Read(buffer []byte) (int, error) {
	body.reads.Add(1)
	return body.reader.Read(buffer)
}
func (body *clientStreamBody) Close() error { body.closes.Add(1); return body.closeErr }

func streamClient(t *testing.T, hooks protocol.Hooks, transport streamTransport) *Client {
	t.Helper()
	provider, err := openai.New(openai.Config{BaseURL: "https://example.com/v1", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	return NewClient(provider, hooks)
}

func TestClientStreamLifecycleIncludesConsumptionAndIsolatedSnapshots(t *testing.T) {
	body := &clientStreamBody{reader: strings.NewReader(": keepalive\n\ndata: [DONE]\n\n")}
	var lifecycle []string
	calls := 0
	var observed protocol.ResponseInfo
	client := streamClient(t, protocol.Hooks{
		OnRequest: []protocol.RequestHook{func(_ context.Context, info protocol.RequestInfo) error {
			lifecycle = append(lifecycle, "request")
			info.Request.Messages[0].Parts[0].Text = "mutated"
			return nil
		}},
		OnResponse: []protocol.ResponseHook{
			func(_ context.Context, info protocol.ResponseInfo) error {
				info.Stream.Complete = false
				info.Stream.CallInfo.Headers["X-Test"][0] = "mutated"
				info.Request.Messages[0].Parts[0].Text = "mutated"
				return nil
			},
			func(_ context.Context, info protocol.ResponseInfo) error {
				lifecycle = append(lifecycle, "response")
				calls++
				observed = info
				return nil
			},
		},
	}, func(*http.Request) (*http.Response, error) {
		lifecycle = append(lifecycle, "open")
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Test": {"original"}}, Body: body}, nil
	})
	request := protocol.ModelRequest{Model: "m", Metadata: map[string]string{protocol.RequestIDMetadataKey: "request-id"}, Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")}}
	stream, err := client.Stream(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if calls != 0 || body.reads.Load() != 0 || strings.Join(lifecycle, ",") != "request,open" {
		t.Fatalf("lifecycle=%v reads=%d", lifecycle, body.reads.Load())
	}
	time.Sleep(2 * time.Millisecond)
	var frames strings.Builder
	for stream.Next() {
		frames.Write(stream.Chunk().Frame)
	}
	if stream.Err() != nil || calls != 1 || !stream.Info().Complete || body.closes.Load() != 1 {
		t.Fatalf("info=%#v err=%v calls=%d closes=%d", stream.Info(), stream.Err(), calls, body.closes.Load())
	}
	if frames.String() != ": keepalive\n\ndata: [DONE]\n\n" || observed.Stream == nil || !observed.Stream.Complete || observed.Error != nil || observed.Duration < time.Millisecond || observed.RequestID != "request-id" || len(observed.Response.Message.Parts) != 0 || observed.Request.Messages[0].Text() != "hi" || observed.Stream.CallInfo.Headers["X-Test"][0] != "original" {
		t.Fatalf("frames=%q observed=%#v", frames.String(), observed)
	}
	snapshot := stream.Info()
	snapshot.CallInfo.Headers["X-Test"][0] = "changed"
	if stream.Info().CallInfo.Headers["X-Test"][0] != "original" {
		t.Fatal("shared Info snapshot")
	}
	_ = stream.Close()
	_ = stream.Close()
	if stream.Next() || calls != 1 || body.closes.Load() != 1 {
		t.Fatal("finalized more than once")
	}
}

func TestClientStreamCloseFinalizesEvenWithoutDraining(t *testing.T) {
	for _, consumeTerminal := range []bool{false, true} {
		body := &clientStreamBody{reader: strings.NewReader("data: [DONE]\n\n")}
		calls := 0
		var observed protocol.ResponseInfo
		client := streamClient(t, protocol.Hooks{OnResponse: []protocol.ResponseHook{func(_ context.Context, info protocol.ResponseInfo) error { calls++; observed = info; return nil }}}, func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: 200, Body: body}, nil })
		stream, err := client.Stream(context.Background(), protocol.ModelRequest{Model: "m"})
		if err != nil {
			t.Fatal(err)
		}
		if consumeTerminal && !stream.Next() {
			t.Fatal(stream.Err())
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		if stream.Next() || calls != 1 || observed.Stream.Complete != consumeTerminal || body.closes.Load() != 1 {
			t.Fatalf("observed=%#v calls=%d closes=%d", observed, calls, body.closes.Load())
		}
		if consumeTerminal {
			if stream.Err() != nil {
				t.Fatal(stream.Err())
			}
		} else if !errors.Is(stream.Err(), context.Canceled) || !errors.Is(observed.Error, context.Canceled) || body.reads.Load() != 0 {
			t.Fatalf("err=%v observed=%#v reads=%d", stream.Err(), observed, body.reads.Load())
		}
	}
}

func TestClientStreamJoinsHookAndCallErrors(t *testing.T) {
	hookFailure := errors.New("response hook failed")
	closeFailure := errors.New("body close failed")
	for _, failed := range []bool{false, true} {
		payload := "data: [DONE]\n\n"
		if failed {
			payload = "data: {\"error\":{\"message\":\"upstream failure\"}}\n\n"
		}
		body := &clientStreamBody{reader: strings.NewReader(payload), closeErr: closeFailure}
		var observed protocol.ResponseInfo
		calls := 0
		client := streamClient(t, protocol.Hooks{OnResponse: []protocol.ResponseHook{func(_ context.Context, info protocol.ResponseInfo) error {
			calls++
			observed = info
			return hookFailure
		}}}, func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: 200, Body: body}, nil })
		stream, err := client.Stream(context.Background(), protocol.ModelRequest{Model: "m"})
		if err != nil {
			t.Fatal(err)
		}
		for stream.Next() {
		}
		if !errors.Is(stream.Err(), hookFailure) || !errors.Is(stream.Err(), closeFailure) || !errors.Is(observed.Error, closeFailure) || observed.Stream.Complete == failed || len(observed.Response.Message.Parts) != 0 {
			t.Fatalf("info=%#v err=%v observed=%#v", stream.Info(), stream.Err(), observed)
		}
		if failed {
			var failure *protocol.Error
			if !errors.As(observed.Error, &failure) || failure.Code != protocol.ErrorUpstream {
				t.Fatalf("call failure=%v", observed.Error)
			}
		}
		if err := stream.Close(); !errors.Is(err, hookFailure) || !errors.Is(err, closeFailure) || calls != 1 || body.closes.Load() != 1 {
			t.Fatalf("close=%v calls=%d", err, calls)
		}
	}
}

func TestClientStreamOpeningFailureRunsResponseHooks(t *testing.T) {
	hookFailure := errors.New("observe failure")
	calls := 0
	var observed protocol.ResponseInfo
	body := &clientStreamBody{reader: strings.NewReader("busy")}
	client := streamClient(t, protocol.Hooks{OnResponse: []protocol.ResponseHook{func(_ context.Context, info protocol.ResponseInfo) error {
		calls++
		observed = info
		return hookFailure
	}}}, func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: 503, Body: body}, nil })
	stream, err := client.Stream(context.Background(), protocol.ModelRequest{Model: "m"})
	var failure *protocol.Error
	if stream != nil || !errors.As(err, &failure) || failure.Code != protocol.ErrorUpstream || !errors.Is(err, hookFailure) || calls != 1 || observed.Stream == nil || observed.Stream.CallInfo.StatusCode != 503 || observed.Stream.CallInfo.Attempts != 1 || observed.Stream.Complete || body.closes.Load() != 1 {
		t.Fatalf("stream=%v err=%v observed=%#v", stream, err, observed)
	}
}

func TestClientStreamRejectsUnsupportedAndBlockedCallsBeforeOpening(t *testing.T) {
	unsupported := NewClient(&scriptedProvider{}, protocol.Hooks{OnRequest: []protocol.RequestHook{func(context.Context, protocol.RequestInfo) error {
		t.Fatal("unsupported provider ran hook")
		return nil
	}}})
	_, err := unsupported.Stream(context.Background(), protocol.ModelRequest{})
	var failure *protocol.Error
	if !errors.As(err, &failure) || failure.Code != protocol.ErrorUnsupported {
		t.Fatal(err)
	}
	blocked := errors.New("blocked")
	client := streamClient(t, protocol.Hooks{
		OnRequest: []protocol.RequestHook{func(context.Context, protocol.RequestInfo) error { return blocked }},
		OnResponse: []protocol.ResponseHook{func(context.Context, protocol.ResponseInfo) error {
			t.Fatal("request rejection ran response hook")
			return nil
		}},
	}, func(*http.Request) (*http.Response, error) { t.Fatal("blocked request sent"); return nil, nil })
	if _, err := client.Stream(context.Background(), protocol.ModelRequest{Model: "m"}); !errors.Is(err, blocked) {
		t.Fatal(err)
	}
	for _, client := range []*Client{nil, NewClient(nil, protocol.Hooks{})} {
		if _, err := client.Stream(context.Background(), protocol.ModelRequest{}); err == nil {
			t.Fatal("missing provider accepted")
		}
	}
}

func TestClientStreamConcurrentCloseFinalizesOnce(t *testing.T) {
	started := make(chan struct{})
	var bodyClosed atomic.Int32
	var hooks atomic.Int32
	client := streamClient(t, protocol.Hooks{OnResponse: []protocol.ResponseHook{func(_ context.Context, info protocol.ResponseInfo) error {
		hooks.Add(1)
		if info.Stream == nil || info.Stream.Complete || !errors.Is(info.Error, context.Canceled) {
			t.Errorf("info=%#v", info)
		}
		return nil
	}}}, func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: &waitingStreamBody{ctx: request.Context(), started: started, closed: &bodyClosed}}, nil
	})
	stream, err := client.Stream(context.Background(), protocol.ModelRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	done := make(chan bool, 1)
	go func() { done <- stream.Next() }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("read did not start")
	}
	var closers sync.WaitGroup
	for range 8 {
		closers.Add(1)
		go func() { defer closers.Done(); _ = stream.Close() }()
	}
	closers.Wait()
	select {
	case next := <-done:
		if next {
			t.Fatal("canceled read yielded frame")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked read")
	}
	if hooks.Load() != 1 || bodyClosed.Load() != 1 || !errors.Is(stream.Err(), context.Canceled) {
		t.Fatalf("hooks=%d closes=%d err=%v", hooks.Load(), bodyClosed.Load(), stream.Err())
	}
}

type waitingStreamBody struct {
	ctx     context.Context
	started chan struct{}
	closed  *atomic.Int32
}

func (body *waitingStreamBody) Read([]byte) (int, error) {
	close(body.started)
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}
func (body *waitingStreamBody) Close() error { body.closed.Add(1); return nil }

func TestClientStreamHookPanicPropagatesAfterBodyIsClosed(t *testing.T) {
	body := &clientStreamBody{reader: strings.NewReader("data: [DONE]\n\n")}
	client := streamClient(t, protocol.Hooks{OnResponse: []protocol.ResponseHook{func(context.Context, protocol.ResponseInfo) error { panic("hook panic") }}}, func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: 200, Body: body}, nil })
	stream, err := client.Stream(context.Background(), protocol.ModelRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recovered := recover(); recovered != "hook panic" || body.closes.Load() != 1 {
			t.Fatalf("panic=%v closes=%d", recovered, body.closes.Load())
		}
		_ = stream.Close()
	}()
	for stream.Next() {
	}
	t.Fatal("hook panic swallowed")
}

// Finalization must wait for body Close, even when another goroutine has
// already published the canceled state. Otherwise hooks miss close errors.
func TestClientStreamHooksObserveConcurrentBodyCloseFailure(t *testing.T) {
	closeFailure := errors.New("concurrent close failed")
	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	readStarted := make(chan struct{})
	hookCalled := make(chan protocol.ResponseInfo, 1)
	client := streamClient(t, protocol.Hooks{OnResponse: []protocol.ResponseHook{
		func(_ context.Context, info protocol.ResponseInfo) error { hookCalled <- info; return nil },
	}}, func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: &orderedCloseBody{ctx: request.Context(), readStarted: readStarted, closeStarted: closeStarted, releaseClose: releaseClose, err: closeFailure}}, nil
	})
	stream, err := client.Stream(context.Background(), protocol.ModelRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClose) }) }
	defer func() { release(); _ = stream.Close() }()
	nextDone := make(chan bool, 1)
	go func() { nextDone <- stream.Next() }()
	select {
	case <-readStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("read did not start")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- stream.Close() }()
	select {
	case <-closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("close did not start")
	}
	select {
	case info := <-hookCalled:
		t.Fatalf("hook ran before close returned: %#v", info)
	default:
	}
	release()
	select {
	case next := <-nextDone:
		if next {
			t.Fatal("canceled read yielded frame")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read blocked")
	}
	select {
	case err := <-closeDone:
		if !errors.Is(err, closeFailure) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close blocked")
	}
	select {
	case info := <-hookCalled:
		if !errors.Is(info.Error, closeFailure) || !errors.Is(info.Error, context.Canceled) || info.Stream.Complete {
			t.Fatalf("info=%#v err=%v", info, info.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("response hook missing")
	}
}

type orderedCloseBody struct {
	ctx                                     context.Context
	readStarted, closeStarted, releaseClose chan struct{}
	err                                     error
}

func (body *orderedCloseBody) Read([]byte) (int, error) {
	close(body.readStarted)
	<-body.ctx.Done()
	return 0, body.ctx.Err()
}
func (body *orderedCloseBody) Close() error {
	close(body.closeStarted)
	<-body.releaseClose
	return body.err
}
