package agent

import (
	"context"
	"fmt"
	"io"
	"os"
)

// ToolEventLogger writes tool lifecycle events to an io.Writer.
// Tool logging should be centralized here (or in a custom EventHandler)
// instead of scattering logs across handlers or post-response loops.
type ToolEventLogger struct {
	Writer io.Writer
}

// StdoutToolEventHandler returns an EventHandler that logs tool start/finish to stdout.
func StdoutToolEventHandler() EventHandlerFunc {
	return ToolEventLogger{Writer: os.Stdout}.EventHandler()
}

// EventHandler returns the EventHandlerFunc used by RunOptions.EventHandler.
func (logger ToolEventLogger) EventHandler() EventHandlerFunc {
	return EventHandlerFunc{
		ToolCallStart:  logger.onToolCallStart,
		ToolCallFinish: logger.onToolCallFinish,
	}
}

func (logger ToolEventLogger) writer() io.Writer {
	if logger.Writer == nil {
		return os.Stdout
	}
	return logger.Writer
}

func (logger ToolEventLogger) onToolCallStart(_ context.Context, event ToolCallStartEvent) error {
	_, err := fmt.Fprintf(logger.writer(), "\ncalling tool: %s\n", event.ToolName)
	return err
}

func (logger ToolEventLogger) onToolCallFinish(_ context.Context, event ToolCallFinishEvent) error {
	if event.Error != nil {
		_, err := fmt.Fprintf(logger.writer(), "tool error: %s %v\n", event.ToolName, event.Error)
		return err
	}
	_, err := fmt.Fprintf(logger.writer(), "tool done: %s\n", event.ToolName)
	return err
}
