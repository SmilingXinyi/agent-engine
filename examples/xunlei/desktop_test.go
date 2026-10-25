package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SmilingXinyi/agent-engine/agent"
	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

func testScreenshot(data string) aiprot.ContentPart {
	encoded, _ := json.Marshal(map[string]string{"type": "image", "mimeType": "image/png", "data": data})
	return aiprot.ContentPart{Type: aiprot.ContentText, Text: string(encoded)}
}

func screenshotExchange(id string, image aiprot.ContentPart) []aiprot.Message {
	call := aiprot.ToolCall{ID: id, Name: "desktop-agent-remote_capture_screen", Arguments: json.RawMessage(`{"mode":"display","display_index":0}`)}
	result := aiprot.ToolResult{ToolCallID: id, ToolName: call.Name, Content: []aiprot.ContentPart{image}}
	return []aiprot.Message{
		{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall, ToolCall: &call}}},
		{Role: aiprot.RoleTool, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolResult, ToolResult: &result}}},
	}
}

func TestVisionProviderBoundsRealMessagesAndSendsLatestImage(t *testing.T) {
	request := aiprot.ModelRequest{Model: "vision", Messages: []aiprot.Message{aiprot.TextMessage(aiprot.RoleSystem, "system"), aiprot.TextMessage(aiprot.RoleUser, "original task and URL")}}
	for index := 0; index < 15; index++ {
		request.Messages = append(request.Messages, screenshotExchange(fmt.Sprint(index), testScreenshot(fmt.Sprintf("image-%d", index)))...)
	}
	original := request.Clone()
	called := false
	provider := visionProvider{maxMessages: 10, output: io.Discard, Provider: confirmationProvider{generate: func(_ context.Context, sent aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		called = true
		if len(sent.Messages) > 10 || sent.Messages[0].Text() != "system" || sent.Messages[1].Text() != "original task and URL" {
			t.Fatalf("sent messages=%d", len(sent.Messages))
		}
		pending := map[string]bool{}
		images := 0
		for _, message := range sent.Messages {
			for _, call := range message.ToolCalls() {
				pending[call.ID] = true
			}
			for _, result := range message.ToolResults() {
				if !pending[result.ToolCallID] {
					t.Fatalf("orphan result %s", result.ToolCallID)
				}
				delete(pending, result.ToolCallID)
				for _, content := range result.Content {
					if screenshotMedia(content) != nil || strings.Contains(content.Text, "image/png") {
						t.Fatal("image remained inside tool text")
					}
				}
			}
			for _, part := range message.Parts {
				if part.Type == aiprot.ContentImage {
					images++
					if len(pending) != 0 || part.Media.Data != "image-14" {
						t.Fatal("wrong screenshot or interrupted tool batch")
					}
				}
			}
		}
		if images != 1 || len(pending) != 0 {
			t.Fatalf("images=%d pending=%v", images, pending)
		}
		if sent.Messages[len(sent.Messages)-1].Role != aiprot.RoleUser {
			t.Fatal("latest screenshot was not retained")
		}
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "next")}, nil
	}}}
	if _, err := provider.Generate(context.Background(), request); err != nil || !called {
		t.Fatalf("called=%v error=%v", called, err)
	}
	if !reflect.DeepEqual(request, original) {
		t.Fatal("vision adaptation mutated stored history")
	}
}

func TestVisionHistoryDoesNotSplitOversizedLatestToolBatch(t *testing.T) {
	messages := []aiprot.Message{aiprot.TextMessage(aiprot.RoleSystem, "system"), aiprot.TextMessage(aiprot.RoleUser, "task")}
	batch := screenshotExchange("capture", testScreenshot("image"))
	for index := 0; index < 10; index++ {
		batch = append(batch, batch[1])
	}
	messages = append(messages, batch...)
	if _, err := visionHistory(messages, 10); err == nil || !strings.Contains(err.Error(), "latest tool batch") {
		t.Fatal(err)
	}
}

