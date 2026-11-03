package protocol

import (
	"fmt"
	"slices"
	"strconv"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

func (denied *ToolDeniedError) Error() string {
	message := fmt.Sprintf("tool %q execution denied (call_id=%q)", denied.ToolName, denied.ToolCallID)
	if denied.Reason != "" {
		message += ": " + denied.Reason
	}
	return message
}

// Clone copies protocol data using SDK snapshot rules. JSON-shaped extensions
// are isolated; application objects, executors and callbacks remain shared.
func (request Request) Clone() Request {
	snapshot := request.modelRequest(request.Messages).Clone()
	request.Messages = snapshot.Messages
	request.Temperature, request.MaxOutputTokens = snapshot.Temperature, snapshot.MaxOutputTokens
	request.Reasoning, request.OutputFormat = snapshot.Reasoning, snapshot.OutputFormat
	request.ProviderOptions, request.Metadata = snapshot.ProviderOptions, snapshot.Metadata
	request.Tools = slices.Clone(request.Tools)
	for index := range request.Tools {
		request.Tools[index].Definition = snapshot.Tools[index]
	}
	return request
}

// modelRequest assembles borrowed fields; callers clone before exposing them.
func (request Request) modelRequest(messages []aiprot.Message) aiprot.ModelRequest {
	modelRequest := aiprot.ModelRequest{
		Model: request.Model, Messages: messages,
		Temperature: request.Temperature, MaxOutputTokens: request.MaxOutputTokens,
		ToolChoice: request.ToolChoice, Reasoning: request.Reasoning,
		OutputFormat: request.OutputFormat, ProviderOptions: request.ProviderOptions,
		Metadata: request.Metadata,
	}
	for _, tool := range request.Tools {
		modelRequest.Tools = append(modelRequest.Tools, tool.Definition)
	}
	return modelRequest
}

// ModelCall builds one provider request. Caller metadata is copied, then the
// execution keys overwrite any caller values with the same names.
func (request Request) ModelCall(messages []aiprot.Message, runID, requestID string, step int) aiprot.ModelRequest {
	modelRequest := request.modelRequest(recentRounds(messages, request.Options.MaxHistoryRounds)).Clone()
	if modelRequest.Metadata == nil {
		modelRequest.Metadata = make(map[string]string, 3)
	}
	modelRequest.Metadata[MetadataRunID] = runID
	modelRequest.Metadata[aiprot.RequestIDMetadataKey] = requestID
	modelRequest.Metadata[MetadataStep] = strconv.Itoa(step)
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
func (result Result) Clone() Result {
	result.LastResponse = result.LastResponse.Clone()
	result.Messages = slices.Clone(result.Messages)
	for index := range result.Messages {
		result.Messages[index] = result.Messages[index].Clone()
	}
	result.Steps = slices.Clone(result.Steps)
	for index := range result.Steps {
		result.Steps[index] = result.Steps[index].Clone()
	}
	result.ToolCalls = cloneToolRecords(result.ToolCalls)
	return result
}

// Clone isolates the model response within a step.
func (step StepResult) Clone() StepResult {
	step.Response = step.Response.Clone()
	return step
}

// Clone isolates arguments, tool results and the confirmation decision.
func (record ToolCallRecord) Clone() ToolCallRecord {
	record.Call = record.Call.Clone()
	record.Result = record.Result.Clone()
	record.Confirmation = cloneDecision(record.Confirmation)
	return record
}

func cloneDecision(decision *ToolConfirmationDecision) *ToolConfirmationDecision {
	if decision == nil {
		return nil
	}
	snapshot := *decision
	return &snapshot
}

func cloneToolRecords(records []ToolCallRecord) []ToolCallRecord {
	records = slices.Clone(records)
	for index := range records {
		records[index] = records[index].Clone()
	}
	return records
}

// Clone returns an independent snapshot for the confirmation callback.
func (info ToolConfirmationInfo) Clone() ToolConfirmationInfo {
	info.Call = info.Call.Clone()
	info.Definition = info.Definition.Clone()
	return info
}

// Clone returns a run-start snapshot, preserving callback functions.
func (info RunStartInfo) Clone() RunStartInfo {
	info.Request = info.Request.Clone()
	return info
}

// Clone returns a run-end snapshot, preserving error identity.
func (info RunEndInfo) Clone() RunEndInfo {
	info.Request = info.Request.Clone()
	info.Result = info.Result.Clone()
	return info
}

// Clone returns an independent step-start snapshot.
func (info StepStartInfo) Clone() StepStartInfo {
	info.Request = info.Request.Clone()
	return info
}

// Clone returns a step-end snapshot, preserving error identity.
func (info StepEndInfo) Clone() StepEndInfo {
	info.Request = info.Request.Clone()
	info.Result = info.Result.Clone()
	info.ToolCalls = cloneToolRecords(info.ToolCalls)
	return info
}

// Clone returns an independent tool-start snapshot.
func (info ToolStartInfo) Clone() ToolStartInfo {
	info.Call = info.Call.Clone()
	return info
}

// Clone returns a tool-end snapshot, preserving error identity.
func (info ToolEndInfo) Clone() ToolEndInfo {
	info.Call = info.Call.Clone()
	info.Result = info.Result.Clone()
	info.Confirmation = cloneDecision(info.Confirmation)
	return info
}
