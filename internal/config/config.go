// Package config loads and scaffolds godio's on-disk configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/klauspost/cpuid/v2"
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

// Whisper points at the whisper.cpp binary and its models, and tunes how hard
// it works.
type Whisper struct {
	Exe      string `toml:"exe"`
	Model    string `toml:"model"`
	VADModel string `toml:"vad_model"`
	Language string `toml:"language"`
	Threads  int    `toml:"threads"`
	// Backend is "auto", "cuda" or "cpu". It selects which whisper.cpp build
	// `godio setup` installs; "auto" uses CUDA when an NVIDIA GPU is present.
	Backend string `toml:"backend"`
	// Preset is "fast", "balanced" or "accurate". See Decoding.
	Preset string `toml:"preset"`
}

// Decoding returns the whisper-cli decoding flags for a preset.
//
// whisper-cli defaults to beam search with a beam of 5 (its strategy is
// beam search whenever beam_size > 1), which costs several times what greedy
// decoding does. Because Claude rewrites the transcript afterwards, most of
// greedy's extra errors never reach the notes — so "fast" is the default.
func (w Whisper) Decoding() []string {
	switch w.Preset {
	case "accurate":
		return []string{"-bs", "5"}
	case "balanced":
		return []string{"-bs", "2"}
	default: // fast
		// -nf disables temperature fallback, which otherwise re-decodes any
		// segment that trips a confidence threshold.
		return []string{"-bs", "1", "-nf"}
	}
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
			Backend:  "auto",
			Preset:   "fast",
			Threads:  PhysicalCores(),
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
	return filepath.Join(dir, "godio", "config.toml"), nil
}

// SessionsDir returns the directory holding per-recording session folders.
func SessionsDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "godio", "sessions"), nil
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
	return writeTo(path, Default())
}

// header explains the file to whoever opens it, and warns that the tray
// rewrites it.
const header = `# Configuration de godio.
#
# Ce fichier est relu à chaque démarrage. L'icône de la zone de notification
# peut le réécrire (menu Configuration), ce qui supprime les commentaires
# ajoutés à la main.
#
#   whisper.preset   fast | balanced | accurate   (vitesse <-> précision)
#   whisper.backend  auto | cuda | cpu            (auto = CUDA si GPU NVIDIA)
#   whisper.threads  0 = un par coeur physique

`

// writeTo writes a config atomically, so an interrupted write cannot leave an
// unparseable config.toml behind.
func writeTo(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".godio-*.toml")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.WriteString(header); err != nil {
		tmp.Close()
		return err
	}
	if err := toml.NewEncoder(tmp).Encode(c); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
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
		c.Whisper.Threads = PhysicalCores()
	}
	if c.Whisper.Backend == "" {
		c.Whisper.Backend = d.Whisper.Backend
	}
	switch c.Whisper.Preset {
	case "fast", "balanced", "accurate":
	default:
		c.Whisper.Preset = d.Whisper.Preset
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
// at the executable's directory so godio works regardless of working directory.
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

// PhysicalCores returns the number of physical CPU cores.
//
// whisper.cpp wants one thread per physical core, not per logical one: its
// matrix kernels already saturate each core's SIMD units, so running on
// hyperthreads adds scheduling contention without adding throughput. Passing
// runtime.NumCPU() (logical) measurably slows transcription down.
func PhysicalCores() int {
	if n := cpuid.CPU.PhysicalCores; n > 0 {
		return n
	}
	// cpuid could not tell; halving the logical count is the right guess on any
	// hyperthreaded machine and harmless on one without.
	if n := runtime.NumCPU(); n > 1 {
		return n / 2
	}
	return 1
}

// Save writes the configuration back to config.toml.
//
// Round-tripping through the struct means comments in a hand-edited file are
// lost, which is why the generated file says so at the top.
func (c Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	return writeTo(path, c)
}
