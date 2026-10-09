//go:build !windows

package winui

import "errors"

// PickFolder is unavailable away from Windows; callers fall back to opening
// config.toml in an editor.
func PickFolder(title string) (string, error) {
	return "", errors.New("sélecteur de dossier disponible uniquement sous Windows")
}
