package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// session builds a session folder aged by the name it is given, optionally with
// audio and with notes.
func purgeSession(t *testing.T, root string, started time.Time, audio, notes bool) string {
	t.Helper()
	dir := mkSession(t, root, started.Format("2006-01-02_150405"))
	write := func(name string, n int) {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("transcript.txt", 100)
	if audio {
		write("me.wav", 1000)
		write("them.wav", 1000)
	}
	if notes {
		write("notes.md", 50)
	}
	return dir
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestPurge(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.Local)
	day := 24 * time.Hour

	root := t.TempDir()
	fresh := purgeSession(t, root, now.Add(-2*day), true, true)
	middle := purgeSession(t, root, now.Add(-20*day), true, true)
	old := purgeSession(t, root, now.Add(-100*day), true, true)
	unprocessed := purgeSession(t, root, now.Add(-101*day), true, false)
	if err := os.MkdirAll(filepath.Join(root, "mes-archives"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldSize, err := dirSize(old)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Purge(root, Retention{Audio: 7 * day, Session: 90 * day}, now, false)
	if err != nil {
		t.Fatal(err)
	}

	if !exists(filepath.Join(fresh, "me.wav")) {
		t.Error("a session inside the audio window lost its audio")
	}
	if exists(filepath.Join(middle, "me.wav")) || exists(filepath.Join(middle, "them.wav")) {
		t.Error("audio past the window was kept")
	}
	// The point of the two-stage policy: the text survives the audio, so the
	// meeting can still be re-extracted in another style.
	if !exists(filepath.Join(middle, "transcript.txt")) {
		t.Error("purging audio also took the transcript")
	}
	if exists(old) {
		t.Error("a session past the session window was kept")
	}
	// An unprocessed recording is the only copy of that meeting.
	if !exists(filepath.Join(unprocessed, "me.wav")) {
		t.Error("a session that never produced notes was purged")
	}
	if got.Kept != 1 {
		t.Errorf("Kept = %d, want 1", got.Kept)
	}
	if !exists(filepath.Join(root, "mes-archives")) {
		t.Error("a folder that is not a session was deleted")
	}
	if len(got.Audio) != 1 || len(got.Sessions) != 1 {
		t.Errorf("reported %d audio and %d sessions, want 1 and 1", len(got.Audio), len(got.Sessions))
	}
	if want := int64(2000) + oldSize; got.Bytes != want {
		t.Errorf("Bytes = %d, want %d (the two WAVs plus the whole old folder)", got.Bytes, want)
	}
}

func TestPurgeDryRun(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.Local)
	root := t.TempDir()
	dir := purgeSession(t, root, now.AddDate(0, 0, -100), true, true)

	got, err := Purge(root, Retention{Session: 24 * time.Hour}, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if !exists(dir) {
		t.Fatal("a dry run deleted a session")
	}
	if len(got.Sessions) != 1 || got.Bytes == 0 {
		t.Errorf("a dry run did not report what it would delete: %+v", got)
	}
}

func TestPurgeDisabled(t *testing.T) {
	now := time.Now()
	root := t.TempDir()
	dir := purgeSession(t, root, now.AddDate(-5, 0, 0), true, true)

	got, err := Purge(root, Retention{}, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Empty() || !exists(filepath.Join(dir, "me.wav")) {
		t.Error("the zero Retention deleted something; zero must mean forever")
	}
}
