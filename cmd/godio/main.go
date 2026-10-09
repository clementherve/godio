// Command godio records a meeting, transcribes it locally, has Claude extract
// what matters, and appends the result to an Obsidian daily note.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/clementherve/godio/internal/audio"
	"github.com/clementherve/godio/internal/claudecli"
	"github.com/clementherve/godio/internal/config"
	"github.com/clementherve/godio/internal/deps"
	"github.com/clementherve/godio/internal/pipeline"
)

// version is stamped at build time by the CI workflow:
//
//	go build -ldflags "-X main.version=<tag>"
//
// A local `go build` leaves it as "dev".
var version = "dev"

const usage = `godio — notes de réunion automatiques

Usage:
  godio run                  lance l'icône de notification (console attachée ;
                             pour un usage normal lancez godio-tray.exe)
  godio devices              liste les périphériques audio
  godio doctor               vérifie la configuration et les dépendances
  godio record [-s N]        enregistre (N secondes, ou Ctrl+C)
  godio transcribe [session] transcrit les deux pistes
  godio extract [session]    génère les notes via Claude
  godio write [session]      ajoute les notes à la note du jour
  godio process [session]    transcribe + extract + write
  godio setup [--dry-run]    télécharge whisper.cpp et les modèles
  godio config [edit]        affiche ou ouvre la configuration
  godio version              affiche la version

[session] est un dossier de session ; par défaut, le plus récent.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}

	cmd, args := os.Args[1], os.Args[2:]
	if err := run(cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	switch cmd {
	case "run":
		return cmdRun()

	case "devices":
		return cmdDevices()
	case "doctor":
		return cmdDoctor()
	case "record":
		return cmdRecord(args)
	case "transcribe", "extract", "write", "process":
		return cmdStage(cmd, args)
	case "config":
		return cmdConfig(args)
	case "setup":
		return cmdSetup(args)
	case "version", "--version", "-v":
		fmt.Printf("godio %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return nil
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Print(usage)
		return fmt.Errorf("commande inconnue %q", cmd)
	}
}

// load reads the config, reporting clearly when it was just scaffolded.
func load() (config.Config, error) {
	cfg, created, err := config.Load()
	if err != nil {
		return cfg, err
	}
	if created {
		path, _ := config.Path()
		fmt.Fprintf(os.Stderr,
			"Configuration créée : %s\nRenseignez le chemin de votre coffre Obsidian, puis relancez.\n\n", path)
	}
	return cfg, nil
}

func cmdConfig(args []string) error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	if _, err := load(); err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "edit" {
		return openInEditor(path)
	}
	fmt.Println(path)
	fmt.Println()
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Print(string(raw))
	return nil
}

func cmdDevices() error {
	ctx, err := audio.NewContext()
	if err != nil {
		return err
	}
	defer ctx.Close()

	capture, err := ctx.CaptureDevices()
	if err != nil {
		return err
	}
	playback, err := ctx.PlaybackDevices()
	if err != nil {
		return err
	}

	fmt.Println("Entrées (micro) — audio.mic_device :")
	printDevices(capture)
	fmt.Println()
	fmt.Println("Sorties (capturées en loopback) — audio.loopback_device :")
	printDevices(playback)
	return nil
}

func printDevices(devs []audio.Device) {
	if len(devs) == 0 {
		fmt.Println("  (aucun)")
		return
	}
	for _, d := range devs {
		mark := " "
		if d.IsDefault {
			mark = "*"
		}
		fmt.Printf("  %s %s\n", mark, d.Name)
	}
}

func cmdDoctor() error {
	cfg, err := load()
	if err != nil {
		return err
	}

	ok := true
	report := func(label string, err error) {
		if err != nil {
			ok = false
			fmt.Printf("  ✗ %s : %v\n", label, err)
			return
		}
		fmt.Printf("  ✓ %s\n", label)
	}

	fmt.Println("Configuration :")
	report("config", cfg.Validate())

	backend, gpu := deps.Resolve(deps.Backend(cfg.Whisper.Backend))
	fmt.Println("Transcription :")
	fmt.Printf("  · GPU        : %s\n", gpu)
	fmt.Printf("  · backend    : %s (whisper.backend = %q)\n", backend, cfg.Whisper.Backend)
	fmt.Printf("  · préréglage : %s (%s)\n", cfg.Whisper.Preset,
		strings.Join(cfg.Whisper.Decoding(), " "))
	fmt.Printf("  · threads    : %d (coeurs physiques)\n", cfg.Whisper.Threads)
	if backend == deps.BackendCPU && gpu.Present {
		fmt.Println("  ! un GPU NVIDIA est présent mais whisper.backend force le CPU")
	}

	fmt.Println("Claude Code :")
	report("claude", claudecli.Runner{Executable: cfg.Claude.Executable}.Check())

	fmt.Println("Audio :")
	actx, err := audio.NewContext()
	if err != nil {
		report("backend audio", err)
	} else {
		defer actx.Close()
		capture, cerr := actx.CaptureDevices()
		report("périphériques d'entrée", cerr)
		if cerr == nil {
			_, e := audio.Find(capture, cfg.Audio.MicDevice)
			report(fmt.Sprintf("micro (%s)", orDefault(cfg.Audio.MicDevice)), e)
		}
		playback, perr := actx.PlaybackDevices()
		report("périphériques de sortie", perr)
		if perr == nil {
			_, e := audio.Find(playback, cfg.Audio.LoopbackDevice)
			report(fmt.Sprintf("sortie (%s)", orDefault(cfg.Audio.LoopbackDevice)), e)
		}
	}

	if !ok {
		return errors.New("configuration incomplète — lancez « godio setup » pour installer les dépendances")
	}
	fmt.Println("\nTout est prêt.")
	return nil
}

// cmdSetup downloads whisper.cpp and the models.
func cmdSetup(args []string) error {
	dryRun := false
	for _, a := range args {
		if a == "-n" || a == "--dry-run" {
			dryRun = true
		}
	}

	cfg, err := load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root := filepath.Dir(filepath.Dir(cfg.Resolve(cfg.Whisper.Exe)))

	fmt.Println("Recherche de la dernière version de whisper.cpp…")
	plan, err := deps.MakePlan(ctx, root, deps.Backend(cfg.Whisper.Backend))
	if err != nil {
		return err
	}

	fmt.Printf("  GPU      : %s\n", plan.GPU)
	fmt.Printf("  backend  : %s\n", plan.Backend)
	fmt.Printf("  version  : %s\n", plan.Release)
	if !plan.NeedsDownload() {
		fmt.Println("\nTout est déjà installé.")
		return nil
	}
	fmt.Println("  à télécharger :")
	for _, m := range plan.Missing {
		fmt.Printf("    - %s\n", m)
	}
	fmt.Println()

	if dryRun {
		fmt.Println("(--dry-run : rien n'a été téléchargé)")
		return nil
	}

	last := ""
	if err := deps.Install(ctx, root, plan, func(stage string, done, total int64) {
		if stage != last {
			if last != "" {
				fmt.Println()
			}
			last = stage
		}
		if total > 0 {
			fmt.Printf("\r  %-28s %s %3d%%", stage, bar(int(done*100/total)), done*100/total)
			return
		}
		fmt.Printf("\r  %-28s %d Mo", stage, done/(1<<20))
	}); err != nil {
		fmt.Println()
		return err
	}
	fmt.Println()

	// Record what was actually installed; the model name depends on the backend.
	cfg.Whisper.Model = filepath.Join("models", plan.Speech.Name)
	cfg.Whisper.VADModel = filepath.Join("models", plan.VAD.Name)
	if err := cfg.Save(); err != nil {
		return err
	}

	fmt.Println("\nInstallation terminée. Vérifiez avec « godio doctor ».")
	return nil
}

// bar renders a 0..100 percentage as a fixed-width progress bar.
func bar(pc int) string {
	const width = 24
	n := pc * width / 100
	if n < 0 {
		n = 0
	}
	if n > width {
		n = width
	}
	return "[" + strings.Repeat("#", n) + strings.Repeat(".", width-n) + "]"
}

// openInEditor opens a file in the user's default editor.
func openInEditor(path string) error {
	if ed := os.Getenv("EDITOR"); ed != "" {
		cmd := exec.Command(ed, path)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

func orDefault(s string) string {
	if strings.TrimSpace(s) == "" {
		return "par défaut"
	}
	return s
}

func cmdRecord(args []string) error {
	seconds := 0
	for i := 0; i < len(args); i++ {
		if args[i] == "-s" || args[i] == "--seconds" {
			if i+1 >= len(args) {
				return errors.New("-s attend un nombre de secondes")
			}
			if _, err := fmt.Sscanf(args[i+1], "%d", &seconds); err != nil {
				return fmt.Errorf("durée invalide %q", args[i+1])
			}
			i++
		}
	}

	cfg, err := load()
	if err != nil {
		return err
	}

	rec, sess, err := startRecording(cfg)
	if err != nil {
		return err
	}

	fmt.Printf("Enregistrement dans %s\n", sess.Dir)
	fmt.Println("Ctrl+C pour arrêter.")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if seconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
		defer cancel()
	}

	// A live level readout turns the most likely failure — loopback pointed at
	// the wrong output, so the other side records silence — into something
	// visible within seconds instead of after the meeting.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-ticker.C:
			l := rec.Levels()
			fmt.Printf("\r  moi %s  |  eux %s   ",
				meter(l[audio.TrackMe]), meter(l[audio.TrackThem]))
		}
	}
	fmt.Println()

	res, err := rec.Stop()
	if err != nil {
		return err
	}
	sess.RecordResult(res)
	if err := sess.Save(); err != nil {
		return err
	}

	for _, t := range []audio.Track{audio.TrackMe, audio.TrackThem} {
		tr := res.Tracks[t]
		fmt.Printf("  %-5s %6.1f s  %s\n", t, tr.Seconds, tr.Path)
	}
	fmt.Printf("\nSession : %s\n", sess.Dir)
	fmt.Printf("Suite   : godio process %s\n", sess.Dir)
	return nil
}

// meter renders one track's level as a short bar plus its dBFS value.
func meter(l audio.TrackLevel) string {
	const width = 20
	// Map -60..0 dBFS onto the bar.
	n := int((l.DBFS + 60) / 60 * width)
	if n < 0 {
		n = 0
	}
	if n > width {
		n = width
	}
	state := "silence"
	if l.Active {
		state = "actif  "
	}
	return fmt.Sprintf("[%s%s] %6.1f dB %s",
		strings.Repeat("#", n), strings.Repeat(".", width-n), l.DBFS, state)
}

// startRecording resolves devices from config and starts a new session.
func startRecording(cfg config.Config) (*audio.Recorder, *pipeline.Session, error) {
	actx, err := audio.NewContext()
	if err != nil {
		return nil, nil, err
	}

	capture, err := actx.CaptureDevices()
	if err != nil {
		actx.Close()
		return nil, nil, err
	}
	mic, err := audio.Find(capture, cfg.Audio.MicDevice)
	if err != nil {
		actx.Close()
		return nil, nil, fmt.Errorf("micro : %w", err)
	}

	playback, err := actx.PlaybackDevices()
	if err != nil {
		actx.Close()
		return nil, nil, err
	}
	out, err := audio.Find(playback, cfg.Audio.LoopbackDevice)
	if err != nil {
		actx.Close()
		return nil, nil, fmt.Errorf("sortie : %w", err)
	}

	root, err := config.SessionsDir()
	if err != nil {
		actx.Close()
		return nil, nil, err
	}
	started := time.Now()
	sess, err := pipeline.NewSession(root, started)
	if err != nil {
		actx.Close()
		return nil, nil, err
	}
	sess.Meta.MicDevice = mic.Name
	sess.Meta.OutputDevice = out.Name

	rec := audio.NewRecorder(actx, sess.Dir)
	err = rec.Start(audio.Options{
		MicDevice:      mic,
		PlaybackDevice: out,
		SilenceDBFS:    cfg.Audio.SilenceDBFS,
		ActivityWindow: cfg.Audio.ActivityWindow.Duration,
	})
	if err != nil {
		actx.Close()
		// A failed start leaves nothing worth keeping, and empty session
		// folders would otherwise pile up on every misconfigured attempt.
		os.RemoveAll(sess.Dir)
		return nil, nil, err
	}
	return rec, sess, nil
}

func cmdStage(stage string, args []string) error {
	cfg, err := load()
	if err != nil {
		return err
	}

	sess, err := resolveSession(args)
	if err != nil {
		return err
	}
	fmt.Printf("Session : %s\n", sess.Dir)

	p := pipeline.Pipeline{
		Cfg: cfg,
		Log: func(s string) { fmt.Println(s) },
		OnProgress: func(stage string, pc int) {
			fmt.Printf("\r  %-16s %s %3d%%", stage, bar(pc), pc)
			if pc >= 100 {
				fmt.Println()
			}
		},
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch stage {
	case "transcribe":
		text, err := p.Transcribe(ctx, sess)
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Print(text)
	case "extract":
		notes, err := p.Extract(ctx, sess)
		if err != nil {
			return err
		}
		fmt.Println()
		fmt.Println(notes)
	case "write":
		_, err := p.Write(sess)
		return err
	case "process":
		res, err := p.Process(ctx, sess)
		if err != nil {
			return err
		}
		fmt.Printf("\n%s\n%d action(s) · %s\n",
			res.NotePath, res.Actions, res.Elapsed.Round(time.Second))
	}
	return nil
}

// resolveSession takes an explicit session folder, or the most recent one.
func resolveSession(args []string) (*pipeline.Session, error) {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return pipeline.OpenSession(a)
		}
	}
	root, err := config.SessionsDir()
	if err != nil {
		return nil, err
	}
	return pipeline.Latest(root)
}
