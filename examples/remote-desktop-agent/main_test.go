package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	mcpprot "github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Verify the complete model → remote MCP → screenshot → model round trip,
// including round-based history trimming and complete batches of tool replies.
func TestRemoteDesktopRun(t *testing.T) {
	for _, scenario := range []struct {
		maxSteps, historyRounds int
		noAPIKey                bool
		timeout                 time.Duration
	}{{4, 0, false, 10 * time.Second}, {4, 1, false, 10 * time.Second}, {1, 0, false, 10 * time.Second}, {4, 0, true, 10 * time.Second}, {4, 0, false, 0}} {
		maxSteps, historyRounds := scenario.maxSteps, scenario.historyRounds
		t.Run(fmt.Sprintf("max-steps=%d/history-rounds=%d/no-api-key=%v/timeout=%s", maxSteps, historyRounds, scenario.noAPIKey, scenario.timeout), func(t *testing.T) {
			var modelCalls, toolCalls, uploads, resizes atomic.Int32
			server := sdk.NewServer(&sdk.Implementation{Name: "desktop-test", Version: "1.0"}, nil)
			server.AddTool(&sdk.Tool{Name: "capture_screen", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				call := toolCalls.Add(1)
				return &sdk.CallToolResult{Content: []sdk.Content{
					&sdk.TextContent{Text: "Desktop is visible"},
					&sdk.ImageContent{Data: []byte(fmt.Sprintf("screenshot-%d", call)), MIMEType: "image/png"},
				}}, nil
			})
			mcpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
			defer mcpServer.Close()
			imageServer := sdk.NewServer(&sdk.Implementation{Name: "tiny-image-test", Version: "1.0"}, nil)
			for _, name := range []string{"upload_image", "resize_image", "get_image"} {
				imageServer.AddTool(&sdk.Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`)}, func(_ context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
					var arguments struct {
						ID           string `json:"id"`
						Data         string `json:"dataBase64"`
						Path         string `json:"path"`
						Format       string `json:"format"`
						IncludeImage bool   `json:"includeImage"`
					}
					if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
						return nil, err
					}
					if name == "upload_image" {
						uploads.Add(1)
						decoded, err := os.ReadFile(arguments.Path)
						if err != nil {
							return nil, err
						}
						id := strings.Replace(string(decoded), "screenshot-", "image-", 1)
						return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: fmt.Sprintf(`{"id":%q,"width":3840,"height":2160,"size":123456}`, id)}}}, nil
					}
					resizes.Add(1)
					if arguments.Format != "webp" || !arguments.IncludeImage {
						t.Error("preprocessing must request an attached WebP image")
					}
					return &sdk.CallToolResult{Content: []sdk.Content{
						&sdk.TextContent{Text: fmt.Sprintf(`{"id":%q,"width":1920,"height":1080,"size":1000,"imageIncluded":true}`, arguments.ID)},
						&sdk.ImageContent{Data: []byte("processed-" + arguments.ID), MIMEType: "image/webp"},
					}}, nil
				})
			}
			imageMCP := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return imageServer }, nil))
			defer imageMCP.Close()
			modelServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				expectedAuthorization := "Bearer test-key"
				if scenario.noAPIKey {
					expectedAuthorization = ""
					if _, present := request.Header["Authorization"]; present {
						t.Error("Authorization header must be omitted without an API key")
					}
				}
				if request.Header.Get("Authorization") != expectedAuthorization {
					t.Error("unexpected model credentials")
				}
				var payload struct {
					Model    string `json:"model"`
					Messages []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
					Tools []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tools"`
				}
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Error(err)
					writer.WriteHeader(http.StatusBadRequest)
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				if payload.Model != "test-model" || len(payload.Tools) != 5 || payload.Tools[0].Function.Name != "desktop-agent-remote_capture_screen" || payload.Tools[1].Function.Name != "tiny-image_get_image" || payload.Tools[2].Function.Name != "tiny-image_resize_image" || payload.Tools[3].Function.Name != "tiny-image_upload_image" || payload.Tools[4].Function.Name != "ocr_image" {
					t.Errorf("unexpected model/tools: %+v", payload)
				}
				call := modelCalls.Add(1)
				if call == 1 {
					if len(payload.Messages) != 2 || string(payload.Messages[1].Content) != `"查看远程桌面"` {
						t.Errorf("task was not passed to the model: %+v", payload.Messages)
					}
					if !strings.Contains(string(payload.Messages[0].Content), "自动缩放") || !strings.Contains(string(payload.Messages[0].Content), "WebP") {
						t.Error("system prompt is missing image compression instructions")
					}
					if !strings.Contains(string(payload.Messages[0].Content), "ocr_image") || !strings.Contains(string(payload.Messages[0].Content), "image_id") {
						t.Error("system prompt is missing OCR usage instructions")
					}
					fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"first","type":"function","function":{"name":"desktop-agent-remote_capture_screen","arguments":"{}"}},{"id":"second","type":"function","function":{"name":"desktop-agent-remote_capture_screen","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
					return
				}
				messageCount, firstTool := 6, 3
				if call == 3 {
					messageCount, firstTool = 9, 6
					if historyRounds == 1 {
						messageCount, firstTool = 5, 2
						if len(payload.Messages) < 2 || payload.Messages[0].Role != "system" || payload.Messages[1].Role != "assistant" {
							t.Errorf("history window did not preserve system and latest round: %+v", payload.Messages)
						}
					} else {
						for _, index := range []int{3, 4} {
							if len(payload.Messages) <= index || payload.Messages[index].Role != "tool" || !strings.Contains(string(payload.Messages[index].Content), "历史截图已省略") || !strings.Contains(string(payload.Messages[index].Content), "Desktop is visible") {
								t.Errorf("old screenshots should become text placeholders: %+v", payload.Messages)
							}
						}
					}
				}
				if len(payload.Messages) != messageCount {
					t.Errorf("expected %d messages, got %+v", messageCount, payload.Messages)
				} else {
					for _, index := range []int{firstTool, firstTool + 1} {
						if payload.Messages[index].Role != "tool" || !strings.Contains(string(payload.Messages[index].Content), "Desktop is visible") {
							t.Errorf("tool reply was not preserved/adapted: %+v", payload.Messages[index])
						}
					}
					images := payload.Messages[messageCount-1]
					if images.Role != "user" || strings.Count(string(images.Content), "data:image/webp;base64,") != 2 {
						t.Errorf("expected two images after both tool replies: %+v", images)
					}
				}
				encoded, _ := json.Marshal(payload.Messages)
				for screenshot := 1; screenshot <= int(toolCalls.Load()); screenshot++ {
					raw := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("screenshot-%d", screenshot)))
					if strings.Contains(string(encoded), raw) {
						t.Error("raw screenshot reached the model")
					}
					data := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("processed-image-%d", screenshot)))
					want := screenshot > int(toolCalls.Load())-2
					if strings.Contains(string(encoded), data) != want {
						t.Errorf("screenshot %d presence should be %v", screenshot, want)
					}
				}
				if call == 2 {
					fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"third","type":"function","function":{"name":"desktop-agent-remote_capture_screen","arguments":"{}"}},{"id":"fourth","type":"function","function":{"name":"desktop-agent-remote_capture_screen","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
					return
				}
				fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","content":"已查看桌面"},"finish_reason":"stop"}]}`)
			}))
			defer modelServer.Close()
			for _, name := range []string{"AGENT_MODEL", "AGENT_LLM_TOKEN"} {
				t.Setenv(name, "")
			}
			for name, value := range map[string]string{"AGENT_LLM_MODEL": "test-model", "AGENT_LLM_API_KEY": "test-key", "AGENT_LLM_BASE_URL": modelServer.URL} {
				t.Setenv(name, value)
			}
			if scenario.noAPIKey {
				if err := os.Unsetenv("AGENT_LLM_API_KEY"); err != nil {
					t.Fatal(err)
				}
			}
			directory := t.TempDir()
			config, err := json.Marshal(mcpprot.Config{Servers: map[string]mcpprot.ServerConfig{
				"desktop-agent-remote": {URL: mcpServer.URL},
				"tiny-image":           {URL: imageMCP.URL},
				// Starting this unrelated server would fail the run.
				"mobile-mcp": {Command: filepath.Join(directory, "must-not-start")},
			}})
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(directory, "mcp.json")
			if err := os.WriteFile(configPath, config, 0600); err != nil {
				t.Fatal(err)
			}
			err = run(context.Background(), options{
				task: "查看远程桌面", configPath: configPath, server: "desktop-agent-remote",
				envPath: filepath.Join(directory, ".env"), maxSteps: maxSteps, historyRounds: historyRounds, timeout: scenario.timeout,
			})
			if maxSteps == 1 {
				if err == nil || !strings.Contains(err.Error(), "调用上限") || modelCalls.Load() != 1 || toolCalls.Load() != 0 {
					t.Fatalf("error=%v models=%d tools=%d", err, modelCalls.Load(), toolCalls.Load())
				}
			} else if err != nil || modelCalls.Load() != 3 || toolCalls.Load() != 4 || uploads.Load() != 4 || resizes.Load() != 4 {
				t.Fatalf("error=%v models=%d tools=%d", err, modelCalls.Load(), toolCalls.Load())
			}
		})
	}
}

