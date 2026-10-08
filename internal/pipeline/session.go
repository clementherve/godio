// Package pipeline orchestrates the stages between a recording and a note.
package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/clementherve/raudio/internal/audio"
)

// Meta is the session.json written beside a recording.
//
// It exists so every later stage can run from disk alone. Re-running extraction
// with a tweaked prompt must never require re-recording a meeting, and the
// per-track offsets are not recoverable from the WAV files themselves.
type Meta struct {
	Started      time.Time                `json:"started"`
	Duration     time.Duration            `json:"duration"`
	Title        string                   `json:"title"`
	MicDevice    string                   `json:"mic_device"`
	OutputDevice string                   `json:"output_device"`
	Offsets      map[string]time.Duration `json:"offsets"`
}

// Session is one recording's folder on disk.
type Session struct {
	Dir  string
	Meta Meta
}

// NewSession creates a timestamped session folder under root.
func NewSession(root string, started time.Time) (*Session, error) {
	dir := filepath.Join(root, started.Format("2006-01-02_150405"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating session folder: %w", err)
	}
	return &Session{Dir: dir, Meta: Meta{Started: started, Offsets: map[string]time.Duration{}}}, nil
}

// OpenSession loads an existing session folder.
func OpenSession(dir string) (*Session, error) {
	s := &Session{Dir: dir}
	raw, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		return nil, fmt.Errorf("reading session metadata: %w", err)
	}
	if err := json.Unmarshal(raw, &s.Meta); err != nil {
		return nil, fmt.Errorf("parsing session metadata: %w", err)
	}
	if s.Meta.Offsets == nil {
		s.Meta.Offsets = map[string]time.Duration{}
	}
	return s, nil
}

// Latest returns the most recent session folder under root.
func Latest(root string) (*Session, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no sessions in %s", root)
	}
	sort.Strings(names) // folder names are timestamps, so lexical == chronological
	return OpenSession(filepath.Join(root, names[len(names)-1]))
}

// Save writes session.json.
func (s *Session) Save() error {
	raw, err := json.MarshalIndent(s.Meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, "session.json"), append(raw, '\n'), 0o644)
}

// Path returns a file inside the session folder.
func (s *Session) Path(name string) string { return filepath.Join(s.Dir, name) }

// WAV returns the path of a track's recording.
func (s *Session) WAV(t audio.Track) string { return s.Path(string(t) + ".wav") }

// Offset returns a track's start offset.
func (s *Session) Offset(t audio.Track) time.Duration { return s.Meta.Offsets[string(t)] }

// RecordResult folds a finished recording into the session metadata.
func (s *Session) RecordResult(res audio.Result) {
	s.Meta.Duration = res.Duration
	if s.Meta.Offsets == nil {
		s.Meta.Offsets = map[string]time.Duration{}
	}
	for t, tr := range res.Tracks {
		s.Meta.Offsets[string(t)] = tr.Offset
	}
}
