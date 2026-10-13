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

	"github.com/urfave/cli/v3"

	"github.com/clementherve/godio/internal/assets"
	"github.com/clementherve/godio/internal/audio"
	"github.com/clementherve/godio/internal/claudecli"
	"github.com/clementherve/godio/internal/config"
	"github.com/clementherve/godio/internal/deps"
	"github.com/clementherve/godio/internal/pipeline"
	"github.com/clementherve/godio/internal/selfupdate"
)

// version is stamped at build time by the CI workflow:
//
//	go build -ldflags "-X main.version=<tag>"
//
// A local `go build` leaves it as "dev".
var version = "dev"

func main() {
	// One interrupt context for the whole process: every long-running command
	// (recording, transcription, a 650 MB download) takes it, so Ctrl+C stops
	// the work in progress rather than killing the binary mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// A previous update left the binary it replaced behind, because a running
	// executable cannot delete itself. This launch is the first chance to.
	if dir, err := selfupdate.InstallDir(); err == nil {
		selfupdate.CleanupOld(dir)
	}

	if err := newApp().Run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		stop()
		os.Exit(1)
	}
}

// newApp builds the command tree.
//
// The help text is the user manual, so the usage strings stay in French like
// the rest of the interface.
func newApp() *cli.Command {
	app := &cli.Command{
		Name:                  "godio",
		Usage:                 "notes de réunion automatiques",
		Version:               version,
		Description:           "[session] est un dossier de session ; par défaut, le plus récent.",
		Suggest:               true, // "godio doctr" should point at "doctor"
		EnableShellCompletion: true,
		// `godio` on its own is a usage error, not a successful run: it has
		// always printed the command list and exited 2, and a script that tests
		// the exit code should keep seeing that.
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := cli.ShowAppHelp(cmd); err != nil {
				return err
			}
			return cli.Exit("", 2)
		},
		// Without this, an unknown command lands in urfave's help-topic path,
		// which answers in English in an otherwise French interface and exits
		// 3. Keep the message in French and the exit code at 1, as before.
		// urfave's default prints "Incorrect Usage", then the whole help, and
		// main then prints the error again. Return it untouched so a bad flag
		// produces the same single « erreur : … » line as every other failure.
		OnUsageError: func(_ context.Context, _ *cli.Command, err error, _ bool) error {
			return err
		},
		CommandNotFound: func(_ context.Context, cmd *cli.Command, name string) {
			fmt.Fprintf(os.Stderr, "erreur : commande inconnue %q\n", name)
			fmt.Fprintf(os.Stderr, "Commandes : %s\n", strings.Join(commandNames(cmd), ", "))
			fmt.Fprintln(os.Stderr, "« godio --help » pour le détail.")
			cli.OsExiter(1)
		},
		Commands: []*cli.Command{
			{
				Name:  "run",
				Usage: "lance l'icône de notification (console attachée ; pour un usage normal lancez godio-tray.exe)",
				Action: func(context.Context, *cli.Command) error {
					return cmdRun()
				},
			},
			{
				Name:  "devices",
				Usage: "liste les périphériques audio",
				Action: func(context.Context, *cli.Command) error {
					return cmdDevices()
				},
			},
			{
				Name:  "doctor",
				Usage: "vérifie la configuration et les dépendances",
				Action: func(context.Context, *cli.Command) error {
					return cmdDoctor()
				},
			},
			{
				Name:  "record",
				Usage: "enregistre (N secondes, ou Ctrl+C)",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:    "seconds",
						Aliases: []string{"s"},
						Usage:   "arrête après `N` secondes (0 = jusqu'à Ctrl+C)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return cmdRecord(ctx, cmd.Int("seconds"))
				},
			},
			stageCommand("transcribe", "transcrit les deux pistes"),
			stageCommand("extract", "génère les notes via Claude", styleFlag()),
			stageCommand("write", "ajoute les notes à la note du jour"),
			stageCommand("process", "transcribe + extract + write", styleFlag()),
			{
				Name:      "purge",
				Usage:     "supprime les vieilles sessions selon [sessions] du config.toml",
				ArgsUsage: " ",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "dry-run",
						Aliases: []string{"n"},
						Usage:   "montre ce qui serait supprimé, sans rien supprimer",
					},
					&cli.IntFlag{
						Name:  "keep-days",
						Usage: "remplace sessions.keep_days pour cette exécution",
					},
					&cli.IntFlag{
						Name:  "keep-audio-days",
						Usage: "remplace sessions.keep_audio_days pour cette exécution",
					},
				},
				Action: func(_ context.Context, cmd *cli.Command) error {
					return cmdPurge(cmd)
				},
			},
			{
				Name:  "setup",
				Usage: "télécharge whisper.cpp et les modèles",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "dry-run",
						Aliases: []string{"n"},
						Usage:   "montre ce qui serait téléchargé, sans rien télécharger",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return cmdSetup(ctx, cmd.Bool("dry-run"))
				},
			},
			{
				Name:  "config",
				Usage: "affiche ou ouvre la configuration",
				Action: func(context.Context, *cli.Command) error {
					return cmdConfigShow()
				},
				Commands: []*cli.Command{
					{
						Name:  "edit",
						Usage: "ouvre config.toml dans votre éditeur",
						Action: func(context.Context, *cli.Command) error {
							return cmdConfigEdit()
						},
					},
				},
			},
			{
				Name:  "update",
				Usage: "met à jour godio depuis GitHub",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "check",
						Aliases: []string{"c"},
						Usage:   "indique s'il existe une version plus récente, sans rien installer",
					},
					&cli.BoolFlag{
						Name:  "prerelease",
						Usage: "considère aussi les pré-versions",
					},
					&cli.BoolFlag{
						Name:  "force",
						Usage: "installe même si la version actuelle est déjà à jour, ou est un build local",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return cmdUpdate(ctx, cmd.Bool("check"), cmd.Bool("prerelease"), cmd.Bool("force"))
				},
			},
			{
				Name:  "version",
				Usage: "affiche la version",
				Action: func(context.Context, *cli.Command) error {
					printVersion()
					return nil
				},
			},
		},
	}

	// OnUsageError is looked up on the command that failed, never inherited
	// from the root, so it has to be attached to every node of the tree.
	setUsageError(app, app.OnUsageError)
	return app
}

