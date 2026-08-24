package goai

import (
	"context"
	"fmt"
	"time"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
	"github.com/zendev-sh/goai"
)

func buildToolEventOptions(ctx context.Context, handler agent.EventHandler, collector *toolCallCollector) []goai.Option {
	return []goai.Option{
		goai.WithOnToolCallStart(func(info goai.ToolCallStartInfo) {
			event := agent.ToolCallStartEvent{
				ToolCallID: info.ToolCallID,
				ToolName:   info.ToolName,
				Input:      copyRawMessage(info.Input),
				StartedAt:  time.Now(),
			}
			collector.recordStart(event)
			if handler != nil {
				collector.recordHandlerError("start", event.ToolName, callToolStartHandler(ctx, handler, event))
			}
		}),
		goai.WithOnToolCall(func(info goai.ToolCallInfo) {
			startedAt := info.StartTime
			finishedAt := time.Now()
			if !startedAt.IsZero() && info.Duration > 0 {
				finishedAt = startedAt.Add(info.Duration)
			}
			event := agent.ToolCallFinishEvent{
				ToolCallID: info.ToolCallID,
				ToolName:   info.ToolName,
				Output:     info.Output,
				Error:      info.Error,
				StartedAt:  startedAt,
				FinishedAt: finishedAt,
			}
			collector.recordFinish(event)
			if handler != nil {
				collector.recordHandlerError("finish", event.ToolName, callToolFinishHandler(ctx, handler, event))
			}
		}),
	}
}

func callToolStartHandler(ctx context.Context, handler agent.EventHandler, event agent.ToolCallStartEvent) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = recoveredHandlerError(recovered)
		}
	}()
	return handler.OnToolCallStart(ctx, event)
}

func callToolFinishHandler(ctx context.Context, handler agent.EventHandler, event agent.ToolCallFinishEvent) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = recoveredHandlerError(recovered)
		}
	}()
	return handler.OnToolCallFinish(ctx, event)
}

func recoveredHandlerError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return fmt.Errorf("panicked: %w", err)
	}
	return fmt.Errorf("panicked: %v", recovered)
}
