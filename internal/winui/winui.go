// Package winui wraps the few native Windows dialogs godio needs.
package winui

import "github.com/clementherve/godio/internal/lang"

// ErrCancelled is returned when the user dismissed a dialog.
type ErrCancelled struct{}

func (ErrCancelled) Error() string { return lang.T().Err.DialogCancelled }
