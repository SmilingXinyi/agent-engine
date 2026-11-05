package workspace

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/workspace/protocol"
)

type pendingCall struct {
	call    aiprot.ToolCall
	started bool
}

type history struct {
	mu       sync.Mutex
	file     *os.File
	sequence uint64
	messages []aiprot.Message
	active   string
	pending  map[string]*pendingCall
	order    []string
	poison   error
}

func openHistory(path string, warn func(string)) (_ *history, err error) {
	if err := regularOrMissing(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = file.Close()
		}
	}()
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	history := &history{file: file, pending: make(map[string]*pendingCall)}
	reader := bufio.NewReader(file)
	var offset int64
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr == io.EOF {
			if len(line) > 0 {
				if err := file.Truncate(offset); err != nil {
					return nil, err
				}
				if err := file.Sync(); err != nil {
					return nil, err
				}
				if warn != nil {
					warn("Discarded an uncommitted trailing history fragment.")
				}
			}
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		var event protocol.Event
		if err := protocol.DecodeJSON(line, &event); err != nil {
			return nil, fmt.Errorf("invalid history at byte %d: %w", offset, err)
		}
		if err := history.validate(event); err != nil {
			return nil, fmt.Errorf("invalid history at byte %d: %w", offset, err)
		}
		history.apply(event)
		offset += int64(len(line))
	}
	return history, nil
}

// validate only examines a record and current replay state; failed validation
// never changes state or disk. No full-history copy is made per event.
func (history *history) validate(event protocol.Event) error {
	if event.Version != protocol.FormatVersion {
		return fmt.Errorf("unsupported history version %d", event.Version)
	}
	if event.Sequence != history.sequence+1 || event.Sequence == 0 {
		return fmt.Errorf("unexpected history sequence %d", event.Sequence)
	}
	if event.Time.IsZero() {
		return fmt.Errorf("history event has no timestamp")
	}
	data := event.Data
	if event.Type == protocol.RunStarted {
		if history.active != "" || event.RunID == "" {
			return fmt.Errorf("invalid run start")
		}
	} else if event.Type == protocol.ContextReset {
		if history.active != "" || event.RunID != "" {
			return fmt.Errorf("cannot reset an active run")
		}
	} else if history.active == "" || event.RunID != history.active {
		return fmt.Errorf("event does not belong to the active run")
	}
	switch event.Type {
	case protocol.RunStarted, protocol.ContextReset:
		if data.Message != nil || data.ToolCallID != "" || data.StopReason != "" || data.Error != "" {
			return fmt.Errorf("unexpected event data")
		}
	case protocol.UserMessage, protocol.ModelMessage:
		if len(history.pending) != 0 {
			return fmt.Errorf("message precedes pending tool results")
		}
		role := aiprot.RoleUser
		if event.Type == protocol.ModelMessage {
			role = aiprot.RoleAssistant
		}
		if data.Message == nil || data.Message.Role != role || len(data.Message.Parts) == 0 || data.ToolCallID != "" || data.StopReason != "" || data.Error != "" {
			return fmt.Errorf("invalid message event")
		}
		if err := validateMessage(*data.Message); err != nil {
			return err
		}
	case protocol.ToolStarted:
		pending := history.pending[data.ToolCallID]
		if pending == nil || pending.started || data.Message != nil || data.StopReason != "" || data.Error != "" {
			return fmt.Errorf("invalid tool start")
		}
	case protocol.ToolResult:
		if data.Message == nil || data.Message.Role != aiprot.RoleTool || len(data.Message.Parts) != 1 || data.ToolCallID != "" || data.StopReason != "" || data.Error != "" {
			return fmt.Errorf("invalid tool result event")
		}
		part := data.Message.Parts[0]
		if part.Type != aiprot.ContentToolResult || part.ToolResult == nil {
			return fmt.Errorf("missing tool result")
		}
		pending := history.pending[part.ToolResult.ToolCallID]
		if pending == nil || pending.call.Name != part.ToolResult.ToolName {
			return fmt.Errorf("tool result does not match pending call")
		}
	case protocol.RunEnded:
		if len(history.pending) != 0 || data.Message != nil || data.ToolCallID != "" {
			return fmt.Errorf("invalid run end")
		}
		switch data.StopReason {
		case loopprot.StopNatural, loopprot.StopMaxSteps, loopprot.StopError:
		default:
			return fmt.Errorf("invalid stop reason")
		}
	default:
		return fmt.Errorf("unknown history event %q", event.Type)
	}
	return nil
}

