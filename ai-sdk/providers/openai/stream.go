package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

// Stream validates the HTTP response without reading its successful body. Next
// delivers original SSE frames without normalizing or accumulating model output.
func (provider *Provider) Stream(ctx context.Context, request protocol.ModelRequest) (protocol.Stream, error) {
	payload, err := provider.buildRequest(request, true)
	if err != nil {
		return nil, provider.failure(err, protocol.ErrorInvalidRequest, protocol.CallInfo{})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, provider.failure(err, protocol.ErrorInvalidRequest, protocol.CallInfo{})
	}
	return provider.openSSE(ctx, encoded)
}

// openSSE sends one streaming body and returns the frame reader. The body is
// not read until Next.
func (provider *Provider) openSSE(ctx context.Context, body []byte) (protocol.Stream, error) {
	streamContext, cancel := context.WithCancel(ctx)
	response, info, err := provider.send(streamContext, body, true)
	if err != nil {
		cancel()
		return nil, err
	}
	if err = provider.validateHTTPResponse(response, info); err != nil {
		cancel()
		return nil, errors.Join(err, response.Body.Close())
	}
	return &responseStream{
		provider: provider, ctx: streamContext, cancel: cancel, body: response.Body,
		reader: sseReader{reader: bufio.NewReader(response.Body), maxFrame: provider.maxEventBytes, maxTotal: provider.maxStreamBytes, first: true},
		info:   protocol.StreamInfo{CallInfo: info},
	}, nil
}

// Only Next touches the reader. The mutex protects publication and terminal
// state, never a blocking Read, so Close can interrupt Next without a goroutine.
type responseStream struct {
	provider        *Provider
	ctx             context.Context
	cancel          context.CancelFunc
	body            io.ReadCloser
	reader          sseReader
	mu              sync.Mutex
	chunk           protocol.StreamChunk
	info            protocol.StreamInfo
	err             error
	hasFinishReason bool
	closeOnce       sync.Once
	closeErr        error
}

func (stream *responseStream) Next() bool {
	stream.mu.Lock()
	stream.chunk = protocol.StreamChunk{}
	ended := stream.endedLocked()
	stream.mu.Unlock()
	var chunk protocol.StreamChunk
	var readErr error
	if !ended {
		chunk, readErr = stream.reader.next(stream.ctx)
	}

	hasChunk := false
	stream.mu.Lock()
	if !stream.endedLocked() {
		if stream.ctx.Err() != nil {
			readErr = stream.ctx.Err()
		}
		if readErr != nil {
			stream.endLocked(false, readErr)
		} else {
			stream.chunk, hasChunk = chunk, true
			if terminal, complete, err := stream.inspect(chunk); terminal {
				stream.endLocked(complete, err)
			}
		}
	}
	ended = stream.endedLocked()
	stream.mu.Unlock()
	if ended {
		_ = stream.closeBody()
	}
	return hasChunk
}

func (stream *responseStream) Chunk() protocol.StreamChunk {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.chunk
}

func (stream *responseStream) Info() protocol.StreamInfo {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.info.Clone()
}

func (stream *responseStream) Err() error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.err
}

// Close returns body-close errors. Early-close cancellation is reported by Err.
func (stream *responseStream) Close() error {
	stream.mu.Lock()
	stream.endLocked(false, context.Canceled)
	stream.mu.Unlock()
	return stream.closeBody()
}

// endLocked is the single state transition for EOF, terminal frames, failures
// and cancellation. The caller holds mu; body closing happens outside the lock.
func (stream *responseStream) endLocked(complete bool, err error) {
	if stream.endedLocked() {
		return
	}
	if err == io.EOF {
		complete = stream.provider.apiType == APITypeChatCompletions && stream.hasFinishReason && !stream.provider.requireDone
		err = nil
		if !complete {
			err = &protocol.Error{Code: protocol.ErrorStreamIncomplete, Message: "stream ended without its protocol terminal condition"}
		}
	}
	stream.info.Complete = complete
	if err != nil {
		stream.err = stream.failure(err)
	}
}

// Terminal state is already represented by completion or failure; it does not
// need a separate mutable flag. The caller holds mu.
func (stream *responseStream) endedLocked() bool {
	return stream.info.Complete || stream.err != nil
}

func (stream *responseStream) closeBody() error {
	stream.closeOnce.Do(func() {
		stream.cancel()
		stream.closeErr = stream.body.Close()
		if stream.closeErr != nil {
			stream.mu.Lock()
			stream.err = errors.Join(stream.err, stream.failure(stream.closeErr))
			stream.mu.Unlock()
		}
	})
	return stream.closeErr
}

func (stream *responseStream) failure(err error) error {
	return stream.provider.failure(err, protocol.ErrorTransport, stream.info.CallInfo)
}

// inspect reads only termination/error markers. Arbitrary and non-JSON payloads
// remain visible. Model content is not decoded or schema-validated. Inspection
// precedes publication, so caller mutation cannot change protocol state.
func (stream *responseStream) inspect(chunk protocol.StreamChunk) (terminal, complete bool, err error) {
	if bytes.Equal(bytes.TrimSpace(chunk.Data), []byte("[DONE]")) {
		if stream.provider.apiType == APITypeChatCompletions {
			return true, true, nil
		}
		return true, false, &protocol.Error{Code: protocol.ErrorStreamIncomplete, Message: "Responses stream ended without a terminal response event"}
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(chunk.Data, &fields)
	event := chunk.Event
	if event == "" {
		_ = json.Unmarshal(fields["type"], &event)
	}
	failed := event == "error" || (stream.provider.apiType == APITypeResponsesAPI && event == "response.failed")
	// Raw fields already came from valid JSON; an object needs no second decode.
	if rawError := bytes.TrimSpace(fields["error"]); len(rawError) > 0 && rawError[0] == '{' {
		failed = true
	}
	if failed {
		message := "upstream stream failed"
		var response struct {
			Error json.RawMessage `json:"error"`
		}
		_ = json.Unmarshal(fields["response"], &response)
		for _, raw := range []json.RawMessage{response.Error, fields["error"]} {
			var detail struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(raw, &detail) == nil && detail.Message != "" {
				message = detail.Message
			}
		}
		return true, false, &protocol.Error{Code: protocol.ErrorUpstream, Message: message, Body: bytes.Clone(chunk.Data)}
	}
	if stream.provider.apiType == APITypeResponsesAPI {
		if event == "response.completed" || event == "response.incomplete" {
			return true, true, nil
		}
	} else {
		var choices []struct {
			FinishReason *string `json:"finish_reason"`
		}
		_ = json.Unmarshal(fields["choices"], &choices)
		for _, choice := range choices {
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				if *choice.FinishReason == "error" {
					return true, false, &protocol.Error{Code: protocol.ErrorUpstream, Message: "upstream reported a failed completion", Body: bytes.Clone(chunk.Data)}
				}
				stream.hasFinishReason = true
			}
		}
	}
	return false, false, nil
}
