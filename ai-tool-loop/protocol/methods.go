package protocol

import (
	"strconv"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tool-loop/utils"
)

// Clone copies mutable request data, preserving executors and callbacks.
func (request Request) Clone() Request { return utils.Clone(request) }

// ModelCall builds one provider request. Caller metadata is copied, then the
// execution keys overwrite any caller values with the same names.
func (request Request) ModelCall(messages []aiprot.Message, runID, requestID string, step int) aiprot.ModelRequest {
	metadata := utils.Clone(request.Metadata)
	if metadata == nil {
		metadata = make(map[string]string, 3)
	}
	metadata[MetadataRunID] = runID
	metadata[aiprot.RequestIDMetadataKey] = requestID
	metadata[MetadataStep] = strconv.Itoa(step)
	modelRequest := aiprot.ModelRequest{
		Model: request.Model, Messages: utils.Clone(recentRounds(messages, request.Options.MaxHistoryRounds)),
		Temperature: utils.Clone(request.Temperature), MaxOutputTokens: utils.Clone(request.MaxOutputTokens),
		ToolChoice: request.ToolChoice, Reasoning: utils.Clone(request.Reasoning),
		OutputFormat: utils.Clone(request.OutputFormat), ProviderOptions: utils.Clone(request.ProviderOptions),
		Metadata: metadata,
	}
	for _, tool := range request.Tools {
		modelRequest.Tools = append(modelRequest.Tools, utils.Clone(tool.Definition))
	}
	return modelRequest
}

// Preserve complete assistant/tool batches and all system messages. Leading
// input belongs to the next assistant reply; consecutive assistant replies are
// separate rounds during autonomous tool execution.
func recentRounds(messages []aiprot.Message, limit int) []aiprot.Message {
	if limit <= 0 {
		limit = DefaultMaxHistoryRounds
	}
	var starts []int
	hasAssistant := false
	for index, message := range messages {
		if message.Role == aiprot.RoleSystem {
			continue
		}
		if len(starts) == 0 || (hasAssistant && message.Role != aiprot.RoleTool) {
			starts = append(starts, index)
			hasAssistant = false
		}
		if message.Role == aiprot.RoleAssistant {
			hasAssistant = true
		}
	}
	if len(starts) <= limit {
		return messages
	}
	cutoff := starts[len(starts)-limit]
	retained := make([]aiprot.Message, 0, len(messages)-cutoff)
	for index, message := range messages {
		if index >= cutoff || message.Role == aiprot.RoleSystem {
			retained = append(retained, message)
		}
	}
	return retained
}

// Clone copies mutable execution records independently of the original result.
func (result Result) Clone() Result { return utils.Clone(result) }
