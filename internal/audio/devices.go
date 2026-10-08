// Package audio captures the microphone and the system output simultaneously.
package audio

import (
	"fmt"
	"strings"

	"github.com/gen2brain/malgo"
)

// Device is one selectable audio endpoint.
type Device struct {
	ID        malgo.DeviceID
	Name      string
	IsDefault bool
}

// Context owns the miniaudio context. Close it when done.
type Context struct{ alloc *malgo.AllocatedContext }

// NewContext initialises miniaudio.
func NewContext() (*Context, error) {
	// The log callback is intentionally a no-op: miniaudio is chatty about
	// backend probing, and it would otherwise drown the CLI output.
	alloc, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(string) {})
	if err != nil {
		return nil, fmt.Errorf("initialising audio backend: %w", err)
	}
	return &Context{alloc: alloc}, nil
}

// Close releases the context.
func (c *Context) Close() {
	if c.alloc == nil {
		return
	}
	_ = c.alloc.Uninit()
	c.alloc.Free()
	c.alloc = nil
}

// CaptureDevices lists microphones and other recording inputs.
func (c *Context) CaptureDevices() ([]Device, error) {
	return c.devices(malgo.Capture)
}

// PlaybackDevices lists audio outputs.
//
// These are what the loopback picker offers: WASAPI loopback captures what a
// *render* endpoint is playing, so selecting "which audio do I want to hear"
// means naming a speaker or headset, not a recording input. This is what makes
// the "Teams is playing to my headset" case fixable rather than a silent failure.
func (c *Context) PlaybackDevices() ([]Device, error) {
	return c.devices(malgo.Playback)
}

func (c *Context) devices(kind malgo.DeviceType) ([]Device, error) {
	infos, err := c.alloc.Devices(kind)
	if err != nil {
		return nil, fmt.Errorf("enumerating audio devices: %w", err)
	}
	out := make([]Device, 0, len(infos))
	for i := range infos {
		out = append(out, Device{
			ID:        infos[i].ID,
			Name:      infos[i].Name(),
			IsDefault: infos[i].IsDefault != 0,
		})
	}
	return out, nil
}

// Find returns the device whose name contains want (case-insensitively), or the
// default device when want is empty.
//
// Matching by name rather than by ID is deliberate: Windows device IDs change
// when hardware is re-enumerated (a headset re-paired, a dock reconnected), and
// a config file that silently stops matching would be worse than one that is
// slightly fuzzy.
func Find(devices []Device, want string) (Device, error) {
	if len(devices) == 0 {
		return Device{}, fmt.Errorf("no audio devices found")
	}
	if strings.TrimSpace(want) == "" {
		for _, d := range devices {
			if d.IsDefault {
				return d, nil
			}
		}
		return devices[0], nil
	}

	want = strings.ToLower(want)
	for _, d := range devices {
		if strings.EqualFold(d.Name, want) {
			return d, nil
		}
	}
	for _, d := range devices {
		if strings.Contains(strings.ToLower(d.Name), want) {
			return d, nil
		}
	}

	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = d.Name
	}
	return Device{}, fmt.Errorf("no device matching %q; available: %s", want, strings.Join(names, ", "))
}
