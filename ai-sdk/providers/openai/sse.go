package openai

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

var errSSEComplete = errors.New("SSE complete")

// readSSE bounds an entire event (not just one line) and keeps exact framing.
// It accepts a final unterminated frame at clean EOF for compatible services.
func (provider *Provider) readSSE(ctx context.Context, body io.Reader, consume func(RawEvent) error) error {
	reader := bufio.NewReader(body)
	var frame []byte
	var total int64
	dispatch := func() error {
		if len(frame) == 0 {
			return nil
		}
		event := parseSSEFrame(frame)
		frame = nil
		if err := ctx.Err(); err != nil {
			return err
		}
		if provider.onRawEvent != nil {
			observed := event
			observed.Frame = bytes.Clone(event.Frame)
			observed.Data = bytes.Clone(event.Data)
			if err := provider.onRawEvent(ctx, observed); err != nil {
				return &protocol.Error{Code: protocol.ErrorSink, Message: "raw event callback: " + err.Error(), Cause: err}
			}
		}
		return consume(event)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var line []byte
		var readErr error
		for {
			fragment, err := reader.ReadSlice('\n')
			total += int64(len(fragment))
			if provider.maxStreamBytes > 0 && total > provider.maxStreamBytes {
				return &protocol.Error{Code: protocol.ErrorResponseTooLarge, Message: fmt.Sprintf("stream exceeds %d bytes", provider.maxStreamBytes)}
			}
			if int64(len(frame)+len(line)+len(fragment)) > provider.maxEventBytes {
				return &protocol.Error{Code: protocol.ErrorResponseTooLarge, Message: fmt.Sprintf("SSE event exceeds %d bytes", provider.maxEventBytes)}
			}
			line = append(line, fragment...)
			if err == bufio.ErrBufferFull {
				continue
			}
			readErr = err
			break
		}
		frame = append(frame, line...)
		if bytes.Equal(line, []byte("\n")) || bytes.Equal(line, []byte("\r\n")) {
			if err := dispatch(); err != nil {
				if errors.Is(err, errSSEComplete) {
					return nil
				}
				return err
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return readErr
			}
			if err := dispatch(); err != nil && !errors.Is(err, errSSEComplete) {
				return err
			}
			return nil
		}
	}
}

func parseSSEFrame(frame []byte) RawEvent {
	event := RawEvent{Frame: frame}
	var data [][]byte
	for _, rawLine := range bytes.Split(frame, []byte("\n")) {
		line := bytes.TrimSuffix(rawLine, []byte("\r"))
		if len(line) == 0 || line[0] == ':' {
			continue
		}
		field, value, found := bytes.Cut(line, []byte(":"))
		if !found {
			value = nil
		}
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "event":
			event.Name = string(value)
		case "id":
			if !bytes.ContainsRune(value, '\x00') {
				event.ID = string(value)
			}
		case "data":
			data = append(data, value)
		}
	}
	if len(data) > 0 {
		event.Data = bytes.Join(data, []byte("\n"))
	}
	return event
}

func emitEvent(ctx context.Context, sink protocol.EventSink, event protocol.StreamEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sink == nil {
		return nil
	}
	if err := sink.OnEvent(ctx, event); err != nil {
		return &protocol.Error{Code: protocol.ErrorSink, Message: "stream event callback: " + err.Error(), Cause: err}
	}
	return nil
}
