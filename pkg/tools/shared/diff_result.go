package toolshared

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

const (
	noContentChangeDiffMessage = "(no content change)"
	noNewlineAtEOFMarker       = `\ No newline at end of file`
)

// DiffResult creates a user-visible tool result containing a unified diff for
// a successful file edit. The diff is included for both the LLM and the user so
// the follow-up assistant response can reason about the resulting change set,
// including EOF newline transitions.
func DiffResult(path string, before, after []byte) *ToolResult {
	diff, err := buildUnifiedDiff(path, before, after)
	if err != nil {
		return UserResult(fmt.Sprintf("File edited: %s\n[diff unavailable: %v]", path, err))
	}

	content := fmt.Sprintf("File edited: %s\n```diff\n%s\n```", path, diff)
	return UserResult(content)
}

func buildUnifiedDiff(path string, before, after []byte) (string, error) {
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        splitDiffLinesPreservingEOF(before),
		B:        splitDiffLinesPreservingEOF(after),
		FromFile: "a/" + diffDisplayPath(path),
		ToFile:   "b/" + diffDisplayPath(path),
		Context:  3,
	})
	if err != nil {
		return "", err
	}

	diff = strings.TrimRight(diff, "\n")
	if diff == "" {
		return noContentChangeDiffMessage, nil
	}

	return diff, nil
}

func splitDiffLinesPreservingEOF(content []byte) []string {
	if len(content) == 0 {
		return nil
	}

	lines := make([]string, 0, bytes.Count(content, []byte{'\n'})+1)
	lineStart := 0
	for i, b := range content {
		if b != '\n' {
			continue
		}
		lines = append(lines, string(content[lineStart:i+1]))
		lineStart = i + 1
	}
	if lineStart < len(content) {
		lines = append(lines, string(content[lineStart:]))
	}

	if lacksTrailingNewline(content) {
		lines[len(lines)-1] += "\n"
		lines = append(lines, noNewlineAtEOFMarker+"\n")
	}

	return lines
}

func lacksTrailingNewline(content []byte) bool {
	return len(content) > 0 && !bytes.HasSuffix(content, []byte("\n"))
}

func diffDisplayPath(path string) string {
	displayPath := strings.TrimLeft(filepath.ToSlash(path), "/")
	if displayPath == "" {
		return "file"
	}
	return displayPath
}
