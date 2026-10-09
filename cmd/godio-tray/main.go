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
	"os"

	"github.com/clementherve/godio/internal/claudecli"
	"github.com/clementherve/godio/internal/config"
	"github.com/clementherve/godio/internal/notify"
	"github.com/clementherve/godio/internal/tray"
)

func main() {
	if err := run(); err != nil {
		// There is no console to print to in the GUI subsystem, so a failure
		// that stops the tray from starting has to surface as a notification.
		notify.New().Failed(err.Error())
		fmt.Fprintln(os.Stderr, "erreur :", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, created, err := config.Load()
	if err != nil {
		return err
	}
	if created {
		path, _ := config.Path()
		notify.New().Done("godio — première configuration",
			"Configuration créée :\n"+path+"\nChoisissez votre coffre Obsidian dans le menu.")
	}

	// A missing vault or model must not stop the tray from starting: the menu
	// is where the user fixes both, so starting and reporting beats refusing.
	if err := cfg.Validate(); err != nil {
		notify.New().Failed(err.Error())
	}
	if err := (claudecli.Runner{Executable: cfg.Claude.Executable}).Check(); err != nil {
		notify.New().Failed(err.Error())
	}

	return tray.Run(cfg)
}
