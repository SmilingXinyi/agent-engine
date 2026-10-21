package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

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
	timeouts, err := provider.streamTimeouts(request.FirstFrameTimeout, request.IdleTimeout)
	if err != nil {
		return nil, err
	}
	return provider.openSSE(ctx, encoded, timeouts)
}

// streamTimeouts resolves one call's timers. A nil override inherits the provider
// default. Zero disables that timer. Negative values are rejected.
func (provider *Provider) streamTimeouts(firstFrame, idle *time.Duration) (streamTimeouts, error) {
	resolvedFirst, err := resolveStreamTimeout(firstFrame, provider.firstFrameTimeout)
	if err != nil {
		return streamTimeouts{}, provider.failure(err, protocol.ErrorInvalidRequest, protocol.CallInfo{})
	}
	resolvedIdle, err := resolveStreamTimeout(idle, provider.idleTimeout)
	if err != nil {
		return streamTimeouts{}, provider.failure(err, protocol.ErrorInvalidRequest, protocol.CallInfo{})
	}
	return streamTimeouts{firstFrame: resolvedFirst, idle: resolvedIdle}, nil
}

func resolveStreamTimeout(override *time.Duration, fallback time.Duration) (time.Duration, error) {
	value := fallback
	if override != nil {
		value = *override
	}
	if value < 0 {
		return 0, fmt.Errorf("stream timeout must be nonnegative")
	}
	return value, nil
}

type streamTimeouts struct {
	firstFrame time.Duration
	idle       time.Duration
}

// openSSE sends one streaming body and returns the frame reader. The body is
// not read until Next.
func (provider *Provider) openSSE(ctx context.Context, body []byte, timeouts streamTimeouts) (protocol.Stream, error) {
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
		provider: provider, caller: ctx, ctx: streamContext, cancel: cancel, body: response.Body,
		reader:            sseReader{reader: bufio.NewReader(response.Body), maxFrame: provider.maxEventBytes, maxTotal: provider.maxStreamBytes, first: true},
		info:              protocol.StreamInfo{CallInfo: info},
		firstFrameTimeout: timeouts.firstFrame,
		idleTimeout:       timeouts.idle,
	}, nil
}

// Only Next touches the reader. The mutex protects publication and terminal
// state, never a blocking Read, so Close can interrupt Next without holding it
// across the read. A timer goroutine may cancel that read.
type responseStream struct {
	provider           *Provider
	caller             context.Context
	ctx                context.Context
	cancel             context.CancelFunc
	body               io.ReadCloser
	reader             sseReader
	mu                 sync.Mutex
	chunk              protocol.StreamChunk
	info               protocol.StreamInfo
	err                error
	hasFinishReason    bool
	closeOnce          sync.Once
	closeErr           error
	firstFrameTimeout  time.Duration
	idleTimeout        time.Duration
	firstFrameDeadline time.Time
	sawData            bool
	readID             int
	readWaiting        bool
	timeoutKind        protocol.TimeoutKind
}

func (stream *responseStream) Next() bool {
	stream.mu.Lock()
	stream.chunk = protocol.StreamChunk{}
	ended := stream.endedLocked()
	limit, kind := stream.readTimeoutLocked()
	stream.mu.Unlock()

	var chunk protocol.StreamChunk
	var readErr error
	if !ended && limit == 0 && kind == protocol.TimeoutFirstFrame {
		readErr = stream.timeoutError(kind)
	} else if !ended {
		stop := stream.watch(limit, kind)
		chunk, readErr = stream.reader.next(stream.ctx)
		stop()
	}

	hasChunk := false
	stream.mu.Lock()
	if !stream.endedLocked() {
		readErr = stream.classifyReadErrorLocked(readErr)
		if readErr != nil {
			stream.endLocked(false, readErr)
		} else {
			stream.chunk, hasChunk = chunk, true
			if len(bytes.TrimSpace(chunk.Data)) > 0 {
				stream.sawData = true
			}
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

// readTimeoutLocked reports the timer for the read that is about to start.
// A zero limit with an empty kind means the timer is disabled. A zero limit
// with TimeoutFirstFrame means the first-frame deadline has already passed.
// The caller holds mu.
func (stream *responseStream) readTimeoutLocked() (time.Duration, protocol.TimeoutKind) {
	if stream.sawData {
		if stream.idleTimeout <= 0 {
			return 0, ""
		}
		return stream.idleTimeout, protocol.TimeoutIdle
	}
	if stream.firstFrameTimeout <= 0 {
		return 0, ""
	}
	if stream.firstFrameDeadline.IsZero() {
		stream.firstFrameDeadline = time.Now().Add(stream.firstFrameTimeout)
	}
	remaining := time.Until(stream.firstFrameDeadline)
	if remaining <= 0 {
		return 0, protocol.TimeoutFirstFrame
	}
	return remaining, protocol.TimeoutFirstFrame
}

// watch cancels the stream read when limit elapses. A non-positive limit does
// not start a timer. stop invalidates the callback so a frame that arrived in
// time is not turned into a timeout.
func (stream *responseStream) watch(limit time.Duration, kind protocol.TimeoutKind) func() {
	if limit <= 0 || kind == "" {
		return func() {}
	}
	stream.mu.Lock()
	stream.readID++
	readID := stream.readID
	stream.readWaiting = true
	stream.mu.Unlock()
	timer := time.AfterFunc(limit, func() {
		stream.mu.Lock()
		if stream.readID != readID || !stream.readWaiting || stream.endedLocked() {
			stream.mu.Unlock()
			return
		}
		stream.readWaiting = false
		stream.timeoutKind = kind
		stream.mu.Unlock()
		stream.cancel()
	})
	return func() {
		timer.Stop()
		stream.mu.Lock()
		if stream.readID == readID {
			stream.readWaiting = false
		}
		stream.mu.Unlock()
	}
}

// classifyReadErrorLocked prefers the caller's context over a stream timer.
// A frame that was read completely is kept; the caller passes a nil read error
// only when this returns nil. The caller holds mu.
func (stream *responseStream) classifyReadErrorLocked(readErr error) error {
	if stream.caller.Err() != nil {
		return stream.caller.Err()
	}
	if readErr == nil {
		return nil
	}
	if stream.timeoutKind != "" {
		return stream.timeoutError(stream.timeoutKind)
	}
	if stream.ctx.Err() != nil {
		return stream.ctx.Err()
	}
	return readErr
}

func (stream *responseStream) timeoutError(kind protocol.TimeoutKind) error {
	message := "timed out waiting for the next SSE frame"
	if kind == protocol.TimeoutFirstFrame {
		message = "timed out waiting for the first SSE data frame"
	}
	return &protocol.Error{
		Code:        protocol.ErrorTimeout,
		Message:     message,
		Retryable:   kind == protocol.TimeoutFirstFrame,
		TimeoutKind: kind,
	}
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
