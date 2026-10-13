package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Progress reports download progress for one file. It has the same shape as
// deps.Progress, so the tray and CLI reporting code is shared with the
// dependency installer. total is -1 when the server does not say.
type Progress func(stage string, done, total int64)

// oldSuffix marks the binary a successful update displaced. A running
// executable cannot be deleted on Windows, only renamed, so the previous
// version lingers under this name until the next launch sweeps it.
const oldSuffix = ".old"

// Available is the result of a version check.
type Available struct {
	// Current is the running version, "dev" for a local build.
	Current string
	// Latest is the newest published tag.
	Latest string
	// Newer reports whether Latest is ahead of Current. A dev build is never
	// behind: there is nothing to compare it to.
	Newer  bool
	Assets []Asset
	// Size totals the download.
	Size int64
}

// HumanSize renders the download size for the UI.
func (a Available) HumanSize() string { return humanSize(a.Size) }

// Check asks GitHub what the newest release is.
func Check(ctx context.Context, current string, prerelease bool) (Available, error) {
	rel, err := Latest(ctx, prerelease)
	if err != nil {
		return Available{}, err
	}

	a := Available{Current: current, Latest: rel.Tag}
	// Assets are resolved even on a platform with no published binaries, so
	// the error says "no windows binaries" at install time rather than during
	// a check that only wanted to report a version.
	if assets, err := Binaries(rel); err == nil {
		a.Assets = assets
		for _, as := range assets {
			a.Size += as.Size
		}
	}

	cur, okCur := ParseVersion(current)
	next, okNext := ParseVersion(rel.Tag)
	a.Newer = okCur && okNext && cur.Less(next)
	return a, nil
}

// Applied describes a completed update.
type Applied struct {
	From, To string
	// Replaced names the binaries that were swapped.
	Replaced []string
}

// Apply installs a release over the binaries in dir.
//
// It runs in two phases, because a half-updated pair of binaries — a new
// godio.exe beside an old godio-tray.exe — is the one outcome worth
// engineering against. Everything is downloaded and verified first; only then
// does anything on disk move.
func Apply(ctx context.Context, a Available, dir string, p Progress) (Applied, error) {
	targets, err := targetsIn(dir, a.Assets)
	if err != nil {
		return Applied{}, err
	}

	staging, err := os.MkdirTemp(dir, ".godio-update-*")
	if err != nil {
		return Applied{}, fmt.Errorf("%w (dossier %s)", err, dir)
	}
	defer os.RemoveAll(staging)

	staged := map[string]string{}
	for _, as := range targets {
		dst := filepath.Join(staging, as.Name)
		if err := download(ctx, as, dst, p); err != nil {
			return Applied{}, err
		}
		staged[as.Name] = dst
	}

	replaced, err := commit(dir, targets, staged)
	if err != nil {
		return Applied{}, err
	}
	return Applied{From: a.Current, To: a.Latest, Replaced: replaced}, nil
}

// targetsIn keeps the assets that correspond to a binary actually installed in
// dir. Someone who deleted godio-tray.exe does not get it back from an update.
func targetsIn(dir string, assets []Asset) ([]Asset, error) {
	if len(assets) == 0 {
		return nil, fmt.Errorf("aucun binaire publié pour %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	var out []Asset
	for _, a := range assets {
		if _, err := os.Stat(filepath.Join(dir, a.Name)); err == nil {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("aucun binaire godio trouvé dans %s", dir)
	}
	return out, nil
}

// commit swaps the staged binaries into place, rolling back everything on the
// first failure. It returns the names replaced.
//
// Renaming a *running* executable is legal on Windows — deleting one is not —
// which is what lets this run while the tray is up.
func commit(dir string, targets []Asset, staged map[string]string) ([]string, error) {
	var done []string

	rollback := func() {
		for i := len(done) - 1; i >= 0; i-- {
			target := filepath.Join(dir, done[i])
			_ = os.Remove(target)
			_ = os.Rename(target+oldSuffix, target)
		}
	}

	for _, a := range targets {
		target := filepath.Join(dir, a.Name)
		old := target + oldSuffix

		// A leftover .old from a previous update, which could not be deleted
		// while that process was running.
		_ = os.Remove(old)

		if err := os.Rename(target, old); err != nil {
			rollback()
			return nil, writeError(dir, err)
		}
		if err := os.Rename(staged[a.Name], target); err != nil {
			_ = os.Rename(old, target) // put this one back before the rest
			rollback()
			return nil, writeError(dir, err)
		}
		done = append(done, a.Name)
		// The displaced binary is deliberately left on disk: it is the only
		// way back if a later swap in this same loop fails, and the running
		// one could not be deleted anyway. CleanupOld sweeps it at the next
		// launch.
	}
	return done, nil
}

// writeError explains the failure people actually hit: godio installed
// somewhere the user cannot write, typically Program Files.
func writeError(dir string, err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("écriture refusée dans %s : déplacez godio dans un dossier qui vous appartient, "+
			"ou relancez la commande en administrateur (%w)", dir, err)
	}
	return err
}

// CleanupOld removes the binaries a previous update displaced. It is called at
// startup by both commands, since a running executable cannot delete itself.
func CleanupOld(dir string) {
	for _, name := range []string{"godio.exe", "godio-tray.exe", "godio", "godio-tray"} {
		_ = os.Remove(filepath.Join(dir, name+oldSuffix))
	}
}

// InstallDir returns the directory holding the running binary, which is where
// its sibling lives too.
func InstallDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// download streams one asset to dst and verifies it against the digest GitHub
// published for it.
//
// The hash is computed while the bytes stream past rather than by re-reading
// the file, and the file is only ever written inside the staging directory, so
// a failed verification leaves nothing to clean up outside it.
func download(ctx context.Context, a Asset, dst string, p Progress) error {
	want, err := wantedSum(a)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "godio")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("téléchargement de %s : %w", a.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotPublic
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("téléchargement de %s : %s", a.Name, resp.Status)
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	sum := sha256.New()
	w := &progressWriter{w: io.MultiWriter(out, sum), stage: a.Name, total: resp.ContentLength, report: p}
	_, copyErr := io.Copy(w, resp.Body)
	if err := out.Close(); err != nil && copyErr == nil {
		copyErr = err
	}
	if copyErr != nil {
		return fmt.Errorf("téléchargement de %s : %w", a.Name, copyErr)
	}

	if got := hex.EncodeToString(sum.Sum(nil)); got != want {
		return fmt.Errorf("%s : empreinte incorrecte (attendu %s, obtenu %s) — téléchargement corrompu, rien n'a été remplacé",
			a.Name, want[:12], got[:12])
	}
	return nil
}

// wantedSum extracts the expected SHA-256 from an asset.
//
// An asset without a digest is refused rather than trusted: this is an
// executable that is about to replace the running one, and "the server did not
// tell us" is not a reason to skip the check.
func wantedSum(a Asset) (string, error) {
	digest := strings.TrimSpace(strings.ToLower(a.Digest))
	hexsum, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || len(hexsum) != 64 {
		return "", fmt.Errorf("%s : GitHub ne publie pas d'empreinte sha256 pour ce fichier, installation refusée", a.Name)
	}
	if _, err := hex.DecodeString(hexsum); err != nil {
		return "", fmt.Errorf("%s : empreinte publiée illisible (%q)", a.Name, a.Digest)
	}
	return hexsum, nil
}

// progressWriter counts bytes and reports them, throttled to whole percents.
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
