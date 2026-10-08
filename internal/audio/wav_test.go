package audio

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func readHeader(t *testing.T, path string) (riffSize, dataSize uint32, body []byte) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading wav: %v", err)
	}
	if len(raw) < wavHeaderSize {
		t.Fatalf("file is %d bytes, shorter than a header", len(raw))
	}
	if string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" || string(raw[36:40]) != "data" {
		t.Fatalf("not a canonical RIFF/WAVE file: %q", raw[:44])
	}
	return binary.LittleEndian.Uint32(raw[4:]),
		binary.LittleEndian.Uint32(raw[40:]),
		raw[wavHeaderSize:]
}

func TestWAVWriterPatchesHeaderOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	w, err := CreateWAV(path)
	if err != nil {
		t.Fatalf("CreateWAV: %v", err)
	}

	pcm := tone(8000, 0.5) // 0.5s at 16 kHz
	for i := 0; i < 3; i++ {
		if _, err := w.Write(pcm); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	wantN := uint32(3 * len(pcm))
	riff, data, body := readHeader(t, path)
	if data != wantN {
		t.Errorf("data size = %d, want %d", data, wantN)
	}
	if riff != 36+wantN {
		t.Errorf("riff size = %d, want %d", riff, 36+wantN)
	}
	if uint32(len(body)) != wantN {
		t.Errorf("payload = %d bytes, want %d", len(body), wantN)
	}
	if got, want := w.Duration(), 1.5; got != want {
		t.Errorf("Duration() = %v, want %v", got, want)
	}
}

func TestWAVWriterCloseIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	w, _ := CreateWAV(path)
	w.Write(tone(100, 0.5))
	if err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close: %v, want nil", err)
	}
	if _, err := w.Write(tone(10, 0.5)); err == nil {
		t.Error("Write after Close succeeded, want error")
	}
}

func TestRepairRecoversCrashedRecording(t *testing.T) {
	// Simulate a crash: PCM is on disk but Close never patched the header.
	path := filepath.Join(t.TempDir(), "crashed.wav")
	w, _ := CreateWAV(path)
	pcm := tone(16000, 0.5) // 1s
	w.Write(pcm)
	w.w.Flush() // flush without patching, as a crash would leave it
	w.f.Close()

	if _, data, _ := readHeader(t, path); data != 0 {
		t.Fatalf("precondition: data size = %d, want 0 (unpatched)", data)
	}

	if err := Repair(path); err != nil {
		t.Fatalf("Repair: %v", err)
	}

	riff, data, body := readHeader(t, path)
	if want := uint32(len(pcm)); data != want {
		t.Errorf("after repair data size = %d, want %d", data, want)
	}
	if riff != 36+data {
		t.Errorf("after repair riff size = %d, want %d", riff, 36+data)
	}
	if len(body) != len(pcm) {
		t.Errorf("repair altered the payload: %d bytes, want %d", len(body), len(pcm))
	}
}

func TestRepairRejectsTruncatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.wav")
	os.WriteFile(path, []byte("RIFF"), 0o644)
	if err := Repair(path); err == nil {
		t.Error("Repair(truncated) = nil, want error")
	}
}

func TestWAVHeaderFields(t *testing.T) {
	h := header(1000)
	checks := []struct {
		name string
		got  uint32
		want uint32
	}{
		{"fmt chunk size", binary.LittleEndian.Uint32(h[16:]), 16},
		{"format tag", uint32(binary.LittleEndian.Uint16(h[20:])), 1},
		{"channels", uint32(binary.LittleEndian.Uint16(h[22:])), Channels},
		{"sample rate", binary.LittleEndian.Uint32(h[24:]), SampleRate},
		{"byte rate", binary.LittleEndian.Uint32(h[28:]), SampleRate * Channels * BitsPerSample / 8},
		{"block align", uint32(binary.LittleEndian.Uint16(h[32:])), Channels * BitsPerSample / 8},
		{"bits per sample", uint32(binary.LittleEndian.Uint16(h[34:])), BitsPerSample},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}