func TestRunRejectsNegativeTimeout(t *testing.T) {
	err := run(context.Background(), options{task: "test", maxSteps: 1, timeout: -time.Second})
	if err == nil || !strings.Contains(err.Error(), "timeout must not be negative") {
		t.Fatalf("expected timeout validation error, got %v", err)
	}
}

func TestCapturePreprocessesBeforeReturning(t *testing.T) {
	for _, mode := range []string{"success", "upload-failure", "resize-failure", "retry", "no-image"} {
		t.Run(mode, func(t *testing.T) {
			const rawData = "b3JpZ2luYWwtc2NyZWVuc2hvdA=="
			uploads, resizes := 0, 0
			var uploadPath string
			tools := []loopprot.Tool{
				{Definition: aiprot.ToolDefinition{Name: "desktop_capture_screen"}, Execute: func(context.Context, json.RawMessage) (aiprot.ToolResult, error) {
					return aiprot.ToolResult{Content: []aiprot.ContentPart{
						{Type: aiprot.ContentText, Text: "截图起点 x=100 y=200"},
						{Type: aiprot.ContentText, Text: `{"type":"image","data":"` + rawData + `","mimeType":"image/png"}`},
					}}, nil
				}},
				{Definition: aiprot.ToolDefinition{Name: "tiny-image_upload_image"}, Execute: func(_ context.Context, input json.RawMessage) (aiprot.ToolResult, error) {
					uploads++
					var arguments map[string]string
					if err := json.Unmarshal(input, &arguments); err != nil || arguments["dataBase64"] != "" || arguments["path"] == "" {
						t.Fatalf("expected local file upload, got %s, error=%v", input, err)
					}
					uploadPath = arguments["path"]
					data, err := os.ReadFile(uploadPath)
					if err != nil || base64.StdEncoding.EncodeToString(data) != rawData {
						t.Fatalf("temporary file does not contain the screenshot: %v", err)
					}
					if mode == "upload-failure" {
						return aiprot.ToolResult{IsError: true, Content: []aiprot.ContentPart{{Type: aiprot.ContentText, Text: "storage unavailable"}}}, nil
					}
					// Text and structuredContent may contain duplicate JSON documents.
					metadata := aiprot.ContentPart{Type: aiprot.ContentText, Text: `{"id":"stored-image-1","width":3840,"height":2160,"size":123456}`}
					return aiprot.ToolResult{Content: []aiprot.ContentPart{metadata, metadata}}, nil
				}},
				{Definition: aiprot.ToolDefinition{Name: "tiny-image_resize_image"}, Execute: func(_ context.Context, input json.RawMessage) (aiprot.ToolResult, error) {
					resizes++
					var arguments struct {
						ID                 string `json:"id"`
						Format             string `json:"format"`
						Fit                string `json:"fit"`
						Source             string `json:"source"`
						Width              int    `json:"width"`
						Height             int    `json:"height"`
						Quality            int    `json:"quality"`
						IncludeImage       bool   `json:"includeImage"`
						WithoutEnlargement bool   `json:"withoutEnlargement"`
					}
					if err := json.Unmarshal(input, &arguments); err != nil {
						t.Fatal(err)
					}
					expectedEdge := []int{1920, 1280, 960}[resizes-1]
					expectedQuality := []int{75, 65, 55}[resizes-1]
					if arguments.ID != "stored-image-1" || arguments.Format != "webp" || arguments.Fit != "inside" || arguments.Source != "raw" || !arguments.IncludeImage || !arguments.WithoutEnlargement || arguments.Width != expectedEdge || arguments.Height != expectedEdge || arguments.Quality != expectedQuality {
						t.Fatalf("incorrect preprocessing arguments: %s", input)
					}
					if mode == "resize-failure" {
						return aiprot.ToolResult{IsError: true}, nil
					}
					result := aiprot.TextToolResult(`{"id":"stored-image-1","width":1920,"height":1080,"size":4567}`)
					if mode != "no-image" && !(mode == "retry" && resizes == 1) {
						result.Content = append(result.Content, aiprot.ContentPart{Type: aiprot.ContentText, Text: `{"type":"image","data":"cHJvY2Vzc2Vk","mimeType":"image/webp"}`})
					}
					return result, nil
				}},
			}
			wrapped, err := prepareScreenshots(tools, "desktop_capture_screen")
			if err != nil {
				t.Fatal(err)
			}
			result, err := wrapped[0].Execute(context.Background(), json.RawMessage(`{}`))
			if _, statErr := os.Stat(uploadPath); !os.IsNotExist(statErr) {
				t.Fatalf("temporary screenshot was not removed: %v", statErr)
			}
			if uploads != 1 {
				t.Fatalf("uploads=%d", uploads)
			}
			if strings.Contains(result.Text(), rawData) {
				t.Fatal("raw screenshot base64 reached the model-visible result")
			}
			if mode == "upload-failure" || mode == "resize-failure" || mode == "no-image" {
				if err == nil {
					t.Fatalf("expected preprocessing failure in mode %s", mode)
				}
				if mode == "no-image" && resizes != 3 {
					t.Fatalf("resizes=%d", resizes)
				}
				return
			}
			expectedResizes := 1
			if mode == "retry" {
				expectedResizes = 2
			}
			if resizes != expectedResizes {
				t.Fatalf("resizes=%d", resizes)
			}
			if err != nil || !strings.Contains(result.Text(), "stored-image-1") || !strings.Contains(result.Text(), "3840x2160") || !strings.Contains(result.Text(), "1920x1080") || !strings.Contains(result.Text(), "x=100 y=200") {
				t.Fatalf("preprocessed image or coordinate metadata missing: result=%+v error=%v", result, err)
			}
			found := false
			for _, content := range result.Content {
				if image := mcpImage(content); image != nil {
					found = image.MIMEType == "image/webp" && image.Data == "cHJvY2Vzc2Vk"
				}
			}
			if !found {
				t.Fatal("processed WebP image was not attached")
			}
			if tools[0].Definition.Description != "" {
				t.Fatal("wrapping screenshot tools changed the original tool definition")
			}
		})
	}
}

