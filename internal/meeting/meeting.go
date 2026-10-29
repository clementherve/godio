// Package meeting detects when another application starts using the microphone.
package meeting

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Detector reports whether another application is capturing the microphone.
// Use New rather than constructing one directly.
type Detector struct {
	self string
}

// New returns a detector that ignores the executable running it. Without that
// exclusion godio's own recording would look like a meeting starting.
func New() (*Detector, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locating the running executable: %w", err)
	}
	return &Detector{self: keyForPath(exe)}, nil
}

// keyForPath turns an executable path into the name Windows uses under the
// NonPackaged registry key: the path with each backslash replaced by '#'.
func keyForPath(path string) string {
	return strings.ReplaceAll(filepath.Clean(path), `\`, "#")
}

// isSelf reports whether a registry key path names the running executable.
func (d *Detector) isSelf(keyPath string) bool {
	return strings.EqualFold(keyPath, `NonPackaged\`+d.self)
}

// inUse interprets the two FILETIME values Windows stores for an application.
// A start time of zero means the app has never used the microphone; a stop
// time of zero means it has not finished using it.
func inUse(start, stop uint64) bool {
	return start > 0 && (stop == 0 || start > stop)
}
