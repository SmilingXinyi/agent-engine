package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

func openRunLog(path string) (io.Writer, func() error, error) {
	if path == "" {
		return os.Stdout, func() error { return nil }, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, nil, err
	}
	return io.MultiWriter(os.Stdout, file), file.Close, nil
}

// Log responses before tool-call validation, including malformed arguments.
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
				fmt.Fprintf(output, "[model tool call] step=%s id=%q name=%q status=requested argument_bytes=%d raw_arguments=%q\n", info.Request.Metadata[loopprot.MetadataStep], call.ID, call.Name, len(call.Arguments), string(call.Arguments))
			}
			return nil
		},
	}}
}

func executionHooks(output io.Writer) loopprot.Hooks {
	return loopprot.Hooks{
		OnRunStart: func(_ context.Context, info loopprot.RunStartInfo) error {
			confirmation := "off"
			if info.Request.Options.ConfirmTool != nil {
				confirmation = string(info.Request.Options.ToolDenialPolicy)
			}
			fmt.Fprintf(output, "[run start] id=%s confirmation=%s\n", info.RunID, confirmation)
			return nil
		},
		OnStepStart: func(_ context.Context, info loopprot.StepStartInfo) error {
			fmt.Fprintf(output, "\n[step %d] request=%s history_messages=%d\n", info.Step, info.RequestID, len(info.Request.Messages))
			return nil
		},
		OnToolStart: func(_ context.Context, info loopprot.ToolStartInfo) error {
			fmt.Fprintf(output, "[tool start] step=%d request=%s id=%q name=%s status=pending\narguments=%s\n", info.Step, info.RequestID, info.Call.ID, info.Call.Name, printableJSON(info.Call.Arguments))
			return nil
		},
		OnToolEnd: func(_ context.Context, info loopprot.ToolEndInfo) error {
			if info.Confirmation != nil {
				fmt.Fprintf(output, "[tool decision] id=%q approved=%v reason=%q\n", info.Call.ID, info.Confirmation.Approved, info.Confirmation.Reason)
			}
			fmt.Fprintf(output, "[tool end] step=%d id=%q name=%s is_error=%v duration=%s error=%v\n", info.Step, info.Call.ID, info.Call.Name, info.Result.IsError, info.Duration.Round(time.Millisecond), info.Error)
			logToolContent(output, info.Call.Name+" id="+info.Call.ID, info.Result)
			return nil
		},
		OnRunEnd: func(_ context.Context, info loopprot.RunEndInfo) error {
			fmt.Fprintf(output, "[run end] id=%s stop=%s steps=%d tool_calls=%d tokens=%d duration=%s error=%v\n", info.RunID, info.Result.StopReason, len(info.Result.Steps), len(info.Result.ToolCalls), info.Result.Usage.TotalTokens, info.Duration.Round(time.Millisecond), info.Error)
			handled := make(map[string]bool)
			for _, record := range info.Result.ToolCalls {
				if record.Step == len(info.Result.Steps) {
					handled[record.Call.ID] = true
				}
			}
			for _, call := range info.Result.LastResponse.Message.ToolCalls() {
				if !handled[call.ID] {
					fmt.Fprintf(output, "[tool not executed] id=%q name=%s stop=%s\n", call.ID, call.Name, info.Result.StopReason)
				}
			}
			return nil
		},
	}
}

// Include textual tool results and errors alongside image payload sizes.
// Omit image bytes from the log without altering the actual tool response.
func logToolContent(output io.Writer, label string, result aiprot.ToolResult) {
	if len(result.Content) == 0 {
		fmt.Fprintf(output, "[tool content] %s (empty)\n", label)
	}
	for index, part := range result.Content {
		text := part.Text
		if part.Type != aiprot.ContentText {
			data, _ := json.Marshal(part)
			text = string(data)
		}
		fmt.Fprintf(output, "[tool content] %s part=%d type=%s\n%s\n", label, index+1, part.Type, printableJSON([]byte(text)))
	}
	if len(result.Metadata) > 0 {
		data, _ := json.Marshal(result.Metadata)
		fmt.Fprintf(output, "[tool metadata] %s\n%s\n", label, printableJSON(data))
	}
}

func printableJSON(data []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return string(data)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return string(data)
	}
	formatted, err := json.MarshalIndent(omitBinary(value), "", "  ")
	if err != nil {
		return string(data)
	}
	return string(formatted)
}

func omitBinary(value any) any {
	switch object := value.(type) {
	case map[string]any:
		for key, child := range object {
			// Covers MCP image content and provider-neutral MediaContent.
			binaryContent := object["type"] == "image" || object["type"] == "audio" || object["mime_type"] != nil || object["mimeType"] != nil
			if encoded, ok := child.(string); ok && ((key == "data" && binaryContent) || key == "image_base64") {
				object[key] = fmt.Sprintf("[binary data omitted: %d encoded bytes]", len(encoded))
			} else {
				object[key] = omitBinary(child)
			}
		}
	case []any:
		for index, child := range object {
			object[index] = omitBinary(child)
		}
	}
	return value
}
