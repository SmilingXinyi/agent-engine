package goai

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
)

type toolCallCollector struct {
	mutex   sync.Mutex
	starts  map[string]agent.ToolCallStartEvent
	records []agent.ToolCallRecord
}

func newToolCallCollector() *toolCallCollector {
	return &toolCallCollector{
		starts: make(map[string]agent.ToolCallStartEvent),
	}
}

func (collector *toolCallCollector) recordStart(event agent.ToolCallStartEvent) {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	if event.StartedAt.IsZero() {
		event.StartedAt = time.Now()
	}
	event.Input = copyRawMessage(event.Input)
	collector.starts[event.ToolCallID] = event
}

func (collector *toolCallCollector) recordFinish(event agent.ToolCallFinishEvent) {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	start := collector.starts[event.ToolCallID]
	startedAt := event.StartedAt
	if startedAt.IsZero() {
		startedAt = start.StartedAt
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	finishedAt := event.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = time.Now()
	}
	errorText := ""
	if event.Error != nil {
		errorText = event.Error.Error()
	}
	collector.records = append(collector.records, agent.ToolCallRecord{
		ToolCallID: event.ToolCallID,
		ToolName:   firstNonEmpty(event.ToolName, start.ToolName),
		Input:      copyRawMessage(start.Input),
		Output:     event.Output,
		Error:      errorText,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	})
	delete(collector.starts, event.ToolCallID)
}

func (collector *toolCallCollector) recordsSnapshot() []agent.ToolCallRecord {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	result := make([]agent.ToolCallRecord, len(collector.records))
	for index, record := range collector.records {
		record.Input = copyRawMessage(record.Input)
		result[index] = record
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func copyRawMessage(input json.RawMessage) json.RawMessage {
	if input == nil {
		return nil
	}
	return append(json.RawMessage(nil), input...)
}
