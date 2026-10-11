package aisdk

import (
	"context"
	"errors"
)

var errToolCallPartMissingData = errors.New("tool call part is missing call data")

// EmitModelResponse synthesizes StreamEvents from a complete ModelResponse.
// Providers without native incremental output can call this from their
// Stream method after obtaining a result via Generate.
func EmitModelResponse(ctx context.Context, sink EventSink, response ModelResponse) error {
	if sink == nil {
		return nil
	}
	toolCallIndex := 0
	for _, part := range response.Message.Parts {
		var event StreamEvent
		switch part.Type {
		case ContentText:
			event = StreamEvent{Type: EventTextDelta, Delta: part.Text}
		case ContentReasoning:
			event = StreamEvent{Type: EventReasoningDelta, Delta: part.Text}
		case ContentToolCall:
			if part.ToolCall == nil {
				return errToolCallPartMissingData
			}
			event = StreamEvent{
				Type: EventToolCallDelta,
				ToolCallDelta: &ToolCallDelta{
					Index:          toolCallIndex,
					ID:             part.ToolCall.ID,
					Name:           part.ToolCall.Name,
					ArgumentsDelta: string(part.ToolCall.Arguments),
				},
			}
			toolCallIndex++
		default:
			continue
		}
		if err := callEventSink(ctx, sink, event); err != nil {
			return err
		}
	}
	usage := response.Usage
	if err := callEventSink(ctx, sink, StreamEvent{Type: EventUsage, Usage: &usage}); err != nil {
		return err
	}
	return callEventSink(ctx, sink, StreamEvent{Type: EventFinish, FinishReason: response.FinishReason})
}
