//go:build unix

package shell_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
)

func TestCancellationStopsOrdinaryDescendants(t *testing.T) {
	directory := t.TempDir()
	tool := newShell(t, protocol.ShellOptions{Dir: directory})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := tool.Execute(ctx, json.RawMessage(`{"command":"sh -c 'sleep 1; printf leaked > marker' & wait"}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation=%v", err)
	}
	// A surviving descendant would create marker after the parent's cancellation.
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(directory, "marker")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant survived: %v", err)
	}
}
