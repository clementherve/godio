// Package transcript merges the two independently-transcribed audio tracks
// into a single, chronologically ordered, speaker-labelled transcript.
package transcript

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Speaker identifies which track a segment came from.
type Speaker string

const (
	// Me is the microphone track.
	Me Speaker = "Moi"
	// Them is the system-audio (loopback) track.
	Them Speaker = "Interlocuteur"
)

// Segment is one span of speech on one track, offset from the start of the recording.
type Segment struct {
	Speaker Speaker
	Start   time.Duration
	End     time.Duration
	Text    string
}

// Merge interleaves two single-track segment lists into one transcript.
//
// Each list carries an offset: the delay between the start of the recording and
// the moment that track's device actually began capturing. Applying it keeps the
// two tracks on a common timeline even when the devices start microseconds apart.
//
// Consecutive segments from the same speaker are collapsed into one block, which
// keeps the transcript readable and cuts the token count handed to Claude.
func Merge(mine, theirs []Segment, mineOffset, theirsOffset time.Duration) []Segment {
	all := make([]Segment, 0, len(mine)+len(theirs))
	for _, src := range []struct {
		segs   []Segment
		offset time.Duration
		who    Speaker
	}{
		{mine, mineOffset, Me},
		{theirs, theirsOffset, Them},
	} {
		for _, s := range src.segs {
			if strings.TrimSpace(s.Text) == "" {
				continue
			}
			s.Speaker = src.who
			s.Start += src.offset
			s.End += src.offset
			all = append(all, s)
		}
	}

	// A stable sort keeps same-start segments in track order rather than shuffling
	// them arbitrarily between runs.
	sort.SliceStable(all, func(i, j int) bool { return all[i].Start < all[j].Start })

	return collapse(all)
}

// collapse joins runs of consecutive segments from the same speaker.
func collapse(segs []Segment) []Segment {
	out := make([]Segment, 0, len(segs))
	for _, s := range segs {
		s.Text = strings.TrimSpace(s.Text)
		if n := len(out); n > 0 && out[n-1].Speaker == s.Speaker {
			out[n-1].Text += " " + s.Text
			if s.End > out[n-1].End {
				out[n-1].End = s.End
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// Format renders the transcript in the form handed to Claude:
//
//	[00:00:12] Moi: ...
//	[00:00:19] Interlocuteur: ...
func Format(segs []Segment) string {
	var b strings.Builder
	for _, s := range segs {
		fmt.Fprintf(&b, "[%s] %s: %s\n", timestamp(s.Start), s.Speaker, s.Text)
	}
	return b.String()
}

// timestamp renders an offset as HH:MM:SS.
func timestamp(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, (total%3600)/60, total%60)
}

// Stats summarises a merged transcript, for logging and for deciding whether a
// recording is worth sending to Claude at all.
type Stats struct {
	Segments  int
	Words     int
	Duration  time.Duration
	BySpeaker map[Speaker]int // word count per speaker
}

// Describe computes Stats over a merged transcript.
func Describe(segs []Segment) Stats {
	st := Stats{BySpeaker: map[Speaker]int{}}
	for _, s := range segs {
		n := len(strings.Fields(s.Text))
		st.Segments++
		st.Words += n
		st.BySpeaker[s.Speaker] += n
		if s.End > st.Duration {
			st.Duration = s.End
		}
	}
	return st
}
