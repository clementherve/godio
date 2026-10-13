package lang

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoStrayFrench walks godio's sources and fails on a French string literal
// outside this package.
//
// The completeness test proves both catalogues are filled; it cannot see a
// *call site* that was never converted, which is the mistake that leaves one
// French sentence in an English interface. Only reading the sources catches
// that, in the same spirit as CI's check that the generated icons are current.
func TestNoStrayFrench(t *testing.T) {
	root := "../.."

	// Accented letters and the French quotation marks. A literal without any of
	// these can still be French ("Prochaine session"), so this is a net, not a
	// proof — it catches the overwhelming majority at no cost.
	french := regexp.MustCompile(`[àâçéèêëîïôùûœÀÂÇÉÈÊËÎÏÔÙÛŒ«»]`)
	literal := regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

	// Allowed: this package holds the French catalogue by definition, tests
	// carry French fixtures on purpose, and the prompts are French text.
	skipDir := map[string]bool{"lang": true, "prompts": true, ".git": true}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			// Comments are prose for whoever reads the code, not interface text.
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, lit := range literal.FindAllString(line, -1) {
				if french.MatchString(lit) {
					t.Errorf("%s:%d: French left in a literal: %s\n\tmove it to internal/lang",
						filepath.Clean(path), i+1, lit)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
