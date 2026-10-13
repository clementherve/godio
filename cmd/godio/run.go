package main

import (
	"fmt"

	"github.com/clementherve/godio/internal/tray"
)

// cmdRun starts the tray from the console binary.
//
// This is the debugging path: it keeps a console attached so startup errors and
// log output are visible. Normal use is godio-tray.exe, which has no console and
// survives the terminal closing.
func cmdRun() error {
	cfg, err := load()
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("%w\n\nLancez « godio setup » puis « godio doctor »", err)
	}
	return tray.Run(cfg, version)
}
