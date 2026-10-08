package audio

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
)

// Track names one side of the conversation.
type Track string

const (
	// TrackMe is the microphone.
	TrackMe Track = "me"
	// TrackThem is the system output, captured via loopback.
	TrackThem Track = "them"
)

// Recorder captures the microphone and the system output to two WAV files.
type Recorder struct {
	ctx *Context
	dir string

	mu      sync.Mutex
	streams map[Track]*stream
	started time.Time
	running bool
}

// stream is one capture device writing to one WAV file.
type stream struct {
	dev    *malgo.Device
	wav    *WAVWriter
	level  *Level
	path   string
	offset time.Duration // delay between recording start and this device starting
	err    error
}

// Options configures a recording.
type Options struct {
	MicDevice      Device
	PlaybackDevice Device // the output endpoint to capture via loopback
	SilenceDBFS    float64
	ActivityWindow time.Duration
}

// NewRecorder prepares a recorder writing into dir.
func NewRecorder(ctx *Context, dir string) *Recorder {
	return &Recorder{ctx: ctx, dir: dir, streams: map[Track]*stream{}}
}

// Start opens both devices and begins writing. Both tracks are captured at
// 16 kHz mono s16 — miniaudio converts from each device's native format (a
// loopback endpoint is typically 48 kHz stereo) inside the capture pipeline, so
// the callback already delivers exactly what Whisper wants. That removes any
// resampling on our side and lets audio stream straight to disk at ~32 KB/s,
// which keeps memory flat regardless of meeting length.
func (r *Recorder) Start(opt Options) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return errors.New("already recording")
	}

	r.started = time.Now()
	r.streams = map[Track]*stream{}

	specs := []struct {
		track    Track
		kind     malgo.DeviceType
		deviceID malgo.DeviceID
	}{
		// Loopback uses the *playback* device's ID in the capture sub-config;
		// that is how miniaudio targets a render endpoint for capture.
		{TrackMe, malgo.Capture, opt.MicDevice.ID},
		{TrackThem, malgo.Loopback, opt.PlaybackDevice.ID},
	}

	for _, spec := range specs {
		s, err := r.openStream(spec.track, spec.kind, spec.deviceID, opt)
		if err != nil {
			r.closeAllLocked()
			return fmt.Errorf("opening %s track: %w", spec.track, err)
		}
		r.streams[spec.track] = s
	}

	r.running = true
	return nil
}

func (r *Recorder) openStream(track Track, kind malgo.DeviceType, id malgo.DeviceID, opt Options) (*stream, error) {
	path := filepath.Join(r.dir, string(track)+".wav")
	wav, err := CreateWAV(path)
	if err != nil {
		return nil, err
	}

	s := &stream{
		wav:   wav,
		level: NewLevel(opt.SilenceDBFS, opt.ActivityWindow),
		path:  path,
	}

	cfg := malgo.DefaultDeviceConfig(kind)
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = Channels
	cfg.SampleRate = SampleRate
	cfg.Capture.DeviceID = id.Pointer()
	cfg.Alsa.NoMMap = 1

	onFrames := func(_, in []byte, _ uint32) {
		if len(in) == 0 {
			return
		}
		s.level.Feed(in)
		if _, err := s.wav.Write(in); err != nil && s.err == nil {
			s.err = err
		}
	}

	dev, err := malgo.InitDevice(r.ctx.alloc.Context, cfg, malgo.DeviceCallbacks{Data: onFrames})
	if err != nil {
		wav.Close()
		return nil, err
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		wav.Close()
		return nil, err
	}

	s.dev = dev
	// Devices start microseconds apart; recording the gap keeps the two
	// transcripts on one timeline instead of assuming a shared zero.
	s.offset = time.Since(r.started)
	return s, nil
}

// Stop halts both devices and finalises both WAV files.
func (r *Recorder) Stop() (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return Result{}, errors.New("not recording")
	}
	r.running = false

	res := Result{
		Dir:      r.dir,
		Started:  r.started,
		Duration: time.Since(r.started),
		Tracks:   map[Track]TrackResult{},
	}
	for t, s := range r.streams {
		res.Tracks[t] = TrackResult{
			Path:    s.path,
			Offset:  s.offset,
			Seconds: s.wav.Duration(),
		}
	}

	err := r.closeAllLocked()
	return res, err
}

// closeAllLocked stops and releases every stream. The caller must hold r.mu.
func (r *Recorder) closeAllLocked() error {
	var errs []error
	for _, s := range r.streams {
		if s.dev != nil {
			// Uninit stops the device and waits for the callback to drain, so
			// no frames arrive after the WAV below is closed.
			s.dev.Uninit()
		}
		if s.err != nil {
			errs = append(errs, s.err)
		}
		if s.wav != nil {
			if err := s.wav.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	r.streams = map[Track]*stream{}
	return errors.Join(errs...)
}

// Levels reports the current loudness and activity of both tracks, for the tray.
func (r *Recorder) Levels() map[Track]TrackLevel {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[Track]TrackLevel{}
	for t, s := range r.streams {
		out[t] = TrackLevel{DBFS: s.level.DBFS(), Active: s.level.Active()}
	}
	return out
}

// BothActive reports whether both sides have carried sound recently. This is
// what turns the tray indicator green.
func (r *Recorder) BothActive() bool {
	levels := r.Levels()
	if len(levels) < 2 {
		return false
	}
	for _, l := range levels {
		if !l.Active {
			return false
		}
	}
	return true
}

// Running reports whether a recording is in progress.
func (r *Recorder) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// TrackLevel is a point-in-time reading of one track.
type TrackLevel struct {
	DBFS   float64
	Active bool
}

// TrackResult describes one finished track.
type TrackResult struct {
	Path    string
	Offset  time.Duration
	Seconds float64
}

// Result describes a finished recording.
type Result struct {
	Dir      string
	Started  time.Time
	Duration time.Duration
	Tracks   map[Track]TrackResult
}
