// Package selfupdate replaces godio's own binaries with the latest GitHub
// release.
//
// It is deliberately separate from internal/deps, which installs whisper.cpp:
// that installer streams hundreds of megabytes and is tuned for it, while this
// one verifies a digest before anything on disk moves. Sharing one downloader
// would mean a change made for either one silently applying to the other, and
// here the file being replaced is the running program.
package selfupdate

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a release tag, parsed enough to be ordered.
type Version struct {
	Major, Minor, Patch int
	// Pre is the pre-release suffix without its dash ("rc1"), empty for a
	// final release.
	Pre string
	// Raw is the tag as published, "v" included.
	Raw string
}

// String returns the tag as published.
func (v Version) String() string { return v.Raw }

// ParseVersion reads "v1.2.3", "1.2.3" or "v1.2.3-rc1".
//
// The bool, rather than an error, is because the only caller that cares treats
// an unparseable version as "a build not made by CI" — which is a state, not a
// failure.
func ParseVersion(s string) (Version, bool) {
	raw := strings.TrimSpace(s)
	v := Version{Raw: raw}

	body := strings.TrimPrefix(raw, "v")
	if i := strings.IndexAny(body, "-+"); i >= 0 {
		v.Pre = body[i+1:]
		body = body[:i]
	}

	parts := strings.Split(body, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, false
		}
		switch i {
		case 0:
			v.Major = n
		case 1:
			v.Minor = n
		case 2:
			v.Patch = n
		}
	}
	return v, true
}

// Less reports whether v precedes o.
//
// Pre-releases sort before the release they lead to, as semver requires:
// v1.0.0-rc1 < v1.0.0. Two pre-releases of the same version compare as plain
// strings, which is enough for "rc1" / "rc2" and is not worth more.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	if v.Patch != o.Patch {
		return v.Patch < o.Patch
	}
	switch {
	case v.Pre == o.Pre:
		return false
	case v.Pre == "":
		return false // a final release never precedes its own pre-releases
	case o.Pre == "":
		return true
	default:
		return v.Pre < o.Pre
	}
}

// IsDev reports whether a version string came from a local build rather than
// from a CI tag. CI stamps "dev-<sha>" off a tag and the Makefile-less local
// build leaves the default "dev".
func IsDev(s string) bool {
	v := strings.TrimSpace(s)
	return v == "" || v == "dev" || strings.HasPrefix(v, "dev-")
}

// humanSize renders a byte count the same way the dependency installer does,
// so the two progress lines read alike.
func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f Go", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f Mo", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d o", n)
	}
}
