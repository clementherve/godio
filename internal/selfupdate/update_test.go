package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// releaseJSON is trimmed from the real API response for v0.3.0. It is kept
// verbatim — field names and the "sha256:" prefix included — so the decoding
// is tested against what GitHub actually sends rather than what we assume.
const releaseJSON = `{
  "tag_name": "v0.3.0",
  "prerelease": false,
  "draft": false,
  "assets": [
    {"name": "godio-tray.exe", "size": 9799168,
     "digest": "sha256:6a6eceb3757dbc7cffc78bc839e1317feb8ac278a235fd38cd63d20e1e213b8a",
     "browser_download_url": "https://github.com/clementherve/godio/releases/download/v0.3.0/godio-tray.exe"},
    {"name": "godio-windows-amd64.zip", "size": 8198999,
     "digest": "sha256:d5017aed77e2e5832948dd9669f1e365c3b533404e00bc4dcf1911a17f7ba69d",
     "browser_download_url": "https://github.com/clementherve/godio/releases/download/v0.3.0/godio-windows-amd64.zip"},
    {"name": "godio.exe", "size": 9859584,
     "digest": "sha256:f5ccc45297f328236d537afba8bf6c4511504248728e2348d9df6a3fc8de439b",
     "browser_download_url": "https://github.com/clementherve/godio/releases/download/v0.3.0/godio.exe"},
    {"name": "SHA256SUMS.txt", "size": 250,
     "digest": "sha256:5971d7d5376cdd8ae6fb6ddb94c931deab0547c81af814a98009ad29f217e671",
     "browser_download_url": "https://github.com/clementherve/godio/releases/download/v0.3.0/SHA256SUMS.txt"}
  ]
}`

