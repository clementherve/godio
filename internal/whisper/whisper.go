// Package whisper drives the whisper.cpp command-line binary and parses its
// JSON output. Shelling out to the official prebuilt binary avoids cgo bindings
// entirely and lets the CPU/CUDA build be swapped without recompiling raudio.
package whisper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/clementherve/raudio/internal/transcript"
)

// Runner invokes whisper-cli with a fixed model and language.
type Runner struct {
	Exe      string
	Model    string
	VADModel string
	Language string
	Threads  int
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
		"--no-prints", // keep stderr for real errors
	}
	if r.Threads > 0 {
		args = append(args, "-t", fmt.Sprint(r.Threads))
	}
	// VAD does double duty: it skips the long stretches where this track is
	// silent because the other party is talking, and it suppresses Whisper's
	// hallucination loops on silence, which otherwise invent whole sentences.
	if r.VADModel != "" {
		args = append(args, "--vad", "--vad-model", r.VADModel)
	}

	cmd := exec.CommandContext(ctx, r.Exe, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = nil

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("whisper-cli %s: %w\n%s", filepath.Base(wavPath), err, tail(stderr.String()))
	}

	return parseFile(base + ".json")
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

// tail returns the last few lines of s, for error messages.
func tail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}
	return strings.Join(lines, "\n")
}
