// Package openai implements Chat Completions and Responses model calls.
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/utils"
)

const (
	defaultMaxResponseBytes = 16 << 20
	defaultMaxEventBytes    = 1 << 20
	defaultRetryBackoff     = 200 * time.Millisecond
	defaultMaxBackoff       = 5 * time.Second
)

type APIType string

const (
	APITypeChatCompletions APIType = "chat_completions"
	APITypeResponsesAPI    APIType = "responses_api"
)

// RetryPolicy is opt-in. MaxRetries=0 sends exactly one application attempt.
// Retries stop before consuming a successful body. Retry-After is a minimum
// delay and may exceed MaxBackoff; context bounds the entire call.
type RetryPolicy struct {
	MaxRetries int
	Backoff    time.Duration
	MaxBackoff time.Duration
}

// AttemptInfo is emitted after each HTTP attempt, before its optional backoff.
type AttemptInfo struct {
	Number     int
	StatusCode int
	Error      error
	WillRetry  bool
	RetryDelay time.Duration
}

type Config struct {
	BaseURL string
	APIKey  string
	APIType APIType
	Name    string
	Headers map[string]string
	// HTTPClient owns transport and timeout configuration when supplied.
	HTTPClient *http.Client
	// ResponseHeaderTimeout applies only to the default transport. Zero disables
	// this timeout. There is no default total timeout; callers should use context.
	ResponseHeaderTimeout time.Duration
	Retry                 RetryPolicy
	OnAttempt             func(context.Context, AttemptInfo) error
	// MaxResponseBytes defaults to 16 MiB. MaxStreamBytes=0 has no total stream
	// limit. MaxEventBytes defaults to 1 MiB including SSE framing.
	MaxResponseBytes int64
	MaxStreamBytes   int64
	MaxEventBytes    int64
	// RequireDone requires [DONE] for Chat Completions. Normally a finish_reason
	// followed by clean EOF also completes the stream. It does not affect Responses.
	RequireDone bool
}

type Provider struct {
	name             string
	apiType          APIType
	baseURL          string
	apiKey           string
	headers          map[string]string
	httpClient       *http.Client
	retry            RetryPolicy
	onAttempt        func(context.Context, AttemptInfo) error
	maxResponseBytes int64
	maxStreamBytes   int64
	maxEventBytes    int64
	requireDone      bool
}

func New(config Config) (*Provider, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, &protocol.Error{Code: protocol.ErrorInvalidRequest, Provider: "openai", Message: "base URL must be an HTTP(S) URL without query or fragment", Cause: err}
	}
	if config.ResponseHeaderTimeout < 0 || config.Retry.MaxRetries < 0 || config.Retry.Backoff < 0 || config.Retry.MaxBackoff < 0 || config.MaxResponseBytes < 0 || config.MaxStreamBytes < 0 || config.MaxEventBytes < 0 {
		return nil, &protocol.Error{Code: protocol.ErrorInvalidRequest, Provider: "openai", Message: "timeouts, retries and limits must be nonnegative"}
	}
	client := config.HTTPClient
	if client != nil && config.ResponseHeaderTimeout != 0 {
		return nil, &protocol.Error{Code: protocol.ErrorInvalidRequest, Provider: "openai", Message: "configure header timeout on the injected HTTPClient"}
	}
	if client == nil {
		transport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, &protocol.Error{Code: protocol.ErrorInvalidRequest, Provider: "openai", Message: "custom default transport requires an explicit HTTPClient"}
		}
		ownedTransport := transport.Clone()
		ownedTransport.ResponseHeaderTimeout = config.ResponseHeaderTimeout
		client = &http.Client{Transport: ownedTransport}
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
		return nil, &protocol.Error{Code: protocol.ErrorInvalidRequest, Provider: name, Message: fmt.Sprintf("unsupported OpenAI API type %q", apiType)}
	}
	retry := config.Retry
	if retry.Backoff == 0 {
		retry.Backoff = defaultRetryBackoff
	}
	if retry.MaxBackoff == 0 {
		retry.MaxBackoff = defaultMaxBackoff
	}
	return &Provider{
		name: name, apiType: apiType, baseURL: baseURL, apiKey: strings.TrimSpace(config.APIKey),
		headers: utils.CloneMap(config.Headers), httpClient: client, retry: retry, onAttempt: config.OnAttempt,
		maxResponseBytes: positiveOrDefault(config.MaxResponseBytes, defaultMaxResponseBytes),
		maxStreamBytes:   config.MaxStreamBytes, maxEventBytes: positiveOrDefault(config.MaxEventBytes, defaultMaxEventBytes),
		requireDone: config.RequireDone,
	}, nil
}

func (provider *Provider) Name() string { return provider.name }

func (provider *Provider) Capabilities() protocol.Capabilities {
	input := []protocol.ContentType{protocol.ContentText, protocol.ContentReasoning, protocol.ContentImage, protocol.ContentFile, protocol.ContentToolCall, protocol.ContentProvider}
	if provider.apiType == APITypeChatCompletions {
		input = append(input, protocol.ContentAudio)
	}
	return protocol.Capabilities{Streaming: true, Tools: true, Reasoning: true, StructuredOutput: true, InputContent: input, ToolResultContent: []protocol.ContentType{protocol.ContentText}}
}

