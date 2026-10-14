package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

func testProvider(t *testing.T, config Config, transport roundTripFunc) *Provider {
	t.Helper()
	config.BaseURL = "https://example.com/v1"
	config.HTTPClient = &http.Client{Transport: transport}
	provider, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func testRequest() protocol.ModelRequest {
	return protocol.ModelRequest{Model: "m", Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "hi")}}
}

func testHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func requireFailure(t *testing.T, err error, code protocol.ErrorCode) *protocol.Error {
	t.Helper()
	var failure *protocol.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("error=%v, want %s", err, code)
	}
	return failure
}

func TestHTTPFailureRetainsFinalBodyHeadersAndAttempts(t *testing.T) {
	attempts := 0
	var observed []AttemptInfo
	body := `{"error":{"message":"quota exhausted","code":"quota"},"extension":true}`
	provider := testProvider(t, Config{Retry: RetryPolicy{MaxRetries: 2, Backoff: time.Nanosecond}, OnAttempt: func(_ context.Context, info AttemptInfo) error { observed = append(observed, info); return nil }}, func(*http.Request) (*http.Response, error) {
		attempts++
		response := testHTTPResponse(429, body)
		response.Header.Set("X-Request-Id", "quota-request")
		return response, nil
	})
	response, err := provider.Generate(context.Background(), testRequest())
	failure := requireFailure(t, err, protocol.ErrorRateLimit)
	if !failure.Retryable || failure.CallInfo.StatusCode != 429 || failure.CallInfo.Attempts != 3 || string(failure.Body) != body || failure.CallInfo.Headers["X-Request-Id"][0] != "quota-request" {
		t.Fatalf("failure=%#v", failure)
	}
	if response.Complete || response.CallInfo.Attempts != attempts || len(observed) != 3 || !observed[0].WillRetry || observed[2].WillRetry {
		t.Fatalf("response=%#v observed=%#v", response, observed)
	}
}

func TestRetryAfterBoundsActualRetryAndCancellation(t *testing.T) {
	attempts := 0
	var observed AttemptInfo
	provider := testProvider(t, Config{Retry: RetryPolicy{MaxRetries: 2, Backoff: time.Nanosecond}, OnAttempt: func(_ context.Context, info AttemptInfo) error { observed = info; return nil }}, func(*http.Request) (*http.Response, error) {
		attempts++
		response := testHTTPResponse(429, `{"error":{"message":"wait"}}`)
		response.Header.Set("Retry-After", "2")
		return response, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	_, err := provider.Generate(ctx, testRequest())
	failure := requireFailure(t, err, protocol.ErrorTimeout)
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 || observed.RetryDelay < 2*time.Second || failure.CallInfo.Attempts != 1 {
		t.Fatalf("err=%v attempts=%d observed=%#v", err, attempts, observed)
	}
}

func TestRetryAfterSupportsSecondsAndDates(t *testing.T) {
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		value string
		want  time.Duration
	}{
		{"3", 3 * time.Second}, {now.Add(5 * time.Second).Format(http.TimeFormat), 5 * time.Second},
		{"-1", 0}, {"invalid", 0}, {now.Add(-time.Second).Format(http.TimeFormat), 0},
	} {
		if got := parseRetryAfter(test.value, now); got != test.want {
			t.Errorf("%q got %s want %s", test.value, got, test.want)
		}
	}
}

func TestRetryDefaultsAndPermanentTransportErrors(t *testing.T) {
	sentinel := errors.New("permanent transport configuration failure")
	for _, test := range []struct {
		name           string
		retry          RetryPolicy
		status         int
		transportError error
	}{
		{name: "default has no retries", status: 503},
		{name: "authentication does not retry", retry: RetryPolicy{MaxRetries: 3}, status: 401},
		{name: "permanent error does not retry", retry: RetryPolicy{MaxRetries: 3}, transportError: sentinel},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			provider := testProvider(t, Config{Retry: test.retry}, func(*http.Request) (*http.Response, error) {
				attempts++
				if test.transportError != nil {
					return nil, test.transportError
				}
				return testHTTPResponse(test.status, "failure"), nil
			})
			_, err := provider.Generate(context.Background(), testRequest())
			if err == nil || attempts != 1 {
				t.Fatalf("err=%v attempts=%d", err, attempts)
			}
			if test.transportError != nil && !errors.Is(err, sentinel) {
				t.Fatalf("cause lost: %v", err)
			}
		})
	}
}

func TestCanceledContextDoesNotSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := testProvider(t, Config{}, func(*http.Request) (*http.Response, error) { t.Fatal("canceled request sent"); return nil, nil })
	response, err := provider.Generate(ctx, testRequest())
	failure := requireFailure(t, err, protocol.ErrorCanceled)
	if !errors.Is(err, context.Canceled) || failure.CallInfo.Attempts != 0 || response.Complete {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

func TestNativeRequestAndResponseRemainUnnormalized(t *testing.T) {
	for _, status := range []int{200, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requestBody := json.RawMessage("{\n  \"stream\": true, \"messages\": [{\"role\":\"developer\",\"vendor\":42}], \"vendor\": [1,2]\n}")
			responseBody := ": keepalive\r\n\r\nevent: vendor\r\ndata: {\"unknown\":42}\r\n\r\ndata: [DONE]\r\n\r\n"
			provider := testProvider(t, Config{}, func(request *http.Request) (*http.Response, error) {
				actual, err := io.ReadAll(request.Body)
				if err != nil || string(actual) != string(requestBody) || request.Header.Get("Accept") != "text/event-stream" {
					t.Fatalf("native request changed: %s err=%v", actual, err)
				}
				response := testHTTPResponse(status, responseBody)
				response.Header["X-Vendor"] = []string{"one", "two"}
				return response, nil
			})
			response, err := provider.DoNative(context.Background(), requestBody)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			actual, err := io.ReadAll(response.Body)
			if err != nil || string(actual) != responseBody || response.StatusCode != status || response.Attempts != 1 || !reflect.DeepEqual(response.Headers["X-Vendor"], []string{"one", "two"}) {
				t.Fatalf("response=%#v body=%q err=%v", response, actual, err)
			}
		})
	}
}

