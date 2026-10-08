package main

import (
	"fmt"
	"os"

	"github.com/clementherve/godio/internal/claudecli"
	"github.com/clementherve/godio/internal/tray"
)

// cmdRun starts the tray application.
func cmdRun() error {
	cfg, err := load()
	if err != nil {
		return err
	}

	// Fail now rather than after a meeting has been recorded: a missing vault
	// or a missing whisper binary is cheap to fix beforehand and infuriating
	// to discover afterwards.
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("%w\n\nLancez « godio doctor » pour le détail", err)
	}
	if err := (claudecli.Runner{Executable: cfg.Claude.Executable}).Check(); err != nil {
		fmt.Fprintln(os.Stderr, "attention :", err)
	}

	return tray.Run(cfg)
}
