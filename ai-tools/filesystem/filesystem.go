// Package filesystem provides text file and directory tools confined to a root.
package filesystem

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
)

// Each execution opens and closes its own root; tools hold no file descriptors
// and can be shared across requests without a Close lifecycle.
func prepareOptions(options protocol.FilesystemOptions) (protocol.FilesystemOptions, error) {
	if strings.TrimSpace(options.Root) == "" {
		return options, fmt.Errorf("filesystem root is required")
	}
	if options.MaxReadBytes < 0 || options.MaxWriteBytes < 0 || options.MaxPatchBytes < 0 || options.MaxEntries < 0 ||
		options.MaxReadBytes == math.MaxInt || options.MaxEntries == math.MaxInt {
		return options, fmt.Errorf("filesystem limits must be nonnegative and read/entry limits must be below MaxInt")
	}
	if options.MaxReadBytes == 0 {
		options.MaxReadBytes = protocol.DefaultMaxReadBytes
	}
	if options.MaxWriteBytes == 0 {
		options.MaxWriteBytes = protocol.DefaultMaxWriteBytes
	}
	if options.MaxPatchBytes == 0 {
		options.MaxPatchBytes = protocol.DefaultMaxPatchBytes
	}
	if options.MaxEntries == 0 {
		options.MaxEntries = protocol.DefaultMaxEntries
	}
	rootPath, err := filepath.Abs(options.Root)
	if err != nil {
		return options, err
	}
	// Resolve the configured root once; arguments remain relative to this root.
	options.Root, err = filepath.EvalSymlinks(rootPath)
	if err != nil {
		return options, fmt.Errorf("resolve filesystem root: %w", err)
	}
	root, err := os.OpenRoot(options.Root)
	if err != nil {
		return options, fmt.Errorf("open filesystem root: %w", err)
	}
	return options, root.Close()
}

func openRoot(ctx context.Context, options protocol.FilesystemOptions, path string) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" || !filepath.IsLocal(path) {
		return nil, fmt.Errorf("path must be a non-empty relative path inside the filesystem root")
	}
	// os.Root enforces containment while opening paths, including symlinks;
	// string prefix checks or EvalSymlinks alone cannot enforce this boundary.
	return os.OpenRoot(options.Root)
}
