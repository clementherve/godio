// Package lang holds godio's interface strings in French and English.
//
// The catalogue is a struct rather than a map: every use site is then checked
// by the compiler, so a mistyped name is a build failure instead of a blank
// menu item, and adding a string to one language without the other is caught
// by a test rather than by a user.
//
// The interface language is not the language of the notes. Notes follow
// whisper.language — the language the meeting was actually held in — because
// the vault, the names and the jargon in it are that language whatever the
// menus say.
package lang

import (
	"strings"
	"sync/atomic"
)

// Lang is a supported interface language.
type Lang string

// The supported languages. Anything else resolves to English.
const (
	FR Lang = "fr"
	EN Lang = "en"
)

// Strings is the whole catalogue, grouped by where the text appears.
type Strings struct {
	// Lang names the catalogue, so code holding one can tell which it has.
	Lang   Lang
	CLI    CLIStrings
	Tray   TrayStrings
	Doctor DoctorStrings
	Err    ErrStrings
	Notes  NotesStrings
}

// current holds the catalogue in use. One language per process, set once at
// startup — and again by the tray when the configuration is reloaded.
var current atomic.Pointer[Strings]

// T returns the catalogue in use, defaulting to English before Use is called
// so a string fetched during initialisation is never empty.
func T() Strings {
	if s := current.Load(); s != nil {
		return *s
	}
	return catalogue(EN)
}

// Use selects the interface language.
func Use(l Lang) {
	s := catalogue(l)
	current.Store(&s)
}

// Current returns the language in use.
func Current() Lang { return T().Lang }

func catalogue(l Lang) Strings {
	if l == FR {
		return french
	}
	return english
}

// Resolve turns the language setting from config.toml into a language.
//
// "auto" asks the operating system; an unknown value does the same rather than
// failing, because a typo in config.toml must not stop godio from starting.
func Resolve(setting string) Lang {
	switch Lang(strings.ToLower(strings.TrimSpace(setting))) {
	case FR:
		return FR
	case EN:
		return EN
	default:
		return Detect()
	}
}

// fromTag maps a locale tag ("fr-FR", "fr_FR.UTF-8", "en-GB") to a language.
// Only French is detected; everything else gets English, which is the language
// the project documents itself in.
func fromTag(tag string) Lang {
	t := strings.ToLower(strings.TrimSpace(tag))
	if strings.HasPrefix(t, "fr") {
		return FR
	}
	return EN
}
