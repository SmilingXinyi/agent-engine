package filesystem

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/utils"
)

func NewReadTool(options protocol.FilesystemOptions) (loopprot.Tool, error) {
	options, err := prepareOptions(options)
	if err != nil {
		return loopprot.Tool{}, err
	}
	return loopprot.Tool{
		Definition: aiprot.ToolDefinition{
			Name:        protocol.ReadFile,
			Description: fmt.Sprintf("Read a UTF-8 text file by path relative to the configured root. Maximum %d bytes; oversized files return an error.", options.MaxReadBytes),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1}},"required":["path"],"additionalProperties":false}`),
		},
		Execute: func(ctx context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
			var input struct {
				Path string `json:"path"`
			}
			if err := utils.DecodeObject(arguments, &input); err != nil {
				return aiprot.ToolResult{}, fmt.Errorf("read_file arguments: %w", err)
			}
			root, err := openRoot(ctx, options, input.Path)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			defer root.Close()
			info, err := root.Stat(input.Path)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			if !info.Mode().IsRegular() {
				return aiprot.ToolResult{}, fmt.Errorf("read_file requires a regular file")
			}
			file, err := root.Open(input.Path)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			defer file.Close()
			content, err := io.ReadAll(io.LimitReader(file, int64(options.MaxReadBytes)+1))
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			if len(content) > options.MaxReadBytes {
				return aiprot.ToolResult{}, fmt.Errorf("file exceeds maxReadBytes (%d)", options.MaxReadBytes)
			}
			if !utf8.Valid(content) {
				return aiprot.ToolResult{}, fmt.Errorf("file is not valid UTF-8 text")
			}
			return aiprot.TextToolResult(string(content)), ctx.Err()
		},
	}, nil
}
