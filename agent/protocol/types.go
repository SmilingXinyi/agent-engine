// Package protocol defines the prompt/context input for an Agent execution.
package protocol

import (
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

// Request organizes one execution. The caller owns history and persistence.
type Request struct {
	Model        string
	SystemPrompt string
	History      []aiprot.Message
	UserPrompt   string
	Tools        []loopprot.Tool
	Options      loopprot.RunOptions
}
