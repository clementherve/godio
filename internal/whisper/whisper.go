// Package whisper drives the whisper.cpp command-line binary and parses its
// JSON output. Shelling out to the official prebuilt binary avoids cgo bindings
// entirely and lets the CPU/CUDA build be swapped without recompiling godio.
package whisper

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/clementherve/godio/internal/proc"
	"github.com/clementherve/godio/internal/transcript"
)

// Runner invokes whisper-cli with a fixed model and language.
type Runner struct {
	Exe      string
	Model    string
	VADModel string
	Language string
	Threads  int
	// Decoding holds the preset's flags, e.g. []string{"-bs", "1", "-nf"}.
	Decoding []string
	// OnProgress, if set, is called with 0..100 as transcription advances.
	OnProgress func(percent int)
}

// jsonOutput mirrors the subset of whisper-cli's -oj output that we need.
type jsonOutput struct {
	Transcription []struct {
		Offsets struct {
			From int64 `json:"from"` // milliseconds
			To   int64 `json:"to"`
		} `json:"offsets"`
		Text string `json:"text"`
	} `json:"transcription"`
}

// progressLine matches whisper-cli's progress output, e.g.
//
//	whisper_print_progress_callback: progress =  45%
var progressLine = regexp.MustCompile(`progress\s*=\s*(\d+)%`)

// Transcribe runs whisper-cli over a 16 kHz mono WAV and returns its segments.
//
// The JSON sidecar is written next to the WAV and left in place: it is the raw
// artifact for re-running later stages without re-transcribing.
func (r Runner) Transcribe(ctx context.Context, wavPath string) ([]transcript.Segment, error) {
	base := strings.TrimSuffix(wavPath, filepath.Ext(wavPath))

	args := []string{
		"-m", r.Model,
		"-f", wavPath,
		"-l", r.Language,
		"-oj",       // write <base>.json
		"-of", base, // output base name
		"-np", // silence the log callback
		"-pp", // ...but keep progress, which writes to stderr directly
	}
	args = append(args, r.Decoding...)
	if r.Threads > 0 {
		args = append(args, "-t", strconv.Itoa(r.Threads))
	}
	// VAD does double duty: it skips the long stretches where this track is
	// silent because the other party is talking, and it suppresses Whisper's
	// hallucination loops on silence, which otherwise invent whole sentences.
	if r.VADModel != "" {
		args = append(args, "--vad", "--vad-model", r.VADModel)
	}

	cmd := exec.CommandContext(ctx, r.Exe, args...)
	proc.HideConsole(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting whisper-cli: %w", err)
	}

	tail := r.scanProgress(stderr)

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("whisper-cli %s: %w\n%s", filepath.Base(wavPath), err, tail())
	}
	if r.OnProgress != nil {
		r.OnProgress(100)
	}

	return parseFile(base + ".json")
}

// scanProgress consumes stderr, reporting progress as it goes, and returns an
// accessor for the last few lines.
//
// Only a bounded tail is kept: whisper is chatty over a long meeting, and the
// lines are only ever wanted to explain a failure.
func (r Runner) scanProgress(stderr io.Reader) func() string {
	const keep = 8
	var ring []string

	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			if m := progressLine.FindStringSubmatch(line); m != nil {
				if r.OnProgress != nil {
					if n, err := strconv.Atoi(m[1]); err == nil {
						r.OnProgress(n)
					}
				}
				continue
			}
			ring = append(ring, line)
			if len(ring) > keep {
				ring = ring[len(ring)-keep:]
			}
		}
	}()

	return func() string {
		<-done
		return strings.Join(ring, "\n")
	}
}

func parseFile(path string) ([]transcript.Segment, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading whisper output: %w", err)
	}
	return Parse(raw)
}

// Parse converts whisper-cli JSON into segments.
func Parse(raw []byte) ([]transcript.Segment, error) {
	var out jsonOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parsing whisper JSON: %w", err)
	}

	segs := make([]transcript.Segment, 0, len(out.Transcription))
	for _, t := range out.Transcription {
		text := strings.TrimSpace(t.Text)
		if text == "" || isNoise(text) {
			continue
		}
		segs = append(segs, transcript.Segment{
			Start: time.Duration(t.Offsets.From) * time.Millisecond,
			End:   time.Duration(t.Offsets.To) * time.Millisecond,
			Text:  text,
		})
	}
	return segs, nil
}

// isNoise drops whisper's bracketed non-speech annotations ("[Musique]",
// "(bruit de fond)", "*soupir*"), which are pure noise in a meeting summary and
// are the most common thing it emits over near-silence even with VAD on.
func isNoise(text string) bool {
	pairs := [][2]string{{"[", "]"}, {"(", ")"}, {"*", "*"}, {"♪", "♪"}}
	for _, p := range pairs {
		if strings.HasPrefix(text, p[0]) && strings.HasSuffix(text, p[1]) &&
			!strings.Contains(strings.TrimSuffix(strings.TrimPrefix(text, p[0]), p[1]), p[0]) {
			return true
		}
	}
	return false
}