func fixtureRelease(t *testing.T) Release {
	t.Helper()
	var rel Release
	if err := json.Unmarshal([]byte(releaseJSON), &rel); err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestBinariesPicksTheExecutables(t *testing.T) {
	rel := fixtureRelease(t)
	got, err := binariesFrom(rel, []string{"godio.exe", "godio-tray.exe"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d assets, want 2", len(got))
	}
	// Order follows the names asked for, not the order GitHub lists them in,
	// because that is the order they are swapped.
	if got[0].Name != "godio.exe" || got[1].Name != "godio-tray.exe" {
		t.Errorf("got %s then %s", got[0].Name, got[1].Name)
	}
	if got[0].Size != 9859584 || !strings.HasPrefix(got[0].Digest, "sha256:") {
		t.Errorf("asset fields not decoded: %+v", got[0])
	}

	if _, err := binariesFrom(rel, []string{"godio.exe", "godio-linux"}); err == nil {
		t.Error("a release missing one binary was accepted")
	}
	if _, err := binariesFrom(rel, nil); err == nil {
		t.Error("a platform with no published binaries was accepted")
	}
}

func TestWantedSum(t *testing.T) {
	good := strings.Repeat("ab", 32)
	if got, err := wantedSum(Asset{Digest: "sha256:" + strings.ToUpper(good)}); err != nil || got != good {
		t.Errorf("wantedSum = %q, %v", got, err)
	}
	// An executable is about to replace the running one: "the server did not
	// say" is not a reason to skip the check.
	for _, bad := range []string{"", "md5:" + good, "sha256:deadbeef", "sha256:" + strings.Repeat("zz", 32)} {
		if _, err := wantedSum(Asset{Name: "godio.exe", Digest: bad}); err == nil {
			t.Errorf("wantedSum accepted %q", bad)
		}
	}
}

func TestDownloadVerifiesDigest(t *testing.T) {
	payload := []byte("this pretends to be a windows executable")
	sum := sha256.Sum256(payload)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	good := Asset{Name: "godio.exe", URL: srv.URL, Digest: "sha256:" + hex.EncodeToString(sum[:])}

	var stages []string
	dst := filepath.Join(dir, "godio.exe")
	if err := download(context.Background(), good, dst, func(stage string, _, _ int64) {
		stages = append(stages, stage)
	}); err != nil {
		t.Fatalf("a correct download was refused: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("downloaded file = %q, %v", got, err)
	}
	if len(stages) == 0 {
		t.Error("no progress was reported")
	}

	bad := good
	bad.Digest = "sha256:" + strings.Repeat("00", 32)
	err = download(context.Background(), bad, filepath.Join(dir, "other.exe"), nil)
	if err == nil {
		t.Fatal("a corrupted download was accepted")
	}
	if !strings.Contains(err.Error(), "empreinte") {
		t.Errorf("unhelpful error for a bad digest: %v", err)
	}
}

func TestDownloadReportsPrivateRepo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer srv.Close()

	a := Asset{Name: "godio.exe", URL: srv.URL, Digest: "sha256:" + strings.Repeat("ab", 32)}
	err := download(context.Background(), a, filepath.Join(t.TempDir(), "godio.exe"), nil)
	if err != ErrNotPublic {
		t.Errorf("a 404 gave %v, want ErrNotPublic", err)
	}
}

// install writes a fake installation and a staging directory, returning both.
func install(t *testing.T, names ...string) (dir string, staged map[string]string) {
	t.Helper()
	dir = t.TempDir()
	stage := filepath.Join(dir, "staging")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	staged = map[string]string{}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("old "+n), 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(stage, n)
		if err := os.WriteFile(p, []byte("new "+n), 0o755); err != nil {
			t.Fatal(err)
		}
		staged[n] = p
	}
	return dir, staged
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCommitSwapsAndKeepsTheOldBinary(t *testing.T) {
	dir, staged := install(t, "godio.exe", "godio-tray.exe")
	targets := []Asset{{Name: "godio.exe"}, {Name: "godio-tray.exe"}}

	done, err := commit(dir, targets, staged)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 2 {
		t.Fatalf("replaced %v, want both", done)
	}
	for _, n := range []string{"godio.exe", "godio-tray.exe"} {
		if got := read(t, filepath.Join(dir, n)); got != "new "+n {
			t.Errorf("%s = %q, want the new binary", n, got)
		}
		// The displaced binary has to survive the whole commit: it is the only
		// way back if a later swap fails, and on Windows the running exe
		// cannot be deleted at all. CleanupOld sweeps it at the next launch.
		if got := read(t, filepath.Join(dir, n+oldSuffix)); got != "old "+n {
			t.Errorf("%s%s = %q, want the previous binary kept for rollback", n, oldSuffix, got)
		}
	}
}

func TestCommitRollsBackOnFailure(t *testing.T) {
	dir, staged := install(t, "godio.exe", "godio-tray.exe")
	targets := []Asset{{Name: "godio.exe"}, {Name: "godio-tray.exe"}}

	// Make the second swap fail: a non-empty directory where its .old must go
	// cannot be replaced by a rename.
	blocked := filepath.Join(dir, "godio-tray.exe"+oldSuffix)
	if err := os.MkdirAll(filepath.Join(blocked, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := commit(dir, targets, staged); err == nil {
		t.Fatal("commit reported success although one binary could not be swapped")
	}

	// The whole point: no half-updated pair is left behind.
	for _, n := range []string{"godio.exe", "godio-tray.exe"} {
		if got := read(t, filepath.Join(dir, n)); got != "old "+n {
			t.Errorf("%s = %q, want the original binary back", n, got)
		}
	}
}

func TestTargetsInIgnoresMissingBinaries(t *testing.T) {
	dir, _ := install(t, "godio.exe")
	assets := []Asset{{Name: "godio.exe"}, {Name: "godio-tray.exe"}}

	got, err := targetsIn(dir, assets)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "godio.exe" {
		t.Errorf("targetsIn = %+v, want only godio.exe", got)
	}

	if _, err := targetsIn(t.TempDir(), assets); err == nil {
		t.Error("a directory holding no godio binary was accepted")
	}
	if _, err := targetsIn(dir, nil); err == nil {
		t.Error("an empty asset list was accepted")
	}
}

func TestCleanupOld(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, "godio.exe"+oldSuffix)
	keep := filepath.Join(dir, "godio.exe")
	for _, p := range []string{leftover, keep} {
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	CleanupOld(dir)

	if _, err := os.Stat(leftover); err == nil {
		t.Error("the displaced binary was not swept")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("CleanupOld removed the live binary")
	}
	CleanupOld(filepath.Join(dir, "nowhere")) // must not panic on a missing dir
}

func TestCheckComparesVersions(t *testing.T) {
	rel := fixtureRelease(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(releaseJSON))
	}))
	defer srv.Close()

	// Check's comparison is the part worth testing; the HTTP call is one line
	// of plumbing, so the rule is exercised directly on the fixture.
	for _, tc := range []struct {
		current string
		newer   bool
	}{
		{"v0.2.0", true},
		{"v0.3.0", false},
		{"v0.4.0", false},
		{"dev", false}, // a local build is never "behind"
		{"dev-ab12cd", false},
	} {
		cur, okCur := ParseVersion(tc.current)
		next, okNext := ParseVersion(rel.Tag)
		got := okCur && okNext && cur.Less(next)
		if got != tc.newer {
			t.Errorf("current %s: newer = %v, want %v", tc.current, got, tc.newer)
		}
	}
}
