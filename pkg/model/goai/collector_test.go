package goai

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
)

func TestToolCallCollectorRecordsStartAndFinish(t *testing.T) {
	collector := newToolCallCollector()
	startedAt := time.Now()
	finishedAt := startedAt.Add(10 * time.Millisecond)
	collector.recordStart(agent.ToolCallStartEvent{ToolCallID: "call-1", ToolName: "read_file", Input: json.RawMessage(`{"path":"a"}`), StartedAt: startedAt})
	collector.recordFinish(agent.ToolCallFinishEvent{ToolCallID: "call-1", ToolName: "read_file", Output: "ok", StartedAt: startedAt, FinishedAt: finishedAt})
	records := collector.recordsSnapshot()
	if len(records) != 1 {
		t.Fatalf("expected one record, got %d", len(records))
	}
	record := records[0]
	if record.ToolCallID != "call-1" || record.ToolName != "read_file" || record.Output != "ok" {
		t.Fatalf("unexpected record: %+v", record)
	}
	if string(record.Input) != `{"path":"a"}` {
		t.Fatalf("unexpected input: %s", record.Input)
	}
	if !record.StartedAt.Equal(startedAt) || !record.FinishedAt.Equal(finishedAt) {
		t.Fatalf("unexpected timestamps: %+v", record)
	}
}

func TestToolCallCollectorRecordsFinishWithoutStart(t *testing.T) {
	collector := newToolCallCollector()
	finishedAt := time.Now()
	collector.recordFinish(agent.ToolCallFinishEvent{ToolCallID: "call-1", ToolName: "read_file", Output: "ok", FinishedAt: finishedAt})
	records := collector.recordsSnapshot()
	if len(records) != 1 {
		t.Fatalf("expected one record, got %d", len(records))
	}
	record := records[0]
	if record.ToolCallID != "call-1" || record.ToolName != "read_file" || record.Output != "ok" {
		t.Fatalf("unexpected record: %+v", record)
	}
	if record.Input != nil {
		t.Fatalf("expected empty input, got %s", record.Input)
	}
	if record.StartedAt.IsZero() || !record.FinishedAt.Equal(finishedAt) {
		t.Fatalf("unexpected timestamps: %+v", record)
	}
}

func TestToolCallCollectorRecordsErrorAndReturnsCopy(t *testing.T) {
	collector := newToolCallCollector()
	collector.recordStart(agent.ToolCallStartEvent{ToolCallID: "call-1", ToolName: "read_file", Input: json.RawMessage(`{"path":"a"}`)})
	collector.recordFinish(agent.ToolCallFinishEvent{ToolCallID: "call-1", ToolName: "read_file", Error: errors.New("failed")})
	records := collector.recordsSnapshot()
	if records[0].Error != "failed" {
		t.Fatalf("expected error text, got %q", records[0].Error)
	}
	records[0].Input[0] = '{'
	secondSnapshot := collector.recordsSnapshot()
	if string(secondSnapshot[0].Input) != `{"path":"a"}` {
		t.Fatalf("expected snapshot copy, got %s", secondSnapshot[0].Input)
	}
}

func TestToolCallCollectorConcurrentUse(t *testing.T) {
	collector := newToolCallCollector()
	var waitGroup sync.WaitGroup
	for index := 0; index < 20; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			collector.recordStart(agent.ToolCallStartEvent{ToolCallID: "same", ToolName: "tool"})
			collector.recordFinish(agent.ToolCallFinishEvent{ToolCallID: "same", ToolName: "tool", Output: "ok"})
		}()
	}
	waitGroup.Wait()
	if len(collector.recordsSnapshot()) == 0 {
		t.Fatal("expected records")
	}
}
