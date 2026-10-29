// Package proc prepares child processes started by the tray.
package proc

import "os/exec"

// HideConsole prevents cmd from opening a console window.
//
// godio-tray is a GUI-subsystem binary. On Windows, starting a console
// application from it creates a new console unless the child is explicitly
// told not to; whisper-cli, claude and nvidia-smi would each flash one.
func HideConsole(cmd *exec.Cmd) {
	hideConsole(cmd)
}
