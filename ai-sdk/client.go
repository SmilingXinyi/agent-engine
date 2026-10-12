// Package ai runs one provider call and the hooks around it.
// The model-call contract lives in package protocol.
package ai

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

// ErrStreamingNotSupported is returned when Stream is called on a Provider
// whose upstream API has no incremental output.
var ErrStreamingNotSupported = errors.New("ai-sdk provider does not support streaming")

// Client invokes a Provider and owns the model-call lifecycle hooks. It has
// no loop and no tool execution: exactly one Provider call per Generate or
// Stream invocation.
type Client struct {
	provider protocol.Provider
	hooks    Hooks
}

// NewClient builds a Client bound to one Provider and its Hooks.
func NewClient(provider protocol.Provider, hooks Hooks) *Client {
	return &Client{provider: provider, hooks: hooks}
}

// Generate performs one non-streaming model call.
func (client *Client) Generate(ctx context.Context, request protocol.ModelRequest) (protocol.ModelResponse, error) {
	return client.call(ctx, request, nil, false)
}

// Stream performs one model call, delivering incremental output to sink.
// It returns ErrStreamingNotSupported when the Provider does not implement StreamingProvider.
func (client *Client) Stream(ctx context.Context, request protocol.ModelRequest, sink protocol.EventSink) (protocol.ModelResponse, error) {
	return client.call(ctx, request, sink, true)
}

func (client *Client) call(ctx context.Context, request protocol.ModelRequest, sink protocol.EventSink, streaming bool) (response protocol.ModelResponse, err error) {
	if client == nil || client.provider == nil {
		return protocol.ModelResponse{}, errors.New("ai-sdk provider is required")
	}

	var streamingProvider protocol.StreamingProvider
	if streaming {
		var ok bool
		streamingProvider, ok = client.provider.(protocol.StreamingProvider)
		if !ok {
			return protocol.ModelResponse{}, ErrStreamingNotSupported
		}
	}

	providerName := client.provider.Name()
	requestID := ""
	if request.Metadata != nil {
		requestID = request.Metadata[RequestIDMetadataKey]
	}
	requestInfo := RequestInfo{Provider: providerName, Request: request, RequestID: requestID, Timestamp: time.Now()}
	for index, hook := range client.hooks.OnRequest {
		if hook == nil {
			continue
		}
		if hookErr := hook(ctx, requestInfo); hookErr != nil {
			return protocol.ModelResponse{}, fmt.Errorf("request hook %d: %w", index, hookErr)
		}
	}

	startedAt := time.Now()
	if streaming {
		response, err = streamingProvider.Stream(ctx, request, sink)
	} else {
		response, err = client.provider.Generate(ctx, request)
	}
	finishedAt := time.Now()

	responseInfo := ResponseInfo{
		Provider:  providerName,
		Request:   request,
		RequestID: requestID,
		Response:  response,
		Duration:  finishedAt.Sub(startedAt),
		Error:     err,
	}
	for index, hook := range client.hooks.OnResponse {
		if hook == nil {
			continue
		}
		if hookErr := hook(ctx, responseInfo); hookErr != nil {
			err = errors.Join(err, fmt.Errorf("response hook %d: %w", index, hookErr))
		}
	}
	return response, err
}
