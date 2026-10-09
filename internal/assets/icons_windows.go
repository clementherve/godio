//go:build windows

package assets

import _ "embed"

// Windows tray icons must be ICO; the files embed a PNG inside an ICO
// container, which Windows has accepted since Vista.

//go:embed icons/idle.ico
var iconIdle []byte

//go:embed icons/rec-mic-sys.ico
var iconRecMicSys []byte

//go:embed icons/rec-mic.ico
var iconRecMic []byte

//go:embed icons/rec-sys.ico
var iconRecSys []byte

//go:embed icons/rec-none.ico
var iconRecNone []byte

//go:embed icons/processing.ico
var iconProcessing []byte

//go:embed icons/error.ico
var iconError []byte
