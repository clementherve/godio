package audio

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"sync"
)

const (
	// SampleRate is what Whisper requires, and what miniaudio converts to for us.
	SampleRate = 16000
	// Channels is mono: Whisper takes one channel, and halving the data is free.
	Channels = 1
	// BitsPerSample is 16-bit signed PCM.
	BitsPerSample = 16

	wavHeaderSize = 44
)

// WAVWriter streams 16 kHz mono s16 PCM to a .wav file.
//
// The RIFF header carries two length fields that are only knowable once the
// recording ends, so a placeholder header is written up front and patched on
// Close. If the process dies before Close, the file still holds valid PCM but
// advertises a zero length; Repair fixes such a file from its size on disk.
type WAVWriter struct {
	mu     sync.Mutex
	f      *os.File
	w      *bufio.Writer
	n      uint32 // bytes of PCM written
	closed bool
}

// CreateWAV creates path and writes a placeholder header.
func CreateWAV(path string) (*WAVWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(header(0)); err != nil {
		f.Close()
		return nil, err
	}
	return &WAVWriter{f: f, w: bufio.NewWriterSize(f, 64*1024)}, nil
}

// Write appends PCM bytes.
func (w *WAVWriter) Write(pcm []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	n, err := w.w.Write(pcm)
	w.n += uint32(n)
	return n, err
}

// Bytes returns how much PCM has been written so far.
func (w *WAVWriter) Bytes() uint32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.n
}

// Close flushes, patches the header with the real lengths, and closes the file.
// It is safe to call more than once.
func (w *WAVWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true

	if err := w.w.Flush(); err != nil {
		w.f.Close()
		return err
	}
	if _, err := w.f.WriteAt(header(w.n), 0); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

// header builds a 44-byte canonical RIFF/WAVE header for n bytes of PCM.
func header(n uint32) []byte {
	const (
		byteRate   = SampleRate * Channels * BitsPerSample / 8
		blockAlign = Channels * BitsPerSample / 8
	)
	b := make([]byte, wavHeaderSize)
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 36+n) // size of everything after this field
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(b[20:], 1)  // format: PCM
	binary.LittleEndian.PutUint16(b[22:], Channels)
	binary.LittleEndian.PutUint32(b[24:], SampleRate)
	binary.LittleEndian.PutUint32(b[28:], byteRate)
	binary.LittleEndian.PutUint16(b[32:], blockAlign)
	binary.LittleEndian.PutUint16(b[34:], BitsPerSample)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], n)
	return b
}

// Repair rewrites the header of a WAV whose length fields are wrong, deriving
// the true PCM length from the file size. This recovers recordings left behind
// by a crash or a forced quit mid-meeting.
func Repair(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Size() < wavHeaderSize {
		return fmt.Errorf("%s is too short to be a WAV (%d bytes)", path, fi.Size())
	}

	n := uint32(fi.Size() - wavHeaderSize)
	if _, err := f.WriteAt(header(n), 0); err != nil {
		return err
	}
	return nil
}

// Duration reports the length of PCM written so far, in seconds.
func (w *WAVWriter) Duration() float64 {
	return float64(w.Bytes()) / float64(SampleRate*Channels*BitsPerSample/8)
}
