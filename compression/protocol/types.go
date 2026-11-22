// Package protocol defines conversation compaction settings and observations.
package protocol

import (
	"context"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

const (
	MetadataPurpose    = "compression_purpose"
	PurposeSummary     = "conversation_summary"
	SummaryMarker      = "[Conversation history compacted]"
	summaryMetadataKey = "compression_summary"
)

// TokenCounter can use a model-specific tokenizer. Messages contain only
// model-visible tool content. The counter must treat its input as read-only.
type TokenCounter func([]aiprot.Message) int

type GenerateFunc func(context.Context, aiprot.ModelRequest) (aiprot.ModelResponse, error)

type Config struct {
	// ContextWindow enables percentage-based triggering when positive.
	ContextWindow int `json:"contextWindow,omitempty"`
	// TriggerPercent defaults to 80 and uses a 0–100 scale.
	TriggerPercent float64 `json:"triggerPercent,omitempty"`
	// ToolResultTokens caps the estimated text tokens per tool result. Default 1024.
	ToolResultTokens int `json:"toolResultTokens,omitempty"`
	// KeepRecentRounds protects the latest rounds. Nil defaults to two. An
	// explicit zero retains no raw rounds unless MaxRecentRounds is positive.
	KeepRecentRounds *int `json:"keepRecentRounds,omitempty"`
	// MaxRecentRounds caps adaptive retention. It defaults to the greater of
	// four and KeepRecentRounds, or zero when KeepRecentRounds is explicitly zero.
	MaxRecentRounds int `json:"maxRecentRounds,omitempty"`
	// SummaryModel defaults to the model of the current conversation.
	SummaryModel string `json:"summaryModel,omitempty"`
	// SummaryProviderOptions sets native provider fields for summary calls.
	SummaryProviderOptions map[string]any `json:"summaryProviderOptions,omitempty"`
	CountTokens            TokenCounter   `json:"-"`
}

type Request struct {
	Model    string
	Messages []aiprot.Message
	// ProviderOptions contains the ordinary request options. An explicit store
	// setting is preserved when making summary requests.
	ProviderOptions map[string]any
	// MaxHistoryRounds triggers summarization when exceeded. Zero disables
	// this trigger. Config.ContextWindow can enable an independent trigger.
	MaxHistoryRounds int
	// ForceSummary requests a summary regardless of the trigger thresholds.
	ForceSummary bool
	// ProtectActiveTurn preserves the latest user prompt and the newest round.
	// Use it immediately before an ordinary model call.
	ProtectActiveTurn bool
}

type ContextUsage struct {
	Tokens        int `json:"tokens"`
	ContextWindow int `json:"context_window,omitempty"`
	// Percent is zero without a context window and may exceed 100.
	Percent float64 `json:"percent"`
}

// ToolArchive records the complete tool result at a position in the input.
type ToolArchive struct {
	MessageIndex int
	PartIndex    int
	Result       aiprot.ToolResult
}

type Result struct {
	Messages           []aiprot.Message
	Before             ContextUsage
	After              ContextUsage
	TrimmedToolResults int
	ToolArchives       []ToolArchive
	SummarizedRounds   int
	SummaryUsage       aiprot.Usage
}