func validateMessage(message aiprot.Message) error {
	messages := []aiprot.Message{message}
	for _, call := range message.ToolCalls() {
		if !json.Valid(call.Arguments) || !bytes.HasPrefix(bytes.TrimSpace(call.Arguments), []byte("{")) {
			return fmt.Errorf("tool arguments must be a JSON object")
		}
		result := aiprot.ToolResult{ToolCallID: call.ID, ToolName: call.Name}
		messages = append(messages, resultMessage(result))
	}
	return (agentprot.Request{Model: "history-validation", History: messages}).Validate()
}

func (history *history) apply(event protocol.Event) {
	history.sequence = event.Sequence
	switch event.Type {
	case protocol.RunStarted:
		history.active = event.RunID
	case protocol.UserMessage, protocol.ModelMessage:
		message := event.Data.Message.Clone()
		history.messages = append(history.messages, message)
		for _, call := range message.ToolCalls() {
			history.pending[call.ID] = &pendingCall{call: call.Clone()}
			history.order = append(history.order, call.ID)
		}
	case protocol.ToolStarted:
		history.pending[event.Data.ToolCallID].started = true
	case protocol.ToolResult:
		message := event.Data.Message.Clone()
		history.messages = append(history.messages, message)
		delete(history.pending, message.Parts[0].ToolResult.ToolCallID)
		if len(history.pending) == 0 {
			history.order = nil
		}
	case protocol.RunEnded:
		history.active = ""
	case protocol.ContextReset:
		history.messages = nil
	}
}

func (history *history) append(kind protocol.EventType, runID string, data protocol.EventData) error {
	history.mu.Lock()
	defer history.mu.Unlock()
	if history.poison != nil {
		return history.poison
	}
	event := protocol.Event{Version: protocol.FormatVersion, Sequence: history.sequence + 1, Time: time.Now().UTC(), Type: kind, RunID: runID, Data: data}
	if err := history.validate(event); err != nil {
		return err
	}
	content, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode history: %w", err)
	}
	content = append(content, '\n')
	count, err := history.file.Write(content)
	if err == nil && count != len(content) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = history.file.Sync()
	}
	if err != nil {
		history.poison = fmt.Errorf("history persistence failed; close and reopen workspace: %w", err)
		return history.poison
	}
	history.apply(event)
	return nil
}

func (history *history) snapshot() []aiprot.Message {
	history.mu.Lock()
	defer history.mu.Unlock()
	result := make([]aiprot.Message, len(history.messages))
	for index, message := range history.messages {
		result[index] = message.Clone()
	}
	return result
}

func (history *history) runID() string {
	history.mu.Lock()
	defer history.mu.Unlock()
	return history.active
}

func (history *history) healthy() error {
	history.mu.Lock()
	defer history.mu.Unlock()
	return history.poison
}

// completePending records recovery results without executing any tool. A tool
// start is persisted by the executor wrapper, after approval and before effects.
func (history *history) completePending(warn func(string)) error {
	history.mu.Lock()
	var pending []pendingCall
	for _, id := range history.order {
		if call := history.pending[id]; call != nil {
			pending = append(pending, *call)
		}
	}
	runID := history.active
	history.mu.Unlock()
	for _, call := range pending {
		text := "Tool was not executed because the run stopped. Do not assume any effects."
		if call.started {
			text = "Tool execution started but its outcome is unknown. Verify external effects before retrying; this call was not replayed."
		}
		if warn != nil {
			warn(fmt.Sprintf("%s (%s): %s", call.call.Name, call.call.ID, text))
		}
		result := aiprot.TextToolResult(text)
		result.ToolCallID, result.ToolName, result.IsError = call.call.ID, call.call.Name, true
		message := resultMessage(result)
		if err := history.append(protocol.ToolResult, runID, protocol.EventData{Message: &message}); err != nil {
			return err
		}
	}
	return nil
}

func resultMessage(result aiprot.ToolResult) aiprot.Message {
	return aiprot.Message{Role: aiprot.RoleTool, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolResult, ToolResult: &result}}}
}

func regularOrMissing(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("state path must be a regular file, not a symlink: %s", path)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
