//go:build windows

package proc

import (
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHideConsole(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	HideConsole(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Error("HideWindow is false")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Error("CREATE_NO_WINDOW is not set")
	}
}

func TestHideConsolePreservesCreationFlags(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	HideConsole(cmd)

	flags := cmd.SysProcAttr.CreationFlags
	if flags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Error("existing creation flags were dropped")
	}
	if flags&windows.CREATE_NO_WINDOW == 0 {
		t.Error("CREATE_NO_WINDOW is not set")
	}
}
