package audio

import "time"

// AutoStop decides when a silent recording should end itself.
//
// The failure this exists for is mundane and expensive: a meeting finishes,
// everyone hangs up, and godio keeps recording the empty room until somebody
// remembers the tray icon — an hour of silence to transcribe, and notes that
// arrive long after they were useful. Watching for silence rather than for a
// closed Teams window keeps it independent of which conferencing tool is in
// use, which is the whole point of capturing the loopback device.
type AutoStop struct {
	// After is how long both tracks must stay silent. Zero disables the
	// watchdog entirely.
	After time.Duration
	// Warning is how long before After the user is told. Zero warns not at
	// all; it is clamped to After, so a warning can never be scheduled before
	// the recording starts.
	Warning time.Duration
}

// AutoStopState is what the watchdog thinks should happen right now.
type AutoStopState int

const (
	// AutoStopNone means the recording should continue, silently.
	AutoStopNone AutoStopState = iota
	// AutoStopWarn means the stop is imminent and the user should be told,
	// while any sound at all would still cancel it.
	AutoStopWarn
	// AutoStopFire means the recording should stop now.
	AutoStopFire
)

// Enabled reports whether the watchdog is armed.
func (a AutoStop) Enabled() bool { return a.After > 0 }

// State classifies a silence duration.
func (a AutoStop) State(silentFor time.Duration) AutoStopState {
	if !a.Enabled() {
		return AutoStopNone
	}
	if silentFor >= a.After {
		return AutoStopFire
	}
	if w := a.warning(); w > 0 && silentFor >= a.After-w {
		return AutoStopWarn
	}
	return AutoStopNone
}

// Remaining reports how long is left before the stop, in whole seconds so
// callers can put it straight in front of a user.
//
// It rounds *up*: with half-second ticks, rounding to nearest would announce
// "arrêt dans 0 s" for half a second while the recording is still running, and
// a countdown that reaches zero without anything happening reads as a bug.
// Zero is therefore reserved for "the stop is due".
func (a AutoStop) Remaining(silentFor time.Duration) time.Duration {
	if !a.Enabled() {
		return 0
	}
	left := a.After - silentFor
	if left <= 0 {
		return 0
	}
	return (left + time.Second - time.Nanosecond).Truncate(time.Second)
}

// warning returns the warning lead time, clamped to the silence threshold.
func (a AutoStop) warning() time.Duration {
	if a.Warning > a.After {
		return a.After
	}
	return a.Warning
}
