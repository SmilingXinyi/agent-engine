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
	if client == nil || client.provider == nil {
		return protocol.ModelResponse{}, &protocol.Error{Code: protocol.ErrorInvalidRequest, Message: "provider is required"}
	}
	requestInfo, err := client.prepare(ctx, request)
	if err != nil {
		return protocol.ModelResponse{}, err
	}

	startedAt := time.Now()
	response, err := client.provider.Generate(ctx, requestInfo.Request)
	hookErr := client.observeResponse(ctx, protocol.ResponseInfo{
		Provider:  requestInfo.Provider,
		Request:   requestInfo.Request,
		RequestID: requestInfo.RequestID,
		Response:  response,
		Duration:  time.Since(startedAt),
		Error:     err,
	})
	if hookErr != nil {
		err = errors.Join(err, hookErr)
	}
	return response, err
}

func (client *Client) prepare(ctx context.Context, request protocol.ModelRequest) (protocol.RequestInfo, error) {
	request = request.Clone()
	info := protocol.RequestInfo{
		Provider: client.provider.Name(), Request: request,
		RequestID: request.Metadata[protocol.RequestIDMetadataKey], Timestamp: time.Now(),
	}
	for index, hook := range client.hooks.OnRequest {
		if hook != nil {
			if err := hook(ctx, cloneRequestInfo(info)); err != nil {
				return protocol.RequestInfo{}, fmt.Errorf("request hook %d: %w", index, err)
			}
		}
	}
	return info, nil
}
