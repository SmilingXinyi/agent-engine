package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	path := filepath.Join(t.TempDir(), "workspace.yaml")
	for _, content := range []string{`{"systemPrompt":"legacy prompt"}`, "null", "[]", "{\"unknown\":true}", "{\"maxSteps\":-1}", "{\"timeoutSeconds\":9223372036854775807}", "{\"apiType\":\"invalid\"}", "{\"apiKeyEnv\":\"A=B\"}", "{} {}"} {
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

func TestYAMLConfiguration(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "workspace.yaml")
	legacy := filepath.Join(directory, "workspace.json")
	if err := os.WriteFile(legacy, []byte(`{"model":"legacy"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "convert") {
		t.Fatalf("migration error=%v", err)
	}
	for _, document := range []string{
		"model: one\nmodel: two", "model: one\n---\nmodel: two", "unknown: true", "maxSteps: ten",
	} {
		if err := os.WriteFile(path, []byte(document), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("accepted %q", document)
		}
	}
	document := "# configuration\nmodel: sample\nmodelMaxRetries: 0\nreasoningEffort: low\nproviderOptions:\n  thinking: {type: disabled}\n"
	if err := os.WriteFile(path, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	config = config.WithDefaults()
	if config.Model != "sample" || *config.ModelMaxRetries != 0 || config.Reasoning().Effort != "low" ||
		config.ProviderOptions["thinking"].(map[string]any)["type"] != "disabled" {
		t.Fatalf("config=%+v", config)
	}
}