func TestVisionProviderLeavesImageFormatAndNativeToolsToModel(t *testing.T) {
	for _, format := range []string{"png", "webp"} {
		t.Run(format, func(t *testing.T) {
			image := aiprot.ContentPart{Type: aiprot.ContentImage, Media: &aiprot.MediaContent{MIMEType: "image/" + format, Data: "original-" + format}}
			request := aiprot.ModelRequest{
				Messages: []aiprot.Message{aiprot.TextMessage(aiprot.RoleSystem, "system"), aiprot.TextMessage(aiprot.RoleUser, "task")},
				Tools:    []aiprot.ToolDefinition{{Name: "desktop-agent-remote_capture_screen"}, {Name: "tiny-image_resize_image"}},
			}
			request.Messages = append(request.Messages, screenshotExchange("capture", image)...)
			original := request.Clone()
			provider := visionProvider{maxMessages: 10, output: io.Discard, Provider: confirmationProvider{generate: func(_ context.Context, sent aiprot.ModelRequest) (aiprot.ModelResponse, error) {
				if !reflect.DeepEqual(sent.Tools, request.Tools) {
					t.Fatal("native image tools were removed or rewritten")
				}
				parts := sent.Messages[len(sent.Messages)-1].Parts
				if !reflect.DeepEqual(parts[1], image) {
					t.Fatal("image format or bytes were changed automatically")
				}
				return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done")}, nil
			}}}
			if _, err := provider.Generate(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(request, original) {
				t.Fatal("stored screenshot was mutated")
			}
		})
	}
}

func TestVisionProviderWaitsUntilEntireToolBatchBeforeAttachingImage(t *testing.T) {
	messages := []aiprot.Message{aiprot.TextMessage(aiprot.RoleSystem, "system"), aiprot.TextMessage(aiprot.RoleUser, "task")}
	batch := screenshotExchange("capture", testScreenshot("latest"))
	call := aiprot.ToolCall{ID: "other", Name: "list_windows", Arguments: json.RawMessage(`{"mode":"display","display_index":0}`)}
	batch[0].Parts = append(batch[0].Parts, aiprot.ContentPart{Type: aiprot.ContentToolCall, ToolCall: &call})
	result := aiprot.ToolResult{ToolCallID: call.ID, ToolName: call.Name, Content: []aiprot.ContentPart{{Type: aiprot.ContentText, Text: "windows"}}}
	batch = append(batch, aiprot.Message{Role: aiprot.RoleTool, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolResult, ToolResult: &result}}})
	messages = append(messages, batch...)
	provider := visionProvider{maxMessages: 10, output: io.Discard, Provider: confirmationProvider{generate: func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		if len(request.Messages) != 6 || request.Messages[3].Role != aiprot.RoleTool || request.Messages[4].Role != aiprot.RoleTool || request.Messages[5].Parts[1].Media.Data != "latest" {
			t.Fatal("screenshot interrupted the tool batch")
		}
		return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done")}, nil
	}}}
	if _, err := provider.Generate(context.Background(), aiprot.ModelRequest{Messages: messages}); err != nil {
		t.Fatal(err)
	}
}

func TestVisionLoopKeepsOriginalTaskAndTenMessageLimitBeyondTenRounds(t *testing.T) {
	t.Setenv("AGENT_TOOL_CONFIRMATION", "off")
	models := 0
	provider := visionProvider{maxMessages: 10, output: io.Discard, Provider: confirmationProvider{generate: func(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
		models++
		if request.Messages[0].Role != aiprot.RoleSystem || request.Messages[0].Text() != systemPrompt {
			t.Fatal("system prompt must match prompt.md throughout the run")
		}
		if len(request.Tools) != 1 || request.Tools[0].Name != "desktop-agent-remote_capture_screen" {
			t.Fatal("native MCP screenshot tool was not forwarded")
		}
		if len(request.Messages) > 10 || len(request.Messages) < 2 || request.Messages[1].Text() != "original task and URL" {
			t.Fatalf("task lost or limit exceeded at step %d: %d messages", models, len(request.Messages))
		}
		if models == 14 {
			return aiprot.ModelResponse{Message: aiprot.TextMessage(aiprot.RoleAssistant, "done")}, nil
		}
		return aiprot.ModelResponse{Message: screenshotExchange(fmt.Sprint(models), testScreenshot("unused"))[0]}, nil
	}}}
	request := agentprot.Request{Model: "vision", SystemPrompt: systemPrompt, UserPrompt: "original task and URL", Options: executionOptions(io.NopCloser(strings.NewReader("")), io.Discard, 16), Tools: []loopprot.Tool{{Definition: aiprot.ToolDefinition{Name: "desktop-agent-remote_capture_screen"}, Execute: func(_ context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
		if string(arguments) != `{"mode":"display","display_index":0}` {
			t.Fatalf("native screenshot arguments were changed: %s", arguments)
		}
		return aiprot.ToolResult{Content: []aiprot.ContentPart{testScreenshot("latest")}}, nil
	}}}}
	result, err := agent.New(ai.NewClient(provider, aiprot.Hooks{})).Execute(context.Background(), request)
	if err != nil || result.StopReason != loopprot.StopNatural || models != 14 || len(result.Messages) <= 10 {
		t.Fatalf("models=%d result=%+v error=%v", models, result, err)
	}
}

func TestDownloadURLRecognizesContentDispositionNames(t *testing.T) {
	for _, key := range []string{"b2ContentDisposition", "response-content-disposition"} {
		raw := "https://example.com/object-id?" + key + "=" + url.QueryEscape(`attachment; filename="target.jpg"`) + "&signature=keep%2Bthis"
		path := filepath.Join(t.TempDir(), "url.txt")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		actual, names, err := loadDownloadURL(path)
		if err != nil || actual != raw || len(names) == 0 || names[0] != "target.jpg" {
			t.Fatalf("url=%s names=%v error=%v", actual, names, err)
		}
	}
}