func TestModelLogsMalformedArgumentsBeforeLoopValidation(t *testing.T) {
	var output bytes.Buffer
	hooks := modelHooks(&output)
	info := aiprot.ResponseInfo{
		Request:   aiprot.ModelRequest{Metadata: map[string]string{loopprot.MetadataStep: "40"}},
		RequestID: "request-40", Duration: 123 * time.Millisecond,
		Response: aiprot.ModelResponse{
			FinishReason: aiprot.FinishLength,
			Usage:        aiprot.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
			Message: aiprot.Message{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{
				Type: aiprot.ContentToolCall, ToolCall: &aiprot.ToolCall{ID: "bad-call", Name: "capture_screen", Arguments: json.RawMessage(`{"mode":`)},
			}}},
		},
	}
	if err := hooks.OnResponse[0](context.Background(), info); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"step=40", "request=request-40", "finish_reason=length", "duration=123ms", "input=100 output=20 total=120", `id="bad-call"`, `name="capture_screen"`, `argument_bytes=8`, `raw_arguments="{\"mode\":"`} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing %q in model log: %s", expected, output.String())
		}
	}
	if !strings.Contains(output.String(), "[model text] step=40\n（模型未返回正文）\n") {
		t.Fatalf("missing empty text indicator: %s", output.String())
	}
	output.Reset()
	info.Response.Message.Parts = []aiprot.ContentPart{
		{Type: aiprot.ContentReasoning, Text: "先查看桌面"},
		{Type: aiprot.ContentText, Text: "已打开添加任务窗口。\n操作完成。"},
	}
	if err := hooks.OnResponse[0](context.Background(), info); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"[model text] step=40\n已打开添加任务窗口。\n操作完成。\n",
		"[model reasoning] step=40\n先查看桌面\n",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing %q in model log: %s", expected, output.String())
		}
	}
}

