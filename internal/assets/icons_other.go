//go:build !windows

package assets

import _ "embed"

// Non-Windows tray backends take PNG. raudio targets Windows, but keeping these
// building means the tray can be exercised during development on another OS.

//go:embed icons/idle.png
var iconIdle []byte

//go:embed icons/active.png
var iconActive []byte

//go:embed icons/silent.png
var iconSilent []byte

//go:embed icons/processing.png
var iconProcessing []byte

//go:embed icons/error.png
var iconError []byte
