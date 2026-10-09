package deps

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Progress reports download progress for one stage. total is -1 when the
// server does not say how big the file is.
type Progress func(stage string, done, total int64)

// Plan is what Install will do, resolved ahead of time so the caller can show
// the user the total download size before committing to it.
type Plan struct {
	Backend     Backend
	GPU         GPU
	Release     string
	WhisperURL  string
	WhisperSize int64
	Speech      Model
	VAD         Model
	// Missing lists only the parts not already installed.
	Missing []string
}

// NeedsDownload reports whether anything is missing.
func (p Plan) NeedsDownload() bool { return len(p.Missing) > 0 }

// MakePlan works out what needs installing into dir.
func MakePlan(ctx context.Context, dir string, want Backend) (Plan, error) {
	backend, gpu := Resolve(want)

	driver := 0.0
	if gpu.Present {
		driver = gpu.Driver
	}
	releases, err := FetchReleases(ctx)
	if err != nil {
		return Plan{}, err
	}
	rel, asset, err := PickAsset(releases, assetPattern(backend, driver))
	if err != nil {
		return Plan{}, err
	}

	p := Plan{
		Backend:     backend,
		GPU:         gpu,
		Release:     rel.Tag,
		WhisperURL:  asset.URL,
		WhisperSize: asset.Size,
		Speech:      SpeechModel(backend),
		VAD:         VADModel,
	}

	if !exists(filepath.Join(dir, "whisper", "whisper-cli.exe")) &&
		!exists(filepath.Join(dir, "whisper", "whisper-cli")) {
		p.Missing = append(p.Missing, "whisper.cpp ("+rel.Tag+", "+humanSize(asset.Size)+")")
	}
	if !exists(filepath.Join(dir, "models", p.Speech.Name)) {
		p.Missing = append(p.Missing, p.Speech.Name)
	}
	if !exists(filepath.Join(dir, "models", p.VAD.Name)) {
		p.Missing = append(p.Missing, p.VAD.Name)
	}
	return p, nil
}

// Install downloads and installs everything the plan says is missing.
//
// Each piece lands in a temp file and is renamed into place only once it has
// downloaded completely, so an interrupted setup never leaves a truncated model
// or a half-extracted whisper that would fail confusingly much later.
func Install(ctx context.Context, dir string, p Plan, progress Progress) error {
	models := filepath.Join(dir, "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		return err
	}

	whisperExe := filepath.Join(dir, "whisper", "whisper-cli.exe")
	if !exists(whisperExe) && !exists(strings.TrimSuffix(whisperExe, ".exe")) {
		if err := installWhisper(ctx, dir, p, progress); err != nil {
			return fmt.Errorf("installing whisper.cpp: %w", err)
		}
	}

	for _, m := range []Model{p.Speech, p.VAD} {
		dst := filepath.Join(models, m.Name)
		if exists(dst) {
			continue
		}
		if err := downloadTo(ctx, m.URL, dst, m.Name, progress); err != nil {
			return fmt.Errorf("downloading %s: %w", m.Name, err)
		}
	}
	return nil
}

// installWhisper downloads the release zip and extracts the binary and the DLLs
// beside it.
func installWhisper(ctx context.Context, dir string, p Plan, progress Progress) error {
	tmpZip, err := os.CreateTemp("", "godio-whisper-*.zip")
	if err != nil {
		return err
	}
	tmpZip.Close()
	defer os.Remove(tmpZip.Name())

	stage := "whisper.cpp " + p.Release
	if err := downloadTo(ctx, p.WhisperURL, tmpZip.Name(), stage, progress); err != nil {
		return err
	}

	staging, err := os.MkdirTemp("", "godio-whisper-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	if err := unzip(tmpZip.Name(), staging); err != nil {
		return err
	}

	// The zip's internal layout varies between builds (sometimes a Release/
	// folder), so find the executable rather than assuming a path.
	src, err := findExeDir(staging)
	if err != nil {
		return err
	}

	final := filepath.Join(dir, "whisper")
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	// The CUDA builds will not start without the runtime DLLs sitting next to
	// the exe, so the whole directory moves, not just the binary.
	if err := os.Rename(src, final); err != nil {
		// Rename fails across filesystems; fall back to a copy.
		if err := copyDir(src, final); err != nil {
			return err
		}
	}

	// Normalise older builds that still ship main.exe.
	for _, old := range []string{"main.exe", "main"} {
		p := filepath.Join(final, old)
		if exists(p) && !exists(filepath.Join(final, "whisper-cli.exe")) {
			_ = os.Rename(p, filepath.Join(final, "whisper-cli"+filepath.Ext(old)))
		}
	}
	return nil
}

// downloadTo streams url into dst via a temp file in the same directory.
func downloadTo(ctx context.Context, url, dst, stage string, progress Progress) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "godio")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".godio-dl-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)

	total := resp.ContentLength
	w := &progressWriter{w: tmp, stage: stage, total: total, report: progress}
	if _, err := io.Copy(w, resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if total > 0 && w.done != total {
		return fmt.Errorf("%s: got %d bytes, expected %d", stage, w.done, total)
	}
	return os.Rename(name, dst)
}

// progressWriter counts bytes and reports them, throttled to whole percents so
// a 650 MB download does not generate a callback per 32 KB chunk.
type progressWriter struct {
	w      io.Writer
	stage  string
	total  int64
	done   int64
	lastPc int
	report Progress
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if p.report != nil {
		pc := -1
		if p.total > 0 {
			pc = int(p.done * 100 / p.total)
		}
		if pc != p.lastPc {
			p.lastPc = pc
			p.report(p.stage, p.done, p.total)
		}
	}
	return n, err
}

// unzip extracts src into dir, refusing entries that would escape it.
func unzip(src, dir string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()

	for _, f := range zr.File {
		// Reject path traversal ("../") in archive entries: this archive comes
		// off the network, so its names are not trusted.
		target := filepath.Join(dir, filepath.FromSlash(f.Name)) //nolint:gosec
		if !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry escapes the archive: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeZipEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	// Bound the copy so a crafted archive cannot fill the disk.
	const maxEntry = 2 << 30 // 2 GiB
	n, err := io.Copy(out, io.LimitReader(rc, maxEntry))
	if err != nil {
		return err
	}
	if n == maxEntry {
		return fmt.Errorf("zip entry %s is implausibly large", f.Name)
	}
	return nil
}

// findExeDir returns the directory holding the whisper executable.
func findExeDir(root string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return err
		}
		if exeName(path) {
			found = filepath.Dir(path)
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", errors.New("no whisper-cli executable inside the archive")
	}
	return found, nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// humanSize renders a byte count for the UI.
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
