package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"
)

func TestPatchCommitRejectsInterveningEditAndCleansTemporaryFile(t *testing.T) {
	for _, replaceIdentity := range []bool{false, true} {
		root, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		if err := root.WriteFile("note", []byte("old\n"), 0600); err != nil {
			t.Fatal(err)
		}
		source, err := readPatchSource(root, "note", 1024)
		if err != nil {
			t.Fatal(err)
		}
		if replaceIdentity {
			if err := root.WriteFile("external", []byte("external\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := root.Rename("external", "note"); err != nil {
				t.Fatal(err)
			}
		} else if err := root.WriteFile("note", []byte("external\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := replacePatchedFile(context.Background(), root, "note", source, []byte("patched\n"), 1024); err == nil {
			t.Fatal("intervening edit overwritten")
		}
		actual, err := root.ReadFile("note")
		if err != nil || string(actual) != "external\n" {
			t.Fatalf("external edit=%q err=%v", actual, err)
		}
		entries, err := os.ReadDir(root.Name())
		if err != nil || len(entries) != 1 || filepath.Base(entries[0].Name()) != "note" {
			t.Fatalf("temporary files leaked: %v err=%v", entries, err)
		}
	}
}

func FuzzParseAndApplyHunks(f *testing.F) {
	f.Add("old\n", "@@ -1 +1 @@\n-old\n+new\n")
	f.Add("", "@@ -0,0 +1 @@\n+new\n")
	f.Add("old", "@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n")
	f.Add("old\n", "@@ -9223372036854775807 +9223372036854775807 @@\n-old\n+new\n")
	f.Fuzz(func(t *testing.T, source, patch string) {
		const limit = 64 << 10
		if len(source) > limit || len(patch) > limit || !utf8.ValidString(source) {
			return
		}
		hunks, err := parsePatch(patch, "note")
		if err != nil {
			return
		}
		updated, err := applyHunks(context.Background(), []byte(source), hunks, limit)
		if err == nil && (len(updated) > limit || !utf8.Valid(updated)) {
			t.Fatalf("invalid patch result: bytes=%d", len(updated))
		}
	})
}
