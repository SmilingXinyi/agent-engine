// Package protocol defines workspace configuration and the durable event format.
package protocol

import (
	"context"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

const FormatVersion = 1

type ApprovalMode string

const (
	ApprovalWrites ApprovalMode = "writes"
	ApprovalAll    ApprovalMode = "all"
	ApprovalAuto   ApprovalMode = "auto"
)

// Config is a workspace-local override of process model settings. APIKeyEnv is
// an environment variable name, never a credential. Zero limits use defaults.
type Config struct {
	Model            string `json:"model,omitempty"`
	BaseURL          string `json:"baseURL,omitempty"`
	APIType          string `json:"apiType,omitempty"`
	APIKeyEnv        string `json:"apiKeyEnv,omitempty"`
	SystemPrompt     string `json:"systemPrompt,omitempty"`
	MaxSteps         int    `json:"maxSteps,omitempty"`
	MaxHistoryRounds int    `json:"maxHistoryRounds,omitempty"`
	TimeoutSeconds   int    `json:"timeoutSeconds,omitempty"`
}

// OpenOptions injects execution dependencies and terminal-independent policy.
// Provider bypasses provider construction, but Config.Model is still required.
// A nil ConfirmTool denies calls requiring approval. Hooks are observational;
// errors stop the existing loop. Warn reports repairs and interrupted runs.
type OpenOptions struct {
	Provider    aiprot.Provider
	Approval    ApprovalMode
	ConfirmTool func(context.Context, loopprot.ToolConfirmationInfo) (loopprot.ToolConfirmationDecision, error)
	Hooks       loopprot.Hooks
	Warn        func(string)
}

type EventType string

const (
	RunStarted   EventType = "run_started"
	UserMessage  EventType = "user_message"
	ModelMessage EventType = "model_message"
	ToolStarted  EventType = "tool_started"
	ToolResult   EventType = "tool_result"
	RunEnded     EventType = "run_ended"
	ContextReset EventType = "context_reset"
)

// Event is one committed, newline-terminated history.ndjson record.
type Event struct {
	Version  int       `json:"version"`
	Sequence uint64    `json:"sequence"`
	Time     time.Time `json:"time"`
	Type     EventType `json:"type"`
	RunID    string    `json:"run_id,omitempty"`
	Data     EventData `json:"data"`
}

type EventData struct {
	Message    *aiprot.Message     `json:"message,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
	StopReason loopprot.StopReason `json:"stop_reason,omitempty"`
	Error      string              `json:"error,omitempty"`
}
