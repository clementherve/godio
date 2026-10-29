//go:build !windows

package notify

// push is a no-op away from Windows, which keeps the rest of godio building
// and testable during development on another OS.
func (n Notifier) push(title, body string) {}

func (n Notifier) ask(title, body, accept, decline string, onAccept func()) {}
