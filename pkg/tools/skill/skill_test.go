package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSkillsParsesNativeSkillFrontmatter(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "demo", "summarize")
	writeTestFile(t, filepath.Join(skillDir, "SKILL.md"), strings.Join([]string{
		"---",
		"name: summarize",
		"description: Use this skill to summarize long documents.",
		"---",
		"",
		"summarize instructions",
	}, "\n"))
	writeTestFile(t, filepath.Join(skillDir, "evals", "evals.json"), "eval fixture")

	skills, err := LoadSkills(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "summarize" {
		t.Fatalf("expected summarize skill, got %q", skills[0].Name)
	}
	if skills[0].Metadata.Description != "Use this skill to summarize long documents." {
		t.Fatalf("unexpected description: %q", skills[0].Metadata.Description)
	}
	if filepath.Base(skills[0].Path) != "SKILL.md" {
		t.Fatalf("unexpected skill path: %s", skills[0].Path)
	}
	if !strings.Contains(skills[0].Body, "summarize instructions") {
		t.Fatalf("expected skill body, got %q", skills[0].Body)
	}
	if strings.Contains(skills[0].Body, "eval fixture") {
		t.Fatal("supplemental files should not be loaded into skill body")
	}
}

func TestLoadSkillsParsesDescriptionBlockScalar(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "tts")
	writeTestFile(t, filepath.Join(skillDir, "SKILL.md"), strings.Join([]string{
		"---",
		"name: tts",
		"description: >-",
		"  Use this skill for customer-facing scripts: ，。；？！、",
		"---",
		"tts instructions",
	}, "\n"))

	skills, err := LoadSkills(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if !strings.Contains(skills[0].Metadata.Description, "scripts: ，。；？！、") {
		t.Fatalf("unexpected description: %q", skills[0].Metadata.Description)
	}
}

func TestLoadSkillsInvalidFrontmatterReturnsError(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "bad")
	writeTestFile(t, filepath.Join(skillDir, "SKILL.md"), strings.Join([]string{
		"---",
		"name: [",
		"---",
		"bad instructions",
	}, "\n"))

	_, err := LoadSkills(filepath.Join(root, "skills"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse skill frontmatter") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadSkillsRequiresNameAndDescription(t *testing.T) {
	testCases := []struct {
		name        string
		frontmatter string
		expected    string
	}{
		{
			name:        "missing-name",
			frontmatter: "description: Use this skill for documents.",
			expected:    "missing required frontmatter field name",
		},
		{
			name:        "missing-description",
			frontmatter: "name: missing_description",
			expected:    "missing required frontmatter field description",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			skillDir := filepath.Join(root, "skills", testCase.name)
			writeTestFile(t, filepath.Join(skillDir, "SKILL.md"), strings.Join([]string{
				"---",
				testCase.frontmatter,
				"---",
				"instructions",
			}, "\n"))

			_, err := LoadSkills(filepath.Join(root, "skills"))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), testCase.expected) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoadSkillsWithoutNativeSkillReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	skillsRoot := filepath.Join(root, "skills")
	writeTestFile(t, filepath.Join(skillsRoot, "b.md"), "b")
	writeTestFile(t, filepath.Join(skillsRoot, "nested", "a.md"), "a")

	skills, err := LoadSkills(skillsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("expected no skills, got %d", len(skills))
	}
	if selected := SelectSkills("no keyword", skills); len(selected) != 0 {
		t.Fatalf("expected no selected skills, got %d", len(selected))
	}
}

func TestLoadSkillsIgnoresSeparateManifestFiles(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "old")
	writeTestFile(t, filepath.Join(skillDir, "skill.yml"), strings.Join([]string{
		"name: old",
		"description: old manifest",
		"files:",
		"  - OLD.md",
	}, "\n"))
	writeTestFile(t, filepath.Join(skillDir, "OLD.md"), "old instructions")

	skills, err := LoadSkills(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("expected old manifest files to be ignored, got %d skills", len(skills))
	}
}

func TestMatchSkillsScoresAndLimits(t *testing.T) {
	skills := []Skill{
		{Name: "beta", Metadata: SkillMetadata{Description: "Use this skill for common data."}, Dir: "/b"},
		{Name: "alpha", Metadata: SkillMetadata{Description: "Use this skill for alpha common data."}, Dir: "/a"},
		{Name: "gamma", Metadata: SkillMetadata{Description: "Use this skill for common data."}, Dir: "/c"},
		{Name: "delta", Metadata: SkillMetadata{Description: "Use this skill for common data."}, Dir: "/d"},
		{Name: "epsilon", Metadata: SkillMetadata{Description: "Use this skill for common data."}, Dir: "/e"},
		{Name: "zeta", Metadata: SkillMetadata{Description: "Use this skill for common data."}, Dir: "/z"},
	}

	matched := MatchSkills("please use alpha with common data", skills)
	if len(matched) != maxMatchedSkills {
		t.Fatalf("expected %d matched skills, got %d", maxMatchedSkills, len(matched))
	}
	if matched[0].Name != "alpha" {
		t.Fatalf("expected alpha first, got %q", matched[0].Name)
	}
	if matched[1].Name != "beta" {
		t.Fatalf("expected beta second by name tie-break, got %q", matched[1].Name)
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
