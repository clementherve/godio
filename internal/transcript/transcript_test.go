package transcript

import (
	"testing"

	"github.com/clementherve/godio/internal/lang"
	"time"
)

func seg(start, end int, text string) Segment {
	return Segment{
		Start: time.Duration(start) * time.Second,
		End:   time.Duration(end) * time.Second,
		Text:  text,
	}
}

func TestMergeInterleavesChronologically(t *testing.T) {
	mine := []Segment{seg(0, 3, "bonjour"), seg(10, 12, "d'accord")}
	theirs := []Segment{seg(4, 8, "salut Clément")}

	got := Merge(mine, theirs, 0, 0)

	want := []struct {
		who  Speaker
		text string
	}{
		{Me, "bonjour"},
		{Them, "salut Clément"},
		{Me, "d'accord"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d segments, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Speaker != w.who || got[i].Text != w.text {
			t.Errorf("segment %d = %q/%q, want %q/%q", i, got[i].Speaker, got[i].Text, w.who, w.text)
		}
	}
}

func TestMergeAppliesPerTrackOffset(t *testing.T) {
	// Without the offset, "ensuite" (t=1 on its own track) would sort before
	// "d'abord" (t=2). The 5s offset is what puts them in the real order.
	mine := []Segment{seg(2, 3, "d'abord")}
	theirs := []Segment{seg(1, 2, "ensuite")}

	got := Merge(mine, theirs, 0, 5*time.Second)

	if len(got) != 2 {
		t.Fatalf("got %d segments, want 2", len(got))
	}
	if got[0].Text != "d'abord" || got[1].Text != "ensuite" {
		t.Errorf("offset not applied: got %q then %q", got[0].Text, got[1].Text)
	}
	if want := 6 * time.Second; got[1].Start != want {
		t.Errorf("offset start = %v, want %v", got[1].Start, want)
	}
}

func TestMergeCollapsesSameSpeakerRuns(t *testing.T) {
	mine := []Segment{seg(0, 2, "bonjour"), seg(2, 4, "ça va"), seg(4, 6, "et toi")}

	got := Merge(mine, nil, 0, 0)

	if len(got) != 1 {
		t.Fatalf("got %d segments, want 1 collapsed: %+v", len(got), got)
	}
	if want := "bonjour ça va et toi"; got[0].Text != want {
		t.Errorf("text = %q, want %q", got[0].Text, want)
	}
	if want := 6 * time.Second; got[0].End != want {
		t.Errorf("end = %v, want %v (should extend to the run's end)", got[0].End, want)
	}
}

func TestMergeDropsEmptySegments(t *testing.T) {
	// Whisper emits blank and whitespace-only segments; they must not create
	// spurious speaker switches that break collapsing.
	mine := []Segment{seg(0, 2, "bonjour"), seg(6, 8, "au revoir")}
	theirs := []Segment{seg(3, 5, "   "), seg(4, 5, "")}

	got := Merge(mine, theirs, 0, 0)

	if len(got) != 1 {
		t.Fatalf("got %d segments, want 1: %+v", len(got), got)
	}
	if want := "bonjour au revoir"; got[0].Text != want {
		t.Errorf("text = %q, want %q", got[0].Text, want)
	}
}

func TestFormat(t *testing.T) {
	segs := []Segment{
		{Speaker: Me, Start: 12 * time.Second, Text: "bonjour"},
		{Speaker: Them, Start: 3725 * time.Second, Text: "très bien"},
	}

	want := "[00:00:12] Moi: bonjour\n[01:02:05] Interlocuteur: très bien\n"
	if got := Format(segs, LabelsFor(lang.FR)); got != want {
		t.Errorf("Format() =\n%q\nwant\n%q", got, want)
	}

	// The labels are the only part that is translated; the timeline and the
	// text are not.
	wantEN := "[00:00:12] Me: bonjour\n[01:02:05] Them: très bien\n"
	if got := Format(segs, LabelsFor(lang.EN)); got != wantEN {
		t.Errorf("Format(EN) =\n%q\nwant\n%q", got, wantEN)
	}
}

func TestDescribe(t *testing.T) {
	segs := Merge(
		[]Segment{seg(0, 2, "un deux trois")},
		[]Segment{seg(3, 9, "quatre cinq")},
		0, 0,
	)

	st := Describe(segs)
	if st.Segments != 2 {
		t.Errorf("Segments = %d, want 2", st.Segments)
	}
	if st.Words != 5 {
		t.Errorf("Words = %d, want 5", st.Words)
	}
	if st.BySpeaker[Me] != 3 || st.BySpeaker[Them] != 2 {
		t.Errorf("BySpeaker = %v, want me:3 them:2", st.BySpeaker)
	}
	if want := 9 * time.Second; st.Duration != want {
		t.Errorf("Duration = %v, want %v", st.Duration, want)
	}
}

func TestMergeEmpty(t *testing.T) {
	if got := Merge(nil, nil, 0, 0); len(got) != 0 {
		t.Errorf("Merge(nil, nil) = %+v, want empty", got)
	}
	if got := Format(nil, LabelsFor(lang.EN)); got != "" {
		t.Errorf("Format(nil) = %q, want empty", got)
	}
}
