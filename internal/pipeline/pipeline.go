package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/clementherve/godio/internal/assets"
	"github.com/clementherve/godio/internal/audio"
	"github.com/clementherve/godio/internal/claudecli"
	"github.com/clementherve/godio/internal/config"
	"github.com/clementherve/godio/internal/obsidian"
	"github.com/clementherve/godio/internal/transcript"
	"github.com/clementherve/godio/internal/whisper"
)

// Pipeline runs the stages after recording: transcribe, extract, write.
type Pipeline struct {
	Cfg config.Config
	// Log receives short progress lines; may be nil.
	Log func(string)
}

func (p Pipeline) logf(format string, args ...any) {
	if p.Log != nil {
		p.Log(fmt.Sprintf(format, args...))
	}
}

// ErrNothingHeard reports that neither track carried usable speech.
var ErrNothingHeard = errors.New("no speech found in either track")

// Transcribe transcribes both tracks and writes transcript.txt into the session.
func (p Pipeline) Transcribe(ctx context.Context, s *Session) (string, error) {
	r := whisper.Runner{
		Exe:      p.Cfg.Resolve(p.Cfg.Whisper.Exe),
		Model:    p.Cfg.Resolve(p.Cfg.Whisper.Model),
		VADModel: p.Cfg.Resolve(p.Cfg.Whisper.VADModel),
		Language: p.Cfg.Whisper.Language,
		Threads:  p.Cfg.Whisper.Threads,
	}

	var mine, theirs []transcript.Segment
	for _, t := range []struct {
		track audio.Track
		into  *[]transcript.Segment
		label string
	}{
		{audio.TrackMe, &mine, "micro"},
		{audio.TrackThem, &theirs, "système"},
	} {
		path := s.WAV(t.track)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("track %s: %w", t.track, err)
		}

		p.logf("Transcription (%s)…", t.label)
		start := time.Now()
		segs, err := r.Transcribe(ctx, path)
		if err != nil {
			return "", err
		}
		*t.into = segs
		p.logf("  %s : %d segments en %s", t.label, len(segs), time.Since(start).Round(time.Second))
	}

	merged := transcript.Merge(mine, theirs, s.Offset(audio.TrackMe), s.Offset(audio.TrackThem))
	if len(merged) == 0 {
		return "", ErrNothingHeard
	}

	text := transcript.Format(merged)
	if err := os.WriteFile(s.Path("transcript.txt"), []byte(text), 0o644); err != nil {
		return "", fmt.Errorf("writing transcript: %w", err)
	}

	st := transcript.Describe(merged)
	p.logf("Transcription : %d blocs, %d mots (moi %d / eux %d)",
		st.Segments, st.Words, st.BySpeaker[transcript.Me], st.BySpeaker[transcript.Them])

	return text, nil
}

// Extract sends the session's transcript to Claude and writes notes.md.
func (p Pipeline) Extract(ctx context.Context, s *Session) (string, error) {
	raw, err := os.ReadFile(s.Path("transcript.txt"))
	if err != nil {
		return "", fmt.Errorf("reading transcript (run transcribe first): %w", err)
	}

	r := claudecli.Runner{
		Executable: p.Cfg.Claude.Executable,
		Model:      p.Cfg.Claude.Model,
		Timeout:    p.Cfg.Claude.Timeout.Duration,
	}

	p.logf("Extraction (%s)…", p.Cfg.Claude.Model)
	notes, err := r.Extract(ctx, assets.ExtractPromptFR, string(raw))
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(s.Path("notes.md"), []byte(notes+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("writing notes: %w", err)
	}
	return notes, nil
}

// Write appends the session's notes to the Obsidian daily note and returns its path.
func (p Pipeline) Write(s *Session) (string, error) {
	raw, err := os.ReadFile(s.Path("notes.md"))
	if err != nil {
		return "", fmt.Errorf("reading notes (run extract first): %w", err)
	}

	path := p.Cfg.DailyNotePath(s.Meta.Started)
	entry := obsidian.Entry{
		Title: s.Title(),
		At:    s.Meta.Started,
		Body:  string(raw),
	}
	if err := obsidian.Append(path, p.Cfg.Obsidian.Heading, entry); err != nil {
		return "", err
	}
	p.logf("Ajouté à %s", path)
	return path, nil
}

// Process runs transcribe, extract and write in sequence.
func (p Pipeline) Process(ctx context.Context, s *Session) (string, error) {
	if _, err := p.Transcribe(ctx, s); err != nil {
		return "", err
	}
	if _, err := p.Extract(ctx, s); err != nil {
		return "", err
	}
	return p.Write(s)
}

// Title returns the session's meeting title, defaulting to its start time.
func (s *Session) Title() string {
	if t := strings.TrimSpace(s.Meta.Title); t != "" {
		return t
	}
	return "Réunion " + s.Meta.Started.Format("15:04")
}
