// Package config loads and scaffolds raudio's on-disk configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the full user configuration, read from config.toml.
type Config struct {
	Obsidian Obsidian `toml:"obsidian"`
	Audio    Audio    `toml:"audio"`
	Whisper  Whisper  `toml:"whisper"`
	Claude   Claude   `toml:"claude"`
}

// Obsidian locates the vault and the daily note within it.
type Obsidian struct {
	Vault       string `toml:"vault"`
	DailyFolder string `toml:"daily_folder"`
	DateFormat  string `toml:"date_format"`
	Heading     string `toml:"heading"`
}

// Audio selects capture devices and tunes the activity indicator.
type Audio struct {
	MicDevice      string   `toml:"mic_device"`
	LoopbackDevice string   `toml:"loopback_device"`
	SilenceDBFS    float64  `toml:"silence_dbfs"`
	ActivityWindow Duration `toml:"activity_window"`
}

// Whisper points at the whisper.cpp binary and its models.
type Whisper struct {
	Exe      string `toml:"exe"`
	Model    string `toml:"model"`
	VADModel string `toml:"vad_model"`
	Language string `toml:"language"`
	Threads  int    `toml:"threads"`
}

// Claude configures the headless Claude Code invocation.
type Claude struct {
	Executable string   `toml:"executable"`
	Model      string   `toml:"model"`
	Timeout    Duration `toml:"timeout"`
}

// Duration wraps time.Duration so TOML can carry it as "10s".
type Duration struct{ time.Duration }

// UnmarshalText parses a Go duration string.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// MarshalText renders the duration back as a Go duration string.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// Default returns the configuration written on first run.
func Default() Config {
	return Config{
		Obsidian: Obsidian{
			Vault:       filepath.Join(userHome(), "Obsidian", "MyVault"),
			DailyFolder: "Daily",
			DateFormat:  "2006-01-02",
			Heading:     "## Réunions",
		},
		Audio: Audio{
			SilenceDBFS:    -45,
			ActivityWindow: Duration{10 * time.Second},
		},
		Whisper: Whisper{
			Exe:      filepath.Join("whisper", "whisper-cli"+exeSuffix()),
			Model:    filepath.Join("models", "ggml-large-v3-turbo.bin"),
			VADModel: filepath.Join("models", "ggml-silero-v5.1.2.bin"),
			Language: "fr",
		},
		Claude: Claude{
			Executable: "claude",
			Model:      "claude-haiku-5-5",
			Timeout:    Duration{5 * time.Minute},
		},
	}
}

// Path returns the location of config.toml.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "raudio", "config.toml"), nil
}

// SessionsDir returns the directory holding per-recording session folders.
func SessionsDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "raudio", "sessions"), nil
}

// Load reads config.toml, writing a default one first if it does not exist.
// The second return value reports whether a fresh config was just scaffolded,
// so the caller can tell the user to go fill in their vault path.
func Load() (Config, bool, error) {
	path, err := Path()
	if err != nil {
		return Config{}, false, err
	}

	created := false
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := writeDefault(path); err != nil {
			return Config{}, false, err
		}
		created = true
	} else if err != nil {
		return Config{}, false, err
	}

	// Start from the defaults so a partial config.toml only overrides what it names.
	cfg := Default()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, created, fmt.Errorf("reading %s: %w", path, err)
	}
	cfg.normalise()
	return cfg, created, nil
}

func writeDefault(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(Default())
}

// normalise fills in anything a hand-edited config left blank or zeroed.
func (c *Config) normalise() {
	d := Default()
	if c.Obsidian.DateFormat == "" {
		c.Obsidian.DateFormat = d.Obsidian.DateFormat
	}
	if c.Obsidian.Heading == "" {
		c.Obsidian.Heading = d.Obsidian.Heading
	}
	if c.Audio.SilenceDBFS == 0 {
		c.Audio.SilenceDBFS = d.Audio.SilenceDBFS
	}
	if c.Audio.ActivityWindow.Duration <= 0 {
		c.Audio.ActivityWindow = d.Audio.ActivityWindow
	}
	if c.Whisper.Language == "" {
		c.Whisper.Language = d.Whisper.Language
	}
	if c.Whisper.Threads <= 0 {
		c.Whisper.Threads = runtime.NumCPU()
	}
	if c.Claude.Executable == "" {
		c.Claude.Executable = d.Claude.Executable
	}
	if c.Claude.Model == "" {
		c.Claude.Model = d.Claude.Model
	}
	if c.Claude.Timeout.Duration <= 0 {
		c.Claude.Timeout = d.Claude.Timeout
	}
}

// Validate reports configuration that would fail only after a meeting has been
// recorded. Call it at startup so the user finds out before the call, not after.
func (c *Config) Validate() error {
	var errs []error
	if c.Obsidian.Vault == "" {
		errs = append(errs, errors.New("obsidian.vault is not set"))
	} else if fi, err := os.Stat(c.Obsidian.Vault); err != nil {
		errs = append(errs, fmt.Errorf("obsidian.vault %q: %w", c.Obsidian.Vault, err))
	} else if !fi.IsDir() {
		errs = append(errs, fmt.Errorf("obsidian.vault %q is not a directory", c.Obsidian.Vault))
	}
	for _, f := range []struct{ name, path string }{
		{"whisper.exe", c.Whisper.Exe},
		{"whisper.model", c.Whisper.Model},
		{"whisper.vad_model", c.Whisper.VADModel},
	} {
		if _, err := os.Stat(c.Resolve(f.path)); err != nil {
			errs = append(errs, fmt.Errorf("%s %q: %w", f.name, f.path, err))
		}
	}
	return errors.Join(errs...)
}

// Resolve turns a possibly-relative asset path into an absolute one, anchored
// at the executable's directory so raudio works regardless of working directory.
func (c *Config) Resolve(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	exe, err := os.Executable()
	if err != nil {
		return path
	}
	return filepath.Join(filepath.Dir(exe), path)
}

// DailyNotePath returns the path of the daily note for the given day.
func (c *Config) DailyNotePath(day time.Time) string {
	return filepath.Join(c.Obsidian.Vault, c.Obsidian.DailyFolder, day.Format(c.Obsidian.DateFormat)+".md")
}

func userHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
