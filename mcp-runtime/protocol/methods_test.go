package protocol

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConfigRejectsInvalidServers(t *testing.T) {
	for _, content := range []string{
		`{`,
		`{"servers":{"bad":{"command":"echo","url":"https://example.com/mcp"}}}`,
		`{"servers":{"bad":{}}}`,
		`{"servers":{"bad":{"url":"file:///tmp/mcp"}}}`,
		`{"servers":{"bad":{"url":"https:///mcp"}}}`,
		`{"servers":{"bad name":{"command":"echo"}}}`,
	} {
		if _, err := ParseConfig(content); err == nil {
			t.Fatalf("expected configuration error for %s", content)
		}
	}
	config, err := ParseConfig(`{"servers":{"stdio":{"command":"echo","args":["test"],"env":{"FOO":"bar"}},"remote":{"url":"https://example.com/mcp","headers":{"Authorization":"Bearer test"}}}}`)
	if err != nil || len(config.Servers) != 2 || config.Servers["stdio"].Env["FOO"] != "bar" || config.Servers["remote"].Headers["Authorization"] != "Bearer test" {
		t.Fatalf("config=%#v error=%v", config, err)
	}
}

func TestParseConfigSupportsCanonicalAndLegacyFormats(t *testing.T) {
	servers := `{"stdio":{"command":"echo","args":["test"],"env":{"FOO":"bar"},"dir":"/tmp"},"remote":{"url":"https://example.com/mcp","headers":{"Authorization":"Bearer test"}}}`
	canonical, err := ParseConfig(`{"mcpServers":` + servers + `}`)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := ParseConfig(`{"servers":` + servers + `}`)
	if err != nil || !reflect.DeepEqual(canonical, legacy) {
		t.Fatalf("canonical=%#v legacy=%#v error=%v", canonical, legacy, err)
	}
	if canonical.Servers["stdio"].Dir != "/tmp" || canonical.Servers["remote"].Headers["Authorization"] != "Bearer test" {
		t.Fatalf("config = %#v", canonical)
	}
	encoded, err := json.Marshal(canonical)
	if err != nil || !strings.Contains(string(encoded), `"mcpServers"`) || strings.Contains(string(encoded), `"servers"`) {
		t.Fatalf("canonical JSON = %s error=%v", encoded, err)
	}
	roundtrip, err := ParseConfig(string(encoded))
	if err != nil || !reflect.DeepEqual(roundtrip, canonical) {
		t.Fatalf("roundtrip=%#v error=%v", roundtrip, err)
	}
}

func TestParseConfigRejectsMalformedDocuments(t *testing.T) {
	for _, content := range []string{
		`{}`,
		`null`,
		`{"mcpServers":null}`,
		`{"mcpServers":[]}`,
		`{"mcpServers":{},"servers":{}}`,
		`{"mcpSevers":{}}`,
		`{"mcpServers":{"local":{"commmand":"echo"}}}`,
		`{"mcpServers":{"local":{"command":"echo","args":"test"}}}`,
		`{"mcpServers":{"remote":{"url":"https://example.com/mcp","headers":{"Authorization":42}}}}`,
		`{"mcpServers":{"local":{"command":"echo","disabled":"true"}}}`,
		`{"mcpServers":{"local":{"command":"echo","disabledTools":"write_file"}}}`,
		`{"mcpServers":{"local":{"command":"echo","disabledTools":[""]}}}`,
		`{"mcpServers":{"local":{"command":"echo","disabledTools":[" write_file "]}}}`,
		`{"mcpServers":{}} {"mcpServers":{}}`,
	} {
		if _, err := ParseConfig(content); err == nil {
			t.Fatalf("expected error for %s", content)
		}
	}
	config, err := ParseConfig(`{"mcpServers":{}}`)
	if err != nil || config.Servers == nil || len(config.Servers) != 0 {
		t.Fatalf("explicit empty config=%#v error=%v", config, err)
	}
}

func TestParseConfigRetainsDisabledServerAndToolSettings(t *testing.T) {
	config, err := ParseConfig(`{"mcpServers":{"offline":{"disabled":true},"local":{"command":"echo","disabledTools":["write_file","edit_file"]}}}`)
	if err != nil || !config.Servers["offline"].Disabled || !reflect.DeepEqual(config.Servers["local"].DisabledTools, []string{"write_file", "edit_file"}) {
		t.Fatalf("config=%#v error=%v", config, err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := ParseConfig(string(encoded))
	if err != nil || !reflect.DeepEqual(roundtrip, config) {
		t.Fatalf("roundtrip=%#v error=%v", roundtrip, err)
	}
}

func TestLoadConfigReadsFileAndReportsErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if _, err := LoadConfig(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"local":{"command":"echo","args":["hello"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil || config.Servers["local"].Command != "echo" || len(config.Servers["local"].Args) != 1 {
		t.Fatalf("config=%#v error=%v", config, err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"local":{}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("invalid file error = %v", err)
	}
}

func TestExampleFileUsesSupportedFormat(t *testing.T) {
	config, err := LoadConfig("../mcp.example.json")
	if err != nil || len(config.Servers) != 2 {
		t.Fatalf("example config=%#v error=%v", config, err)
	}
}
