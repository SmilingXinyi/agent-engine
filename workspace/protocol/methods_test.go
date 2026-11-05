package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDefaultsAndValidation(t *testing.T) {
	t.Setenv("AGENT_MODEL", "")
	t.Setenv("AGENT_LLM_MODEL", "global-model")
	t.Setenv("AGENT_LLM_BASE_URL", "https://global.example/v1")
	defaults := (Config{}).WithDefaults()
	if defaults.Model != "global-model" || defaults.MaxSteps != 40 || defaults.TimeoutSeconds != 900 || defaults.APIType != "chat_completions" {
		t.Fatal(defaults)
	}
	explicit := (Config{Model: "local", BaseURL: "https://local.example", APIKeyEnv: "LOCAL_KEY", APIType: "responses_api"}).WithDefaults()
	if explicit.Model != "local" || explicit.BaseURL != "https://local.example" || explicit.APIKeyEnv != "LOCAL_KEY" || explicit.APIType != "responses_api" {
		t.Fatal(explicit)
	}
	path := filepath.Join(t.TempDir(), "workspace.json")
	for _, content := range []string{"null", "[]", "{\"unknown\":true}", "{\"maxSteps\":-1}", "{\"timeoutSeconds\":9223372036854775807}", "{\"apiType\":\"invalid\"}", "{\"apiKeyEnv\":\"A=B\"}", "{} {}"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("accepted %s", content)
		}
	}
	var record map[string]any
	if err := DecodeJSON([]byte("{\"value\":900719925474099312345}"), &record); err != nil {
		t.Fatal(err)
	}
	if record["value"] != json.Number("900719925474099312345") {
		t.Fatal(record)
	}
	if err := ApprovalMode("invalid").Validate(); err == nil {
		t.Fatal("invalid approval accepted")
	}
}
