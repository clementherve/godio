package assets

// IconState names a tray icon.
type IconState int

// Tray icon states. The four recording states encode which sides are carrying
// sound: the upper dot is the microphone, the lower one the system output.
const (
	IconIdle IconState = iota
	IconRecBoth
	IconRecMicOnly
	IconRecSysOnly
	IconRecNeither
	IconProcessing
	IconError
)

// Recording returns the icon for the current activity of both tracks.
func Recording(micActive, sysActive bool) IconState {
	switch {
	case micActive && sysActive:
		return IconRecBoth
	case micActive:
		return IconRecMicOnly
	case sysActive:
		return IconRecSysOnly
	default:
		return IconRecNeither
	}
}

// Icon returns the embedded icon bytes for a state, in this platform's format.
func Icon(s IconState) []byte {
	switch s {
	case IconRecBoth:
		return iconRecMicSys
	case IconRecMicOnly:
		return iconRecMic
	case IconRecSysOnly:
		return iconRecSys
	case IconRecNeither:
		return iconRecNone
	case IconProcessing:
		return iconProcessing
	case IconError:
		return iconError
	default:
		return iconIdle
	}
}
