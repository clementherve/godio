// Command godio-tray is the system-tray application.
//
// It exists as a binary separate from godio.exe so it can be linked into the
// Windows GUI subsystem (-H windowsgui). A GUI-subsystem process started from a
// terminal is not attached to that console, so it shows no console window and
// survives the terminal being closed — neither of which is true of the console
// binary that carries the CLI.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/clementherve/godio/internal/claudecli"
	"github.com/clementherve/godio/internal/config"
	"github.com/clementherve/godio/internal/lang"
	"github.com/clementherve/godio/internal/notify"
	"github.com/clementherve/godio/internal/selfupdate"
	"github.com/clementherve/godio/internal/tray"
)

// version is stamped at build time, exactly as it is for godio.exe: CI passes
// -X main.version=<tag> to both builds, so this variable has to exist here too
// or the tray cannot tell whether it is out of date.
var version = "dev"

func main() {
	// A GUI-subsystem binary has no usable stderr: a panic or a systray error
	// would vanish with no trace. Keep both in a file next to the sessions.
	if f := setupLog(); f != nil {
		defer f.Close()
		log.Printf("godio-tray %s starting", version)
	}

	if err := run(); err != nil {
		// There is no console to print to in the GUI subsystem, so a failure
		// that stops the tray from starting has to surface as a notification.
		notify.New().Failed(err.Error())
		fmt.Fprintln(os.Stderr, lang.T().CLI.ErrorPrefix, err)
		os.Exit(1)
	}
}

// setupLog points the standard logger and Go's crash handler at a file, so a
// crash in the tray leaves something to diagnose. It returns nil when the file
// cannot be opened; logging is not important enough to stop the tray.
func setupLog() *os.File {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil
	}
	dir = filepath.Join(dir, "godio")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, "tray.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	// SetCrashOutput duplicates the descriptor, so closing f at exit is safe.
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
	return f
}

func run() error {
	// A previous update left the binary it replaced behind; this launch is the
	// first moment it can be deleted.
	if dir, err := selfupdate.InstallDir(); err == nil {
		selfupdate.CleanupOld(dir)
	}

	// Before config.Load, so a failure while reading it is already reported in
	// the user's language; Load may rewrite it afterwards with the real value.
	lang.Use(lang.Resolve(config.LanguageSetting()))

	cfg, created, err := config.Load()
	if err != nil {
		return err
	}
	lang.Use(lang.Resolve(cfg.Language))
	if created {
		path, _ := config.Path()
		notify.New().Done(lang.T().Tray.FirstRunTitle,
			fmt.Sprintf(lang.T().Tray.FirstRunBodyFmt, path))
	}

	// A missing vault or model must not stop the tray from starting: the menu
	// is where the user fixes both, so starting and reporting beats refusing.
	if err := cfg.Validate(); err != nil {
		notify.New().Failed(err.Error())
	}
	if err := (claudecli.Runner{Executable: cfg.Claude.Executable}).Check(); err != nil {
		notify.New().Failed(err.Error())
	}

	return tray.Run(cfg, version)
}
