package assets

// IconState names a tray icon.
type IconState int

// Tray icon states.
const (
	IconIdle IconState = iota
	// IconActive means recording with both sides carrying sound.
	IconActive
	// IconSilent means recording with at least one side silent.
	IconSilent
	IconProcessing
	IconError
)

// Icon returns the embedded icon bytes for a state, in this platform's format.
func Icon(s IconState) []byte {
	switch s {
	case IconActive:
		return iconActive
	case IconSilent:
		return iconSilent
	case IconProcessing:
		return iconProcessing
	case IconError:
		return iconError
	default:
		return iconIdle
	}
}