// setUsageError attaches fn to cmd and to all of its descendants.
func setUsageError(cmd *cli.Command, fn cli.OnUsageErrorFunc) {
	cmd.OnUsageError = fn
	for _, c := range cmd.Commands {
		setUsageError(c, fn)
	}
}

// commandNames lists the visible subcommands of cmd, for the unknown-command
// message.
func commandNames(cmd *cli.Command) []string {
	names := make([]string, 0, len(cmd.Commands))
	for _, c := range cmd.Commands {
		if !c.Hidden {
			names = append(names, c.Name)
		}
	}
	return names
}

// stageCommand builds one of the four pipeline stages, which differ only by
// name: each takes an optional session folder, and the two that call Claude
// also take --style.
func stageCommand(stage, usage string, flags ...cli.Flag) *cli.Command {
	return &cli.Command{
		Name:      stage,
		Usage:     usage,
		ArgsUsage: "[session]",
		Flags:     append([]cli.Flag{dayFlag()}, flags...),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cmdStage(ctx, stage, cmd.Args().First(), cmd.String("style"), cmd.String("day"))
		},
	}
}

// dayFlag replays a whole day. The transcripts are kept, so a day already
// processed can be run again — in another style, or after fixing a setting —
// without touching the recordings.
func dayFlag() cli.Flag {
	return &cli.StringFlag{
		Name:    "day",
		Aliases: []string{"d"},
		Usage:   "toutes les sessions d'un jour : `AAAA-MM-JJ`, « hier », ou « -2 »",
	}
}

// styleFlag overrides claude.style for one run. Because the transcript is kept,
// this is what lets you re-read a meeting in another voice without recording it
// again: `godio extract --style eli5`.
func styleFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "style",
		Usage: "style des notes : " + strings.Join(assets.Styles, " | ") + " (défaut : claude.style)",
	}
}

