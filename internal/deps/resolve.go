// Package deps installs godio's runtime dependencies: the whisper.cpp binary
// and the model files. It replaces what used to be a PowerShell script, so the
// app can set itself up without the user leaving the tray.
package deps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// Backend selects which whisper.cpp build to install.
type Backend string

// Supported backends.
const (
	// BackendAuto picks CUDA when an NVIDIA GPU is present, CPU otherwise.
	BackendAuto Backend = "auto"
	// BackendCUDA is the NVIDIA build: several times faster, ~650 MB.
	BackendCUDA Backend = "cuda"
	// BackendCPU is the portable build, ~9 MB.
	BackendCPU Backend = "cpu"
)

const releasesURL = "https://api.github.com/repos/ggml-org/whisper.cpp/releases?per_page=20"

// Release is the subset of a GitHub release we care about.
type Release struct {
	Tag    string  `json:"tag_name"`
	Assets []Asset `json:"assets"`
}

// Asset is one downloadable file on a release.
type Asset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}

// Asset name patterns per build. CUDA 12.4 needs a recent driver, so the 11.8
// build stays available as a fallback.
var (
	patternCPU     = regexp.MustCompile(`^whisper-bin-x64\.zip$`)
	patternCUDA124 = regexp.MustCompile(`^whisper-bin-win-cuda-12\.4.*-x64\.zip$`)
	patternCUDA118 = regexp.MustCompile(`^whisper-bin-win-cuda-11\.8.*-x64\.zip$`)
)

// assetPattern returns the asset name pattern for a resolved backend and driver.
func assetPattern(b Backend, driver float64) *regexp.Regexp {
	if b != BackendCUDA {
		return patternCPU
	}
	// CUDA 12.4 requires driver >= 550. Below that, the 11.8 build still works
	// and is a third of the size.
	if driver > 0 && driver < 550 {
		return patternCUDA118
	}
	return patternCUDA124
}

// FetchReleases lists whisper.cpp's recent releases.
func FetchReleases(ctx context.Context) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "godio")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing whisper.cpp releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing whisper.cpp releases: %s", resp.Status)
	}

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("parsing release list: %w", err)
	}
	return releases, nil
}

// PickAsset finds the newest release carrying an asset matching pattern.
//
// This cannot just take the latest release: whisper.cpp publishes its `v1.x.y`
// tags with release notes and **no assets at all**, and hangs the binaries off
// rolling `b####` build tags instead. Resolving by version number finds nothing
// to download.
func PickAsset(releases []Release, pattern *regexp.Regexp) (Release, Asset, error) {
	for _, rel := range releases {
		for _, a := range rel.Assets {
			if pattern.MatchString(a.Name) {
				return rel, a, nil
			}
		}
	}
	return Release{}, Asset{}, fmt.Errorf(
		"no whisper.cpp asset matching %s in the %d most recent releases",
		pattern, len(releases))
}

// Model describes a model file to fetch from Hugging Face.
type Model struct {
	Name string
	URL  string
}

const hfWhisper = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/"

// VADModel is Silero VAD, used to skip the stretches where a track is silent
// and to stop whisper hallucinating over them.
var VADModel = Model{
	Name: "ggml-silero-v5.1.2.bin",
	URL:  "https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin",
}

// SpeechModel returns the transcription model suited to a backend.
//
// Quantisation is a CPU optimisation, not a universal one: on CPU the q5_0
// turbo model is a third of the size and markedly faster, while on a GPU the
// full model fits in VRAM and weight bandwidth is not the bottleneck, so the
// quantised version would cost accuracy for nothing.
func SpeechModel(b Backend) Model {
	name := "ggml-large-v3-turbo.bin"
	if b == BackendCPU {
		name = "ggml-large-v3-turbo-q5_0.bin"
	}
	return Model{Name: name, URL: hfWhisper + name}
}

// Resolve turns "auto" into a concrete backend by looking for an NVIDIA GPU.
func Resolve(b Backend) (Backend, GPU) {
	gpu := DetectGPU()
	if b == BackendCUDA || b == BackendCPU {
		return b, gpu
	}
	if gpu.Present {
		return BackendCUDA, gpu
	}
	return BackendCPU, gpu
}

// exeName is the whisper binary's name inside the release zip.
func exeName(path string) bool {
	base := strings.ToLower(path)
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	return base == "whisper-cli.exe" || base == "whisper-cli" ||
		base == "main.exe" || base == "main"
}
