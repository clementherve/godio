//go:build !windows

package assets

import _ "embed"

// Non-Windows tray backends take PNG. godio targets Windows, but keeping these
// building means the tray can be exercised during development on another OS.

//go:embed icons/idle.png
var iconIdle []byte

//go:embed icons/rec-mic-sys.png
var iconRecMicSys []byte

//go:embed icons/rec-mic.png
var iconRecMic []byte

//go:embed icons/rec-sys.png
var iconRecSys []byte

//go:embed icons/rec-none.png
var iconRecNone []byte

//go:embed icons/processing.png
var iconProcessing []byte

//go:embed icons/error.png
var iconError []byte