// cmdUpdate replaces godio's binaries with the latest release.
//
// This is the sibling of `setup`: that one updates the models, this one
// updates godio itself.
func cmdUpdate(ctx context.Context, check, prerelease, force bool) error {
	cfg, err := load()
	if err != nil {
		return err
	}
	if cfg.Update.Prerelease {
		prerelease = true
	}

	fmt.Printf("Version installée : %s\n", version)
	fmt.Println("Recherche de la dernière version…")

	avail, err := selfupdate.Check(ctx, version, prerelease)
	if err != nil {
		return err
	}
	fmt.Printf("Dernière version   : %s\n", avail.Latest)

	switch {
	case avail.Newer:
		fmt.Printf("Une mise à jour est disponible (%s).\n", avail.HumanSize())
	case selfupdate.IsDev(version):
		fmt.Println("Build local : aucune comparaison possible (« --force » pour installer quand même).")
	default:
		fmt.Println("godio est à jour.")
	}

	if check {
		return nil
	}
	if !avail.Newer && !force {
		return nil
	}

	dir, err := selfupdate.InstallDir()
	if err != nil {
		return err
	}
	// Left over from a previous update: sweep before taking more space.
	selfupdate.CleanupOld(dir)

	fmt.Printf("Installation dans %s\n", dir)
	applied, err := selfupdate.Apply(ctx, avail, dir, func(stage string, done, total int64) {
		if total <= 0 {
			fmt.Printf("\r  %-16s %d Mo", stage, done/(1<<20))
			return
		}
		pc := int(done * 100 / total)
		fmt.Printf("\r  %-16s %s %3d%%", stage, bar(pc), pc)
		if pc >= 100 {
			fmt.Println()
		}
	})
	if err != nil {
		return err
	}

	fmt.Printf("\n%s → %s : %s\n", applied.From, applied.To, strings.Join(applied.Replaced, ", "))
	// The running process still holds the old code; nothing it does from here
	// reflects the update.
	fmt.Println("Redémarrez godio-tray.exe pour utiliser la nouvelle version.")
	return nil
}