type recordingProvider struct {
	request aiprot.ModelRequest
}

func (provider *recordingProvider) Name() string { return "recording" }

func (provider *recordingProvider) Generate(_ context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
	provider.request = request
	return aiprot.ModelResponse{}, nil
}

func TestScreenshotReplacementPreservesOriginalHistory(t *testing.T) {
	// A single retained tool result may contain more than two screenshots.
	// Older images are omitted without changing original history or tool text.
	result := aiprot.ToolResult{ToolCallID: "capture", ToolName: "observe", Content: []aiprot.ContentPart{
		{Type: aiprot.ContentText, Text: "窗口标题：迅雷"},
		{Type: aiprot.ContentText, Text: `{"type":"image","data":"Zmlyc3Q=","mimeType":"image/png"}`},
		{Type: aiprot.ContentText, Text: `{"type":"image","data":"c2Vjb25k","mimeType":"image/png"}`},
		{Type: aiprot.ContentText, Text: `{"type":"image","data":"dGhpcmQ=","mimeType":"image/png"}`},
	}}
	request := aiprot.ModelRequest{Model: "test", Messages: []aiprot.Message{
		aiprot.TextMessage(aiprot.RoleUser, "打开迅雷"),
		{Role: aiprot.RoleAssistant, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolCall, ToolCall: &aiprot.ToolCall{ID: "capture", Name: "observe", Arguments: json.RawMessage(`{}`)}}}},
		{Role: aiprot.RoleTool, Parts: []aiprot.ContentPart{{Type: aiprot.ContentToolResult, ToolResult: &result}}},
	}}
	original := request.Clone()
	recorder := &recordingProvider{}
	provider := screenshotProvider{Provider: recorder}
	for iteration := 0; iteration < 2; iteration++ {
		if _, err := provider.Generate(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(request, original) {
			t.Fatal("preparing an outbound request changed the original history")
		}
		messages := recorder.request.Messages
		if len(messages) != 4 || !reflect.DeepEqual(messages[:2], original.Messages[:2]) {
			t.Fatalf("task or assistant tool calls changed: %+v", messages)
		}
		toolResult := messages[2].Parts[0].ToolResult
		if toolResult.ToolCallID != "capture" || toolResult.ToolName != "observe" || toolResult.Content[0].Text != "窗口标题：迅雷" || !strings.Contains(toolResult.Content[1].Text, "历史截图已省略") {
			t.Fatalf("tool result structure/text was not retained: %+v", toolResult)
		}
		images := messages[3].Parts
		if len(images) != 4 || images[1].Media.Data != "c2Vjb25k" || images[3].Media.Data != "dGhpcmQ=" {
			t.Fatalf("expected only the last two images from the retained tool result: %+v", images)
		}
	}
}
