package protocol

import (
	"strconv"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/tool-loop/utils"
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
		Model: request.Model, Messages: utils.Clone(messages),
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

// Clone copies mutable execution records independently of the original result.
func (result Result) Clone() Result { return utils.Clone(result) }
