package filesystem_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
	"github.com/SmilingXinyi/agent-engine/ai-tools/filesystem"
	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
)

func newTools(t *testing.T, options protocol.FilesystemOptions) map[string]loopprot.Tool {
	t.Helper()
	tools := make(map[string]loopprot.Tool)
	for _, constructor := range []func(protocol.FilesystemOptions) (loopprot.Tool, error){filesystem.NewReadTool, filesystem.NewWriteTool, filesystem.NewListTool} {
		tool, err := constructor(options)
		if err != nil {
			t.Fatal(err)
		}
		tools[tool.Definition.Name] = tool
	}
	return tools
}

func TestWriteReadAndListRealFiles(t *testing.T) {
	root := t.TempDir()
	tools := newTools(t, protocol.FilesystemOptions{Root: root})
	ctx := context.Background()
	for _, content := range []string{"你好，世界", "replacement", ""} {
		arguments, _ := json.Marshal(map[string]string{"path": "note.txt", "content": content})
		written, err := tools[protocol.WriteFile].Execute(ctx, arguments)
		if err != nil || written.IsError {
			t.Fatalf("write=%+v err=%v", written, err)
		}
		actual, err := os.ReadFile(filepath.Join(root, "note.txt"))
		if err != nil || string(actual) != content {
			t.Fatalf("disk=%q err=%v", actual, err)
		}
		read, err := tools[protocol.ReadFile].Execute(ctx, json.RawMessage(`{"path":"note.txt"}`))
		if err != nil || read.Text() != content {
			t.Fatalf("read=%+v err=%v", read, err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "a-directory"), 0700); err != nil {
		t.Fatal(err)
	}
	listed, err := tools[protocol.ListDirectory].Execute(ctx, json.RawMessage(`{"path":"."}`))
	if err != nil {
		t.Fatal(err)
	}
	var entries []protocol.DirectoryEntry
	if err := json.Unmarshal([]byte(listed.Text()), &entries); err != nil {
		t.Fatal(err)
	}
	want := []protocol.DirectoryEntry{{Name: "a-directory", Type: "directory"}, {Name: "note.txt", Type: "file"}}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("entries=%+v", entries)
	}
	empty, err := tools[protocol.ListDirectory].Execute(ctx, json.RawMessage(`{"path":"a-directory"}`))
	if err != nil || empty.Text() != "[]" {
		t.Fatalf("empty=%s err=%v", empty.Text(), err)
	}
}

func TestRootBoundaryIncludesSymlinks(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	tools := newTools(t, protocol.FilesystemOptions{Root: root})
	for _, path := range []string{"../outside.txt", outside} {
		for name, tool := range tools {
			arguments, _ := json.Marshal(map[string]string{"path": path, "content": "changed"})
			if name != protocol.WriteFile {
				arguments, _ = json.Marshal(map[string]string{"path": path})
			}
			if _, err := tool.Execute(context.Background(), arguments); err == nil {
				t.Fatalf("%s accepted %q", name, path)
			}
		}
	}
	if err := os.Symlink("../outside.txt", filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink("..", filepath.Join(root, "parent")); err != nil {
		t.Fatal(err)
	}
	for name, tool := range tools {
		path := "escape"
		if name == protocol.ListDirectory {
			path = "parent"
		}
		arguments := map[string]string{"path": path}
		if name == protocol.WriteFile {
			arguments["content"] = "changed"
		}
		encoded, _ := json.Marshal(arguments)
		if _, err := tool.Execute(context.Background(), encoded); err == nil {
			t.Fatalf("%s followed escaping symlink", name)
		}
	}
	actual, err := os.ReadFile(outside)
	if err != nil || string(actual) != "keep" {
		t.Fatalf("outside=%q err=%v", actual, err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside.txt", filepath.Join(root, "inside-link")); err != nil {
		t.Fatal(err)
	}
	read, err := tools[protocol.ReadFile].Execute(context.Background(), json.RawMessage(`{"path":"inside-link"}`))
	if err != nil || read.Text() != "inside" {
		t.Fatalf("internal symlink=%+v err=%v", read, err)
	}
}

func TestLimitsAndInvalidArgumentsDoNotOverwriteFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary"), []byte{0xff}, 0600); err != nil {
		t.Fatal(err)
	}
	tools := newTools(t, protocol.FilesystemOptions{Root: root, MaxReadBytes: 3, MaxWriteBytes: 3, MaxEntries: 1})
	for _, test := range []struct{ name, arguments string }{
		{protocol.ReadFile, `{"path":"note"}`},
		{protocol.ReadFile, `{"path":"binary"}`},
		{protocol.ReadFile, `{"path":"."}`},
		{protocol.ReadFile, `{"path":"missing"}`},
		{protocol.ListDirectory, `{"path":"."}`},
		{protocol.ListDirectory, `{"path":"note"}`},
		{protocol.WriteFile, `{"path":"note","content":"oversized"}`},
		{protocol.WriteFile, `{"path":"note"}`},
		{protocol.WriteFile, `{"path":"note","content":null}`},
		{protocol.WriteFile, `{"path":".","content":"x"}`},
		{protocol.WriteFile, `{"path":"missing/child","content":"x"}`},
	} {
		if _, err := tools[test.name].Execute(context.Background(), json.RawMessage(test.arguments)); err == nil {
			t.Fatalf("%s accepted %s", test.name, test.arguments)
		}
	}
	for name, tool := range tools {
		for _, arguments := range []string{`null`, `[]`, `{}`, `{"path":null}`, `{"path":""}`, `{"path":".","unknown":true}`, `{"path":"."} {}`} {
			if _, err := tool.Execute(context.Background(), json.RawMessage(arguments)); err == nil {
				t.Fatalf("%s accepted %s", name, arguments)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		arguments := `{"path":"note"}`
		if name == protocol.WriteFile {
			arguments = `{"path":"note","content":"x"}`
		}
		if _, err := tool.Execute(ctx, json.RawMessage(arguments)); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s cancellation=%v", name, err)
		}
	}
	actual, err := os.ReadFile(filepath.Join(root, "note"))
	if err != nil || string(actual) != "keep" {
		t.Fatalf("original=%q err=%v", actual, err)
	}
}

func TestConstructorsValidateDependencies(t *testing.T) {
	for _, options := range []protocol.FilesystemOptions{{}, {Root: filepath.Join(t.TempDir(), "missing")}, {Root: t.TempDir(), MaxReadBytes: -1}} {
		if _, err := filesystem.NewReadTool(options); err == nil {
			t.Fatalf("accepted %+v", options)
		}
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte(strings.Repeat("x", 4)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := filesystem.NewReadTool(protocol.FilesystemOptions{Root: file}); err == nil {
		t.Fatal("accepted file as root")
	}
}
