package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"

	protocol "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

type responsesStreamEvent struct {
	Type        string                `json:"type"`
	Delta       string                `json:"delta"`
	Arguments   string                `json:"arguments"`
	OutputIndex int                   `json:"output_index"`
	ItemID      string                `json:"item_id"`
	Item        responsesOutputItem   `json:"item"`
	Response    responsesResponse     `json:"response"`
	Error       *responsesStreamError `json:"error,omitempty"`
}

type responsesStreamError struct {
	Message string `json:"message"`
}

type responsesToolCallBuilder struct {
	id        string
	callID    string
	name      string
	arguments strings.Builder
}

type responsesStreamAccumulator struct {
	text         strings.Builder
	reasoning    strings.Builder
	toolCalls    map[int]*responsesToolCallBuilder
	indexesByID  map[string]int
	providerName string
	finishReason protocol.FinishReason
	usage        protocol.Usage
	id           string
	model        string
	created      int64
	status       string
	incomplete   string
	completed    bool
}

func (provider *Provider) streamResponses(ctx context.Context, body io.Reader, sink protocol.EventSink) (protocol.ModelResponse, error) {
	accumulator := responsesStreamAccumulator{toolCalls: make(map[int]*responsesToolCallBuilder), indexesByID: make(map[string]int), providerName: provider.name}
	err := provider.readSSE(ctx, body, func(event RawEvent) error {
		if len(event.Data) == 0 {
			return nil
		}
		if bytes.Equal(bytes.TrimSpace(event.Data), []byte("[DONE]")) {
			return errSSEComplete
		}
		var decoded responsesStreamEvent
		if err := json.Unmarshal(event.Data, &decoded); err != nil {
			return &protocol.Error{Code: protocol.ErrorDecode, Message: err.Error(), Cause: err}
		}
		name := event.Name
		if name == "" {
			name = decoded.Type
		}
		if err := accumulator.consume(ctx, name, decoded, sink); err != nil {
			return err
		}
		if accumulator.completed {
			return errSSEComplete
		}
		return nil
	})
	result := accumulator.response()
	if err != nil {
		return result, err
	}
	if !accumulator.completed {
		return result, &protocol.Error{Code: protocol.ErrorStreamIncomplete, Message: "Responses stream ended without a terminal response event"}
	}
	return result, nil
}

func (accumulator *responsesStreamAccumulator) consume(ctx context.Context, eventName string, event responsesStreamEvent, sink protocol.EventSink) error {
	switch eventName {
	case "response.output_text.delta":
		if event.Delta == "" {
			return nil
		}
		accumulator.text.WriteString(event.Delta)
		return emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventTextDelta, Delta: event.Delta}, "text")
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta", "response.reasoning_content.delta":
		if event.Delta == "" {
			return nil
		}
		accumulator.reasoning.WriteString(event.Delta)
		return emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventReasoningDelta, Delta: event.Delta}, "reasoning")
	case "response.function_call_arguments.delta":
		index := accumulator.toolCallIndex(event.OutputIndex, event.ItemID)
		builder := accumulator.toolCalls[index]
		if builder == nil {
			builder = &responsesToolCallBuilder{id: event.ItemID}
			accumulator.toolCalls[index] = builder
		}
		builder.arguments.WriteString(event.Delta)
		return emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventToolCallDelta, ToolCallDelta: &protocol.ToolCallDelta{
			Index: index, ArgumentsDelta: event.Delta,
		}}, "tool call")
	case "response.function_call_arguments.done":
		index := accumulator.toolCallIndex(event.OutputIndex, event.ItemID)
		builder := accumulator.toolCalls[index]
		if builder == nil {
			builder = &responsesToolCallBuilder{id: event.ItemID}
			accumulator.toolCalls[index] = builder
		}
		return accumulator.setArguments(ctx, sink, index, builder, event.Arguments)
	case "response.output_item.added":
		if event.Item.Type == "function_call" {
			index := accumulator.toolCallIndex(event.OutputIndex, event.Item.ID)
			builder := accumulator.toolCalls[index]
			if builder == nil {
				builder = &responsesToolCallBuilder{id: event.Item.ID}
				accumulator.toolCalls[index] = builder
			}
			return accumulator.setMetadata(ctx, sink, index, builder, event.Item.CallID, event.Item.Name)
		}
	case "response.output_item.done":
		if event.Item.Type == "function_call" {
			index := accumulator.toolCallIndex(event.OutputIndex, event.Item.ID)
			builder := accumulator.toolCalls[index]
			if builder == nil {
				builder = &responsesToolCallBuilder{id: event.Item.ID}
				accumulator.toolCalls[index] = builder
			}
			if err := accumulator.setMetadata(ctx, sink, index, builder, event.Item.CallID, event.Item.Name); err != nil {
				return err
			}
			return accumulator.setArguments(ctx, sink, index, builder, event.Item.Arguments)
		}
	case "response.completed":
		if event.Response.Status == "" {
			event.Response.Status = "completed"
		}
		accumulator.completed = true
		return accumulator.mergeResponse(ctx, sink, event.Response)
	case "response.incomplete":
		if event.Response.Status == "" {
			event.Response.Status = "incomplete"
		}
		accumulator.completed = true
		return accumulator.mergeResponse(ctx, sink, event.Response)
	case "response.failed", "error":
		if eventName == "response.failed" {
			if event.Response.Status == "" {
				event.Response.Status = "failed"
			}
			if err := accumulator.mergeResponse(ctx, sink, event.Response); err != nil {
				return err
			}
		}
		message := "OpenAI Responses stream failed"
		if event.Error != nil && event.Error.Message != "" {
			message = event.Error.Message
		}
		return &protocol.Error{Code: protocol.ErrorUpstream, Message: message}
	}
	return nil
}

