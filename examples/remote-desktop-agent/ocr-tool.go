package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

// Reuse tiny-image's original file so OCR and vision refer to the same frame.
func ocrImageTool(binary string, getImage loopprot.Tool) loopprot.Tool {
	return loopprot.Tool{
		Definition: aiprot.ToolDefinition{
			Name:        "ocr_image",
			Description: "按 image_id 复用 tiny-image 已保存的原图进行 OCR，不重新截图。返回文字、置信度、box 和 center。target 可选，按包含关系匹配，忽略大小写。坐标属于该原始图片像素，结合对应截图起点换算桌面坐标，不使用 WebP 缩放比例。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"image_id":{"type":"string","minLength":1,"description":"已有截图返回的 image_id"},"target":{"type":"string","description":"需要查找的文字；省略时识别全部文字"}},"required":["image_id"],"additionalProperties":false}`),
		},
		Execute: func(ctx context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
			var input struct {
				ImageID string `json:"image_id"`
				Target  string `json:"target"`
			}
			if err := json.Unmarshal(arguments, &input); err != nil {
				return aiprot.ToolResult{}, fmt.Errorf("decode OCR arguments: %w", err)
			}
			if strings.TrimSpace(input.ImageID) == "" {
				return aiprot.ToolResult{}, fmt.Errorf("image_id is required; capture the screen first")
			}
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			lookup, _ := json.Marshal(map[string]string{"id": input.ImageID})
			result, err := getImage.Execute(ctx, lookup)
			if err != nil || result.IsError {
				return result, err
			}
			for _, part := range result.Content {
				var image struct {
					ID      string `json:"id"`
					RawPath string `json:"rawPath"`
					Width   int    `json:"width"`
					Height  int    `json:"height"`
				}
				if part.Type != aiprot.ContentText || json.Unmarshal([]byte(part.Text), &image) != nil || image.ID != input.ImageID || image.RawPath == "" || image.Width <= 0 || image.Height <= 0 {
					continue
				}
				text, err := runOCR(ctx, binary, image.RawPath, input.Target)
				if err != nil {
					return aiprot.ToolResult{}, err
				}
				return aiprot.TextToolResult(fmt.Sprintf("image_id=%s；原图=%dx%d；OCR 坐标属于这张原图，结合对应截图起点及桌面像素比例换算。\n%s", image.ID, image.Width, image.Height, text)), nil
			}
			return aiprot.ToolResult{}, fmt.Errorf("tiny-image returned no original file metadata for %q", input.ImageID)
		},
	}
}

func runOCR(ctx context.Context, binary, imagePath, target string) (string, error) {
	arguments := []string{"-image", imagePath, "-json"}
	if target != "" {
		arguments = append(arguments, "-target", target)
	}
	command := exec.CommandContext(ctx, binary, arguments...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("tiny-ocr: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if !json.Valid(output) {
		return "", fmt.Errorf("tiny-ocr returned invalid JSON")
	}
	return string(output), nil
}
