package assets

import (
	"embed"
	"strings"

	"github.com/clementherve/godio/internal/lang"
)

//go:embed prompts/extract_*.md prompts/styles/*.md
var prompts embed.FS

// Note styles. They vary how the notes are written; they never vary what the
// notes contain structurally, because Write and CountActions depend on the four
// sections being there whatever the style.
const (
	// StyleBullets is telegraphic: one line per point, nothing else.
	StyleBullets = "bullets"
	// StyleNormal is the default, and reproduces godio's original notes.
	StyleNormal = "normal"
	// StyleDetailed keeps the numbers, the arguments and the open questions.
	StyleDetailed = "detailed"
	// StyleELI5 explains the jargon for someone outside the subject.
	StyleELI5 = "eli5"
)

// Styles lists the note styles, from the tersest to the most explanatory. The
// order is the one the menus and the help text present.
var Styles = []string{StyleBullets, StyleNormal, StyleDetailed, StyleELI5}

// ValidStyle reports whether s names a style.
func ValidStyle(s string) bool {
	for _, known := range Styles {
		if s == known {
			return true
		}
	}
	return false
}

// stylePlaceholder is where the style fragment lands in the base prompt: after
// the rules, so it refines them, and before the line handing over to the
// transcript.
const stylePlaceholder = "{{STYLE}}"

// ExtractPrompt returns the extraction prompt for a language and a style.
//
// The language here is the language of the *notes*, which follows
// whisper.language rather than the interface: a French meeting is written up
// in French whatever the menus say.
//
// An unknown style falls back to the default rather than failing: a typo in
// config.toml must not cost someone the notes of a meeting already recorded.
// The CLI flag is stricter, because there a typo is still correctable.
func ExtractPrompt(l lang.Lang, style string) string {
	if !ValidStyle(style) {
		style = StyleNormal
	}
	suffix := "_en.md"
	if l == lang.FR {
		suffix = "_fr.md"
	}

	base, err := prompts.ReadFile("prompts/extract" + suffix)
	if err != nil {
		return ""
	}
	fragment, err := prompts.ReadFile("prompts/styles/" + style + suffix)
	if err != nil {
		// Only reachable if a style is listed without its file being embedded,
		// which the tests catch; drop the placeholder and keep going.
		return strings.ReplaceAll(string(base), stylePlaceholder, "")
	}
	return strings.ReplaceAll(string(base), stylePlaceholder, strings.TrimSpace(string(fragment)))
}