// printVersion reports the build alongside the toolchain that produced it,
// which is the first thing worth knowing about a cgo binary that someone else
// compiled.
func printVersion() {
	fmt.Printf("godio %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

func init() {
	// urfave's default --version output is just the version string; keep the
	// richer line the `version` command has always printed.
	cli.VersionPrinter = func(*cli.Command) { printVersion() }
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

// cmdConfigEdit opens config.toml, creating it first if this is a first run.
func cmdConfigEdit() error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	if _, err := load(); err != nil {
		return err
	}
	return openInEditor(path)
}

func cmdConfigShow() error {
	path, err := config.Path()
	if err != nil {
		return err
	}
	if _, err := load(); err != nil {
		return err
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

	fmt.Printf("godio %s", version)
	if selfupdate.IsDev(version) {
		fmt.Print(" (build local — « godio update » ne s'applique qu'aux versions publiées)")
	}
	fmt.Println()
	fmt.Println()

	fmt.Println("Configuration :")
	report("config", cfg.Validate())

	backend, gpu := deps.Resolve(deps.Backend(cfg.Whisper.Backend))
	installed, present := deps.InstalledBackend(whisperRoot(cfg))
	fmt.Println("Transcription :")
	fmt.Printf("  · GPU        : %s\n", gpu)
	fmt.Printf("  · backend    : %s (whisper.backend = %q)\n", backend, cfg.Whisper.Backend)
	switch {
	case !present:
		fmt.Println("  · build      : whisper.cpp n'est pas installé")
	case installed == backend:
		fmt.Printf("  · build      : whisper.cpp %s (installé)\n", installed)
	default:
		ok = false
		fmt.Printf("  ✗ build      : whisper.cpp installé en %s, alors que le backend demandé est %s\n",
			installed, backend)
		fmt.Println("                 relancez « godio setup » pour installer la bonne version")
	}
	fmt.Printf("  · préréglage : %s (%s)\n", cfg.Whisper.Preset,
		strings.Join(cfg.Whisper.Decoding(), " "))
	fmt.Printf("  · threads    : %d (coeurs physiques)\n", cfg.Whisper.Threads)
	if backend == deps.BackendCPU && gpu.Present {
		fmt.Println("  ! un GPU NVIDIA est présent mais whisper.backend force le CPU")
	}

	fmt.Println("Claude Code :")
	report("claude", claudecli.Runner{Executable: cfg.Claude.Executable}.Check())
	fmt.Printf("  · modèle     : %s\n", cfg.Claude.Model)
	fmt.Printf("  · style      : %s (claude.style)\n", cfg.Claude.Style)

	fmt.Println("Sessions :")
	if root, err := config.SessionsDir(); err == nil {
		fmt.Printf("  · dossier    : %s\n", root)
	}
	fmt.Printf("  · audio      : %s\n", retentionLine(cfg.Sessions.KeepAudioDays, "conservé"))
	fmt.Printf("  · sessions   : %s\n", retentionLine(cfg.Sessions.KeepDays, "conservées"))

	fmt.Println("Audio :")
	if d := cfg.Audio.AutoStop.Duration; d > 0 {
		warn := "sans préavis"
		if w := cfg.Audio.AutoStopWarning.Duration; w > 0 {
			warn = "préavis " + w.String()
		}
		fmt.Printf("  · arrêt auto : après %s de silence (%s)\n", d, warn)
	} else {
		fmt.Println("  · arrêt auto : désactivé (audio.auto_stop = \"0s\")")
	}
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

// retentionLine describes one half of the retention policy in words, since
// "0" on its own reads like "delete immediately" rather than "keep forever".
func retentionLine(days int, kept string) string {
	if days <= 0 {
		return kept + " indéfiniment (0)"
	}
	return fmt.Sprintf("%s %d jour(s)", kept, days)
}

// cmdSetup downloads whisper.cpp and the models.
func cmdSetup(ctx context.Context, dryRun bool) error {
	cfg, err := load()
	if err != nil {
		return err
	}

	root := whisperRoot(cfg)

	fmt.Println("Recherche de la dernière version de whisper.cpp…")
	plan, err := deps.MakePlan(ctx, root, deps.Backend(cfg.Whisper.Backend))
	if err != nil {
		return err
	}

	fmt.Printf("  GPU      : %s\n", plan.GPU)
	fmt.Printf("  backend  : %s\n", plan.Backend)
	fmt.Printf("  version  : %s\n", plan.Release)
	if plan.BackendMismatch() {
		fmt.Printf("  ! le whisper.cpp installé est la version %s : il sera remplacé\n", plan.Installed)
	}
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

// whisperRoot returns the directory holding whisper/ and models/.
func whisperRoot(cfg config.Config) string {
	return filepath.Dir(filepath.Dir(cfg.Resolve(cfg.Whisper.Exe)))
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

func cmdRecord(ctx context.Context, seconds int) error {
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

	if seconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
		defer cancel()
	}

	watchdog := audio.AutoStop{
		After:   cfg.Audio.AutoStop.Duration,
		Warning: cfg.Audio.AutoStopWarning.Duration,
	}
	if watchdog.Enabled() {
		fmt.Printf("Arrêt automatique après %s de silence.\n", watchdog.After)
	}

	// A live level readout turns the most likely failure — loopback pointed at
	// the wrong output, so the other side records silence — into something
	// visible within seconds instead of after the meeting.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	autoStopped := false
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-ticker.C:
			l := rec.Levels()
			line := fmt.Sprintf("  moi %s  |  eux %s   ",
				meter(l[audio.TrackMe]), meter(l[audio.TrackThem]))

			silentFor := rec.SilentFor()
			switch watchdog.State(silentFor) {
			case audio.AutoStopFire:
				autoStopped = true
				fmt.Printf("\r%s\n", line)
				break loop
			case audio.AutoStopWarn:
				line += fmt.Sprintf("arrêt dans %s ", watchdog.Remaining(silentFor))
			}
			fmt.Printf("\r%s", line)
		}
	}
	fmt.Println()
	if autoStopped {
		fmt.Printf("Silence des deux côtés pendant %s : arrêt automatique.\n", watchdog.After)
	}

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

func cmdStage(ctx context.Context, stage, session, style, day string) error {
	cfg, err := load()
	if err != nil {
		return err
	}
	// A bad style in config.toml falls back silently; a bad --style is a typo
	// the user is standing right there to fix, so say so instead of quietly
	// producing notes in another style.
	if style != "" {
		if !assets.ValidStyle(style) {
			return fmt.Errorf("style inconnu %q (styles : %s)", style, strings.Join(assets.Styles, ", "))
		}
		cfg.Claude.Style = style
	}

	sessions, err := resolveSessions(session, day)
	if err != nil {
		return err
	}

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
	for i, sess := range sessions {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("Session : %s\n", sess.Dir)
		if err := runStage(ctx, p, stage, sess); err != nil {
			return fmt.Errorf("%s : %w", filepath.Base(sess.Dir), err)
		}
	}
	return nil
}

// runStage runs one stage against one session.
func runStage(ctx context.Context, p pipeline.Pipeline, stage string, sess *pipeline.Session) error {
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

// resolveSessions picks what a stage runs on: an explicit folder, every
// session of a day, or — as before — the most recent one.
func resolveSessions(dir, day string) ([]*pipeline.Session, error) {
	if dir != "" && day != "" {
		return nil, errors.New("--day et un dossier de session s'excluent")
	}
	if dir != "" {
		s, err := pipeline.OpenSession(dir)
		if err != nil {
			return nil, err
		}
		return []*pipeline.Session{s}, nil
	}

	root, err := config.SessionsDir()
	if err != nil {
		return nil, err
	}
	if day != "" {
		d, err := pipeline.ParseDay(day, time.Now())
		if err != nil {
			return nil, err
		}
		return pipeline.OnDay(root, d)
	}

	s, err := pipeline.Latest(root)
	if err != nil {
		return nil, err
	}
	return []*pipeline.Session{s}, nil
}

// cmdPurge applies the retention policy, reporting what it freed.
func cmdPurge(cmd *cli.Command) error {
	cfg, err := load()
	if err != nil {
		return err
	}
	if cmd.IsSet("keep-days") {
		cfg.Sessions.KeepDays = int(cmd.Int("keep-days"))
	}
	if cmd.IsSet("keep-audio-days") {
		cfg.Sessions.KeepAudioDays = int(cmd.Int("keep-audio-days"))
	}

	root, err := config.SessionsDir()
	if err != nil {
		return err
	}
	p := pipeline.Pipeline{Cfg: cfg}
	policy := p.Retention()
	if !policy.Enabled() {
		fmt.Println("Aucune purge configurée : sessions.keep_days et sessions.keep_audio_days valent 0.")
		fmt.Println("Essayez « godio purge --keep-days 90 --keep-audio-days 7 --dry-run ».")
		return nil
	}

	dry := cmd.Bool("dry-run")
	res, err := pipeline.Purge(root, policy, time.Now(), dry)
	if err != nil {
		return err
	}
	printPurge(res, dry)
	return nil
}

func printPurge(res pipeline.Purged, dry bool) {
	verb, freed := "Supprimé", "libérés"
	if dry {
		verb, freed = "À supprimer", "seraient libérés"
	}
	if res.Empty() {
		fmt.Println("Rien à supprimer.")
	}
	for _, name := range res.Audio {
		fmt.Printf("  %s : audio (%s)\n", name, verb)
	}
	for _, name := range res.Sessions {
		fmt.Printf("  %s : session entière (%s)\n", name, verb)
	}
	if !res.Empty() {
		fmt.Printf("%.1f Mo %s\n", float64(res.Bytes)/(1<<20), freed)
	}
	if res.Kept > 0 {
		fmt.Printf("%d session(s) conservée(s) : pas de notes.md, le traitement n'est jamais allé au bout.\n", res.Kept)
	}
}
