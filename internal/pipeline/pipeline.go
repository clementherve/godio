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
	"github.com/clementherve/godio/internal/lang"
	"github.com/clementherve/godio/internal/obsidian"
	"github.com/clementherve/godio/internal/transcript"
	"github.com/clementherve/godio/internal/whisper"
)

// Pipeline runs the stages after recording: transcribe, extract, write.
type Pipeline struct {
	Cfg config.Config
	// Log receives short progress lines; may be nil.
	Log func(string)
	// OnProgress receives a stage name and a 0..100 percentage; may be nil.
	// Transcription is the only stage that can report a real percentage, so the
	// others report 0 and 100 around themselves.
	OnProgress func(stage string, percent int)
}

// Result describes a completed run, for the completion notification.
type Result struct {
	NotePath string
	Actions  int           // unchecked action items Claude produced
	Elapsed  time.Duration // wall time of the whole pipeline
}

func (p Pipeline) progress(stage string, percent int) {
	if p.OnProgress != nil {
		p.OnProgress(stage, percent)
	}
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
		Decoding: p.Cfg.Whisper.Decoding(),
	}

	var mine, theirs []transcript.Segment
	for i, t := range []struct {
		track audio.Track
		into  *[]transcript.Segment
		label string
	}{
		{audio.TrackMe, &mine, lang.T().Notes.TrackMic},
		{audio.TrackThem, &theirs, lang.T().Notes.TrackSystem},
	} {
		path := s.WAV(t.track)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("track %s: %w", t.track, err)
		}

		p.logf(lang.T().Notes.TranscribingFmt, t.label)
		start := time.Now()

		// Two tracks are transcribed back to back, so each one's 0..100 covers
		// half of the overall bar.
		half := i
		r.OnProgress = func(pc int) {
			p.progress(lang.T().Notes.StageTranscription, (half*100+pc)/2)
		}

		segs, err := r.Transcribe(ctx, path)
		if err != nil {
			return "", err
		}
		*t.into = segs
		p.logf(lang.T().Notes.TranscribedTrackFmt, t.label, len(segs), time.Since(start).Round(time.Second))
	}

	merged := transcript.Merge(mine, theirs, s.Offset(audio.TrackMe), s.Offset(audio.TrackThem))
	if len(merged) == 0 {
		return "", ErrNothingHeard
	}

	text := transcript.Format(merged, transcript.LabelsFor(p.Cfg.NotesLang()))
	if err := os.WriteFile(s.Path("transcript.txt"), []byte(text), 0o644); err != nil {
		return "", fmt.Errorf("writing transcript: %w", err)
	}

	st := transcript.Describe(merged)
	p.logf(lang.T().Notes.TranscribedFmt,
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

	p.logf(lang.T().Notes.ExtractingFmt, p.Cfg.Claude.Model, p.Cfg.Claude.Style)
	p.progress(lang.T().Notes.StageExtraction, 0)
	notes, err := r.Extract(ctx, assets.ExtractPrompt(p.Cfg.NotesLang(), p.Cfg.Claude.Style), string(raw))
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
	p.logf(lang.T().Notes.AppendedFmt, path)
	return path, nil
}

// Process runs transcribe, extract and write in sequence.
func (p Pipeline) Process(ctx context.Context, s *Session) (Result, error) {
	start := time.Now()

	if _, err := p.Transcribe(ctx, s); err != nil {
		return Result{}, err
	}
	notes, err := p.Extract(ctx, s)
	if err != nil {
		return Result{}, err
	}
	path, err := p.Write(s)
	if err != nil {
		return Result{}, err
	}
	p.progress(lang.T().Notes.Done, 100)

	return Result{
		NotePath: path,
		Actions:  CountActions(notes),
		Elapsed:  time.Since(start),
	}, nil
}

// CountActions counts the unchecked Markdown checkboxes in Claude's notes, so
// the completion notification can say something more useful than "done".
func CountActions(notes string) int {
	n := 0
	for _, line := range strings.Split(notes, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- [ ]") {
			n++
		}
	}
	return n
}

// Title returns the session's meeting title, defaulting to its start time.
func (s *Session) Title() string {
	if t := strings.TrimSpace(s.Meta.Title); t != "" {
		return t
	}
	return lang.T().Notes.MeetingPrefix + s.Meta.Started.Format("15:04")
}
