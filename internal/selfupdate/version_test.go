package selfupdate

import "testing"

func TestParseVersion(t *testing.T) {
	ok := []struct {
		in            string
		maj, min, pat int
		pre           string
	}{
		{"v0.3.0", 0, 3, 0, ""},
		{"0.3.0", 0, 3, 0, ""},
		{"v1.2.3", 1, 2, 3, ""},
		{"v1.0.0-rc1", 1, 0, 0, "rc1"},
		{"v10.20.30", 10, 20, 30, ""},
		{" v0.3.0 ", 0, 3, 0, ""},
	}
	for _, tc := range ok {
		v, got := ParseVersion(tc.in)
		if !got {
			t.Errorf("ParseVersion(%q) failed", tc.in)
			continue
		}
		if v.Major != tc.maj || v.Minor != tc.min || v.Patch != tc.pat || v.Pre != tc.pre {
			t.Errorf("ParseVersion(%q) = %d.%d.%d-%q", tc.in, v.Major, v.Minor, v.Patch, v.Pre)
		}
	}

	// A build that did not come from a CI tag has no version to compare, and
	// must not be mistaken for 0.0.0 — which would make it eternally stale.
	for _, bad := range []string{"dev", "dev-ab12cd", "", "v1.2", "v1.2.3.4", "latest", "vX.Y.Z", "v1.-2.3"} {
		if _, ok := ParseVersion(bad); ok {
			t.Errorf("ParseVersion(%q) accepted a value it should not", bad)
		}
	}
}

func TestVersionLess(t *testing.T) {
	less := func(a, b string) bool {
		va, oka := ParseVersion(a)
		vb, okb := ParseVersion(b)
		if !oka || !okb {
			t.Fatalf("fixture does not parse: %q %q", a, b)
		}
		return va.Less(vb)
	}

	ordered := []struct{ lo, hi string }{
		{"v0.3.0", "v0.4.0"},
		{"v0.3.0", "v1.0.0"},
		{"v0.3.0", "v0.3.1"},
		{"v0.9.0", "v0.10.0"},    // not string order
		{"v1.0.0-rc1", "v1.0.0"}, // a pre-release precedes its release
		{"v1.0.0-rc1", "v1.0.0-rc2"},
		{"v0.3.0", "v1.0.0-rc1"},
	}
	for _, tc := range ordered {
		if !less(tc.lo, tc.hi) {
			t.Errorf("%s should precede %s", tc.lo, tc.hi)
		}
		if less(tc.hi, tc.lo) {
			t.Errorf("%s must not precede %s", tc.hi, tc.lo)
		}
	}

	for _, same := range []string{"v0.3.0", "v1.0.0-rc1"} {
		if less(same, same) {
			t.Errorf("%s precedes itself", same)
		}
	}
}

func TestIsDev(t *testing.T) {
	for _, s := range []string{"dev", "dev-ab12cd", "", "  "} {
		if !IsDev(s) {
			t.Errorf("IsDev(%q) = false", s)
		}
	}
	for _, s := range []string{"v0.3.0", "0.3.0", "v1.0.0-rc1"} {
		if IsDev(s) {
			t.Errorf("IsDev(%q) = true", s)
		}
	}
}

func TestHumanSize(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{512, "512 o"},
		{9859584, "9 Mo"},
		{2 << 30, "2.0 Go"},
	} {
		if got := humanSize(tc.in); got != tc.want {
			t.Errorf("humanSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