func (accumulator *responsesStreamAccumulator) mergeResponse(ctx context.Context, sink protocol.EventSink, response responsesResponse) error {
	if response.ID != "" {
		accumulator.id = response.ID
	}
	if response.Model != "" {
		accumulator.model = response.Model
	}
	if response.CreatedAt != 0 {
		accumulator.created = response.CreatedAt
	}
	if response.Status != "" {
		accumulator.status = response.Status
	}
	accumulator.incomplete = response.IncompleteDetail.Reason
	hasUsage := response.Usage.TotalTokens != 0 || response.Usage.InputTokens != 0 || response.Usage.OutputTokens != 0
	if hasUsage {
		accumulator.usage = convertResponsesUsage(response.Usage)
	}
	final, err := convertResponsesResponse(response, accumulator.providerName)
	if err != nil {
		return err
	}
	if text := final.Message.Text(); text != "" {
		if err := reconcileText(ctx, sink, &accumulator.text, text, protocol.EventTextDelta); err != nil {
			return err
		}
	}
	if reasoning := final.Message.Reasoning(); reasoning != "" {
		if err := reconcileText(ctx, sink, &accumulator.reasoning, reasoning, protocol.EventReasoningDelta); err != nil {
			return err
		}
	}
	for _, item := range response.Output {
		switch item.Type {
		case "function_call":
			index := accumulator.toolCallIndex(-1, item.ID)
			builder := accumulator.toolCalls[index]
			if builder == nil {
				builder = &responsesToolCallBuilder{id: item.ID}
				accumulator.toolCalls[index] = builder
			}
			if err := accumulator.setMetadata(ctx, sink, index, builder, item.CallID, item.Name); err != nil {
				return err
			}
			if err := accumulator.setArguments(ctx, sink, index, builder, item.Arguments); err != nil {
				return err
			}
		}
	}
	if hasUsage {
		usage := accumulator.usage
		if err := emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventUsage, Usage: &usage}, "usage"); err != nil {
			return err
		}
	}
	return nil
}

