package mcpruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
)

func TestStdioFrameLimit(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const responseBytes = 17 * 1024 * 1024
	for _, scenario := range []struct {
		name  string
		limit int
		fails bool
	}{
		{name: "default-16-MiB", fails: true},
		{name: "configured-64-MiB", limit: 64 * 1024 * 1024},
		{name: "configured-1-MiB", limit: 1024 * 1024, fails: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Large JSON frames under the race detector can take more than the
			// usual 10 seconds on hosted macOS runners.
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			runtime, err := Start(ctx, protocol.Config{Servers: map[string]protocol.ServerConfig{
				"local": {
					Command: executable, Args: []string{"-test.run=^$"}, MaxLineLength: scenario.limit,
					Env: map[string]string{helperServerEnv: "1", "MCP_TEST_RESPONSE_BYTES": strconv.Itoa(responseBytes)},
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			result, err := runtime.Tools[0].Execute(ctx, json.RawMessage(`{}`))
			if scenario.fails {
				if err == nil || !strings.Contains(err.Error(), "exceeded the configured maximum line length") {
					t.Fatalf("expected frame limit error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if text := result.Text(); len(text) != responseBytes || strings.Trim(text, "x") != "" {
				t.Fatalf("large response was truncated or corrupted; bytes=%d", len(text))
			}
			// The same session must remain usable after a large frame.
			if _, err := runtime.Tools[0].Execute(ctx, json.RawMessage(`{}`)); err != nil {
				t.Fatalf("session failed after receiving a large frame: %v", err)
			}
			if err := runtime.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCommandTransportReapsUnresponsiveProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"wait-for-term", "ignore-term"} {
		t.Run(mode, func(t *testing.T) {
			command := exec.Command(executable, "-test.run=^$")
			command.Env = append(os.Environ(), helperServerEnv+"="+mode, "GORACE=atexit_sleep_ms=0")
			transport := &commandTransport{command: command, maxLineLength: 1024, terminateDuration: 25 * time.Millisecond}
			connection, err := transport.Connect(testContext(t))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			// The helper announces readiness after installing its signal handler.
			if _, err := connection.Read(testContext(t)); err != nil {
				t.Fatal(err)
			}
			err = connection.Close()
			if command.ProcessState == nil {
				t.Fatal("subprocess was not reaped")
			}
			if mode == "wait-for-term" {
				if err != nil || !command.ProcessState.Success() {
					t.Fatalf("SIGTERM shutdown failed: %v", err)
				}
			} else {
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) {
					t.Fatalf("expected killed subprocess error, got %v", err)
				}
				status, ok := exitError.Sys().(syscall.WaitStatus)
				if !ok || status.Signal() != syscall.SIGKILL {
					t.Fatalf("expected SIGKILL, got %v", exitError)
				}
			}
			if repeated := connection.Close(); (repeated == nil) != (err == nil) {
				t.Fatalf("repeated Close changed error: first=%v second=%v", err, repeated)
			}
		})
	}
}
