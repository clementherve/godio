package deps

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Shaped after the real whisper.cpp release list: the semver tags carry release
// notes and no assets, the b#### build tags carry the binaries.
func fixtureReleases() []Release {
	return []Release{
		{Tag: "v1.9.5"}, // no assets — the trap
		{Tag: "b5454", Assets: []Asset{
			{Name: "whisper-bin-ubuntu-x64.tar.gz", URL: "u/linux"},
			{Name: "whisper-bin-win-cuda-12.4.0-x64.zip", Size: 653 << 20, URL: "u/cuda124"},
			{Name: "whisper-bin-win-cuda-11.8.0-x64.zip", Size: 271 << 20, URL: "u/cuda118"},
			{Name: "whisper-bin-x64.zip", Size: 8 << 20, URL: "u/cpu"},
		}},
		{Tag: "v1.9.4"},
		{Tag: "b5130", Assets: []Asset{{Name: "whisper-bin-x64.zip", URL: "u/older"}}},
	}
}

func TestPickAssetSkipsAssetlessSemverTags(t *testing.T) {
	rel, asset, err := PickAsset(fixtureReleases(), patternCPU)
	if err != nil {
		t.Fatalf("PickAsset: %v", err)
	}
	if rel.Tag != "b5454" {
		t.Errorf("tag = %q, want b5454 (v1.9.5 has no assets and must be skipped)", rel.Tag)
	}
	if asset.URL != "u/cpu" {
		t.Errorf("url = %q, want u/cpu", asset.URL)
	}
}

func TestPickAssetCPUPatternIsNotFooledByCUDA(t *testing.T) {
	// "whisper-bin-x64.zip" must not match the cuda or arm64 names.
	_, asset, err := PickAsset(fixtureReleases(), patternCPU)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(asset.Name, "cuda") || strings.Contains(asset.Name, "arm64") {
		t.Errorf("CPU pattern matched %q", asset.Name)
	}
}

func TestPickAssetNoMatch(t *testing.T) {
	if _, _, err := PickAsset([]Release{{Tag: "v1.0.0"}}, patternCUDA124); err == nil {
		t.Error("PickAsset with no matching asset = nil error, want error")
	}
}

func TestAssetPatternPicksCUDAByDriver(t *testing.T) {
	for _, tc := range []struct {
		backend Backend
		driver  float64
		want    string
	}{
		{BackendCUDA, 566.36, "u/cuda124"},
		{BackendCUDA, 0, "u/cuda124"},      // unknown driver: assume recent
		{BackendCUDA, 537.58, "u/cuda118"}, // too old for 12.4
		{BackendCPU, 566.36, "u/cpu"},
	} {
		_, asset, err := PickAsset(fixtureReleases(), assetPattern(tc.backend, tc.driver))
		if err != nil {
			t.Fatalf("backend=%s driver=%v: %v", tc.backend, tc.driver, err)
		}
		if asset.URL != tc.want {
			t.Errorf("backend=%s driver=%v -> %s, want %s", tc.backend, tc.driver, asset.URL, tc.want)
		}
	}
}

func TestSpeechModelQuantisesOnlyForCPU(t *testing.T) {
	if got := SpeechModel(BackendCPU).Name; !strings.Contains(got, "q5_0") {
		t.Errorf("CPU model = %q, want a quantised one", got)
	}
	if got := SpeechModel(BackendCUDA).Name; strings.Contains(got, "q5_0") {
		t.Errorf("CUDA model = %q, want the full model (VRAM is not the bottleneck)", got)
	}
}

func TestParseDriver(t *testing.T) {
	for in, want := range map[string]float64{
		"566.36":    566.36,
		"572.83.01": 572.83, // three-component Linux-style versions
		"":          0,
		"garbage":   0,
	} {
		if got := parseDriver(in); got != want {
			t.Errorf("parseDriver(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestGPUSupportsCUDA124(t *testing.T) {
	cases := []struct {
		gpu  GPU
		want bool
	}{
		{GPU{Present: true, Driver: 566}, true},
		{GPU{Present: true, Driver: 550}, true},
		{GPU{Present: true, Driver: 537}, false},
		{GPU{Present: true, Driver: 0}, true},
		{GPU{}, false},
	}
	for _, c := range cases {
		if got := c.gpu.SupportsCUDA124(); got != c.want {
			t.Errorf("%+v.SupportsCUDA124() = %v, want %v", c.gpu, got, c.want)
		}
	}
}

// makeZip builds a zip from a name -> content map.
func makeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnzipRejectsPathTraversal(t *testing.T) {
	// The archive comes off the network, so a crafted entry name must not be
	// able to write outside the destination.
	src := makeZip(t, map[string]string{"../escaped.txt": "nope"})
	dst := t.TempDir()

	if err := unzip(src, dst); err == nil {
		t.Fatal("unzip accepted a path-traversal entry, want error")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "escaped.txt")); err == nil {
		t.Error("path-traversal entry was written outside the destination")
	}
}

func TestUnzipAndFindExeDir(t *testing.T) {
	src := makeZip(t, map[string]string{
		"Release/whisper-cli.exe": "binary",
		"Release/cublas64_12.dll": "dll",
		"readme.txt":              "doc",
	})
	dst := t.TempDir()

	if err := unzip(src, dst); err != nil {
		t.Fatalf("unzip: %v", err)
	}
	dir, err := findExeDir(dst)
	if err != nil {
		t.Fatalf("findExeDir: %v", err)
	}
	if filepath.Base(dir) != "Release" {
		t.Errorf("findExeDir = %q, want the Release/ subdirectory", dir)
	}
	// The DLLs must come along: the CUDA build will not start without them.
	if _, err := os.Stat(filepath.Join(dir, "cublas64_12.dll")); err != nil {
		t.Errorf("DLL beside the exe was not extracted: %v", err)
	}
}

func TestFindExeDirNoExecutable(t *testing.T) {
	src := makeZip(t, map[string]string{"readme.txt": "doc"})
	dst := t.TempDir()
	if err := unzip(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := findExeDir(dst); err == nil {
		t.Error("findExeDir on an archive with no exe = nil, want error")
	}
}

func TestHumanSize(t *testing.T) {
	for in, want := range map[int64]string{
		653 << 20: "653 Mo",
		2 << 30:   "2.0 Go",
		512:       "512 o",
	} {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
}