func (accumulator *responsesStreamAccumulator) setMetadata(ctx context.Context, sink protocol.EventSink, index int, builder *responsesToolCallBuilder, id, name string) error {
	if (builder.callID != "" && id != "" && builder.callID != id) || (builder.name != "" && name != "" && builder.name != name) {
		return &protocol.Error{Code: protocol.ErrorDecode, Message: "tool metadata changed within one stream index"}
	}
	delta := &protocol.ToolCallDelta{Index: index}
	if builder.callID == "" && id != "" {
		builder.callID = id
		delta.ID = id
	}
	if builder.name == "" && name != "" {
		builder.name = name
		delta.Name = name
	}
	if delta.ID == "" && delta.Name == "" {
		return nil
	}
	return emitEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventToolCallDelta, ToolCallDelta: delta})
}

func (accumulator *responsesStreamAccumulator) setArguments(ctx context.Context, sink protocol.EventSink, index int, builder *responsesToolCallBuilder, final string) error {
	if final == "" {
		return nil
	}
	current := builder.arguments.String()
	if strings.HasPrefix(final, current) {
		suffix := strings.TrimPrefix(final, current)
		if suffix != "" {
			builder.arguments.WriteString(suffix)
			return emitResponsesEvent(ctx, sink, protocol.StreamEvent{Type: protocol.EventToolCallDelta, ToolCallDelta: &protocol.ToolCallDelta{
				Index: index, ArgumentsDelta: suffix,
			}}, "tool call")
		}
	}
	if current != final {
		return &protocol.Error{Code: protocol.ErrorDecode, Message: "final tool arguments disagree with streamed arguments"}
	}
	return nil
}

func (accumulator *responsesStreamAccumulator) toolCallIndex(outputIndex int, itemID string) int {
	if itemID != "" {
		if index, ok := accumulator.indexesByID[itemID]; ok {
			return index
		}
	}
	if outputIndex >= 0 {
		if itemID != "" {
			accumulator.indexesByID[itemID] = outputIndex
		}
		return outputIndex
	}
	index := 0
	for accumulator.toolCalls[index] != nil {
		index++
	}
	if itemID != "" {
		accumulator.indexesByID[itemID] = index
	}
	return index
}

func (accumulator *responsesStreamAccumulator) response() protocol.ModelResponse {
	message := protocol.Message{Role: protocol.RoleAssistant}
	if accumulator.text.Len() > 0 {
		message.Parts = append(message.Parts, protocol.ContentPart{Type: protocol.ContentText, Text: accumulator.text.String()})
	}
	if accumulator.reasoning.Len() > 0 {
		message.Parts = append(message.Parts, protocol.ContentPart{Type: protocol.ContentReasoning, Text: accumulator.reasoning.String()})
	}
	indexes := make([]int, 0, len(accumulator.toolCalls))
	for index := range accumulator.toolCalls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		builder := accumulator.toolCalls[index]
		call := protocol.ToolCall{ID: builder.callID, Name: builder.name, Arguments: json.RawMessage(builder.arguments.String())}
		message.Parts = append(message.Parts, protocol.ContentPart{Type: protocol.ContentToolCall, ToolCall: &call})
	}
	finishReason := accumulator.finishReason
	if finishReason == "" {
		finishReason = responsesFinishReason(accumulator.status, accumulator.incomplete, len(accumulator.toolCalls) > 0)
		if finishReason == "" && accumulator.completed {
			finishReason = protocol.FinishStop
		}
	}
	return protocol.ModelResponse{
		Message: message, FinishReason: finishReason, Usage: accumulator.usage,
		Metadata: responseMetadata(accumulator.providerName, accumulator.id, accumulator.model, accumulator.created),
	}
}

func emitResponsesEvent(ctx context.Context, sink protocol.EventSink, event protocol.StreamEvent, _ string) error {
	return emitEvent(ctx, sink, event)
}

func reconcileText(ctx context.Context, sink protocol.EventSink, target *strings.Builder, final string, kind protocol.StreamEventType) error {
	current := target.String()
	if !strings.HasPrefix(final, current) {
		return &protocol.Error{Code: protocol.ErrorDecode, Message: "final content disagrees with streamed content"}
	}
	suffix := strings.TrimPrefix(final, current)
	if suffix == "" {
		return nil
	}
	target.WriteString(suffix)
	return emitEvent(ctx, sink, protocol.StreamEvent{Type: kind, Delta: suffix})
}
