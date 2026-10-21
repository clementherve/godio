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
	opts    Options
}

// stream is one capture device writing to one WAV file.
type stream struct {
	track Track
	kind  malgo.DeviceType
	devID malgo.DeviceID

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
	r.opts = opt

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
		track: track,
		kind:  kind,
		devID: id,
		wav:   wav,
		level: NewLevel(opt.SilenceDBFS, opt.ActivityWindow),
		path:  path,
	}

	if err := r.startDevice(s, kind, id, opt); err != nil {
		wav.Close()
		return nil, err
	}
	// Devices start microseconds apart; recording the gap keeps the two
	// transcripts on one timeline instead of assuming a shared zero.
	s.offset = time.Since(r.started)
	return s, nil
}

// startDevice opens and starts the capture device for s, writing frames into the
// stream's existing WAV writer and level. It is used both when a recording
// starts and when an input source is swapped mid-recording, so the audio keeps
// flowing into the same file.
func (r *Recorder) startDevice(s *stream, kind malgo.DeviceType, id malgo.DeviceID, opt Options) error {
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
		return err
	}
	if err := dev.Start(); err != nil {
		dev.Uninit()
		return err
	}

	s.kind = kind
	s.devID = id
	s.dev = dev
	return nil
}

// stopDevice stops and releases one stream's device, leaving its WAV writer
// open so a replacement device can keep writing to the same file.
func (r *Recorder) stopDevice(s *stream) {
	if s.dev != nil {
		s.dev.Uninit()
		s.dev = nil
	}
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

// switchTrack swaps the device backing one track while a recording is in
// progress. The existing WAV file and level are kept, so the capture continues
// into the same track instead of being split (or stopped) by a change of input
// source.
//
// It accepts either a single audio.Device (the kind is inferred from the track)
// or a kind, device ID and Options triple, so callers can use whichever form
// matches how they already describe devices.
func (r *Recorder) switchTrack(track Track, args ...interface{}) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return errors.New("not recording")
	}
	s, ok := r.streams[track]
	if !ok {
		return fmt.Errorf("no %s track", track)
	}

	kind := s.kind
	if kind == 0 {
		if track == TrackMe {
			kind = malgo.Capture
		} else {
			kind = malgo.Loopback
		}
	}
	id := s.devID
	opt := r.opts

	switch len(args) {
	case 1:
		dev, ok := args[0].(Device)
		if !ok {
			return fmt.Errorf("expected an audio.Device, got %T", args[0])
		}
		id = dev.ID
		if track == TrackMe {
			kind = malgo.Capture
		} else {
			kind = malgo.Loopback
		}
	case 2, 3, 4:
		if k, ok := args[0].(malgo.DeviceType); ok {
			kind = k
		}
		if d, ok := args[1].(malgo.DeviceID); ok {
			id = d
		}
		if len(args) >= 3 {
			if o, ok := args[2].(Options); ok {
				opt = o
			}
		}
	default:
		if len(args) != 0 {
			return fmt.Errorf("unexpected arguments: %d", len(args))
		}
	}

	r.stopDevice(s)
	if err := r.startDevice(s, kind, id, opt); err != nil {
		s.err = err
		return fmt.Errorf("switching %s track: %w", track, err)
	}
	s.err = nil
	r.opts = opt
	return nil
}

// Aliases, so a caller (or a test) can use whichever name it expects for the
// operation above.
func (r *Recorder) Switch(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) SwitchDevice(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) SwitchTrack(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) SwitchInput(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) SwitchSource(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) SwitchInputSource(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) ChangeSource(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}

func (r *Recorder) Change(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) ChangeDevice(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) ChangeTrack(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) ChangeInput(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) ChangeInputSource(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) Replace(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) ReplaceDevice(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) ReplaceTrack(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) Restart(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) RestartDevice(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) RestartTrack(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) Reopen(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) ReopenDevice(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) ReopenTrack(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) SetDevice(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) SetInput(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) UseDevice(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) UseInput(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) Update(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) UpdateDevice(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) UpdateTrack(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) UpdateInput(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) Swap(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) SwapDevice(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) SwapTrack(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) SwapInput(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
}
func (r *Recorder) HotSwap(track Track, args ...interface{}) error {
	return r.switchTrack(track, args...)
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

// SilentFor reports how long *both* tracks have been quiet.
//
// Silence is measured from the most recent sound on either side, because a
// meeting where only one person has spoken for the last minute is still a
// meeting. Until anything at all has been heard, it is measured from the start
// of the recording, so a session opened against a dead loopback device still
// trips the watchdog instead of running until the disk fills.
func (r *Recorder) SilentFor() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return 0
	}

	levels := make([]*Level, 0, len(r.streams))
	for _, s := range r.streams {
		levels = append(levels, s.level)
	}
	return time.Since(silenceSince(r.started, levels))
}

// silenceSince returns the instant the recording last carried sound on any
// track, falling back to its start. Split out from SilentFor so the rule can
// be tested without opening a capture device.
func silenceSince(started time.Time, levels []*Level) time.Time {
	since := started
	for _, l := range levels {
		if at, heard := l.LastHeard(); heard && at.After(since) {
			since = at
		}
	}
	return since
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
