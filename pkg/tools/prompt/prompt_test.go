package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SmilingXinyi/agent-engine/pkg/tools/skill"
)

func TestBuildSystemPromptIncludesOnlyMatchedNativeSkill(t *testing.T) {
	root := t.TempDir()
	summarizeDir := filepath.Join(root, "skills", "demo", "summarize")
	writeTestFile(t, filepath.Join(summarizeDir, "SKILL.md"), strings.Join([]string{
		"---",
		"name: summarize",
		"description: Use this skill whenever the user asks to summarize articles or documents.",
		"---",
		"",
		"summarize instructions",
	}, "\n"))

	translateDir := filepath.Join(root, "skills", "demo", "translate")
	writeTestFile(t, filepath.Join(translateDir, "SKILL.md"), strings.Join([]string{
		"---",
		"name: translate",
		"description: Use this skill whenever the user asks to translate text.",
		"---",
		"",
		"translate instructions",
	}, "\n"))

	skills, err := skill.LoadSkills(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	prompt := BuildSystemPrompt("agent instructions", skills, "please summarize this article")
	if !strings.Contains(prompt, "agent instructions") {
		t.Fatal("expected AGENTS.md content")
	}
	if !strings.Contains(prompt, "summarize instructions") {
		t.Fatal("expected matched summarize skill")
	}
	if strings.Contains(prompt, "translate instructions") {
		t.Fatal("unexpected unmatched translate skill")
	}
	if strings.Contains(prompt, "---") || strings.Contains(prompt, "name:") || strings.Contains(prompt, "description:") {
		t.Fatal("frontmatter should not be injected")
	}
}

func TestBuildSystemPromptDoesNotInjectNonNativeSkillFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "skills", "b.md"), "legacy b")
	writeTestFile(t, filepath.Join(root, "skills", "nested", "a.md"), "legacy a")

	skills, err := skill.LoadSkills(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	prompt := BuildSystemPrompt("agent instructions", skills, "unrelated prompt")
	if strings.Contains(prompt, "legacy a") || strings.Contains(prompt, "legacy b") {
		t.Fatalf("did not expect legacy files, got:\n%s", prompt)
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
