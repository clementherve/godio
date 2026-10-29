//go:build !windows

package meeting

// InUse is always false where the Windows per-user consent store does not
// exist, so the tray can keep one code path on every platform.
func (*Detector) InUse() (bool, error) { return false, nil }
