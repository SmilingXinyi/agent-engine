package protocol_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
)

func TestConfigSelectionAndFileLoading(t *testing.T) {
	content := `{"builtinTools":{"write_file":{"disabled":true},"apply_patch":{},"read_file":{},"list_directory":{"disabled":false}},"filesystem":{"root":"workspace","maxReadBytes":32,"maxPatchBytes":64}}`
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := protocol.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config.EnabledNames(), []string{"apply_patch", "list_directory", "read_file"}) || config.Filesystem.Root != "workspace" || config.Filesystem.MaxReadBytes != 32 || config.Filesystem.MaxPatchBytes != 64 {
		t.Fatalf("config=%+v names=%v", config, config.EnabledNames())
	}
	// Loading does not require the configured root to exist.
	if _, err := protocol.LoadConfig(path + ".missing"); err == nil {
		t.Fatal("missing file accepted")
	}
	empty, err := protocol.ParseConfig(`{"builtinTools":{}}`)
	if err != nil || len(empty.EnabledNames()) != 0 {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
}

func TestConfigRejectsMalformedOrAmbiguousSelection(t *testing.T) {
	for _, content := range []string{
		`null`, `[]`, `{}`, `{"builtinTools":null}`, `{"builtinTools":[]}`,
		`{"builtinTools":{"shell":null}}`, `{"builtinTools":{"shell":{"disabled":null}}}`,
		`{"builtinTools":{"shell":{"disabled":"true"}}}`, `{"builtinTools":{"shell":{"disable":true}}}`,
		`{"builtinTools":{"unknown":{"disabled":true}}}`, `{"builtinTools":{},"extra":1}`,
		`{"builtinTools":{},"filesystem":{"rooot":"."}}`, `{"builtinTools":{}} {}`,
	} {
		t.Run(content, func(t *testing.T) {
			if _, err := protocol.ParseConfig(content); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
