package agentskill_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	agentskill "github.com/SmilingXinyi/agent-engine/agent-skill"
	"github.com/SmilingXinyi/agent-engine/agent-skill/protocol"
)

func writeSkill(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestParseFrontmatterPreservesBody(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		body := "\nRead [reference](references/guide.md)." + newline + "  preserve indentation" + newline
		content := "\ufeff" + strings.Join([]string{
			"---", "name: ' demo '", "description: >-", "  First line:", "  中文说明。", "license: MIT", "---", "",
		}, newline) + body
		skill, err := agentskill.Parse(content)
		if err != nil || skill.Name != "demo" || skill.Description != "First line: 中文说明。" || skill.Body != body {
			t.Fatalf("skill=%#v error=%v", skill, err)
		}
	}
}

func TestParseRejectsMalformedAndIncompleteSkills(t *testing.T) {
	for _, content := range []string{
		"plain markdown", "---\nname: demo", "---\nname: [\n---\ninstructions",
		"---\nname: demo\nname: other\ndescription: info\n---\ninstructions",
		"---\nname: demo\n---\ninstructions",
		"---\ndescription: info\n---\ninstructions",
		"---\nname: demo\ndescription: info\n---\n \t\n",
	} {
		if _, err := agentskill.Parse(content); err == nil {
			t.Fatalf("accepted %q", content)
		}
	}
}

func TestLoadSortsAndDoesNotDiscoverResourcesAsSkills(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "nested", "zeta", "SKILL.md"), "---\nname: zeta\ndescription: zeta description\n---\nzeta body")
	writeSkill(t, filepath.Join(root, "alpha", "SKILL.md"), "---\nname: alpha\ndescription: alpha description\n---\nalpha body")
	writeSkill(t, filepath.Join(root, "alpha", "references", "SKILL.md"), "not a skill")
	writeSkill(t, filepath.Join(root, "notes.md"), "not a skill")
	skills, err := agentskill.Load(root)
	if err != nil || len(skills) != 2 {
		t.Fatalf("skills=%#v error=%v", skills, err)
	}
	if skills[0].Name != "alpha" || skills[1].Name != "zeta" || skills[0].Path != filepath.Join(root, "alpha", "SKILL.md") {
		t.Fatalf("skills=%#v", skills)
	}
	listing := agentskill.BuildListing(skills)
	if !strings.Contains(listing, "alpha description") || strings.Contains(listing, "alpha body") {
		t.Fatal(listing)
	}
	rootSkill, err := agentskill.Load(filepath.Join(root, "alpha"))
	if err != nil || !reflect.DeepEqual(rootSkill, skills[:1]) {
		t.Fatalf("root skill=%#v error=%v", rootSkill, err)
	}
}

func TestLoadReportsDuplicateAndMalformedFiles(t *testing.T) {
	if _, err := agentskill.Load(""); err == nil {
		t.Fatal("accepted an empty root")
	}
	for _, second := range []string{"---\nname: demo\ndescription: second\n---\nbody", "malformed"} {
		root := t.TempDir()
		writeSkill(t, filepath.Join(root, "a", "SKILL.md"), "---\nname: demo\ndescription: first\n---\nbody")
		path := filepath.Join(root, "b", "SKILL.md")
		writeSkill(t, path, second)
		if _, err := agentskill.Load(root); err == nil || !strings.Contains(err.Error(), path) {
			t.Fatal(err)
		}
	}
	if _, err := agentskill.Load(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "file")
	writeSkill(t, file, "file")
	if _, err := agentskill.Load(file); err == nil {
		t.Fatal("accepted file as root")
	}
}

func TestListingSortsWithoutChangingCaller(t *testing.T) {
	skills := []protocol.Skill{{Name: "zeta", Description: "one\ntwo"}, {Name: "alpha", Description: "alpha"}}
	before := append([]protocol.Skill(nil), skills...)
	listing := agentskill.BuildListing(skills)
	if !reflect.DeepEqual(skills, before) || strings.Index(listing, "- alpha") > strings.Index(listing, "- zeta") || !strings.Contains(listing, "one two") {
		t.Fatal(listing)
	}
	if agentskill.BuildListing(nil) != "" {
		t.Fatal("empty listing must be empty")
	}
}
