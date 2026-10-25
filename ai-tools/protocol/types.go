// Package protocol defines built-in tool configuration and structured results.
// Executable tools use ai-tool-loop/protocol.Tool directly.
package protocol

const (
	ReadFile      = "read_file"
	WriteFile     = "write_file"
	ApplyPatch    = "apply_patch"
	ListDirectory = "list_directory"
	Shell         = "shell"
)

const (
	DefaultMaxReadBytes   = 1 << 20
	DefaultMaxWriteBytes  = 1 << 20
	DefaultMaxPatchBytes  = 2 << 20
	DefaultMaxEntries     = 1000
	DefaultTimeoutSeconds = 30
	DefaultMaxOutputBytes = 64 << 10
)

// Config selects compiled-in tools. Only explicitly listed, non-disabled tools
// are built; an empty configuration enables nothing.
type Config struct {
	BuiltinTools map[string]ToolConfig `json:"builtinTools"`
	Filesystem   FilesystemOptions     `json:"filesystem,omitempty"`
	Shell        ShellOptions          `json:"shell,omitempty"`
}

type ToolConfig struct {
	Disabled bool `json:"disabled,omitempty"`
}

// FilesystemOptions restricts file tools to Root. Zero limits use defaults.
// Relative roots are resolved against the process working directory at build time.
type FilesystemOptions struct {
	Root          string `json:"root"`
	MaxReadBytes  int    `json:"maxReadBytes,omitempty"`
	MaxWriteBytes int    `json:"maxWriteBytes,omitempty"`
	MaxPatchBytes int    `json:"maxPatchBytes,omitempty"`
	MaxEntries    int    `json:"maxEntries,omitempty"`
}

// ShellOptions controls command execution. Dir is an initial working directory,
// not a filesystem restriction. Executable defaults to sh and must accept -c.
type ShellOptions struct {
	Dir            string `json:"dir"`
	Executable     string `json:"executable,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	MaxOutputBytes int    `json:"maxOutputBytes,omitempty"`
}

type DirectoryEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// PatchResult describes a successful, single-file unified diff update.
type PatchResult struct {
	Path         string `json:"path"`
	HunksApplied int    `json:"hunks_applied"`
	BytesWritten int    `json:"bytes_written"`
}

// CommandResult retains bounded stdout/stderr. A nonzero exit code also sets
// ToolResult.IsError; launch failures and cancellation return Go errors.
type CommandResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated"`
}