func TestDefaultTransportHasNoTotalTimeout(t *testing.T) {
	provider, err := New(Config{BaseURL: "https://example.com/v1", ResponseHeaderTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if provider.httpClient.Timeout != 0 || provider.httpClient.Transport.(*http.Transport).ResponseHeaderTimeout != 10*time.Millisecond {
		t.Fatal("unexpected timeout policy")
	}
	// Delay body reading beyond the header timeout. Header deadlines must not
	// become stream deadlines, and the default client must not own a total limit.
	provider.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &delayedBody{ctx: request.Context(), delay: 25 * time.Millisecond, reader: strings.NewReader("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")}}, nil
	})
	response, err := provider.Stream(context.Background(), testRequest(), nil)
	if err != nil || !response.Complete || response.Message.Text() != "ok" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

type delayedBody struct {
	ctx    context.Context
	delay  time.Duration
	reader *strings.Reader
}

func (body *delayedBody) Read(buffer []byte) (int, error) {
	if body.delay > 0 {
		timer := time.NewTimer(body.delay)
		body.delay = 0
		defer timer.Stop()
		select {
		case <-body.ctx.Done():
			return 0, body.ctx.Err()
		case <-timer.C:
		}
	}
	return body.reader.Read(buffer)
}
func (*delayedBody) Close() error { return nil }

func TestInjectedClientTimeoutAndCallerDeadlineArePreserved(t *testing.T) {
	for _, useClientTimeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller context", true: "injected client"}[useClientTimeout], func(t *testing.T) {
			provider := testProvider(t, Config{}, func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &delayedBody{ctx: request.Context(), delay: time.Second, reader: strings.NewReader("data: [DONE]\n\n")}}, nil
			})
			ctx := context.Background()
			cancel := func() {}
			if useClientTimeout {
				provider.httpClient.Timeout = 5 * time.Millisecond
			} else {
				ctx, cancel = context.WithTimeout(ctx, 5*time.Millisecond)
			}
			defer cancel()
			_, err := provider.Stream(ctx, testRequest(), nil)
			requireFailure(t, err, protocol.ErrorTimeout)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline cause lost: %v", err)
			}
		})
	}
}

func TestTransientNetworkTimeoutCanRetryWhileContextLives(t *testing.T) {
	attempts := 0
	provider := testProvider(t, Config{Retry: RetryPolicy{MaxRetries: 1, Backoff: time.Nanosecond}}, func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, testTimeout{}
		}
		return testHTTPResponse(200, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`), nil
	})
	response, err := provider.Generate(context.Background(), testRequest())
	if err != nil || !response.Complete || response.CallInfo.Attempts != 2 {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

type testTimeout struct{}

func (testTimeout) Error() string   { return "header timeout" }
func (testTimeout) Timeout() bool   { return true }
func (testTimeout) Temporary() bool { return true }
func (testTimeout) Unwrap() error   { return context.DeadlineExceeded }

func TestRetryObserverCanAbortWithoutRetry(t *testing.T) {
	sentinel := errors.New("observer canceled")
	attempts := 0
	provider := testProvider(t, Config{Retry: RetryPolicy{MaxRetries: 2}, OnAttempt: func(_ context.Context, info AttemptInfo) error {
		failure := requireFailure(t, info.Error, protocol.ErrorRateLimit)
		if failure.CallInfo.Attempts != 1 || !info.WillRetry {
			t.Fatalf("info=%#v", info)
		}
		return sentinel
	}}, func(*http.Request) (*http.Response, error) { attempts++; return testHTTPResponse(429, "wait"), nil })
	_, err := provider.Generate(context.Background(), testRequest())
	requireFailure(t, err, protocol.ErrorSink)
	if attempts != 1 || !errors.Is(err, sentinel) {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestAttemptCallbackSDKErrorRemainsAConsumerFailure(t *testing.T) {
	sentinel := &protocol.Error{Code: protocol.ErrorRateLimit, Retryable: true, Message: "consumer quota"}
	provider := testProvider(t, Config{OnAttempt: func(context.Context, AttemptInfo) error { return sentinel }}, func(*http.Request) (*http.Response, error) { return testHTTPResponse(200, `{"choices":[]}`), nil })
	_, err := provider.Generate(context.Background(), testRequest())
	failure := requireFailure(t, err, protocol.ErrorSink)
	if failure.Retryable || !errors.Is(err, sentinel) {
		t.Fatalf("callback misclassified: %#v", failure)
	}
}
