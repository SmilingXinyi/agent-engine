package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

func (provider *Provider) send(ctx context.Context, body []byte, streaming bool) (*http.Response, protocol.CallInfo, error) {
	var info protocol.CallInfo
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, info, provider.failure(err, protocol.ErrorCanceled, info)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.endpoint(), bytes.NewReader(body))
		if err != nil {
			return nil, info, provider.failure(err, protocol.ErrorInvalidRequest, info)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		if streaming {
			request.Header.Set("Accept", "text/event-stream")
		}
		if provider.apiKey != "" {
			request.Header.Set("Authorization", "Bearer "+provider.apiKey)
		}
		for name, value := range provider.headers {
			request.Header.Set(name, value)
		}
		response, transportErr := provider.httpClient.Do(request)
		info = protocol.CallInfo{Attempts: attempt}
		if response != nil {
			info.StatusCode = response.StatusCode
			info.Headers = map[string][]string(response.Header.Clone())
		}
		transient := isRetryableTransport(transportErr)
		if transportErr == nil {
			transient = isRetryableStatus(response.StatusCode)
		}
		willRetry := transient && attempt <= provider.retry.MaxRetries && ctx.Err() == nil
		delay := time.Duration(0)
		if willRetry {
			delay = provider.retryDelay(attempt)
			if response != nil {
				if retryAfter := parseRetryAfter(response.Header.Get("Retry-After"), time.Now()); retryAfter > delay {
					delay = retryAfter
				}
			}
		}
		var attemptErr error
		if transportErr != nil {
			attemptErr = provider.failure(transportErr, protocol.ErrorTransport, info)
			if ctx.Err() != nil {
				attemptErr = provider.failure(ctx.Err(), protocol.ErrorCanceled, info)
			}
		} else if response.StatusCode < 200 || response.StatusCode >= 300 {
			code := protocol.ErrorUpstream
			if response.StatusCode == 429 {
				code = protocol.ErrorRateLimit
			}
			if response.StatusCode == 401 || response.StatusCode == 403 {
				code = protocol.ErrorAuthentication
			}
			attemptErr = &protocol.Error{Code: code, Provider: provider.name, Message: fmt.Sprintf("HTTP %d", response.StatusCode), Retryable: transient, CallInfo: info.Clone()}
		}
		if provider.onAttempt != nil {
			if hookErr := provider.onAttempt(ctx, AttemptInfo{Number: attempt, StatusCode: info.StatusCode, Error: attemptErr, WillRetry: willRetry, RetryDelay: delay}); hookErr != nil {
				if response != nil {
					response.Body.Close()
				}
				return nil, info, &protocol.Error{
					Code: protocol.ErrorSink, Provider: provider.name,
					Message:  "attempt callback: " + hookErr.Error(),
					CallInfo: info.Clone(), Cause: hookErr,
				}
			}
		}
		if !willRetry {
			if transportErr != nil {
				if response != nil {
					response.Body.Close()
				}
				return nil, info, attemptErr
			}
			return response, info, nil
		}
		if response != nil {
			response.Body.Close()
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, info, provider.failure(ctx.Err(), protocol.ErrorCanceled, info)
		case <-timer.C:
		}
	}
}

func (provider *Provider) validateHTTPResponse(response *http.Response, info protocol.CallInfo) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, provider.maxResponseBytes))
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	message := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &payload) == nil && payload.Error.Message != "" {
		message = payload.Error.Message
	}
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	code := protocol.ErrorUpstream
	switch response.StatusCode {
	case 401, 403:
		code = protocol.ErrorAuthentication
	case 429:
		code = protocol.ErrorRateLimit
	}
	return &protocol.Error{Code: code, Provider: provider.name, Message: fmt.Sprintf("HTTP %d: %s", response.StatusCode, message), Retryable: isRetryableStatus(response.StatusCode), CallInfo: info.Clone(), Body: body, Cause: readErr}
}

func (provider *Provider) failure(err error, code protocol.ErrorCode, info protocol.CallInfo) error {
	var existing *protocol.Error
	if errors.As(err, &existing) {
		copied := *existing
		if copied.Provider == "" {
			copied.Provider = provider.name
		}
		if copied.CallInfo.Attempts == 0 {
			copied.CallInfo = info.Clone()
		}
		return &copied
	}
	retryable := false
	// Sink failures describe consumers, not transport. Preserve their cause but
	// never classify them as a retryable provider failure.
	if code != protocol.ErrorSink && code != protocol.ErrorInvalidRequest {
		switch {
		case errors.Is(err, context.Canceled):
			code = protocol.ErrorCanceled
		case errors.Is(err, context.DeadlineExceeded):
			code = protocol.ErrorTimeout
			retryable = isRetryableTransport(err)
		default:
			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Timeout() {
				code = protocol.ErrorTimeout
				retryable = true
			}
			if code == protocol.ErrorTransport {
				retryable = isRetryableTransport(err)
			}
		}
	}
	return &protocol.Error{Code: code, Provider: provider.name, Message: err.Error(), Retryable: retryable, CallInfo: info.Clone(), Cause: err}
}

func isRetryableStatus(status int) bool {
	return status == 408 || status == 429 || (status >= 500 && status <= 599)
}

func isRetryableTransport(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || err == context.DeadlineExceeded {
		return false
	}
	var networkError net.Error
	if errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary()) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func (provider *Provider) retryDelay(attempt int) time.Duration {
	delay := provider.retry.Backoff
	for index := 1; index < attempt && delay < provider.retry.MaxBackoff; index++ {
		if delay > provider.retry.MaxBackoff/2 {
			return provider.retry.MaxBackoff
		}
		delay *= 2
	}
	if delay > provider.retry.MaxBackoff {
		return provider.retry.MaxBackoff
	}
	return delay
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		if seconds > math.MaxInt64/int64(time.Second) {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}
