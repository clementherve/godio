//go:build !windows

package winui

import (
	"errors"
	"github.com/clementherve/godio/internal/lang"
)

// PickFolder is unavailable away from Windows; callers fall back to opening
// config.toml in an editor.
func PickFolder(title string) (string, error) {
	return "", errors.New(lang.T().Err.PickerWindowsOnly)
}
