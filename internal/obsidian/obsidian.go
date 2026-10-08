// Package obsidian appends meeting notes to an Obsidian daily note.
//
// The daily note is an ordinary Markdown file that the user also edits by hand,
// so every operation here is strictly additive: existing bytes are never
// rewritten, reordered or reformatted.
package obsidian

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Entry is one meeting's notes.
type Entry struct {
	Title string    // short meeting name, used in the subheading
	At    time.Time // when the recording started
	Body  string    // Markdown returned by Claude
}

// Append inserts the entry into the note at path, under the given heading
// (e.g. "## Réunions"), creating the file and/or the heading as needed.
func Append(path, heading string, e Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating daily-note folder: %w", err)
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading daily note: %w", err)
	}

	updated := Insert(string(existing), heading, e)

	// Write via a temp file in the same directory, then rename, so a crash or a
	// full disk can never leave a half-written note where a good one used to be.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".raudio-*.md")
	if err != nil {
		return fmt.Errorf("creating temp note: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.WriteString(updated); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp note: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp note: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replacing daily note: %w", err)
	}
	return nil
}

// Insert returns doc with the entry added under heading. It is pure, which is
// what makes the section logic testable without touching a vault.
func Insert(doc, heading string, e Entry) string {
	block := renderEntry(e)

	lines := strings.Split(doc, "\n")
	at, found := sectionEnd(lines, heading)

	if !found {
		// No such heading: start one at the end of the document.
		out := strings.TrimRight(doc, "\n")
		if out != "" {
			out += "\n\n"
		}
		return out + heading + "\n\n" + block
	}

	head := strings.Join(lines[:at], "\n")
	tail := strings.Join(lines[at:], "\n")

	head = strings.TrimRight(head, "\n")
	out := head + "\n\n" + strings.TrimRight(block, "\n") + "\n"
	if strings.TrimSpace(tail) != "" {
		out += "\n" + strings.TrimLeft(tail, "\n")
	}
	return out
}

// renderEntry formats one meeting as a Markdown block.
func renderEntry(e Entry) string {
	title := strings.TrimSpace(e.Title)
	if title == "" {
		title = "Réunion"
	}
	return fmt.Sprintf("### %s — %s\n\n%s\n",
		e.At.Format("15:04"), title, strings.TrimSpace(e.Body))
}

// sectionEnd finds the line index just past the end of heading's section,
// i.e. the next heading at the same or a shallower level, or end of document.
//
// Trailing blank lines are excluded so the new entry lands tight against the
// previous content rather than drifting further down on every append.
func sectionEnd(lines []string, heading string) (int, bool) {
	want := strings.TrimSpace(heading)
	level := headingLevel(want)
	if level == 0 {
		return 0, false
	}

	start := -1
	inFence := false
	for i, ln := range lines {
		if isFence(ln) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if start < 0 {
			if strings.TrimSpace(ln) == want {
				start = i
			}
			continue
		}
		if l := headingLevel(strings.TrimSpace(ln)); l > 0 && l <= level {
			return trimBlankBefore(lines, i), true
		}
	}
	if start < 0 {
		return 0, false
	}
	return trimBlankBefore(lines, len(lines)), true
}

// trimBlankBefore walks back from i over blank lines.
func trimBlankBefore(lines []string, i int) int {
	for i > 0 && strings.TrimSpace(lines[i-1]) == "" {
		i--
	}
	return i
}

// headingLevel returns the number of leading '#' of an ATX heading, or 0.
func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}

// isFence reports whether the line opens or closes a fenced code block.
func isFence(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}
