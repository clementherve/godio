package meeting

import (
	"runtime"
	"testing"
)

func TestInUse(t *testing.T) {
	for _, tc := range []struct {
		name        string
		start, stop uint64
		want        bool
	}{
		{"never used", 0, 0, false},
		{"in use", 100, 0, true},
		{"in use with a stale stop", 100, 90, true},
		{"finished", 100, 110, false},
		{"same timestamp", 100, 100, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inUse(tc.start, tc.stop); got != tc.want {
				t.Errorf("inUse(%d, %d) = %v, want %v", tc.start, tc.stop, got, tc.want)
			}
		})
	}
}

func TestKeyForPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("NonPackaged key names are a Windows registry detail")
	}
	got := keyForPath(`C:\Users\Alice\godio-tray.exe`)
	want := `C:#Users#Alice#godio-tray.exe`
	if got != want {
		t.Errorf("keyForPath = %q, want %q", got, want)
	}
}

func TestIsSelf(t *testing.T) {
	d := &Detector{self: `C:#Users#Alice#godio-tray.exe`}
	if !d.isSelf(`NonPackaged\C:#Users#Alice#godio-tray.exe`) {
		t.Error("isSelf did not recognise godio's own key")
	}
	if d.isSelf(`NonPackaged\C:#Users#Alice#teams.exe`) {
		t.Error("isSelf mistook another application for godio")
	}
	if d.isSelf(`C:#Users#Alice#godio-tray.exe`) {
		t.Error("isSelf matched outside the NonPackaged tree")
	}
}
