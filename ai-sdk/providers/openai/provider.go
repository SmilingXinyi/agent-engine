// Package openai implements an OpenAI-compatible adapter for ai-sdk.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	protocol "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

const defaultTimeout = 2 * time.Minute

const (
	defaultMaxResponseBytes = 16 << 20
	defaultMaxRetries       = 2
	defaultRetryBackoff     = 200 * time.Millisecond
)

type Config struct {
	BaseURL string
	APIKey  string
	// APIType selects the OpenAI-compatible endpoint and wire format. An empty
	// value keeps the historical Chat Completions behavior.
	APIType          APIType
	Headers          map[string]string
	HTTPClient       *http.Client
	MaxResponseBytes int64
	// MaxRetries controls retries for transient HTTP failures. Zero uses the
	// default; a negative value disables retries.
	MaxRetries   int
	RetryBackoff time.Duration
	// Name overrides the provider identity reported via Provider.Name and
	// surfaced in hooks (RequestInfo.Provider / ResponseInfo.Provider). This
	// lets OpenAI-compatible backends (e.g. DeepSeek) report their own name
	// without a dedicated provider package. Defaults to "openai".
	Name string
}

// APIType identifies the OpenAI API surface used by the provider.
type APIType string

const (
	APITypeChatCompletions APIType = "chat_completions"
	APITypeResponsesAPI    APIType = "responses_api"

	// Short aliases make configuration convenient while retaining explicit
	// names for callers that prefer them.
	ChatCompletions    APIType = APITypeChatCompletions
	ChatCompletionsAPI APIType = APITypeChatCompletions
	ResponsesAPI       APIType = APITypeResponsesAPI
	APIChatCompletions         = APITypeChatCompletions
	APIResponses               = APITypeResponsesAPI
)

type Provider struct {
	name             string
	apiType          APIType
	baseURL          string
	apiKey           string
	headers          map[string]string
	httpClient       *http.Client
	maxResponseBytes int64
	maxRetries       int
	retryBackoff     time.Duration
}

func New(config Config) (*Provider, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("openai base URL is required")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = "openai"
	}
	apiType := config.APIType
	if apiType == "" {
		apiType = APITypeChatCompletions
	}
	if apiType != APITypeChatCompletions && apiType != APITypeResponsesAPI {
		return nil, fmt.Errorf("unsupported OpenAI API type %q", apiType)
	}
	return &Provider{
		name:             name,
		apiType:          apiType,
		baseURL:          baseURL,
		apiKey:           strings.TrimSpace(config.APIKey),
		headers:          cloneStringMap(config.Headers),
		httpClient:       client,
		maxResponseBytes: positiveOrDefault(config.MaxResponseBytes, defaultMaxResponseBytes),
		maxRetries:       configuredRetries(config.MaxRetries),
		retryBackoff:     positiveDurationOrDefault(config.RetryBackoff, defaultRetryBackoff),
	}, nil
}

func (provider *Provider) Name() string {
	return provider.name
}

func (provider *Provider) Generate(ctx context.Context, request protocol.ModelRequest) (protocol.ModelResponse, error) {
	payload, err := provider.buildRequest(request, false)
	if err != nil {
		return protocol.ModelResponse{}, err
	}
	response, err := provider.send(ctx, payload)
	if err != nil {
		return protocol.ModelResponse{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if err := validateHTTPResponse(response); err != nil {
		return protocol.ModelResponse{}, err
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(response.Body, provider.maxResponseBytes+1))
	if err != nil {
		return protocol.ModelResponse{}, fmt.Errorf("read OpenAI response: %w", err)
	}
	if int64(len(bodyBytes)) > provider.maxResponseBytes {
		return protocol.ModelResponse{}, fmt.Errorf("OpenAI response exceeds %d bytes", provider.maxResponseBytes)
	}
	if provider.apiType == APITypeResponsesAPI {
		var body responsesResponse
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			return protocol.ModelResponse{}, fmt.Errorf("decode OpenAI Responses response: %w", err)
		}
		return convertResponsesResponse(body, provider.name)
	}
	var body chatResponse
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return protocol.ModelResponse{}, fmt.Errorf("decode OpenAI response: %w", err)
	}
	return convertResponse(body, provider.name)
}

func (provider *Provider) send(ctx context.Context, payload map[string]any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode OpenAI request: %w", err)
	}
	newRequest := func() (*http.Request, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.endpoint(), bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create OpenAI request: %w", err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		if stream, _ := payload["stream"].(bool); stream {
			request.Header.Set("Accept", "text/event-stream")
		}
		if provider.apiKey != "" {
			request.Header.Set("Authorization", "Bearer "+provider.apiKey)
		}
		for name, value := range provider.headers {
			request.Header.Set(name, value)
		}
		return request, nil
	}
	var lastErr error
	for attempt := 0; attempt <= provider.maxRetries; attempt++ {
		request, err := newRequest()
		if err != nil {
			return nil, err
		}
		response, err := provider.httpClient.Do(request)
		if err == nil && !isRetryableStatus(response.StatusCode) {
			return response, nil
		}
		if err == nil {
			lastErr = fmt.Errorf("OpenAI HTTP %d", response.StatusCode)
			_ = response.Body.Close()
		} else {
			lastErr = err
		}
		if attempt == provider.maxRetries {
			break
		}
		timer := time.NewTimer(provider.retryBackoff * time.Duration(1<<attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("send OpenAI request after %d attempts: %w", provider.maxRetries+1, lastErr)
}

func (provider *Provider) endpoint() string {
	suffix := "/chat/completions"
	if provider.apiType == APITypeResponsesAPI {
		suffix = "/responses"
	}
	if strings.HasSuffix(provider.baseURL, suffix) {
		return provider.baseURL
	}
	return provider.baseURL + suffix
}

func (provider *Provider) buildRequest(request protocol.ModelRequest, stream bool) (map[string]any, error) {
	if provider.apiType == APITypeResponsesAPI {
		return buildResponsesRequest(request, stream)
	}
	return buildRequest(request, stream)
}

func validateHTTPResponse(response *http.Response) error {
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	message := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &payload) == nil && payload.Error.Message != "" {
		message = payload.Error.Message
	}
	return fmt.Errorf("OpenAI HTTP %d: %s", response.StatusCode, message)
}

func isRetryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func positiveOrDefault(value, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}

func configuredRetries(value int) int {
	if value < 0 {
		return 0
	}
	if value == 0 {
		return defaultMaxRetries
	}
	return value
}

func positiveDurationOrDefault(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
