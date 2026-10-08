package audio

import (
	"encoding/binary"
	"math"
	"sync"
	"time"
)

// Level tracks how loud one capture track is and when it last carried sound.
//
// The "last heard" timestamp, rather than an instantaneous loudness test, is what
// drives the tray indicator. Conversation is almost always one-sided at any given
// instant, so an instantaneous "both sides loud" test would flicker red constantly.
// A rolling window keeps normal turn-taking green while still catching the failure
// this indicator exists for: a stream that is genuinely dead, because the loopback
// device is wrong or the mic is muted.
type Level struct {
	threshold float64       // dBFS below which a buffer counts as silence
	window    time.Duration // how long a side stays "active" after last sound

	mu        sync.Mutex
	now       func() time.Time // injectable for tests
	lastDBFS  float64
	lastHeard time.Time
	everHeard bool
}

// Silence is the dBFS value reported for a digitally silent buffer.
const Silence = -120.0

// NewLevel returns a Level with the given silence threshold and activity window.
func NewLevel(thresholdDBFS float64, window time.Duration) *Level {
	return &Level{
		threshold: thresholdDBFS,
		window:    window,
		now:       time.Now,
		lastDBFS:  Silence,
	}
}

// Feed consumes a buffer of 16-bit little-endian mono PCM and updates the level.
func (l *Level) Feed(pcm []byte) {
	db := dbfs(pcm)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.lastDBFS = db
	if db > l.threshold {
		l.lastHeard = l.now()
		l.everHeard = true
	}
}

// DBFS returns the loudness of the most recent buffer.
func (l *Level) DBFS() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastDBFS
}

// Active reports whether this side carried sound within the activity window.
func (l *Level) Active() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.everHeard {
		return false
	}
	return l.now().Sub(l.lastHeard) <= l.window
}

// dbfs computes the RMS of a 16-bit mono PCM buffer, in dB relative to full scale.
func dbfs(pcm []byte) float64 {
	n := len(pcm) / 2
	if n == 0 {
		return Silence
	}
	var sum float64
	for i := 0; i < n; i++ {
		s := float64(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) / 32768.0
		sum += s * s
	}
	rms := math.Sqrt(sum / float64(n))
	if rms <= 0 {
		return Silence
	}
	db := 20 * math.Log10(rms)
	if db < Silence {
		return Silence
	}
	return db
}
