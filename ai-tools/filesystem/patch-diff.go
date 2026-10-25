package filesystem

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

type patchHunk struct {
	position int
	oldLines []string
	newLines []string
}

var hunkHeader = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@(?: .*)?$`)

func parsePatch(patch, path string) ([]patchHunk, error) {
	if !utf8.ValidString(patch) {
		return nil, fmt.Errorf("patch is not valid UTF-8")
	}
	lines := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	index := 0
	if strings.HasPrefix(lines[0], "--- ") {
		if len(lines) < 2 || !strings.HasPrefix(lines[1], "+++ ") {
			return nil, fmt.Errorf("patch requires paired ---/+++ file headers")
		}
		requestedPath := filepath.ToSlash(filepath.Clean(path))
		for offset, prefix := range []string{"a/", "b/"} {
			name, _, _ := strings.Cut(strings.TrimSuffix(lines[offset][4:], "\r"), "\t")
			if name != requestedPath && name != prefix+requestedPath {
				return nil, fmt.Errorf("patch header %q must name the requested path %q", name, path)
			}
		}
		index = 2
	}
	var hunks []patchHunk
	previousEnd, lineDelta := 0, 0
	for index < len(lines) {
		fields := hunkHeader.FindStringSubmatch(strings.TrimSuffix(lines[index], "\r"))
		if fields == nil {
			return nil, fmt.Errorf("expected unified diff hunk header at patch line %d", index+1)
		}
		numbers := [4]int{0, 1, 0, 1}
		for fieldIndex, value := range fields[1:] {
			if value == "" {
				continue
			}
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid hunk range: %w", err)
			}
			numbers[fieldIndex] = parsed
		}
		if (numbers[0] == 0 && numbers[1] != 0) || (numbers[2] == 0 && numbers[3] != 0) {
			return nil, fmt.Errorf("non-empty hunk ranges must start at line 1 or later")
		}
		hunk := patchHunk{position: numbers[0]}
		if numbers[1] != 0 {
			hunk.position--
		}
		newPosition := numbers[2]
		if numbers[3] != 0 {
			newPosition--
		}
		// Compare by subtraction to avoid overflowing large user-supplied ranges.
		if hunk.position < previousEnd || newPosition-hunk.position != lineDelta {
			return nil, fmt.Errorf("hunk ranges must be ordered, non-overlapping and consistent")
		}
		index++
		previous := byte(0)
		changed := false
		for index < len(lines) && !strings.HasPrefix(lines[index], "@@ ") {
			line := lines[index]
			index++
			if strings.TrimSuffix(line, "\r") == `\ No newline at end of file` {
				if previous == 0 {
					return nil, fmt.Errorf("misplaced no-newline marker at patch line %d", index)
				}
				if previous != '+' {
					hunk.oldLines[len(hunk.oldLines)-1] = strings.TrimSuffix(hunk.oldLines[len(hunk.oldLines)-1], "\n")
				}
				if previous != '-' {
					hunk.newLines[len(hunk.newLines)-1] = strings.TrimSuffix(hunk.newLines[len(hunk.newLines)-1], "\n")
				}
				previous = 0
				continue
			}
			// Stop exactly at the declared counts; additional file headers or hunks
			// must not be interpreted as ordinary removed/added file content.
			if len(hunk.oldLines) == numbers[1] && len(hunk.newLines) == numbers[3] {
				return nil, fmt.Errorf("unexpected content after hunk at patch line %d", index)
			}
			if line == "" || !strings.ContainsRune(" +-", rune(line[0])) {
				return nil, fmt.Errorf("invalid hunk body at patch line %d", index)
			}
			previous = line[0]
			if previous != '+' {
				hunk.oldLines = append(hunk.oldLines, line[1:]+"\n")
			}
			if previous != '-' {
				hunk.newLines = append(hunk.newLines, line[1:]+"\n")
			}
			changed = changed || previous != ' '
		}
		if !changed || len(hunk.oldLines) != numbers[1] || len(hunk.newLines) != numbers[3] {
			return nil, fmt.Errorf("hunk must contain changes and match its declared line counts")
		}
		if numbers[1] > math.MaxInt-hunk.position {
			return nil, fmt.Errorf("hunk range overflows")
		}
		previousEnd = hunk.position + numbers[1]
		lineDelta += numbers[3] - numbers[1]
		hunks = append(hunks, hunk)
	}
	if len(hunks) == 0 {
		return nil, fmt.Errorf("patch must contain at least one unified diff hunk")
	}
	return hunks, nil
}

func applyHunks(ctx context.Context, content []byte, hunks []patchHunk, limit int) ([]byte, error) {
	lines := strings.SplitAfter(string(content), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var output strings.Builder
	terminated := true
	appendLines := func(block []string) error {
		for _, line := range block {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !terminated {
				return fmt.Errorf("no-newline marker is only valid at end of file")
			}
			if len(line) > limit-output.Len() {
				return fmt.Errorf("patched file exceeds maxWriteBytes (%d)", limit)
			}
			output.WriteString(line)
			terminated = strings.HasSuffix(line, "\n")
		}
		return nil
	}
	cursor := 0
	for hunkIndex, hunk := range hunks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		position := hunk.position
		matches := func(position int) bool {
			return position >= cursor && position <= len(lines)-len(hunk.oldLines) && slices.Equal(lines[position:position+len(hunk.oldLines)], hunk.oldLines)
		}
		if !matches(position) {
			if len(hunk.oldLines) == 0 {
				return nil, fmt.Errorf("hunk %d insertion position is outside the remaining file", hunkIndex+1)
			}
			position = -1
			for candidate := cursor; candidate <= len(lines)-len(hunk.oldLines); candidate++ {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if matches(candidate) {
					if position >= 0 {
						return nil, fmt.Errorf("hunk %d context is ambiguous; include more context or correct line numbers", hunkIndex+1)
					}
					position = candidate
				}
			}
			if position < 0 {
				return nil, fmt.Errorf("hunk %d context does not match the file", hunkIndex+1)
			}
		}
		if err := appendLines(lines[cursor:position]); err != nil {
			return nil, err
		}
		if err := appendLines(hunk.newLines); err != nil {
			return nil, err
		}
		cursor = position + len(hunk.oldLines)
	}
	if err := appendLines(lines[cursor:]); err != nil {
		return nil, err
	}
	return []byte(output.String()), nil
}
