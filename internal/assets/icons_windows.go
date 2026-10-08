//go:build windows

package assets

import _ "embed"

// Windows tray icons must be ICO; the files embed a PNG inside an ICO
// container, which Windows has accepted since Vista.

//go:embed icons/idle.ico
var iconIdle []byte

//go:embed icons/active.ico
var iconActive []byte

//go:embed icons/silent.ico
var iconSilent []byte

//go:embed icons/processing.ico
var iconProcessing []byte

//go:embed icons/error.ico
var iconError []byte
