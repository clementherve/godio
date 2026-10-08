package audio

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// tone builds n samples of a sine at the given amplitude (0..1), as s16 LE mono.
func tone(n int, amp float64) []byte {
	buf := make([]byte, n*2)
	for i := 0; i < n; i++ {
		v := int16(amp * 32767 * math.Sin(2*math.Pi*float64(i)/32))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
	}
	return buf
}

func TestDBFS(t *testing.T) {
	if got := dbfs(nil); got != Silence {
		t.Errorf("dbfs(nil) = %v, want %v", got, Silence)
	}
	if got := dbfs(make([]byte, 512)); got != Silence {
		t.Errorf("dbfs(zeros) = %v, want %v", got, Silence)
	}

	// A full-scale sine has RMS = 1/sqrt(2), i.e. about -3 dBFS.
	if got := dbfs(tone(1024, 1.0)); math.Abs(got-(-3.0)) > 0.5 {
		t.Errorf("dbfs(full-scale sine) = %.2f, want about -3", got)
	}
	// Halving amplitude drops the level by about 6 dB.
	loud, quiet := dbfs(tone(1024, 1.0)), dbfs(tone(1024, 0.5))
	if d := loud - quiet; math.Abs(d-6.0) > 0.5 {
		t.Errorf("halving amplitude changed level by %.2f dB, want about 6", d)
	}
}

func TestLevelActiveWindow(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	l := NewLevel(-45, 10*time.Second)
	l.now = func() time.Time { return now }

	if l.Active() {
		t.Error("Active() = true before any audio, want false")
	}

	l.Feed(tone(1024, 0.5)) // well above -45 dBFS
	if !l.Active() {
		t.Error("Active() = false right after loud audio, want true")
	}

	// Silence inside the window: still active. This is the turn-taking case —
	// the other person talking must not flip our side to red.
	now = now.Add(9 * time.Second)
	l.Feed(make([]byte, 1024))
	if !l.Active() {
		t.Error("Active() = false 9s into a 10s window, want true")
	}

	// Past the window with nothing heard: the stream is genuinely dead.
	now = now.Add(2 * time.Second)
	if l.Active() {
		t.Error("Active() = true 11s after last sound, want false")
	}

	// Sound returns: active again.
	l.Feed(tone(1024, 0.5))
	if !l.Active() {
		t.Error("Active() = false after audio resumed, want true")
	}
}

func TestLevelThreshold(t *testing.T) {
	l := NewLevel(-45, time.Second)

	// About -60 dBFS: below the threshold, must not count as activity.
	l.Feed(tone(1024, 0.001))
	if l.Active() {
		t.Errorf("Active() = true for %.1f dBFS, want false (threshold -45)", l.DBFS())
	}

	l.Feed(tone(1024, 0.5))
	if !l.Active() {
		t.Errorf("Active() = false for %.1f dBFS, want true", l.DBFS())
	}
}

func TestLevelDBFSReportsLatestBuffer(t *testing.T) {
	l := NewLevel(-45, time.Second)
	l.Feed(tone(1024, 1.0))
	loud := l.DBFS()
	l.Feed(make([]byte, 1024))
	if quiet := l.DBFS(); quiet >= loud {
		t.Errorf("DBFS() = %.1f after silence, want below %.1f", quiet, loud)
	}
}
