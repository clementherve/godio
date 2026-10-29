//go:build windows

package notify

import (
	"sync"

	toast "git.sr.ht/~jackmordaunt/go-toast"
)

// push shows a Windows toast.
//
// Failures are swallowed: a notification is a convenience, and an unavailable
// notification service must never take down a run whose real work succeeded.
func (n Notifier) push(title, body string) {
	_ = (&toast.Notification{
		AppID: n.AppID,
		Title: title,
		Body:  body,
	}).Push()
}

// ask shows a Windows toast with buttons.
//
// The activation callback is global in go-toast and must be installed before
// the first interactive toast. It fires on a COM thread, so the callback it
// invokes is responsible for getting back to its own event loop.
func (n Notifier) ask(title, body, accept, decline string, onAccept func()) {
	askMu.Lock()
	askAccept = onAccept
	askMu.Unlock()

	askOnce.Do(func() {
		toast.SetActivationCallback(func(args string, _ []toast.UserData) {
			if args != "start" {
				return
			}
			askMu.Lock()
			cb := askAccept
			askMu.Unlock()
			if cb != nil {
				cb()
			}
		})
	})

	_ = (&toast.Notification{
		AppID:               n.AppID,
		Title:               title,
		Body:                body,
		ActivationType:      toast.Foreground,
		ActivationArguments: "start",
		Actions: []toast.Action{
			{Type: toast.Foreground, Content: accept, Arguments: "start"},
			{Type: toast.Foreground, Content: decline, Arguments: "dismiss"},
		},
	}).Push()
}

var (
	askOnce   sync.Once
	askMu     sync.Mutex
	askAccept func()
)
