// Package protocol defines sequential model/tool execution and its observations.
package protocol

import (
	"context"
	"encoding/json"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

const DefaultMaxSteps = 10

// StopReason describes why execution ended, not whether a business task succeeded.
type StopReason string

const (
	StopNatural  StopReason = "natural"
	StopMaxSteps StopReason = "max_steps"
	StopError    StopReason = "error"
)

// Tool pairs a model-visible definition with its required executor.
type Tool struct {
	Definition aiprot.ToolDefinition
	Execute    func(context.Context, json.RawMessage) (aiprot.ToolResult, error)
}

type Request struct {
	Model    string
	Messages []aiprot.Message
	Tools    []Tool
	Options  RunOptions
}

type RunOptions struct {
	// MaxSteps counts model calls, including the final answer. Zero uses DefaultMaxSteps.
	MaxSteps int
	Hooks    Hooks
}

// Result owns execution snapshots. Usage is cumulative; LastResponse and each
// step's Response keep the provider's single-call usage. Hook errors can end a
// run after a successful model response, so callers must also inspect StopReason.
type Result struct {
	RunID        string
	LastResponse aiprot.ModelResponse
	Messages     []aiprot.Message
	Steps        []StepResult
	ToolCalls    []ToolCallRecord
	Usage        aiprot.Usage
	StopReason   StopReason
}

type StepResult struct {
	Step       int
	RequestID  string
	Response   aiprot.ModelResponse
	StartedAt  time.Time
	FinishedAt time.Time
}

type ToolCallRecord struct {
	Step       int
	Call       aiprot.ToolCall
	Result     aiprot.ToolResult
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

// Hooks receive independent snapshots. Returning an error stops execution;
// end-hook errors are joined with the original error. Panics become errors.
type Hooks struct {
	OnRunStart  func(context.Context, RunStartInfo) error
	OnRunEnd    func(context.Context, RunEndInfo) error
	OnStepStart func(context.Context, StepStartInfo) error
	OnStepEnd   func(context.Context, StepEndInfo) error
	OnToolStart func(context.Context, ToolStartInfo) error
	OnToolEnd   func(context.Context, ToolEndInfo) error
}

type RunStartInfo struct {
	RunID     string
	Request   Request
	StartedAt time.Time
}

type RunEndInfo struct {
	RunID      string
	Request    Request
	Result     Result
	Error      error
	StartedAt  time.Time
	FinishedAt time.Time
	Duration   time.Duration
}

type StepStartInfo struct {
	RunID     string
	Step      int
	RequestID string
	Request   aiprot.ModelRequest
	StartedAt time.Time
}

type StepEndInfo struct {
	RunID      string
	Step       int
	RequestID  string
	Request    aiprot.ModelRequest
	Result     StepResult
	ToolCalls  []ToolCallRecord
	Error      error
	StartedAt  time.Time
	FinishedAt time.Time
	Duration   time.Duration
}

type ToolStartInfo struct {
	RunID     string
	Step      int
	RequestID string
	Call      aiprot.ToolCall
	StartedAt time.Time
}

type ToolEndInfo struct {
	RunID      string
	Step       int
	RequestID  string
	Call       aiprot.ToolCall
	Result     aiprot.ToolResult
	Error      error
	StartedAt  time.Time
	FinishedAt time.Time
	Duration   time.Duration
}
