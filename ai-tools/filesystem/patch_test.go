package filesystem_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/filesystem"
	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
)

func patchArguments(path, patch string) json.RawMessage {
	arguments, _ := json.Marshal(map[string]string{"path": path, "patch": patch})
	return arguments
}

func newPatchTool(t *testing.T, options protocol.FilesystemOptions) loopprot.Tool {
	t.Helper()
	tool, err := filesystem.NewPatchTool(options)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func TestApplyPatchUpdatesExistingFiles(t *testing.T) {
	for _, test := range []struct {
		name, before, patch, after string
		hunks                      int
	}{
		{"replace with context", "head\nold\ntail\n", "@@ -1,3 +1,3 @@\n head\n-old\n+new\n tail\n", "head\nnew\ntail\n", 1},
		{"multiple hunks", "one\ntwo\nthree\nfour\n", "@@ -1 +1,2 @@\n-one\n+ONE\n+extra\n@@ -4 +5 @@\n-four\n+FOUR\n", "ONE\nextra\ntwo\nthree\nFOUR\n", 2},
		{"shifted unique context", "inserted\nhead\nold\ntail\n", "@@ -1,3 +1,3 @@\n head\n-old\n+new\n tail\n", "inserted\nhead\nnew\ntail\n", 1},
		{"line numbers disambiguate", "same\nmiddle\nsame\n", "@@ -3 +3 @@\n-same\n+changed\n", "same\nmiddle\nchanged\n", 1},
		{"insert into empty file", "", "@@ -0,0 +1,2 @@\n+first\n+second\n", "first\nsecond\n", 1},
		{"insert at end", "first\n", "@@ -1,0 +2 @@\n+second\n", "first\nsecond\n", 1},
		{"delete all content", "old\n", "@@ -1 +0,0 @@\n-old\n", "", 1},
		{"no final newline", "old", "@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n", "new", 1},
		{"add final newline", "old", "@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n", "new\n", 1},
		{"remove final newline", "old\n", "@@ -1 +1 @@\n-old\n+new\n\\ No newline at end of file\n", "new", 1},
		{"CRLF content", "old\r\n", "@@ -1 +1 @@\n-old\r\n+new\r\n", "new\r\n", 1},
		{"Unicode and empty line", "标题\n\n旧内容\n", "@@ -1,3 +1,3 @@\n 标题\n \n-旧内容\n+新内容\n", "标题\n\n新内容\n", 1},
		{"optional file headers", "old\n", "--- a/note\n+++ b/note\n@@ -1 +1 @@ section\n-old\n+new\n", "new\n", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "note")
			if err := os.WriteFile(path, []byte(test.before), 0700); err != nil {
				t.Fatal(err)
			}
			originalInfo, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			tool := newPatchTool(t, protocol.FilesystemOptions{Root: root})
			result, err := tool.Execute(context.Background(), patchArguments("note", test.patch))
			if err != nil || result.IsError {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			actual, err := os.ReadFile(path)
			if err != nil || string(actual) != test.after {
				t.Fatalf("actual=%q want=%q err=%v", actual, test.after, err)
			}
			var summary protocol.PatchResult
			if err := json.Unmarshal([]byte(result.Text()), &summary); err != nil {
				t.Fatal(err)
			}
			if summary.Path != "note" || summary.HunksApplied != test.hunks || summary.BytesWritten != len(test.after) {
				t.Fatalf("summary=%+v", summary)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != originalInfo.Mode().Perm() {
				t.Fatalf("mode changed: %v", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary files leaked: %v err=%v", entries, err)
			}
		})
	}
}

func TestRejectedPatchLeavesOriginalUntouched(t *testing.T) {
	for _, test := range []struct{ name, before, patch string }{
		{"later mismatch", "one\ntwo\nthree\n", "@@ -1 +1 @@\n-one\n+ONE\n@@ -3 +3 @@\n-missing\n+THREE\n"},
		{"ambiguous shifted context", "same\nmiddle\nsame\n", "@@ -99 +99 @@\n-same\n+changed\n"},
		{"incorrect count", "old\n", "@@ -1,2 +1 @@\n-old\n+new\n"},
		{"no actual change", "old\n", "@@ -1 +1 @@\n-old\n+old\n"},
		{"context only", "old\n", "@@ -1 +1 @@\n old\n"},
		{"overlapping ranges", "old\nold\n", "@@ -1 +1 @@\n-old\n+new\n@@ -1 +1 @@\n-old\n+new\n"},
		{"inconsistent new ranges", "old\n", "@@ -1 +2 @@\n-old\n+new\n"},
		{"nonexistent insertion point", "old\n", "@@ -9,0 +10 @@\n+new\n"},
		{"missing newline marker", "old", "@@ -1 +1 @@\n-old\n+new\n"},
		{"misplaced newline marker", "old\n", "@@ -1 +1 @@\n\\ No newline at end of file\n-old\n+new\n"},
		{"newline marker inside file", "old\ntail\n", "@@ -1 +1 @@\n-old\n+new\n\\ No newline at end of file\n"},
		{"foreign file header", "old\n", "--- a/other\n+++ b/other\n@@ -1 +1 @@\n-old\n+new\n"},
		{"multiple files", "old\n", "--- a/note\n+++ b/note\n@@ -1 +1 @@\n-old\n+new\n--- a/other\n+++ b/other\n@@ -1 +1 @@\n-old\n+new\n"},
		{"unsupported patch format", "old\n", "*** Begin Patch\n*** Update File: note\n@@\n-old\n+new\n*** End Patch"},
		{"integer overflow", "old\n", "@@ -999999999999999999999 +1 @@\n-old\n+new\n"},
		{"unprefixed line", "old\n", "@@ -1 +1 @@\nold\n+new\n"},
		{"binary source", string([]byte{0xff}), "@@ -1 +1 @@\n-old\n+new\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "note")
			if err := os.WriteFile(path, []byte(test.before), 0600); err != nil {
				t.Fatal(err)
			}
			beforeInfo, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			tool := newPatchTool(t, protocol.FilesystemOptions{Root: root})
			if _, err := tool.Execute(context.Background(), patchArguments("note", test.patch)); err == nil {
				t.Fatal("invalid patch accepted")
			}
			actual, err := os.ReadFile(path)
			if err != nil || string(actual) != test.before {
				t.Fatalf("file changed: %q err=%v", actual, err)
			}
			afterInfo, err := os.Stat(path)
			if err != nil || !os.SameFile(beforeInfo, afterInfo) {
				t.Fatalf("original file replaced: %v", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary files leaked: %v err=%v", entries, err)
			}
		})
	}
}

func TestPatchPathsArgumentsLimitsAndCancellation(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside")
	if err := os.WriteFile(outside, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "note"), []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tool := newPatchTool(t, protocol.FilesystemOptions{Root: root})
	patch := "@@ -1 +1 @@\n-old\n+new\n"
	for _, path := range []string{"", "../outside", outside, "missing", "."} {
		if _, err := tool.Execute(context.Background(), patchArguments(path, patch)); err == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
	for _, arguments := range []string{`null`, `[]`, `{}`, `{"path":"note"}`, `{"path":"note","patch":null}`, `{"path":"note","patch":1}`, `{"path":"note","patch":""}`, `{"path":"note","patch":"x","extra":1}`, `{"path":"note","patch":"x"} {}`} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(arguments)); err == nil {
			t.Fatalf("accepted %s", arguments)
		}
	}
	for _, options := range []protocol.FilesystemOptions{
		{Root: root, MaxReadBytes: 3}, {Root: root, MaxWriteBytes: 3}, {Root: root, MaxPatchBytes: 3},
	} {
		limited := newPatchTool(t, options)
		if _, err := limited.Execute(context.Background(), patchArguments("note", patch)); err == nil {
			t.Fatalf("ignored limits: %+v", options)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(ctx, patchArguments("note", patch)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	actual, err := os.ReadFile(filepath.Join(root, "note"))
	if err != nil || string(actual) != "old\n" {
		t.Fatalf("original changed: %q err=%v", actual, err)
	}
	if _, err := filesystem.NewPatchTool(protocol.FilesystemOptions{Root: root, MaxPatchBytes: -1}); err == nil {
		t.Fatal("negative patch limit accepted")
	}
	if err := os.Symlink("../outside", filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink("note", filepath.Join(root, "internal-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..", filepath.Join(root, "parent")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"escape", "internal-link", "parent/outside"} {
		if _, err := tool.Execute(context.Background(), patchArguments(path, patch)); err == nil {
			t.Fatalf("accepted symlink path %q", path)
		}
	}
	actual, err = os.ReadFile(outside)
	if err != nil || string(actual) != "old\n" {
		t.Fatalf("outside changed: %q err=%v", actual, err)
	}
}

func TestConcurrentPatchesDoNotOverwriteEachOther(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note"), []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tool := newPatchTool(t, protocol.FilesystemOptions{Root: root})
	var workers sync.WaitGroup
	outcomes := make(chan error, 8)
	for index := 0; index < cap(outcomes); index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := tool.Execute(context.Background(), patchArguments("note", "@@ -1 +1 @@\n-old\n+new\n"))
			outcomes <- err
		}()
	}
	workers.Wait()
	close(outcomes)
	successes := 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else if !strings.Contains(err.Error(), "context does not match") {
			t.Errorf("unexpected failure: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful conflicting patches=%d", successes)
	}
}

func TestPatchThroughParentSymlinkUsesActualDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real/nested", filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "real", "note"), []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "note"), []byte("untouched\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tool := newPatchTool(t, protocol.FilesystemOptions{Root: root})
	if _, err := tool.Execute(context.Background(), patchArguments("alias/../note", "@@ -1 +1 @@\n-old\n+new\n")); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(root, "real", "note"))
	if err != nil || string(actual) != "new\n" {
		t.Fatalf("target=%q err=%v", actual, err)
	}
	actual, err = os.ReadFile(filepath.Join(root, "note"))
	if err != nil || string(actual) != "untouched\n" {
		t.Fatalf("wrong file changed: %q err=%v", actual, err)
	}
}
