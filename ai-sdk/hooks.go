package ai

import (
	"context"
	"errors"
	"fmt"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

func cloneRequestInfo(info protocol.RequestInfo) protocol.RequestInfo {
	info.Request = info.Request.Clone()
	return info
}

func cloneResponseInfo(info protocol.ResponseInfo) protocol.ResponseInfo {
	info.Request = info.Request.Clone()
	info.Response = info.Response.Clone()
	if info.Stream != nil {
		snapshot := info.Stream.Clone()
		info.Stream = &snapshot
	}
	return info
}

func (client *Client) observeResponse(ctx context.Context, info protocol.ResponseInfo) (err error) {
	for index, hook := range client.hooks.OnResponse {
		if hook != nil {
			if hookErr := hook(ctx, cloneResponseInfo(info)); hookErr != nil {
				err = errors.Join(err, fmt.Errorf("response hook %d: %w", index, hookErr))
			}
		}
	}
	return err
}
