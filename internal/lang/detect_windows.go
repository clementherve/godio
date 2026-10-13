//go:build windows

package lang

import "golang.org/x/sys/windows"

// Detect reads the user's preferred interface language from Windows.
//
// GetUserPreferredUILanguages returns the list the user actually ordered in
// Settings, most preferred first, which is a better answer than the system
// locale: a French keyboard and date format does not mean a French interface.
func Detect() Lang {
	tags, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return EN
	}
	for _, tag := range tags {
		if l := fromTag(tag); l == FR {
			return FR
		}
	}
	return EN
}
