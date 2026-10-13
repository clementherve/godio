package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseDay(t *testing.T) {
	now := time.Date(2026, 10, 13, 15, 4, 5, 0, time.Local)
	want := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	}

	ok := []struct {
		in   string
		want time.Time
	}{
		{"2026-10-12", want(2026, 10, 12)},
		{"today", want(2026, 10, 13)},
		{"aujourd'hui", want(2026, 10, 13)},
		{"yesterday", want(2026, 10, 12)},
		{"hier", want(2026, 10, 12)},
		{"Hier", want(2026, 10, 12)},
		{" hier ", want(2026, 10, 12)},
		{"-2", want(2026, 10, 11)},
		{"-13", want(2026, 9, 30)}, // crosses a month boundary
	}
	for _, tc := range ok {
		got, err := ParseDay(tc.in, now)
		if err != nil {
			t.Errorf("ParseDay(%q) = error %v", tc.in, err)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("ParseDay(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}

	// "today" must be midnight, not now: the result is used as a folder-name
	// prefix, and it is also what a purge cutoff would be measured from.
	if got, _ := ParseDay("today", now); got.Hour() != 0 || got.Minute() != 0 {
		t.Errorf("ParseDay(today) = %s, want midnight", got)
	}

	for _, bad := range []string{"", "demain", "2026-13-01", "12/10/2026", "2", "hier soir"} {
		if _, err := ParseDay(bad, now); err == nil {
			t.Errorf("ParseDay(%q) accepted a value it should not", bad)
		}
	}
}

func TestOnDay(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"2026-10-12_090000",
		"2026-10-12_143000",
		"2026-10-12_110000",
		"2026-10-13_090000",
		"notes-perso", // not a session: must be ignored, not opened
	} {
		mkSession(t, root, name)
	}

	day, err := ParseDay("2026-10-12", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got, err := OnDay(root, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("OnDay returned %d sessions, want 3", len(got))
	}
	// Chronological order matters: replaying a day writes the meetings back in
	// the order they happened.
	for i, want := range []string{"2026-10-12_090000", "2026-10-12_110000", "2026-10-12_143000"} {
		if base := filepath.Base(got[i].Dir); base != want {
			t.Errorf("session %d = %s, want %s", i, base, want)
		}
	}

	empty, _ := ParseDay("2026-10-14", time.Now())
	if _, err := OnDay(root, empty); err == nil {
		t.Error("a day with no session returned no error")
	}
}

// mkSession creates a minimal session folder, enough for OpenSession.
func mkSession(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(`{"started":"2026-10-12T09:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
