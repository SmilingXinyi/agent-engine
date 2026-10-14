package openai

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

// sseReader owns only the current frame and a bounded read buffer. Transport
// reads may contain many frames; next still returns exactly one of them.
type sseReader struct {
	reader   *bufio.Reader
	maxFrame int64
	maxTotal int64
	total    int64
	first    bool
}

func (reader *sseReader) next(ctx context.Context) (protocol.StreamChunk, error) {
	var frame []byte
	lineStart := 0
	appendByte := func(value byte) error {
		reader.total++
		if reader.maxTotal > 0 && reader.total > reader.maxTotal {
			return &protocol.Error{Code: protocol.ErrorResponseTooLarge, Message: fmt.Sprintf("stream exceeds %d bytes", reader.maxTotal)}
		}
		if int64(len(frame))+1 > reader.maxFrame {
			return &protocol.Error{Code: protocol.ErrorResponseTooLarge, Message: fmt.Sprintf("SSE frame exceeds %d bytes", reader.maxFrame)}
		}
		frame = append(frame, value)
		return nil
	}
	for {
		if reader.reader.Buffered() == 0 {
			if err := ctx.Err(); err != nil {
				return protocol.StreamChunk{}, err
			}
		}
		value, err := reader.reader.ReadByte()
		if err != nil {
			if err == io.EOF && len(frame) > 0 {
				return reader.chunk(frame), nil
			}
			return protocol.StreamChunk{}, err
		}
		blankLine := len(frame) == lineStart || (reader.first && lineStart == 0 && bytes.Equal(frame, []byte{0xef, 0xbb, 0xbf}))
		if err := appendByte(value); err != nil {
			return protocol.StreamChunk{}, err
		}
		if value != '\r' && value != '\n' {
			continue
		}
		if value == '\r' {
			// A CRLF split across HTTP reads still belongs to this frame.
			if next, _ := reader.reader.Peek(1); len(next) == 1 && next[0] == '\n' {
				_, _ = reader.reader.ReadByte()
				if err := appendByte('\n'); err != nil {
					return protocol.StreamChunk{}, err
				}
			}
		}
		if blankLine {
			return reader.chunk(frame), nil
		}
		lineStart = len(frame)
	}
}

func (reader *sseReader) chunk(frame []byte) protocol.StreamChunk {
	chunk := protocol.StreamChunk{Frame: frame}
	remaining := frame
	if reader.first {
		remaining = bytes.TrimPrefix(remaining, []byte{0xef, 0xbb, 0xbf})
		reader.first = false
	}
	var data [][]byte
	for len(remaining) > 0 {
		end := bytes.IndexAny(remaining, "\r\n")
		line := remaining
		if end < 0 {
			remaining = nil
		} else {
			line, remaining = remaining[:end], remaining[end:]
			if bytes.HasPrefix(remaining, []byte("\r\n")) {
				remaining = remaining[2:]
			} else {
				remaining = remaining[1:]
			}
		}
		if len(line) == 0 || line[0] == ':' {
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "event":
			chunk.Event = string(value)
		case "id":
			if !bytes.ContainsRune(value, '\x00') {
				chunk.ID = string(value)
			}
		case "data":
			data = append(data, value)
		}
	}
	if len(data) > 0 {
		chunk.Data = bytes.Join(data, []byte("\n"))
	}
	return chunk
}
