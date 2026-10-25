package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
)

func screenshotMedia(content aiprot.ContentPart) *aiprot.MediaContent {
	if content.Type == aiprot.ContentImage && content.Media != nil {
		return content.Media
	}
	var image struct {
		Type     string `json:"type"`
		Data     string `json:"data"`
		MIMEType string `json:"mimeType"`
	}
	if content.Type != aiprot.ContentText || json.Unmarshal([]byte(content.Text), &image) != nil || image.Type != "image" || image.Data == "" || image.MIMEType == "" {
		return nil
	}
	return &aiprot.MediaContent{Data: image.Data, MIMEType: image.MIMEType}
}

// Chat Completions tool replies only accept text. Attach the latest screenshot
// as a user image after its complete tool batch, then bound the actual request.
type visionProvider struct {
	aiprot.Provider
	maxMessages int
	output      io.Writer
}

func (provider visionProvider) Generate(ctx context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
	messages := make([]aiprot.Message, len(request.Messages))
	lastImageMessage := -1
	var latestImage *aiprot.MediaContent
	for index, original := range request.Messages {
		message := original.Clone()
		for _, part := range message.Parts {
			if part.ToolResult == nil {
				continue
			}
			for contentIndex, content := range part.ToolResult.Content {
				if media := screenshotMedia(content); media != nil {
					lastImageMessage, latestImage = index, media
					part.ToolResult.Content[contentIndex] = aiprot.ContentPart{Type: aiprot.ContentText, Text: "截图已获取；最新截图附在后续消息。"}
				}
			}
		}
		messages[index] = message
	}
	if latestImage != nil {
		end := lastImageMessage + 1
		for end < len(messages) && messages[end].Role == aiprot.RoleTool {
			end++
		}
		observation := aiprot.Message{Role: aiprot.RoleUser, Parts: []aiprot.ContentPart{
			{Type: aiprot.ContentText, Text: "最新桌面截图。"},
			{Type: aiprot.ContentImage, Media: latestImage},
		}}
		messages = append(messages[:end], append([]aiprot.Message{observation}, messages[end:]...)...)
	}
	var err error
	request.Messages, err = visionHistory(messages, provider.maxMessages)
	if err != nil {
		return aiprot.ModelResponse{}, err
	}
	fmt.Fprintf(provider.output, "[vision request] messages=%d limit=%d\n", len(request.Messages), provider.maxMessages)
	return provider.Provider.Generate(ctx, request)
}

// Keep system instructions and the original task, then the newest complete
// assistant/tool/image exchanges. Never keep orphaned results or replay actions
// after dropping the latest batch merely to satisfy a small message budget.
func visionHistory(messages []aiprot.Message, limit int) ([]aiprot.Message, error) {
	var pinned []aiprot.Message
	var batches [][]aiprot.Message
	hasTask := false
	for _, message := range messages {
		if message.Role == aiprot.RoleSystem || (!hasTask && message.Role == aiprot.RoleUser) {
			pinned = append(pinned, message)
			if message.Role == aiprot.RoleUser {
				hasTask = true
			}
			continue
		}
		if message.Role == aiprot.RoleAssistant || len(batches) == 0 {
			batches = append(batches, nil)
		}
		batches[len(batches)-1] = append(batches[len(batches)-1], message)
	}
	if len(pinned) > limit {
		return nil, fmt.Errorf("history-messages=%d cannot fit system instructions and task", limit)
	}
	remaining, start := limit-len(pinned), len(batches)
	for start > 0 && len(batches[start-1]) <= remaining {
		start--
		remaining -= len(batches[start])
	}
	if start == len(batches) && len(batches) > 0 {
		return nil, fmt.Errorf("latest tool batch needs %d messages plus %d pinned messages; increase -history-messages to at least %d", len(batches[start-1]), len(pinned), len(batches[start-1])+len(pinned))
	}
	for _, batch := range batches[start:] {
		pinned = append(pinned, batch...)
	}
	return pinned, nil
}
