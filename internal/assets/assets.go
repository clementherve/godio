// Package assets holds the files compiled into the godio binary.
package assets

import _ "embed"

// ExtractPromptFR is the French meeting-notes extraction prompt sent to Claude.
//
//go:embed prompts/extract_fr.md
var ExtractPromptFR string
