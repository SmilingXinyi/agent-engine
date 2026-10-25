package filesystem

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/utils"
)

// NewPatchTool applies single-file unified diffs to existing regular UTF-8 files.
// All hunks are validated before a same-directory temporary file replaces the
// original. Calls to the same tool are serialized; unrelated writers must still
// coordinate access. Final-component symlinks are rejected, not replaced.
func NewPatchTool(options protocol.FilesystemOptions) (loopprot.Tool, error) {
	options, err := prepareOptions(options)
	if err != nil {
		return loopprot.Tool{}, err
	}
	gate := make(chan struct{}, 1)
	return loopprot.Tool{
		Definition: aiprot.ToolDefinition{
			Name:        protocol.ApplyPatch,
			Description: fmt.Sprintf("Modify one existing UTF-8 file relative to the configured root using a unified diff. patch must contain @@ -old_start,old_count +new_start,new_count @@ hunks; prefix context lines with a space, removals with -, additions with +. Optional ---/+++ headers must name path (a/ and b/ prefixes allowed). Line numbers are preferred; shifted context must match uniquely. No fuzzy matching, create/delete/rename, multi-file patches or final symlinks. All hunks are checked before replacing the file. Maximum patch %d bytes, source %d bytes, result %d bytes.", options.MaxPatchBytes, options.MaxReadBytes, options.MaxWriteBytes),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"patch":{"type":"string","minLength":1}},"required":["path","patch"],"additionalProperties":false}`),
		},
		Execute: func(ctx context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
			var input struct {
				Path  string  `json:"path"`
				Patch *string `json:"patch"`
			}
			if err := utils.DecodeObject(arguments, &input); err != nil {
				return aiprot.ToolResult{}, fmt.Errorf("apply_patch arguments: %w", err)
			}
			if input.Patch == nil || *input.Patch == "" {
				return aiprot.ToolResult{}, fmt.Errorf("patch is required and must be a non-empty string")
			}
			if len(*input.Patch) > options.MaxPatchBytes {
				return aiprot.ToolResult{}, fmt.Errorf("patch exceeds maxPatchBytes (%d)", options.MaxPatchBytes)
			}
			select {
			case gate <- struct{}{}:
				defer func() { <-gate }()
			case <-ctx.Done():
				return aiprot.ToolResult{}, ctx.Err()
			}
			root, err := openRoot(ctx, options, input.Path)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			defer root.Close()
			// Pin the actual parent directory so a symlink in the parent path cannot
			// redirect temporary-file creation and commit to different directories.
			parentPath, filename := filepath.Split(input.Path)
			if parentPath == "" {
				parentPath = "."
			}
			parent, err := root.OpenRoot(parentPath)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			defer parent.Close()
			source, err := readPatchSource(parent, filename, options.MaxReadBytes)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			hunks, err := parsePatch(*input.Patch, input.Path)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			updated, err := applyHunks(ctx, source.content, hunks, options.MaxWriteBytes)
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			if bytes.Equal(updated, source.content) {
				return aiprot.ToolResult{}, fmt.Errorf("patch does not change the file")
			}
			if err := replacePatchedFile(ctx, parent, filename, source, updated, options.MaxReadBytes); err != nil {
				return aiprot.ToolResult{}, err
			}
			content, err := json.Marshal(protocol.PatchResult{Path: input.Path, HunksApplied: len(hunks), BytesWritten: len(updated)})
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			return aiprot.TextToolResult(string(content)), nil
		},
	}, nil
}

type patchSource struct {
	content []byte
	info    os.FileInfo
}

func readPatchSource(root *os.Root, path string, limit int) (patchSource, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return patchSource{}, err
	}
	if !info.Mode().IsRegular() {
		return patchSource{}, fmt.Errorf("apply_patch requires an existing regular file, not a symlink or directory")
	}
	file, err := root.Open(path)
	if err != nil {
		return patchSource{}, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return patchSource{}, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return patchSource{}, fmt.Errorf("file changed while opening for patch")
	}
	content, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return patchSource{}, err
	}
	if len(content) > limit {
		return patchSource{}, fmt.Errorf("file exceeds maxReadBytes (%d)", limit)
	}
	if !utf8.Valid(content) {
		return patchSource{}, fmt.Errorf("file is not valid UTF-8 text")
	}
	return patchSource{content: content, info: openedInfo}, nil
}

func replacePatchedFile(ctx context.Context, root *os.Root, path string, source patchSource, updated []byte, readLimit int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	temporaryPath := filepath.Join(filepath.Dir(path), ".apply-patch-"+rand.Text())
	file, err := root.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporaryPath)
	// Close on all error paths before the deferred removal (needed on Windows).
	writeErr := func() (err error) {
		defer func() { err = errors.Join(err, file.Close()) }()
		if _, err := file.Write(updated); err != nil {
			return err
		}
		if err := file.Chmod(source.info.Mode().Perm()); err != nil {
			return err
		}
		return file.Sync()
	}()
	if writeErr != nil {
		return writeErr
	}
	// Recheck identity and bytes before commit. This catches ordinary intervening
	// edits, but is not an OS-level compare-and-swap against external writers.
	current, err := readPatchSource(root, path, readLimit)
	if err != nil {
		return err
	}
	if !os.SameFile(source.info, current.info) || source.info.Mode() != current.info.Mode() || !bytes.Equal(source.content, current.content) {
		return fmt.Errorf("file changed while applying patch; read it again before retrying")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return root.Rename(temporaryPath, path)
}
