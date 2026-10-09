// Package winui wraps the few native Windows dialogs godio needs.
package winui

// ErrCancelled is returned when the user dismissed a dialog.
type ErrCancelled struct{}

func (ErrCancelled) Error() string { return "dialogue annulé" }
