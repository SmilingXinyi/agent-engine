package ai

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

// Stream opens one upstream stream. Always defer Close, then iterate with Next
// and inspect Err. Response hooks run once when iteration ends or Close returns,
// not when this method returns a successfully opened reader.
func (client *Client) Stream(ctx context.Context, request protocol.ModelRequest) (protocol.Stream, error) {
	if client == nil || client.provider == nil {
		return nil, &protocol.Error{Code: protocol.ErrorInvalidRequest, Message: "provider is required"}
	}
	provider, ok := client.provider.(protocol.StreamingProvider)
	if !ok {
		return nil, &protocol.Error{Code: protocol.ErrorUnsupported, Message: "provider does not support streaming"}
	}
	requestInfo, err := client.prepare(ctx, request)
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	upstream, err := provider.Stream(ctx, requestInfo.Request)
	if err == nil && upstream == nil {
		err = &protocol.Error{Code: protocol.ErrorInvalidRequest, Provider: requestInfo.Provider, Message: "provider returned a nil stream"}
	}
	if err != nil {
		info := protocol.StreamInfo{}
		if upstream != nil {
			err = errors.Join(err, upstream.Close())
			info = upstream.Info()
		} else {
			var failure *protocol.Error
			if errors.As(err, &failure) {
				info.CallInfo = failure.CallInfo
			}
		}
		hookErr := client.observeResponse(ctx, protocol.ResponseInfo{
			Provider: requestInfo.Provider, Request: requestInfo.Request,
			RequestID: requestInfo.RequestID, Stream: &info, Duration: time.Since(startedAt), Error: err,
		})
		return nil, errors.Join(err, hookErr)
	}
	return &streamCall{
		upstream: upstream, client: client, ctx: ctx, requestInfo: requestInfo, startedAt: startedAt,
	}, nil
}

// streamCall wraps an upstream reader with the Client's response hooks and
// call timing. Response hooks run once, including when Close interrupts Next.
type streamCall struct {
	upstream          protocol.Stream
	client            *Client
	ctx               context.Context
	requestInfo       protocol.RequestInfo
	startedAt         time.Time
	responseHooksOnce sync.Once
	hookErrMu         sync.Mutex
	hookErr           error
}

func (call *streamCall) Next() bool {
	if call.upstream.Next() {
		return true
	}
	call.runResponseHooks()
	return false
}

func (call *streamCall) Chunk() protocol.StreamChunk { return call.upstream.Chunk() }
func (call *streamCall) Info() protocol.StreamInfo   { return call.upstream.Info() }

func (call *streamCall) Err() error {
	return errors.Join(call.upstream.Err(), call.responseHookError())
}

func (call *streamCall) Close() error {
	err := call.upstream.Close()
	return errors.Join(err, call.runResponseHooks())
}

func (call *streamCall) runResponseHooks() error {
	call.responseHooksOnce.Do(func() {
		streamInfo := call.upstream.Info()
		err := call.client.observeResponse(call.ctx, protocol.ResponseInfo{
			Provider: call.requestInfo.Provider, Request: call.requestInfo.Request,
			RequestID: call.requestInfo.RequestID, Stream: &streamInfo,
			Duration: time.Since(call.startedAt), Error: call.upstream.Err(),
		})
		call.hookErrMu.Lock()
		call.hookErr = err
		call.hookErrMu.Unlock()
	})
	return call.responseHookError()
}

func (call *streamCall) responseHookError() error {
	call.hookErrMu.Lock()
	defer call.hookErrMu.Unlock()
	return call.hookErr
}
