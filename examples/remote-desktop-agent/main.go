// Run from the workspace root: go run ./examples/remote-desktop-agent/main.go ./examples/remote-desktop-agent/ocr-tool.go -task "你的任务"
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/SmilingXinyi/agent-engine/agent"
	agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
	ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/providers/openai"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	mcpruntime "github.com/SmilingXinyi/agent-engine/mcp-runtime"
	mcpprot "github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
)

type options struct {
	task          string
	configPath    string
	server        string
	envPath       string
	maxSteps      int
	historyRounds int
	timeout       time.Duration
}

func main() {
	var settings options
	flag.StringVar(&settings.task, "task", "", "task to execute on the remote desktop (required)")
	flag.StringVar(&settings.configPath, "config", "examples/.agents/mcp.json", "MCP configuration file")
	flag.StringVar(&settings.server, "server", "desktop-agent-remote", "remote desktop MCP server name")
	flag.StringVar(&settings.envPath, "env", ".env", "model environment file")
	flag.IntVar(&settings.maxSteps, "max-steps", 40, "maximum model calls, including the final answer")
	flag.IntVar(&settings.historyRounds, "history-rounds", loopprot.DefaultMaxHistoryRounds, "maximum history rounds sent to the model; system messages are always retained")
	flag.DurationVar(&settings.timeout, "timeout", 0, "total execution timeout; 0 disables the time limit")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, settings); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, settings options) (err error) {
	if strings.TrimSpace(settings.task) == "" {
		return errors.New("请通过 -task 指定任务，例如：-task '查看远程桌面当前打开的应用'")
	}
	if settings.maxSteps <= 0 {
		return errors.New("max-steps must be positive")
	}
	if settings.timeout < 0 {
		return errors.New("timeout must not be negative; 0 disables the time limit")
	}
	if settings.historyRounds < 0 {
		return errors.New("history-rounds must not be negative")
	}
	if err := loadEnv(settings.envPath); err != nil {
		return err
	}
	model := firstEnv("AGENT_MODEL", "AGENT_LLM_MODEL")
	token := firstEnv("AGENT_LLM_TOKEN", "AGENT_LLM_API_KEY")
	baseURL := os.Getenv("AGENT_LLM_BASE_URL")
	if model == "" || baseURL == "" {
		return errors.New("请在 .env 或进程环境中设置 AGENT_LLM_MODEL 和 AGENT_LLM_BASE_URL；AGENT_LLM_API_KEY 可选")
	}
	provider, err := openai.New(openai.Config{BaseURL: baseURL, APIKey: token})
	if err != nil {
		return err
	}
	config, err := mcpprot.LoadConfig(settings.configPath)
	if err != nil {
		return err
	}
	server, exists := config.Servers[settings.server]
	if !exists || server.Disabled {
		return fmt.Errorf("MCP server %q is missing or disabled in %s", settings.server, settings.configPath)
	}
	// Start the selected desktop server and the optional local image service.
	selected := map[string]mcpprot.ServerConfig{settings.server: server}
	if imageServer, exists := config.Servers["tiny-image"]; exists && !imageServer.Disabled {
		selected["tiny-image"] = imageServer
	}
	config.Servers = selected
	if settings.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, settings.timeout)
		defer cancel()
	}
	runtime, err := mcpruntime.Start(ctx, config)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, runtime.Close()) }()
	if len(runtime.Tools) == 0 {
		return errors.New("remote desktop MCP returned no enabled tools")
	}
	tools, err := prepareScreenshots(runtime.Tools, settings.server+"_capture_screen")
	if err != nil {
		return err
	}
	ocrEnabled := false
	for _, tool := range runtime.Tools {
		if tool.Definition.Name == "tiny-image_get_image" {
			tools = append(tools, ocrImageTool("./examples/tiny-ocr-cli", tool))
			ocrEnabled = true
			break
		}
	}
	fmt.Printf("Model: %s; Base URL: %s; MCP: %s; tools: %d\nTask: %s\n", model, baseURL, strings.Join(config.ServerNames(), ", "), len(tools), settings.task)
	if ocrEnabled {
		fmt.Println("[local tool] ocr_image registered (image_id, target)")
	} else {
		fmt.Println("[local tool] ocr_image unavailable: tiny-image_get_image is missing")
	}
	client := ai.NewClient(screenshotProvider{Provider: provider}, modelHooks(os.Stdout))
	result, err := agent.New(client).Execute(ctx, agentprot.Request{
		Model: model,
		SystemPrompt: "你是远程桌面执行助手。使用 remote-desktop-agent 提供的 MCP 工具完成用户任务。" +
			"先观察当前桌面，再根据实际界面和工具结果操作；页面变化后重新观察，不猜测元素或坐标。" +
			"启用 tiny-image 时，程序已在截图返回前自动缩放、压缩并转换为 WebP；你收到的是处理后的图片，可以直接观察，无需先调用压缩工具。" +
			"需要精细文字时优先截取或裁剪目标区域。缩放或裁剪后，根据原图尺寸、返回尺寸和截图起点换算真实桌面坐标。" +
			"若有 ocr_image 工具，识别或定位按钮文字时优先传入截图返回的 image_id 和可选 target 调用它；OCR 使用已有原图，不重新截图。返回坐标属于原图像素，结合对应截图起点及桌面像素比例换算，不再套用压缩图缩放比例。" +
			"始终通过图片 id 复用图片，不自行生成或复制 base64，不将远程电脑路径作为本地 upload_image 的 path。" +
			"任务完成后检查实际结果，用中文简要报告；遇到无法解决的阻碍时如实说明，不宣称未验证的成功。",
		UserPrompt: settings.task,
		Tools:      tools,
		Options: loopprot.RunOptions{MaxSteps: settings.maxSteps, MaxHistoryRounds: settings.historyRounds, Hooks: loopprot.Hooks{
			OnStepStart: func(_ context.Context, info loopprot.StepStartInfo) error {
				fmt.Printf("[step %d] 请求模型\n", info.Step)
				return nil
			},
			OnToolStart: func(_ context.Context, info loopprot.ToolStartInfo) error {
				fmt.Printf("[tool] %s %s\n", info.Call.Name, info.Call.Arguments)
				return nil
			},
			OnToolEnd: func(_ context.Context, info loopprot.ToolEndInfo) error {
				fmt.Printf("[tool result] %s error=%v duration=%s\n", info.Call.Name, info.Result.IsError, info.Duration.Round(time.Millisecond))
				if info.Result.IsError {
					fmt.Println(info.Result.Text())
				}
				return nil
			},
		}},
	})
	fmt.Printf("\n%s\nStop: %s; steps: %d; tokens: %d\n", result.LastResponse.Message.Text(), result.StopReason, len(result.Steps), result.Usage.TotalTokens)
	if err == nil && result.StopReason == loopprot.StopMaxSteps {
		return errors.New("已达到模型调用上限，任务可能尚未完成；可通过 -max-steps 增加上限")
	}
	return err
}

