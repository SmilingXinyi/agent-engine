package filesystem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/utils"
)

func NewWriteTool(options protocol.FilesystemOptions) (loopprot.Tool, error) {
	options, err := prepareOptions(options)
	if err != nil {
		return loopprot.Tool{}, err
	}
	return loopprot.Tool{
		Definition: aiprot.ToolDefinition{
			Name:        protocol.WriteFile,
			Description: fmt.Sprintf("Create or overwrite a UTF-8 text file relative to the configured root. Parent directory must exist. Maximum %d bytes.", options.MaxWriteBytes),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`),
		},
		Execute: func(ctx context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
			var input struct {
				Path    string  `json:"path"`
				Content *string `json:"content"`
			}
			if err := utils.DecodeObject(arguments, &input); err != nil {
				return aiprot.ToolResult{}, fmt.Errorf("write_file arguments: %w", err)
			}
			if input.Content == nil {
				return aiprot.ToolResult{}, fmt.Errorf("content is required and must be a string")
			}
			if len(*input.Content) > options.MaxWriteBytes {
				return aiprot.ToolResult{}, fmt.Errorf("content exceeds maxWriteBytes (%d)", options.MaxWriteBytes)
			}
			root, err := openRoot(ctx, options, input.Path)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			defer root.Close()
			info, err := root.Stat(input.Path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return aiprot.ToolResult{}, err
			}
			if err == nil && !info.Mode().IsRegular() {
				return aiprot.ToolResult{}, fmt.Errorf("write_file requires a regular file")
			}
			if err := ctx.Err(); err != nil {
				return aiprot.ToolResult{}, err
			}
			if err := root.WriteFile(input.Path, []byte(*input.Content), 0600); err != nil {
				return aiprot.ToolResult{}, err
			}
			result, err := json.Marshal(map[string]any{"path": input.Path, "bytes_written": len(*input.Content)})
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			return aiprot.TextToolResult(string(result)), nil
		},
	}, nil
}
