//go:build windows

package winui

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	ole32   = windows.NewLazySystemDLL("ole32.dll")

	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
)

// browseInfoW mirrors the Win32 BROWSEINFOW structure.
type browseInfoW struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

const (
	bifReturnOnlyFSDirs = 0x0001
	bifNewDialogStyle   = 0x0040 // resizable dialog with a "New folder" button

	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4

	maxPath = 260
)

// PickFolder shows the native folder chooser and returns the selected path.
//
// SHBrowseForFolder is used rather than the newer IFileOpenDialog because it is
// a plain exported function: no COM vtable dispatch to get wrong, which matters
// for code that cannot be exercised on the development machine.
func PickFolder(title string) (string, error) {
	type result struct {
		path string
		err  error
	}
	ch := make(chan result, 1)

	// The dialog is modal and needs a single-threaded apartment, so it gets its
	// own OS thread. Running it on the tray's message-loop thread would freeze
	// the menu for as long as the dialog is open.
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		// BIF_NEWDIALOGSTYLE requires COM to be initialised as STA.
		if err := windows.CoInitializeEx(0, coinitApartmentThreaded|coinitDisableOLE1DDE); err != nil {
			// RPC_E_CHANGED_MODE means COM is already up on this thread in
			// another mode, which is survivable; anything else is not.
			if errno, ok := err.(syscall.Errno); !ok || uintptr(errno) != 0x80010106 {
				ch <- result{err: err}
				return
			}
		} else {
			defer windows.CoUninitialize()
		}

		path, err := browse(title)
		ch <- result{path: path, err: err}
	}()

	r := <-ch
	return r.path, r.err
}

func browse(title string) (string, error) {
	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return "", err
	}
	display := make([]uint16, maxPath)

	bi := browseInfoW{
		pszDisplayName: &display[0],
		lpszTitle:      titlePtr,
		ulFlags:        bifReturnOnlyFSDirs | bifNewDialogStyle,
	}

	pidl, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", ErrCancelled{}
	}
	// The PIDL is allocated by the shell; it is ours to free.
	defer procCoTaskMemFree.Call(pidl)

	buf := make([]uint16, maxPath)
	ok, _, _ := procSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&buf[0])))
	if ok == 0 {
		return "", ErrCancelled{}
	}

	// Keep bi alive until the call has returned.
	runtime.KeepAlive(bi)
	runtime.KeepAlive(display)

	return windows.UTF16ToString(buf), nil
}
