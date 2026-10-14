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

// Client invokes a Provider and owns the model-call lifecycle hooks. It has
// no loop and no tool execution: exactly one Provider call per Generate or
// Stream invocation.
type Client struct {
	provider protocol.Provider
	hooks    protocol.Hooks
}

// NewClient builds a Client bound to one Provider and its Hooks.
func NewClient(provider protocol.Provider, hooks protocol.Hooks) *Client {
	hooks.OnRequest = append([]protocol.RequestHook(nil), hooks.OnRequest...)
	hooks.OnResponse = append([]protocol.ResponseHook(nil), hooks.OnResponse...)
	return &Client{provider: provider, hooks: hooks}
}

// Generate performs one non-streaming model call.
func (client *Client) Generate(ctx context.Context, request protocol.ModelRequest) (protocol.ModelResponse, error) {
	return client.call(ctx, request, nil, false)
}

// Stream performs one model call, delivering incremental output to sink.
// It returns a protocol.Error with ErrorUnsupported when the provider has no stream.
func (client *Client) Stream(ctx context.Context, request protocol.ModelRequest, sink protocol.EventSink) (protocol.ModelResponse, error) {
	return client.call(ctx, request, sink, true)
}

func (client *Client) call(ctx context.Context, request protocol.ModelRequest, sink protocol.EventSink, streaming bool) (response protocol.ModelResponse, err error) {
	if client == nil || client.provider == nil {
		return protocol.ModelResponse{}, &protocol.Error{Code: protocol.ErrorInvalidRequest, Message: "provider is required"}
	}

	var streamingProvider protocol.StreamingProvider
	if streaming {
		var ok bool
		streamingProvider, ok = client.provider.(protocol.StreamingProvider)
		if !ok {
			return protocol.ModelResponse{}, &protocol.Error{Code: protocol.ErrorUnsupported, Message: "provider does not support streaming"}
		}
	}

	request = request.Clone()
	providerName := client.provider.Name()
	requestID := ""
	if request.Metadata != nil {
		requestID = request.Metadata[protocol.RequestIDMetadataKey]
	}
	requestInfo := protocol.RequestInfo{Provider: providerName, Request: request, RequestID: requestID, Timestamp: time.Now()}
	for index, hook := range client.hooks.OnRequest {
		if hook == nil {
			continue
		}
		if hookErr := hook(ctx, cloneRequestInfo(requestInfo)); hookErr != nil {
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

	responseInfo := protocol.ResponseInfo{
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
		if hookErr := hook(ctx, cloneResponseInfo(responseInfo)); hookErr != nil {
			err = errors.Join(err, fmt.Errorf("response hook %d: %w", index, hookErr))
		}
	}
	return response, err
}
