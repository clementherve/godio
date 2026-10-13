//go:build !windows

package lang

import "testing"

// TestDetectFromEnvironment covers the POSIX half of Detect. It is build-tagged
// because Windows reads the UI language the user chose in Settings, which no
// environment variable overrides — asserting on LC_ALL there would be testing
// this file's implementation rather than the rule.
func TestDetectFromEnvironment(t *testing.T) {
	clear := func(t *testing.T) {
		t.Helper()
		for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
			t.Setenv(key, "")
		}
	}

	t.Run("LANG", func(t *testing.T) {
		clear(t)
		t.Setenv("LANG", "fr_FR.UTF-8")
		if got := Detect(); got != FR {
			t.Errorf("Detect() = %q, want %q", got, FR)
		}
	})

	// LC_ALL outranks the rest, as POSIX says.
	t.Run("LC_ALL wins", func(t *testing.T) {
		clear(t)
		t.Setenv("LANG", "fr_FR.UTF-8")
		t.Setenv("LC_MESSAGES", "fr_FR.UTF-8")
		t.Setenv("LC_ALL", "en_US.UTF-8")
		if got := Detect(); got != EN {
			t.Errorf("Detect() = %q, want %q", got, EN)
		}
	})

	t.Run("LC_MESSAGES over LANG", func(t *testing.T) {
		clear(t)
		t.Setenv("LANG", "en_US.UTF-8")
		t.Setenv("LC_MESSAGES", "fr_FR.UTF-8")
		if got := Detect(); got != FR {
			t.Errorf("Detect() = %q, want %q", got, FR)
		}
	})

	// An unset environment is the common case on Windows and in CI.
	t.Run("nothing set", func(t *testing.T) {
		clear(t)
		if got := Detect(); got != EN {
			t.Errorf("Detect() = %q, want %q", got, EN)
		}
	})
}
