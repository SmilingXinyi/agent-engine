package goai

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
	"github.com/zendev-sh/goai"
	"github.com/zendev-sh/goai/provider"
	"gopkg.in/yaml.v3"
)

type requestMessagePart struct {
	Type       string `yaml:"type"`
	Text       string `yaml:"text,omitempty"`
	ToolCallID string `yaml:"tool_call_id,omitempty"`
	ToolName   string `yaml:"tool_name,omitempty"`
	ToolInput  string `yaml:"tool_input,omitempty"`
	ToolOutput string `yaml:"tool_output,omitempty"`
}

type requestMessage struct {
	Role  string               `yaml:"role"`
	Parts []requestMessagePart `yaml:"parts"`
}

type requestDump struct {
	Model        string           `yaml:"model"`
	Timestamp    time.Time        `yaml:"timestamp"`
	MessageCount int              `yaml:"message_count"`
	ToolCount    int              `yaml:"tool_count"`
	Messages     []requestMessage `yaml:"messages"`
}

func dumpToSink(sink agent.DebugSink, info goai.RequestInfo) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = recoveredHandlerError(recovered)
		}
	}()
	messages := make([]agent.DebugMessage, 0, len(info.Messages))
	for _, message := range info.Messages {
		parts := make([]agent.DebugPart, 0, len(message.Content))
		for _, part := range message.Content {
			entry := agent.DebugPart{Type: string(part.Type)}
			switch part.Type {
			case provider.PartText, provider.PartReasoning:
				entry.Text = part.Text
			case provider.PartToolCall:
				entry.ToolCallID = part.ToolCallID
				entry.ToolName = part.ToolName
				if len(part.ToolInput) > 0 {
					entry.ToolInput = string(part.ToolInput)
				}
			case provider.PartToolResult:
				entry.ToolCallID = part.ToolCallID
				entry.ToolName = part.ToolName
				entry.ToolOutput = part.ToolOutput
			}
			parts = append(parts, entry)
		}
		messages = append(messages, agent.DebugMessage{Role: string(message.Role), Parts: parts})
	}
	return sink.DumpRequest(info.Model, info.ToolCount, messages)
}

// YAMLFileSink is the built-in DebugSink that writes each request to <dir>/<timestamp>.yaml.
type YAMLFileSink struct {
	Dir string
}

// DumpRequest writes the request payload to a timestamped YAML file.
func (sink YAMLFileSink) DumpRequest(model string, toolCount int, messages []agent.DebugMessage) error {
	dir := sink.Dir
	if dir == "" {
		dir = "requests"
	}
	parts := make([]requestMessage, 0, len(messages))
	for _, message := range messages {
		rawParts := make([]requestMessagePart, 0, len(message.Parts))
		for _, part := range message.Parts {
			rawParts = append(rawParts, requestMessagePart{
				Type:       part.Type,
				Text:       part.Text,
				ToolCallID: part.ToolCallID,
				ToolName:   part.ToolName,
				ToolInput:  part.ToolInput,
				ToolOutput: part.ToolOutput,
			})
		}
		parts = append(parts, requestMessage{Role: message.Role, Parts: rawParts})
	}
	dump := requestDump{
		Model:        model,
		Timestamp:    time.Now(),
		MessageCount: len(messages),
		ToolCount:    toolCount,
		Messages:     parts,
	}
	filename := fmt.Sprintf("%s/%s.yaml", dir, dump.Timestamp.Format("20060102-150405.000"))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return fmt.Errorf("create debug request directory: %w", err)
	}
	content, err := yaml.Marshal(dump)
	if err != nil {
		return fmt.Errorf("marshal debug request: %w", err)
	}
	if err := os.WriteFile(filename, content, 0o644); err != nil {
		return fmt.Errorf("write debug request %s: %w", filename, err)
	}
	return nil
}
