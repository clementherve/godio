//go:build !windows

package lang

import "os"

// Detect reads the language from the POSIX locale environment.
//
// godio runs on Windows; this exists so the interface can be exercised during
// development on another system, which is also how the English catalogue gets
// tested at all before a release.
func Detect() Lang {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(key); v != "" {
			return fromTag(v)
		}
	}
	return EN
}