func (provider *Provider) Generate(ctx context.Context, request protocol.ModelRequest) (result protocol.ModelResponse, err error) {
	payload, err := provider.buildRequest(request, false)
	if err != nil {
		return result, provider.failure(err, protocol.ErrorInvalidRequest, protocol.CallInfo{})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return result, provider.failure(err, protocol.ErrorInvalidRequest, protocol.CallInfo{})
	}
	bodyBytes, info, err := provider.postJSON(ctx, body, false)
	result.CallInfo = info
	if err != nil {
		return result, err
	}
	if provider.apiType == APITypeResponsesAPI {
		var decoded responsesResponse
		if err = json.Unmarshal(bodyBytes, &decoded); err == nil {
			result, err = convertResponsesResponse(decoded, provider.name)
		}
	} else {
		var response map[string]any
		if response, err = decodeJSONObject(bodyBytes); err == nil {
			if response, err = normalizeChatCompletion(response, false); err == nil {
				result, err = convertChatCompletion(response, provider.name)
			}
		}
	}
	result.CallInfo = info
	result.Raw = append(json.RawMessage(nil), bodyBytes...)
	if err != nil {
		return result, provider.failure(err, protocol.ErrorDecode, info)
	}
	if result.FinishReason == protocol.FinishError {
		return result, provider.failure(fmt.Errorf("upstream returned a failed model response"), protocol.ErrorUpstream, info)
	}
	result.Complete = true
	return result, nil
}

// Chat performs one non-streaming Chat Completions call.
// The returned object uses "object": "chat.completion" and keeps unknown fields.
func (provider *Provider) Chat(ctx context.Context, request ChatRequest) (map[string]any, error) {
	body, err := provider.chatJSON(request, false)
	if err != nil {
		return nil, err
	}
	bodyBytes, info, err := provider.postJSON(ctx, body, false)
	if err != nil {
		return nil, err
	}
	response, err := decodeJSONObject(bodyBytes)
	if err != nil {
		return nil, provider.failure(err, protocol.ErrorDecode, info)
	}
	response, err = normalizeChatCompletion(response, true)
	if err != nil {
		return nil, provider.failure(err, protocol.ErrorDecode, info)
	}
	return response, nil
}

// ChatStream sends one Chat Completions request with stream set to true.
// Messages stay native objects. Next delivers the original SSE frames.
func (provider *Provider) ChatStream(ctx context.Context, request ChatRequest) (protocol.Stream, error) {
	body, err := provider.chatJSON(request, true)
	if err != nil {
		return nil, err
	}
	return provider.openSSE(ctx, body)
}

func (provider *Provider) chatJSON(request ChatRequest, stream bool) ([]byte, error) {
	method := "Chat"
	if stream {
		method = "ChatStream"
	}
	if provider.apiType != APITypeChatCompletions {
		return nil, provider.failure(fmt.Errorf("%s requires the Chat Completions API", method), protocol.ErrorUnsupported, protocol.CallInfo{})
	}
	if !stream && request.Stream {
		return nil, provider.failure(fmt.Errorf("Chat is non-streaming; use ChatStream for SSE"), protocol.ErrorInvalidRequest, protocol.CallInfo{})
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, provider.failure(fmt.Errorf("model is required"), protocol.ErrorInvalidRequest, protocol.CallInfo{})
	}
	if request.Messages == nil {
		return nil, provider.failure(fmt.Errorf("messages are required"), protocol.ErrorInvalidRequest, protocol.CallInfo{})
	}
	request.Stream = stream
	body, err := json.Marshal(request.Payload())
	if err != nil {
		return nil, provider.failure(err, protocol.ErrorInvalidRequest, protocol.CallInfo{})
	}
	return body, nil
}

func (provider *Provider) postJSON(ctx context.Context, body []byte, stream bool) ([]byte, protocol.CallInfo, error) {
	response, info, err := provider.send(ctx, body, stream)
	if err != nil {
		return nil, info, err
	}
	defer response.Body.Close()
	if err = provider.validateHTTPResponse(response, info); err != nil {
		return nil, info, err
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(response.Body, provider.maxResponseBytes+1))
	if err != nil {
		return nil, info, provider.failure(err, protocol.ErrorTransport, info)
	}
	if int64(len(bodyBytes)) > provider.maxResponseBytes {
		return nil, info, provider.failure(fmt.Errorf("response exceeds %d bytes", provider.maxResponseBytes), protocol.ErrorResponseTooLarge, info)
	}
	return bodyBytes, info, nil
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
	if strings.TrimSpace(request.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if request.MaxOutputTokens != nil && *request.MaxOutputTokens <= 0 {
		return nil, fmt.Errorf("max output tokens must be positive")
	}
	if request.Reasoning != nil && strings.TrimSpace(request.Reasoning.Effort) == "" {
		return nil, fmt.Errorf("reasoning effort is required")
	}
	if candidates, exists := request.ProviderOptions["n"]; exists {
		encoded, err := json.Marshal(candidates)
		var count int
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(encoded, &count); err != nil {
			return nil, fmt.Errorf("n must be an integer")
		}
		if count != 1 {
			return nil, &protocol.Error{Code: protocol.ErrorUnsupported, Message: "normalized calls return one candidate"}
		}
	}
	if provider.apiType == APITypeResponsesAPI {
		return buildResponsesRequest(request, stream)
	}
	return buildRequest(request, stream)
}

func positiveOrDefault(value, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}
