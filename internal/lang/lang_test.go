package lang

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// walk calls fn for every string field of a catalogue, with its dotted path.
func walk(t *testing.T, s Strings, fn func(path, value string)) {
	t.Helper()
	v := reflect.ValueOf(s)
	for i := 0; i < v.NumField(); i++ {
		group := v.Field(i)
		if group.Kind() != reflect.Struct {
			continue
		}
		name := v.Type().Field(i).Name
		for j := 0; j < group.NumField(); j++ {
			fn(name+"."+group.Type().Field(j).Name, group.Field(j).String())
		}
	}
}

// TestCatalogueComplete is the test that makes a struct worth more than a map:
// a string added to one language and forgotten in the other fails here instead
// of showing up as a blank menu item.
func TestCatalogueComplete(t *testing.T) {
	for _, c := range []Strings{french, english} {
		walk(t, c, func(path, value string) {
			if strings.TrimSpace(value) == "" {
				t.Errorf("%s.%s is empty", c.Lang, path)
			}
		})
	}

	if french.Lang != FR || english.Lang != EN {
		t.Error("a catalogue does not know which language it is")
	}
}

// verbs extracts the format verbs of a string, in order. %% is an escaped
// percent sign, not a verb.
var verbPattern = regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z]`)

func verbs(s string) []string {
	return verbPattern.FindAllString(strings.ReplaceAll(s, "%%", ""), -1)
}

// TestFormatVerbsMatch guards the failure that only shows up in the language
// nobody runs: a translated format string whose verbs no longer match the
// arguments the call site passes.
func TestFormatVerbsMatch(t *testing.T) {
	fr := map[string]string{}
	walk(t, french, func(path, value string) { fr[path] = value })

	walk(t, english, func(path, value string) {
		a, b := verbs(fr[path]), verbs(value)
		if strings.Join(a, " ") != strings.Join(b, " ") {
			t.Errorf("%s: French has %v, English has %v", path, a, b)
		}
	})
}

// TestFmtNaming keeps the convention honest: a field taking arguments is named
// …Fmt, so a reader can tell at the call site.
func TestFmtNaming(t *testing.T) {
	walk(t, french, func(path, value string) {
		// …Fmt, or …FmtW / …FmtS where two variants of one message differ
		// only by the verb they wrap.
		named := strings.Contains(path, "Fmt")
		takesArgs := len(verbs(value)) > 0
		if takesArgs && !named {
			t.Errorf("%s takes arguments %v but is not named …Fmt", path, verbs(value))
		}
		if named && !takesArgs {
			t.Errorf("%s is named …Fmt but takes no arguments", path)
		}
	})
}

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Lang
	}{
		{"fr", FR},
		{"FR", FR},
		{" fr ", FR},
		{"en", EN},
	} {
		if got := Resolve(tc.in); got != tc.want {
			t.Errorf("Resolve(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// "auto" and anything unrecognised both defer to the system: a typo in
	// config.toml must not stop godio from starting.
	//
	// The assertion is against Detect() rather than a fixed language, because
	// what the system answers is platform-specific — the environment on POSIX,
	// the Windows UI language on Windows, where no environment variable would
	// change it. Detection itself is covered by TestFromTag and, where it
	// reads the environment, by TestDetectFromEnvironment.
	for _, in := range []string{"auto", "", "français", "de"} {
		if got, want := Resolve(in), Detect(); got != want {
			t.Errorf("Resolve(%q) = %q, want the detected language %q", in, got, want)
		}
	}
}

func TestFromTag(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Lang
	}{
		{"fr-FR", FR},
		{"fr_FR.UTF-8", FR},
		{"fr", FR},
		{"FR-ca", FR},
		{"en-GB", EN},
		{"de-DE", EN},
		{"", EN},
	} {
		if got := fromTag(tc.in); got != tc.want {
			t.Errorf("fromTag(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUseAndT(t *testing.T) {
	// T must answer before Use has ever been called, so a string fetched
	// during package initialisation is never empty.
	current.Store(nil)
	if T().Tray.Ready == "" {
		t.Error("T() before Use returned an empty catalogue")
	}

	Use(FR)
	if Current() != FR || T().Tray.Quit != "Quitter" {
		t.Errorf("Use(FR) did not take: %q", T().Tray.Quit)
	}
	Use(EN)
	if Current() != EN || T().Tray.Quit != "Quit" {
		t.Errorf("Use(EN) did not take: %q", T().Tray.Quit)
	}
}
