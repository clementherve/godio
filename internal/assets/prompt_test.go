package assets

import (
	"strings"
	"testing"
)

// TestExtractPromptFR checks what the pipeline relies on: every style has a
// fragment, the fragment lands in the prompt, and the structure the rest of
// godio parses survives whichever style is picked.
func TestExtractPromptFR(t *testing.T) {
	// The four section names are a contract: obsidian renders them and
	// CountActions counts the checkboxes under the last one.
	sections := []string{"**Résumé**", "**Points clés**", "**Décisions**", "**Actions**", "- [ ]"}

	seen := map[string]bool{}
	for _, style := range Styles {
		t.Run(style, func(t *testing.T) {
			p := ExtractPromptFR(style)
			if strings.Contains(p, stylePlaceholder) {
				t.Error("the style placeholder was left in the prompt")
			}
			for _, s := range sections {
				if !strings.Contains(p, s) {
					t.Errorf("prompt lost %q", s)
				}
			}
			if !strings.HasSuffix(strings.TrimSpace(p), "sur l'entrée standard.") {
				t.Error("the prompt no longer ends by handing over to the transcript")
			}
			if seen[p] {
				t.Error("this style produces the same prompt as another one")
			}
			seen[p] = true
		})
	}
}

func TestExtractPromptFRUnknownStyle(t *testing.T) {
	// A typo in config.toml must still produce notes, not an error after the
	// meeting has already been recorded.
	for _, style := range []string{"", "Normal", "verbose", "bullet"} {
		if got := ExtractPromptFR(style); got != ExtractPromptFR(StyleNormal) {
			t.Errorf("%q did not fall back to %q", style, StyleNormal)
		}
	}
}

func TestValidStyle(t *testing.T) {
	for _, s := range Styles {
		if !ValidStyle(s) {
			t.Errorf("%q is listed but not valid", s)
		}
	}
	if ValidStyle("eli10") {
		t.Error("an unknown style was accepted")
	}
}
