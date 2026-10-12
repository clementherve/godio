package deps

import (
	"os"
	"path/filepath"
	"strings"
)

// InstalledBackend reports which whisper.cpp build is sitting in dir/whisper,
// and whether one is installed at all.
//
// This matters because the backend is a property of the files on disk, not of
// the configuration: a whisper directory installed by an older godio (or by the
// pre-1.0 fetch-deps.ps1, which defaulted to the CPU build) keeps running on
// the CPU no matter what whisper.backend says afterwards.
//
// The build is told apart by the CUDA runtime libraries the archive ships
// beside the executable: the CPU zip carries none of them, and the CUDA builds
// will not start without them.
func InstalledBackend(dir string) (Backend, bool) {
	whisper := filepath.Join(dir, "whisper")
	if !exists(filepath.Join(whisper, "whisper-cli.exe")) &&
		!exists(filepath.Join(whisper, "whisper-cli")) {
		return "", false
	}

	entries, err := os.ReadDir(whisper)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if isCUDALib(e.Name()) {
			return BackendCUDA, true
		}
	}
	return BackendCPU, true
}

// isCUDALib reports whether a filename is one of the CUDA libraries shipped
// alongside whisper-cli in the CUDA builds: ggml-cuda.dll plus the cuBLAS and
// CUDA runtime redistributables. Substrings, not exact names, since the CUDA
// ones carry their version ("cudart64_12.dll") and a Linux build prefixes
// "lib".
func isCUDALib(name string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{"ggml-cuda", "cudart", "cublas"} {
		if strings.Contains(n, p) {
			return true
		}
	}
	return false
}
