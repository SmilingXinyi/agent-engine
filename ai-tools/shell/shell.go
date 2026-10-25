// Package shell provides bounded, cancellable command execution.
package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/utils"
)

func NewTool(options protocol.ShellOptions) (loopprot.Tool, error) {
	if strings.TrimSpace(options.Dir) == "" {
		return loopprot.Tool{}, fmt.Errorf("shell working directory is required")
	}
	if options.TimeoutSeconds < 0 || int64(options.TimeoutSeconds) > math.MaxInt64/int64(time.Second) || options.MaxOutputBytes < 0 {
		return loopprot.Tool{}, fmt.Errorf("shell limits must be nonnegative and timeout must fit a time.Duration")
	}
	if options.TimeoutSeconds == 0 {
		options.TimeoutSeconds = protocol.DefaultTimeoutSeconds
	}
	if options.MaxOutputBytes == 0 {
		options.MaxOutputBytes = protocol.DefaultMaxOutputBytes
	}
	directory, err := filepath.Abs(options.Dir)
	if err != nil {
		return loopprot.Tool{}, err
	}
	info, err := os.Stat(directory)
	if err != nil {
		return loopprot.Tool{}, err
	}
	if !info.IsDir() {
		return loopprot.Tool{}, fmt.Errorf("shell working directory must be a directory")
	}
	if options.Executable == "" {
		options.Executable = "sh"
	}
	executable, err := exec.LookPath(options.Executable)
	if err != nil {
		return loopprot.Tool{}, fmt.Errorf("find shell executable: %w", err)
	}
	// Capture relative executable paths before setting the command's working directory.
	executable, err = filepath.Abs(executable)
	if err != nil {
		return loopprot.Tool{}, err
	}
	return loopprot.Tool{
		Definition: aiprot.ToolDefinition{
			Name:        protocol.Shell,
			Description: fmt.Sprintf("Execute a shell command in the configured working directory. Returns stdout, stderr and exit_code. Timeout %d seconds; captures up to %d bytes per output stream. Working directory is not a filesystem sandbox.", options.TimeoutSeconds, options.MaxOutputBytes),
			InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","minLength":1}},"required":["command"],"additionalProperties":false}`),
		},
		Execute: func(ctx context.Context, arguments json.RawMessage) (aiprot.ToolResult, error) {
			var input struct {
				Command string `json:"command"`
			}
			if err := utils.DecodeObject(arguments, &input); err != nil {
				return aiprot.ToolResult{}, fmt.Errorf("shell arguments: %w", err)
			}
			if strings.TrimSpace(input.Command) == "" {
				return aiprot.ToolResult{}, fmt.Errorf("command is required")
			}
			ctx, cancel := context.WithTimeout(ctx, time.Duration(options.TimeoutSeconds)*time.Second)
			defer cancel()
			if err := ctx.Err(); err != nil {
				return aiprot.ToolResult{}, err
			}
			command := exec.CommandContext(ctx, executable, "-c", input.Command)
			command.Dir = directory
			command.WaitDelay = time.Second
			configureProcess(command)
			stdout := &limitedBuffer{limit: options.MaxOutputBytes}
			stderr := &limitedBuffer{limit: options.MaxOutputBytes}
			command.Stdout, command.Stderr = stdout, stderr
			runErr := command.Run()
			if err := ctx.Err(); err != nil {
				return aiprot.ToolResult{}, err
			}
			exitCode := 0
			if runErr != nil {
				var exitErr *exec.ExitError
				if !errors.As(runErr, &exitErr) {
					return aiprot.ToolResult{}, fmt.Errorf("execute shell: %w", runErr)
				}
				exitCode = exitErr.ExitCode()
			}
			content, err := json.Marshal(protocol.CommandResult{
				Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode,
				Truncated: stdout.truncated || stderr.truncated,
			})
			if err != nil {
				return aiprot.ToolResult{}, err
			}
			result := aiprot.TextToolResult(string(content))
			result.IsError = runErr != nil
			return result, nil
		},
	}, nil
}

// Discard excess output while continuing to drain pipes, so truncation cannot
// block a command or turn a successful command into a short-write failure.
type limitedBuffer struct {
	content   bytes.Buffer
	limit     int
	truncated bool
}

func (buffer *limitedBuffer) Write(content []byte) (int, error) {
	remaining := buffer.limit - buffer.content.Len()
	kept := min(len(content), remaining)
	buffer.content.Write(content[:kept])
	buffer.truncated = buffer.truncated || kept < len(content)
	return len(content), nil
}

func (buffer *limitedBuffer) String() string {
	return buffer.content.String()
}
