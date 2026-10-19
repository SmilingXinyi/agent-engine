// Package protocol defines the prompt/context input for an Agent execution.
package protocol

import (
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/tool-loop/protocol"
)

// Request organizes one execution. The caller owns history and persistence.
// Generation settings are forwarded unchanged on every model call.
type Request struct {
	Model           string
	SystemPrompt    string
	History         []aiprot.Message
	UserPrompt      string
	Tools           []loopprot.Tool
	Temperature     *float64
	MaxOutputTokens *int
	ToolChoice      aiprot.ToolChoice
	Reasoning       *aiprot.ReasoningOptions
	OutputFormat    *aiprot.OutputFormat
	ProviderOptions map[string]any
	Metadata        map[string]string
	Options         loopprot.RunOptions
}
