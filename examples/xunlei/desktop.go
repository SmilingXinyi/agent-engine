package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

func desktopTools(available []loopprot.Tool, display int, output io.Writer) ([]loopprot.Tool, loopprot.Tool, error) {
	var capture, ocr loopprot.Tool
	var tools []loopprot.Tool
	for _, tool := range available {
		switch strings.TrimPrefix(tool.Definition.Name, "desktop-agent-remote_") {
		case "capture_screen":
			capture = tool
		case "list_displays", "screen_scale", "list_windows", "get_window", "focus_window",
			"open_app", "fire_shortcut", "key_tap", "type_text", "mouse_move", "mouse_position", "mouse_click",
			"uia_list_windows", "uia_dump", "uia_find", "uia_element_at", "uia_invoke", "uia_set_value":
			tools = append(tools, tool)
		}
		if tool.Definition.Name == "tiny-ocr-mcp_ocr" {
			ocr = tool
		}
	}
	if capture.Execute == nil || ocr.Execute == nil {
		return nil, loopprot.Tool{}, errors.New("desktop capture_screen and tiny-ocr-mcp ocr tools are required")
	}
	observe := loopprot.Tool{
		Definition: aiprot.ToolDefinition{
			Name:        "observe_desktop",
			Description: "获取当前远程显示器原始截图并立即通过 tiny-ocr-mcp OCR。返回截图元数据、原图尺寸、文字、置信度、box 和 center；坐标为原始截图像素，按显示器起点换算为桌面物理坐标。每次调用都重新截图；无需传入图片路径或 base64。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		},
		Execute: func(ctx context.Context, _ json.RawMessage) (aiprot.ToolResult, error) {
			arguments, _ := json.Marshal(map[string]any{"mode": "display", "display_index": display})
			started := time.Now()
			fmt.Fprintf(output, "[observe capture start] tool=%s display=%d\n", capture.Definition.Name, display)
			frame, err := capture.Execute(ctx, arguments)
			fmt.Fprintf(output, "[observe capture end] duration=%s is_error=%v error=%v\n", time.Since(started).Round(time.Millisecond), frame.IsError, err)
			if err != nil || frame.IsError {
				logToolContent(output, capture.Definition.Name, frame)
				return frame, err
			}
			var metadata []aiprot.ContentPart
			var encoded string
			for _, part := range frame.Content {
				var media struct {
					Type string `json:"type"`
					Data string `json:"data"`
				}
				if part.Type == aiprot.ContentText && json.Unmarshal([]byte(part.Text), &media) == nil && media.Type == "image" {
					encoded = media.Data
					continue
				}
				metadata = append(metadata, part)
			}
			if encoded == "" {
				return aiprot.ToolResult{}, errors.New("capture_screen returned no inline screenshot")
			}
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return aiprot.ToolResult{}, fmt.Errorf("decode screenshot: %w", err)
			}
			// capture_screen promises PNG. Read its IHDR dimensions without
			// decoding the full desktop image a second time.
			if len(data) < 24 || !bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) || string(data[12:16]) != "IHDR" {
				return aiprot.ToolResult{}, errors.New("capture_screen returned an invalid PNG header")
			}
			width, height := binary.BigEndian.Uint32(data[16:20]), binary.BigEndian.Uint32(data[20:24])
			if width == 0 || height == 0 {
				return aiprot.ToolResult{}, errors.New("capture_screen returned zero image dimensions")
			}
			// The screenshot belongs to the remote backend. Materialize its bytes
			// locally so OCR never tries to read a path on the remote Windows host.
			file, err := os.CreateTemp("", "xunlei-capture-*.png")
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			defer os.Remove(file.Name())
			_, writeErr := file.Write(data)
			if err := errors.Join(writeErr, file.Close()); err != nil {
				return aiprot.ToolResult{}, err
			}
			arguments, _ = json.Marshal(map[string]string{"image_path": file.Name()})
			fmt.Fprintf(output, "[observe OCR start] tool=%s image=%dx%d bytes=%d\n", ocr.Definition.Name, width, height, len(data))
			started = time.Now()
			recognized, err := ocr.Execute(ctx, arguments)
			fmt.Fprintf(output, "[observe OCR end] duration=%s is_error=%v error=%v\n", time.Since(started).Round(time.Millisecond), recognized.IsError, err)
			if err != nil || recognized.IsError {
				return recognized, err
			}
			result := aiprot.TextToolResult(fmt.Sprintf("当前远程显示器截图 OCR；原图=%dx%d；未缩放。\n", width, height))
			result.Content = append(result.Content, metadata...)
			// Only textual OCR and capture metadata enter model history.
			result.Content = append(result.Content, recognized.Content...)
			return result, nil
		},
	}
	return append(tools, observe), observe, nil
}

func verifyDownloadTask(ctx context.Context, observe loopprot.Tool, filenames []string, output io.Writer) error {
	lastIssue := "未在最新截图 OCR 中找到对应文件名"
	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprintf(output, "[verify start] attempt=%d filenames=%q\n", attempt+1, filenames)
		result, err := observe.Execute(ctx, json.RawMessage(`{}`))
		fmt.Fprintf(output, "[verify result] attempt=%d is_error=%v error=%v\n", attempt+1, result.IsError, err)
		logToolContent(output, "final verification", result)
		if err != nil {
			return fmt.Errorf("最终界面验证失败：%w", err)
		}
		if result.IsError {
			return errors.New("最终截图或 OCR 返回工具错误，无法确认下载任务")
		}
		for _, part := range result.Content {
			// Read actual OCR text, not metadata or the model's final assertion.
			var recognized struct {
				Text string `json:"text"`
			}
			if json.Unmarshal([]byte(part.Text), &recognized) != nil {
				continue
			}
			text := normalizeText(recognized.Text)
			// The main-window search placeholder also contains “贴链接”.
			// Only use task-dialog markers to reject an unsubmitted filename.
			if strings.Contains(text, "立即下载") || strings.Contains(text, "添加多个下载链接") {
				lastIssue = "新建任务输入界面仍可见，无法确认下载已提交"
				continue
			}
			hasFilename := false
			for _, filename := range filenames {
				if strings.Contains(text, normalizeText(filename)) {
					hasFilename = true
				}
			}
			if !hasFilename {
				continue
			}
			for _, failure := range []string{"下载失败", "链接失效", "无效链接", "任务出错", "HTTP403", "HTTP401", "403FORBIDDEN", "401UNAUTHORIZED"} {
				if strings.Contains(text, failure) {
					return fmt.Errorf("列表包含目标文件，但界面出现 %s 提示，无法确认下载正常", failure)
				}
			}
			fmt.Fprintln(output, "[verify matched] filename found; no detected input dialog or download error")
			return nil
		}
		fmt.Fprintf(output, "[verify pending] attempt=%d reason=%s\n", attempt+1, lastIssue)
		if attempt < 2 {
			timer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("下载任务未确认：%s；请检查迅雷界面或 OCR 识别结果", lastIssue)
}

func normalizeText(text string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsSpace(character) {
			return -1
		}
		return unicode.ToUpper(character)
	}, text)
}
