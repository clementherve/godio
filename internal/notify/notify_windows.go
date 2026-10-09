//go:build windows

package notify

import toast "git.sr.ht/~jackmordaunt/go-toast"

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
