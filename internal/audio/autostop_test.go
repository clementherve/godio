package audio

import (
	"testing"
	"time"
)

func TestAutoStopState(t *testing.T) {
	const (
		after = 5 * time.Minute
		warn  = 30 * time.Second
	)

	tests := []struct {
		name      string
		a         AutoStop
		silentFor time.Duration
		want      AutoStopState
	}{
		{"disabled never fires", AutoStop{}, time.Hour, AutoStopNone},
		{"disabled ignores a warning", AutoStop{Warning: warn}, time.Hour, AutoStopNone},
		{"fresh silence", AutoStop{After: after, Warning: warn}, 0, AutoStopNone},
		{"just before the warning", AutoStop{After: after, Warning: warn}, 4*time.Minute + 29*time.Second, AutoStopNone},
		{"at the warning", AutoStop{After: after, Warning: warn}, 4*time.Minute + 30*time.Second, AutoStopWarn},
		{"inside the warning", AutoStop{After: after, Warning: warn}, 4*time.Minute + 59*time.Second, AutoStopWarn},
		{"at the threshold", AutoStop{After: after, Warning: warn}, after, AutoStopFire},
		{"past the threshold", AutoStop{After: after, Warning: warn}, time.Hour, AutoStopFire},
		{"no warning configured", AutoStop{After: after}, 4*time.Minute + 59*time.Second, AutoStopNone},
		// A warning longer than the silence threshold would otherwise be due
		// before the recording even started.
		{"warning longer than the threshold", AutoStop{After: time.Minute, Warning: time.Hour}, 0, AutoStopWarn},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.State(tc.silentFor); got != tc.want {
				t.Errorf("State(%s) = %v, want %v", tc.silentFor, got, tc.want)
			}
		})
	}
}

func TestAutoStopRemaining(t *testing.T) {
	a := AutoStop{After: 5 * time.Minute, Warning: 30 * time.Second}

	tests := []struct {
		silentFor, want time.Duration
	}{
		{0, 5 * time.Minute},
		{4*time.Minute + 30*time.Second, 30 * time.Second},
		{4*time.Minute + 59*time.Second + 600*time.Millisecond, time.Second}, // rounded up, never 0 while still recording
		{5 * time.Minute, 0},
		{time.Hour, 0}, // never negative
	}

	for _, tc := range tests {
		if got := a.Remaining(tc.silentFor); got != tc.want {
			t.Errorf("Remaining(%s) = %s, want %s", tc.silentFor, got, tc.want)
		}
	}

	if got := (AutoStop{}).Remaining(time.Minute); got != 0 {
		t.Errorf("disabled Remaining = %s, want 0", got)
	}
}

// TestLevelLastHeard covers the signal SilentFor is built on: a track that has
// never carried sound must be distinguishable from one that fell silent at the
// zero time.
func TestLevelLastHeard(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	l := NewLevel(-45, 10*time.Second)
	l.now = func() time.Time { return now }

	if _, heard := l.LastHeard(); heard {
		t.Error("a fresh Level reports having heard something")
	}

	l.Feed(silentPCM(1600))
	if _, heard := l.LastHeard(); heard {
		t.Error("silence counted as sound")
	}

	l.Feed(loudPCM(1600))
	at, heard := l.LastHeard()
	if !heard {
		t.Fatal("sound was not recorded")
	}
	if !at.Equal(now) {
		t.Errorf("LastHeard = %s, want %s", at, now)
	}
}

// TestSilenceSince covers the rule SilentFor applies across both tracks:
// silence is measured from the most recent sound on *either* side, and from
// the start of the recording when nothing has ever been heard.
func TestSilenceSince(t *testing.T) {
	started := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

	// at returns a Level that last heard sound after d, or never when heard
	// is false.
	at := func(d time.Duration, heard bool) *Level {
		l := NewLevel(-45, 10*time.Second)
		l.now = func() time.Time { return started.Add(d) }
		if heard {
			l.Feed(loudPCM(16))
		} else {
			l.Feed(silentPCM(16))
		}
		return l
	}

	tests := []struct {
		name   string
		levels []*Level
		want   time.Duration // offset from started
	}{
		{"no tracks", nil, 0},
		{"nothing ever heard", []*Level{at(time.Minute, false), at(time.Minute, false)}, 0},
		{"one side speaking", []*Level{at(2*time.Minute, true), at(time.Minute, false)}, 2 * time.Minute},
		{"the later side wins", []*Level{at(time.Minute, true), at(3*time.Minute, true)}, 3 * time.Minute},
		{"order does not matter", []*Level{at(3*time.Minute, true), at(time.Minute, true)}, 3 * time.Minute},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := silenceSince(started, tc.levels)
			if want := started.Add(tc.want); !got.Equal(want) {
				t.Errorf("silenceSince = %s, want %s", got, want)
			}
		})
	}
}

// silentPCM returns n samples of digital silence.
func silentPCM(n int) []byte { return make([]byte, n*2) }

// loudPCM returns n samples near full scale.
func loudPCM(n int) []byte {
	b := make([]byte, n*2)
	for i := 0; i < n; i++ {
		b[i*2], b[i*2+1] = 0x00, 0x40 // 0x4000, about -6 dBFS
	}
	return b
}
