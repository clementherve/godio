// Package claudecli runs Claude Code headlessly to turn a transcript into notes.
package claudecli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/clementherve/godio/internal/proc"
)

// Runner invokes the claude CLI in print mode.
type Runner struct {
	Executable string
	Model      string
	Timeout    time.Duration
}

// ErrNotInstalled reports that the claude executable could not be found.
var ErrNotInstalled = errors.New("claude CLI not found in PATH")

// Check verifies the executable exists. Call it at startup: discovering that
// Claude Code is missing *after* a meeting has been recorded is a bad trade.
func (r Runner) Check() error {
	if _, err := exec.LookPath(r.Executable); err != nil {
		return fmt.Errorf("%w (looked for %q)", ErrNotInstalled, r.Executable)
	}
	return nil
}

// Extract feeds the transcript to Claude on stdin and returns the Markdown notes.
func (r Runner) Extract(ctx context.Context, prompt, transcript string) (string, error) {
	if strings.TrimSpace(transcript) == "" {
		return "", errors.New("transcript is empty, nothing to extract")
	}
	if err := r.Check(); err != nil {
		return "", err
	}

	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}

	args := []string{"-p", prompt, "--output-format", "text"}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}

	cmd := exec.CommandContext(ctx, r.Executable, args...)
	proc.HideConsole(cmd)
	cmd.Stdin = strings.NewReader(transcript)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("claude timed out after %s", r.Timeout)
		}
		return "", fmt.Errorf("claude: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return "", errors.New("claude returned no output")
	}
	return out, nil
}
