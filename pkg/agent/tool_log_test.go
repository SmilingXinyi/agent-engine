package agent

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestToolEventLoggerLogsStartAndSuccessfulFinish(t *testing.T) {
	var output bytes.Buffer
	handler := ToolEventLogger{Writer: &output}.EventHandler()

	if err := handler.OnToolCallStart(context.Background(), ToolCallStartEvent{ToolName: "get_datetime"}); err != nil {
		t.Fatalf("on start: %v", err)
	}
	if err := handler.OnToolCallFinish(context.Background(), ToolCallFinishEvent{ToolName: "get_datetime", Output: "20260610120000"}); err != nil {
		t.Fatalf("on finish: %v", err)
	}

	got := output.String()
	if !strings.Contains(got, "calling tool: get_datetime") {
		t.Fatalf("expected start log, got %q", got)
	}
	if !strings.Contains(got, "tool done: get_datetime") {
		t.Fatalf("expected done log, got %q", got)
	}
	if strings.Contains(got, "tool error") {
		t.Fatalf("did not expect error log, got %q", got)
	}
}

func TestToolEventLoggerLogsFinishError(t *testing.T) {
	var output bytes.Buffer
	handler := ToolEventLogger{Writer: &output}.EventHandler()

	if err := handler.OnToolCallFinish(context.Background(), ToolCallFinishEvent{
		ToolName: "read_file",
		Error:    errors.New("permission denied"),
	}); err != nil {
		t.Fatalf("on finish: %v", err)
	}

	got := output.String()
	if !strings.Contains(got, "tool error: read_file permission denied") {
		t.Fatalf("expected error log, got %q", got)
	}
	if strings.Contains(got, "tool done") {
		t.Fatalf("did not expect done log, got %q", got)
	}
}

func TestStdoutToolEventHandlerReturnsUsableHandler(t *testing.T) {
	handler := StdoutToolEventHandler()
	if handler.ToolCallStart == nil || handler.ToolCallFinish == nil {
		t.Fatal("expected non-nil tool event callbacks")
	}
}
