package whisper

import (
	"testing"
	"time"
)

// Shape taken from whisper-cli's -oj output.
const sample = `{
  "systeminfo": "AVX = 1 | ...",
  "model": {"type": "large"},
  "result": {"language": "fr"},
  "transcription": [
    {"timestamps": {"from": "00:00:00,000", "to": "00:00:02,500"},
     "offsets": {"from": 0, "to": 2500}, "text": " Bonjour à tous."},
    {"timestamps": {"from": "00:00:02,500", "to": "00:00:04,000"},
     "offsets": {"from": 2500, "to": 4000}, "text": " [Musique]"},
    {"timestamps": {"from": "00:00:04,000", "to": "00:00:06,000"},
     "offsets": {"from": 4000, "to": 6000}, "text": "   "},
    {"timestamps": {"from": "00:00:06,000", "to": "00:00:09,000"},
     "offsets": {"from": 6000, "to": 9000}, "text": " On commence par le budget."}
  ]
}`

func TestParse(t *testing.T) {
	segs, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2 (noise and blanks dropped): %+v", len(segs), segs)
	}
	if want := "Bonjour à tous."; segs[0].Text != want {
		t.Errorf("text = %q, want %q (should be trimmed)", segs[0].Text, want)
	}
	if want := 2500 * time.Millisecond; segs[0].End != want {
		t.Errorf("end = %v, want %v", segs[0].End, want)
	}
	if want := 6 * time.Second; segs[1].Start != want {
		t.Errorf("start = %v, want %v", segs[1].Start, want)
	}
}

func TestIsNoise(t *testing.T) {
	noise := []string{"[Musique]", "(bruit de fond)", "*soupir*", "♪♪", "[BLANK_AUDIO]"}
	for _, s := range noise {
		if !isNoise(s) {
			t.Errorf("isNoise(%q) = false, want true", s)
		}
	}
	// Real speech that merely contains brackets must survive.
	speech := []string{
		"Bonjour",
		"Le budget (révisé) est validé.",
		"[inaudible] puis on enchaîne sur le sprint",
		"On note [A] et [B] comme options",
	}
	for _, s := range speech {
		if isNoise(s) {
			t.Errorf("isNoise(%q) = true, want false", s)
		}
	}
}

func TestParseEmptyTranscription(t *testing.T) {
	segs, err := Parse([]byte(`{"transcription": []}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(segs) != 0 {
		t.Errorf("got %d segments, want 0", len(segs))
	}
}

func TestParseInvalidJSON(t *testing.T) {
	if _, err := Parse([]byte("not json")); err == nil {
		t.Error("Parse(invalid) = nil error, want error")
	}
}
