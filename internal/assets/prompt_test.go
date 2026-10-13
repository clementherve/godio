package assets

import (
	"strings"
	"testing"

	"github.com/clementherve/godio/internal/lang"
)

// TestExtractPrompt checks what the pipeline relies on: every language and
// style has a fragment, the fragment lands in the prompt, and the structure the
// rest of godio depends on survives whichever pair is picked.
func TestExtractPrompt(t *testing.T) {
	// The four sections and the checkbox are the contract: obsidian renders the
	// block and CountActions counts the checkboxes under the last section.
	languages := []struct {
		l        lang.Lang
		sections []string
		ending   string
	}{
		{lang.FR, []string{"**Résumé**", "**Points clés**", "**Décisions**", "**Actions**", "- [ ]"}, "sur l'entrée standard."},
		{lang.EN, []string{"**Summary**", "**Key points**", "**Decisions**", "**Actions**", "- [ ]"}, "on standard input."},
	}

	seen := map[string]bool{}
	for _, tc := range languages {
		for _, style := range Styles {
			t.Run(string(tc.l)+"/"+style, func(t *testing.T) {
				p := ExtractPrompt(tc.l, style)
				if p == "" {
					t.Fatal("no prompt for this language and style")
				}
				if strings.Contains(p, stylePlaceholder) {
					t.Error("the style placeholder was left in the prompt")
				}
				for _, s := range tc.sections {
					if !strings.Contains(p, s) {
						t.Errorf("prompt lost %q", s)
					}
				}
				if !strings.HasSuffix(strings.TrimSpace(p), tc.ending) {
					t.Error("the prompt no longer ends by handing over to the transcript")
				}
				if seen[p] {
					t.Error("this language and style produce the same prompt as another pair")
				}
				seen[p] = true
			})
		}
	}
}

// TestExtractPromptLabels covers replaying an old session: transcripts written
// before the labels were translated carry the French ones, and --day makes
// re-reading them routine, so both prompts have to name both label sets.
func TestExtractPromptLabels(t *testing.T) {
	for _, l := range []lang.Lang{lang.FR, lang.EN} {
		p := ExtractPrompt(l, StyleNormal)
		for _, label := range []string{"Moi", "Interlocuteur", "Me", "Them"} {
			if !strings.Contains(p, label) {
				t.Errorf("the %s prompt does not mention the %q label", l, label)
			}
		}
	}
}

func TestExtractPromptUnknownStyle(t *testing.T) {
	// A typo in config.toml must still produce notes, not an error after the
	// meeting has already been recorded.
	for _, style := range []string{"", "Normal", "verbose", "bullet"} {
		if got := ExtractPrompt(lang.FR, style); got != ExtractPrompt(lang.FR, StyleNormal) {
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