// Log model output before the loop validates it. ToolStart only reports calls
// that passed validation, so it cannot diagnose malformed model arguments.
func modelHooks(output io.Writer) aiprot.Hooks {
	return aiprot.Hooks{OnResponse: []aiprot.ResponseHook{
		func(_ context.Context, info aiprot.ResponseInfo) error {
			response := info.Response
			fmt.Fprintf(output, "[model result] step=%s request=%s finish_reason=%s duration=%s input=%d output=%d total=%d error=%v\n",
				info.Request.Metadata[loopprot.MetadataStep], info.RequestID, response.FinishReason, info.Duration.Round(time.Millisecond),
				response.Usage.InputTokens, response.Usage.OutputTokens, response.Usage.TotalTokens, info.Error)
			text := response.Message.Text()
			if text == "" {
				text = "（模型未返回正文）"
			}
			fmt.Fprintf(output, "[model text] step=%s\n%s\n", info.Request.Metadata[loopprot.MetadataStep], text)
			if reasoning := response.Message.Reasoning(); reasoning != "" {
				fmt.Fprintf(output, "[model reasoning] step=%s\n%s\n", info.Request.Metadata[loopprot.MetadataStep], reasoning)
			}
			for _, call := range response.Message.ToolCalls() {
				fmt.Fprintf(output, "[model tool call] id=%q name=%q argument_bytes=%d raw_arguments=%q\n", call.ID, call.Name, len(call.Arguments), string(call.Arguments))
			}
			return nil
		},
	}}
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

// Load simple dotenv assignments without executing a shell. Process variables win.
func loadEnv(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			return fmt.Errorf("invalid assignment in environment file %q", path)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if strings.HasPrefix(value, "\"") {
			value, err = strconv.Unquote(value)
			if err != nil {
				return fmt.Errorf("invalid quoted value for environment variable %s", key)
			}
		} else if len(value) >= 2 && strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
			value = value[1 : len(value)-1]
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// Finish image preprocessing inside the capture tool, before any model request.
func prepareScreenshots(tools []loopprot.Tool, captureName string) ([]loopprot.Tool, error) {
	var upload, resize func(context.Context, json.RawMessage) (aiprot.ToolResult, error)
	for _, tool := range tools {
		switch tool.Definition.Name {
		case "tiny-image_upload_image":
			upload = tool.Execute
		case "tiny-image_resize_image":
			resize = tool.Execute
		}
	}
	if upload == nil && resize == nil {
		return tools, nil
	}
	if upload == nil || resize == nil {
		return nil, errors.New("tiny-image_upload_image and tiny-image_resize_image are required for screenshot preprocessing")
	}
	tools = append([]loopprot.Tool(nil), tools...)
	for index, tool := range tools {
		if tool.Definition.Name != captureName {
			continue
		}
		tools[index].Definition.Description += "\n返回前已自动缩放、压缩并转换为 WebP。请直接观察返回的图片，根据原图/处理后尺寸及截图起点换算桌面坐标。"
		execute := tool.Execute
		tools[index].Execute = func(ctx context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
			result, err := execute(ctx, arguments)
			if err != nil || result.IsError {
				return result, err
			}
			var processed []aiprot.ContentPart
			for _, content := range result.Content {
				media := mcpImage(content)
				if media == nil {
					processed = append(processed, content)
					continue
				}
				image, err := preprocessImage(ctx, media, upload, resize)
				if err != nil {
					// Never fall back to sending the unprocessed screenshot.
					return aiprot.ToolResult{}, err
				}
				processed = append(processed, image.Content...)
			}
			result.Content = processed
			return result, nil
		}
	}
	return tools, nil
}

type imageInfo struct {
	ID     string `json:"id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int    `json:"size"`
}

func readImageInfo(result aiprot.ToolResult) (imageInfo, error) {
	// MCP runtime retains text and structuredContent as separate JSON text
	// parts. Decode each part independently instead of joining JSON documents.
	for _, content := range result.Content {
		var info imageInfo
		if content.Type == aiprot.ContentText && json.Unmarshal([]byte(content.Text), &info) == nil && info.ID != "" && info.Width > 0 && info.Height > 0 {
			return info, nil
		}
	}
	return imageInfo{}, errors.New("tiny-image returned no image id or dimensions")
}

func preprocessImage(ctx context.Context, media *aiprot.MediaContent, upload, resize func(context.Context, json.RawMessage) (aiprot.ToolResult, error)) (aiprot.ToolResult, error) {
	// tiny-image's stdio input is bounded too. Pass a local temporary path
	// rather than embedding the full original base64 in another RPC frame.
	file, err := os.CreateTemp("", "agent-engine-capture-*")
	if err != nil {
		return aiprot.ToolResult{}, err
	}
	defer os.Remove(file.Name())
	_, copyErr := io.Copy(file, base64.NewDecoder(base64.StdEncoding, strings.NewReader(media.Data)))
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return aiprot.ToolResult{}, err
	}
	input, err := json.Marshal(map[string]string{"path": file.Name(), "filename": "desktop-capture"})
	if err != nil {
		return aiprot.ToolResult{}, err
	}
	uploaded, err := upload(ctx, input)
	if err != nil || uploaded.IsError {
		return aiprot.ToolResult{}, errors.Join(fmt.Errorf("upload screenshot to tiny-image: %s", uploaded.Text()), err)
	}
	original, err := readImageInfo(uploaded)
	if err != nil {
		return aiprot.ToolResult{}, err
	}
	for _, settings := range []struct{ edge, quality int }{{1920, 75}, {1280, 65}, {960, 55}} {
		arguments, err := json.Marshal(map[string]any{
			"id": original.ID, "width": settings.edge, "height": settings.edge,
			"fit": "inside", "withoutEnlargement": true, "format": "webp",
			"quality": settings.quality, "source": "raw", "includeImage": true,
		})
		if err != nil {
			return aiprot.ToolResult{}, err
		}
		resized, err := resize(ctx, arguments)
		if err != nil || resized.IsError {
			return aiprot.ToolResult{}, errors.Join(fmt.Errorf("preprocess screenshot with tiny-image: %s", resized.Text()), err)
		}
		for _, content := range resized.Content {
			image := mcpImage(content)
			if image == nil || image.MIMEType != "image/webp" {
				continue
			}
			output, err := readImageInfo(resized)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			mapping := fmt.Sprintf("截图已自动缩放压缩为 WebP。image_id=%s；原图=%dx%d；处理后=%dx%d；尺寸=%d bytes。图片坐标乘以原图/处理后尺寸，再加原截图起点，得到桌面坐标。", original.ID, original.Width, original.Height, output.Width, output.Height, output.Size)
			fmt.Printf("[image preprocess] %dx%d %d bytes -> %dx%d %d bytes webp\n", original.Width, original.Height, original.Size, output.Width, output.Height, output.Size)
			return aiprot.ToolResult{Content: []aiprot.ContentPart{{Type: aiprot.ContentText, Text: mapping}, content}}, nil
		}
	}
	return aiprot.ToolResult{}, errors.New("tiny-image returned no WebP image after resizing; raw screenshot was not sent")
}

func mcpImage(content aiprot.ContentPart) *aiprot.MediaContent {
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

// MCP runtime serializes non-text content as JSON text. Send screenshot data as
// user images so a vision model can see it. Chat Completions tool replies only
// accept text; insert images after the entire batch of tool replies. Within the
// loop's history window, only the two latest screenshots are sent as images.
type screenshotProvider struct {
	aiprot.Provider
}

func (provider screenshotProvider) Generate(ctx context.Context, request aiprot.ModelRequest) (aiprot.ModelResponse, error) {
	const maxRecentScreenshots = 2
	type screenshotReference struct {
		messageIndex int
		content      *aiprot.ContentPart
		media        *aiprot.MediaContent
		label        string
	}
	messages := make([]aiprot.Message, len(request.Messages))
	var recent []screenshotReference
	omitted := 0
	for index, original := range request.Messages {
		message := original.Clone()
		if message.Role == aiprot.RoleTool {
			for _, part := range message.Parts {
				if part.ToolResult == nil {
					continue
				}
				for contentIndex, content := range part.ToolResult.Content {
					media := mcpImage(content)
					if media == nil {
						continue
					}
					label := fmt.Sprintf("截图来自工具 %s（调用 %s）", part.ToolResult.ToolName, part.ToolResult.ToolCallID)
					content := &part.ToolResult.Content[contentIndex]
					*content = aiprot.ContentPart{Type: aiprot.ContentText, Text: label + "：历史截图已省略，仅保留最近两张截图；画面内容请参考已有文字记录。"}
					reference := screenshotReference{
						messageIndex: index, content: content, label: label,
						media: media,
					}
					if len(recent) == maxRecentScreenshots {
						recent[0], recent[1] = recent[1], reference
						omitted++
					} else {
						recent = append(recent, reference)
					}
				}
			}
		}
		messages[index] = message
	}
	outbound := make([]aiprot.Message, 0, len(messages)+len(recent))
	var images []aiprot.ContentPart
	nextScreenshot := 0
	for index, message := range messages {
		for nextScreenshot < len(recent) && recent[nextScreenshot].messageIndex == index {
			screenshot := recent[nextScreenshot]
			screenshot.content.Text = screenshot.label + "，图像附在后续消息中。"
			images = append(images,
				aiprot.ContentPart{Type: aiprot.ContentText, Text: screenshot.label},
				aiprot.ContentPart{Type: aiprot.ContentImage, Media: screenshot.media},
			)
			nextScreenshot++
		}
		outbound = append(outbound, message)
		if len(images) > 0 && (index+1 == len(messages) || messages[index+1].Role != aiprot.RoleTool) {
			outbound = append(outbound, aiprot.Message{Role: aiprot.RoleUser, Parts: images})
			images = nil
		}
	}
	if len(recent) > 0 {
		fmt.Printf("[images] retained=%d omitted=%d\n", len(recent), omitted)
	}
	request.Messages = outbound
	return provider.Provider.Generate(ctx, request)
}
