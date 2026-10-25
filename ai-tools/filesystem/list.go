package filesystem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/utils"
)

func NewListTool(options protocol.FilesystemOptions) (loopprot.Tool, error) {
	options, err := prepareOptions(options)
	if err != nil {
		return loopprot.Tool{}, err
	}
	return loopprot.Tool{
		Definition: aiprot.ToolDefinition{
			Name:        protocol.ListDirectory,
			Description: fmt.Sprintf("List directory entries sorted by name, relative to the configured root. Use '.' for the root. Maximum %d entries; larger directories return an error.", options.MaxEntries),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1}},"required":["path"],"additionalProperties":false}`),
		},
		Execute: func(ctx context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
			var input struct {
				Path string `json:"path"`
			}
			if err := utils.DecodeObject(arguments, &input); err != nil {
				return aiprot.ToolResult{}, fmt.Errorf("list_directory arguments: %w", err)
			}
			root, err := openRoot(ctx, options, input.Path)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			defer root.Close()
			directory, err := root.Open(input.Path)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			defer directory.Close()
			// Reading one extra entry detects oversized directories without loading
			// an unbounded listing or returning a misleading partial result.
			entries, err := directory.ReadDir(options.MaxEntries + 1)
			if err != nil && !errors.Is(err, io.EOF) {
				return aiprot.ToolResult{}, err
			}
			if len(entries) > options.MaxEntries {
				return aiprot.ToolResult{}, fmt.Errorf("directory exceeds maxEntries (%d)", options.MaxEntries)
			}
			listing := make([]protocol.DirectoryEntry, 0, len(entries))
			for _, entry := range entries {
				kind := "other"
				switch {
				case entry.Type()&os.ModeSymlink != 0:
					kind = "symlink"
				case entry.IsDir():
					kind = "directory"
				case entry.Type().IsRegular():
					kind = "file"
				}
				listing = append(listing, protocol.DirectoryEntry{Name: entry.Name(), Type: kind})
			}
			sort.Slice(listing, func(first, second int) bool { return listing[first].Name < listing[second].Name })
			result, err := json.Marshal(listing)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			return aiprot.TextToolResult(string(result)), ctx.Err()
		},
	}, nil
}
