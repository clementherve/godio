//go:build windows

package meeting

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

const microphoneKey = `Software\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\microphone`

// InUse reports whether an application other than godio is capturing the
// microphone. Windows records the start and stop time of every app that used a
// privacy-sensitive capability in this per-user consent store.
func (d *Detector) InUse() (bool, error) {
	root, err := registry.OpenKey(registry.CURRENT_USER, microphoneKey,
		registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return false, fmt.Errorf("opening the microphone consent store: %w", err)
	}
	defer root.Close()

	return d.walk(root, "")
}

// walk checks every subkey, including the NonPackaged tree, for a capture that
// has started and not stopped. Unreadable subkeys are skipped: one stale or
// protected entry must not hide the meeting that is actually in progress.
func (d *Detector) walk(k registry.Key, prefix string) (bool, error) {
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return false, err
	}
	for _, name := range names {
		child, err := registry.OpenKey(k, name,
			registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}

		path := name
		if prefix != "" {
			path = prefix + `\` + name
		}

		if !d.isSelf(path) {
			start, stop, err := lastUsed(child)
			if err == nil && inUse(start, stop) {
				child.Close()
				return true, nil
			}
		}

		found, err := d.walk(child, path)
		child.Close()
		if err != nil {
			continue
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

// lastUsed reads the QWORD timestamps Windows writes for one application. A
// missing stop value is the normal state while an app is still capturing.
func lastUsed(k registry.Key) (start, stop uint64, err error) {
	start, _, err = k.GetIntegerValue("LastUsedTimeStart")
	if errors.Is(err, registry.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	stop, _, err = k.GetIntegerValue("LastUsedTimeStop")
	if errors.Is(err, registry.ErrNotExist) {
		return start, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	return start, stop, nil
}
