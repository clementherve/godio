package pipeline

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/clementherve/godio/internal/audio"
	"github.com/clementherve/godio/internal/config"
)

// Retention says how long a session's parts are kept. Zero means forever,
// which is godio's historical behaviour and stays the default: a release that
// starts deleting recordings on upgrade would be a nasty surprise.
type Retention struct {
	// Audio is how long the two WAV files are kept. They are the only large
	// files — about 115 MB an hour for the pair — while everything needed to
	// re-run extraction is a few kilobytes of text.
	Audio time.Duration
	// Session is how long the whole folder is kept, transcript and notes
	// included.
	Session time.Duration
}

// Enabled reports whether anything would ever be deleted.
func (r Retention) Enabled() bool { return r.Audio > 0 || r.Session > 0 }

// Retention returns the configured policy.
func (p Pipeline) Retention() Retention {
	return Retention{
		Audio:   config.Days(p.Cfg.Sessions.KeepAudioDays),
		Session: config.Days(p.Cfg.Sessions.KeepDays),
	}
}

// Purged reports what a purge did, or would do.
type Purged struct {
	Audio    []string // sessions whose WAV files were removed
	Sessions []string // sessions removed entirely
	Bytes    int64    // freed, or that would be freed
	Kept     int      // sessions left alone because they were never processed
}

// Empty reports whether there was nothing to do.
func (p Purged) Empty() bool { return len(p.Audio) == 0 && len(p.Sessions) == 0 }

// Purge applies a retention policy to the sessions under root.
//
// Three rules keep this from ever costing someone a meeting:
//
//   - only folders whose name parses as a session timestamp are touched, so a
//     directory someone else put there is never deleted;
//   - a session that produced no notes.md is never purged, whatever its age —
//     an unprocessed recording is exactly the one still worth something;
//   - age is taken from the folder name, not from mtime, which re-running
//     extract would otherwise push forward.
//
// dryRun walks and measures without deleting, which is what `--dry-run` and the
// first run of a new policy want.
func Purge(root string, r Retention, now time.Time, dryRun bool) (Purged, error) {
	var out Purged
	if !r.Enabled() {
		return out, nil
	}

	ents, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil // nothing recorded yet
		}
		return out, err
	}

	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		started, ok := startedAt(e.Name())
		if !ok {
			continue
		}
		dir := filepath.Join(root, e.Name())
		age := now.Sub(started)

		// Notes are the proof the pipeline ran to the end. Without them the
		// recording is the only copy of the meeting.
		if _, err := os.Stat(filepath.Join(dir, "notes.md")); err != nil {
			if r.Session > 0 && age > r.Session {
				out.Kept++
			}
			continue
		}

		if r.Session > 0 && age > r.Session {
			size, err := dirSize(dir)
			if err != nil {
				return out, err
			}
			if !dryRun {
				if err := os.RemoveAll(dir); err != nil {
					return out, err
				}
			}
			out.Sessions = append(out.Sessions, e.Name())
			out.Bytes += size
			continue
		}

		if r.Audio > 0 && age > r.Audio {
			freed, err := removeTracks(dir, dryRun)
			if err != nil {
				return out, err
			}
			if freed > 0 {
				out.Audio = append(out.Audio, e.Name())
				out.Bytes += freed
			}
		}
	}
	return out, nil
}

// removeTracks deletes a session's WAV files, leaving the transcript and the
// notes — so `godio extract --day` still works on a session whose audio is
// gone. It returns the bytes freed.
func removeTracks(dir string, dryRun bool) (int64, error) {
	var freed int64
	for _, t := range []audio.Track{audio.TrackMe, audio.TrackThem} {
		path := filepath.Join(dir, string(t)+".wav")
		fi, err := os.Stat(path)
		if err != nil {
			continue // already gone
		}
		if !dryRun {
			if err := os.Remove(path); err != nil {
				return freed, err
			}
		}
		freed += fi.Size()
	}
	return freed, nil
}

// dirSize totals a session folder, for the "freed N MB" line.
func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		total += fi.Size()
		return nil
	})
	return total, err
}
