package deps

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/clementherve/godio/internal/lang"
)

// GPU describes the NVIDIA GPU found on this machine, if any.
type GPU struct {
	Present bool
	Name    string
	Driver  float64 // e.g. 566.36 -> 566.36; 0 when unknown
}

// String renders the GPU for doctor output.
func (g GPU) String() string {
	if !g.Present {
		return lang.T().Err.NoGPU
	}
	if g.Driver > 0 {
		return g.Name + " (pilote " + strconv.FormatFloat(g.Driver, 'f', -1, 64) + ")"
	}
	return g.Name
}

// SupportsCUDA124 reports whether the driver is new enough for the CUDA 12.4
// build. Below driver 550 the 11.8 build is the right choice.
func (g GPU) SupportsCUDA124() bool {
	return g.Present && (g.Driver == 0 || g.Driver >= 550)
}

// DetectGPU looks for an NVIDIA GPU via nvidia-smi, which ships with the
// driver. Its absence is the signal that there is no usable NVIDIA GPU — far
// more reliable than guessing from a device list, since what actually matters
// is whether the CUDA runtime will load.
func DetectGPU() GPU {
	exe, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return GPU{}
	}

	// nvidia-smi can hang on a wedged driver; it is not worth blocking setup.
	done := make(chan []byte, 1)
	cmd := exec.Command(exe, "--query-gpu=name,driver_version", "--format=csv,noheader")
	go func() {
		out, err := cmd.Output()
		if err != nil {
			done <- nil
			return
		}
		done <- out
	}()

	var out []byte
	select {
	case out = <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		return GPU{}
	}
	if out == nil {
		return GPU{}
	}

	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		g := GPU{Present: true, Name: strings.TrimSpace(parts[0])}
		if len(parts) == 2 {
			g.Driver = parseDriver(strings.TrimSpace(parts[1]))
		}
		return g // first GPU is the one CUDA will use by default
	}
	return GPU{}
}

// parseDriver turns "566.36" or "572.83.01" into a comparable major.minor float.
func parseDriver(s string) float64 {
	fields := strings.Split(s, ".")
	if len(fields) > 2 {
		s = fields[0] + "." + fields[1]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
