// Package notify shows a desktop notification when a meeting has been
// processed, so you can start a recording and walk away from the machine.
package notify

import "github.com/clementherve/godio/internal/lang"

// Notifier shows desktop notifications.
type Notifier struct {
	// AppID is what Windows shows as the notification's source.
	AppID string
}

// New returns a Notifier for godio.
func New() Notifier { return Notifier{AppID: "godio"} }

// Done reports a successful run.
func (n Notifier) Done(title, body string) {
	n.push(title, body)
}

// Failed reports an error. It is deliberately the same channel as success: if
// transcription dies twenty minutes after you walked away, a silent failure is
// the worst outcome.
func (n Notifier) Failed(body string) {
	n.push(lang.T().Tray.FailedTitle, body)
}

// Ask shows a notification with an accept and a decline button, and calls
// onAccept when the accept button is clicked.
func (n Notifier) Ask(title, body, accept, decline string, onAccept func()) {
	n.ask(title, body, accept, decline, onAccept)
}
