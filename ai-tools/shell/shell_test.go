package shell_test

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/shell"
)

func newShell(t *testing.T, options protocol.ShellOptions) loopprot.Tool {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is unavailable")
	}
	tool, err := shell.NewTool(options)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func TestCommandWorkingDirectoryAndExitStatus(t *testing.T) {
	directory := t.TempDir()
	tool := newShell(t, protocol.ShellOptions{Dir: directory})
	for _, test := range []struct {
		command  string
		exitCode int
		stderr   string
	}{
		{`printf '%s' "$PWD"`, 0, ""},
		{`printf 'partial'; printf 'failure' >&2; exit 7`, 7, "failure"},
	} {
		arguments, _ := json.Marshal(map[string]string{"command": test.command})
		result, err := tool.Execute(context.Background(), arguments)
		if err != nil {
			t.Fatal(err)
		}
		var output protocol.CommandResult
		if err := json.Unmarshal([]byte(result.Text()), &output); err != nil {
			t.Fatal(err)
		}
		if output.ExitCode != test.exitCode || output.Stderr != test.stderr || result.IsError != (test.exitCode != 0) {
			t.Fatalf("output=%+v isError=%v", output, result.IsError)
		}
		if test.exitCode == 0 {
			actual, err := filepath.EvalSymlinks(output.Stdout)
			if err != nil {
				t.Fatal(err)
			}
			want, err := filepath.EvalSymlinks(directory)
			if err != nil || actual != want {
				t.Fatalf("cwd=%q want=%q err=%v", actual, want, err)
			}
		} else if output.Stdout != "partial" {
			t.Fatalf("stdout=%q", output.Stdout)
		}
	}
}

func TestOutputIsBoundedAndPipesContinueDraining(t *testing.T) {
	tool := newShell(t, protocol.ShellOptions{Dir: t.TempDir(), MaxOutputBytes: 8})
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"i=0; while [ $i -lt 5000 ]; do printf 'abcdefgh'; printf '12345678' >&2; i=$((i+1)); done"}`))
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var output protocol.CommandResult
	if err := json.Unmarshal([]byte(result.Text()), &output); err != nil {
		t.Fatal(err)
	}
	if output.Stdout != "abcdefgh" || output.Stderr != "12345678" || !output.Truncated || output.ExitCode != 0 {
		t.Fatalf("output=%+v", output)
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	tool := newShell(t, protocol.ShellOptions{Dir: t.TempDir(), TimeoutSeconds: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(ctx, json.RawMessage(`{"command":"printf should-not-run"}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	for _, parentDeadline := range []time.Duration{100 * time.Millisecond, 3 * time.Second} {
		ctx, cancel := context.WithTimeout(context.Background(), parentDeadline)
		started := time.Now()
		_, err := tool.Execute(ctx, json.RawMessage(`{"command":"exec sleep 30"}`))
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2500*time.Millisecond {
			t.Fatalf("deadline=%s err=%v elapsed=%s", parentDeadline, err, time.Since(started))
		}
	}
}

func TestInvalidArgumentsAndDependencies(t *testing.T) {
	tool := newShell(t, protocol.ShellOptions{Dir: t.TempDir()})
	for _, arguments := range []string{`null`, `[]`, `{}`, `{"command":null}`, `{"command":" "}`, `{"command":"true","extra":1}`, `{"command":"true"} {}`} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(arguments)); err == nil {
			t.Fatalf("accepted %s", arguments)
		}
	}
	for _, options := range []protocol.ShellOptions{
		{}, {Dir: filepath.Join(t.TempDir(), "missing")}, {Dir: t.TempDir(), TimeoutSeconds: -1},
		{Dir: t.TempDir(), MaxOutputBytes: -1}, {Dir: t.TempDir(), Executable: filepath.Join(t.TempDir(), "missing-shell")},
	} {
		if _, err := shell.NewTool(options); err == nil {
			t.Fatalf("accepted %+v", options)
		}
	}
}
