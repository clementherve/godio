// Package tray is godio's system-tray application.
package tray

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/clementherve/godio/internal/assets"
	"github.com/clementherve/godio/internal/audio"
	"github.com/clementherve/godio/internal/config"
	"github.com/clementherve/godio/internal/deps"
	"github.com/clementherve/godio/internal/notify"
	"github.com/clementherve/godio/internal/pipeline"
	"github.com/clementherve/godio/internal/winui"
)

// App holds the tray's state.
type App struct {
	cfg config.Config

	mu      sync.Mutex
	actx    *audio.Context
	rec     *audio.Recorder
	sess    *pipeline.Session
	working bool
	// autoWarned keeps the silence warning to one notification per recording,
	// instead of one every 500 ms for the whole countdown.
	autoWarned bool

	mToggle *systray.MenuItem
	mStatus *systray.MenuItem
	mNote   *systray.MenuItem
	mFolder *systray.MenuItem
	mQuit   *systray.MenuItem

	mVault  *systray.MenuItem
	mEdit   *systray.MenuItem
	mReload *systray.MenuItem
	mSetup  *systray.MenuItem

	styleItems []*styleItem

	notifier notify.Notifier

	micItems  []*deviceItem
	outItems  []*deviceItem
	lastState assets.IconState
}

type deviceItem struct {
	item *systray.MenuItem
	name string
}

// styleItem is one note style in the Configuration submenu.
type styleItem struct {
	item  *systray.MenuItem
	style string
}

// Run starts the tray and blocks until the user quits.
func Run(cfg config.Config) error {
	a := &App{cfg: cfg, lastState: -1, notifier: notify.New()}
	systray.Run(a.onReady, a.onExit)
	return nil
}

func (a *App) onReady() {
	a.setIcon(assets.IconIdle)
	systray.SetTooltip("godio — prêt")

	a.mToggle = systray.AddMenuItem("Démarrer l'enregistrement", "")
	a.mStatus = systray.AddMenuItem("Prêt", "")
	a.mStatus.Disable()

	systray.AddSeparator()
	a.buildDeviceMenus()

	systray.AddSeparator()
	cfgMenu := systray.AddMenuItem("Configuration", "")
	a.mVault = cfgMenu.AddSubMenuItem("Choisir le coffre Obsidian…", "")
	a.buildStyleMenu(cfgMenu)
	a.mEdit = cfgMenu.AddSubMenuItem("Ouvrir config.toml", "")
	a.mReload = cfgMenu.AddSubMenuItem("Recharger", "")
	a.mSetup = systray.AddMenuItem("Installer / mettre à jour les modèles…", "")

	systray.AddSeparator()
	a.mNote = systray.AddMenuItem("Ouvrir la note du jour", "")
	a.mFolder = systray.AddMenuItem("Ouvrir le dossier de session", "")

	systray.AddSeparator()
	a.mQuit = systray.AddMenuItem("Quitter", "")

	go a.loop()
}

func (a *App) onExit() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rec != nil && a.rec.Running() {
		_, _ = a.rec.Stop()
	}
	if a.actx != nil {
		a.actx.Close()
	}
}

// buildDeviceMenus lists the input and output devices as radio-style submenus.
//
// The output submenu is the fix for the single most likely failure: Teams
// playing to a headset while loopback listens to the default device, which
// records silence from the other party.
func (a *App) buildDeviceMenus() {
	mic := systray.AddMenuItem("Micro", "Périphérique d'entrée")
	out := systray.AddMenuItem("Sortie capturée", "Périphérique dont le son est enregistré")

	actx, err := audio.NewContext()
	if err != nil {
		mic.Disable()
		out.Disable()
		return
	}
	defer actx.Close()

	if devs, err := actx.CaptureDevices(); err == nil {
		a.micItems = addDeviceItems(mic, devs, a.cfg.Audio.MicDevice)
	}
	if devs, err := actx.PlaybackDevices(); err == nil {
		a.outItems = addDeviceItems(out, devs, a.cfg.Audio.LoopbackDevice)
	}
}

// buildStyleMenu offers the note styles as a radio-style submenu. It is the one
// setting worth changing between two meetings — a stand-up and a design review
// do not want the same notes — so it belongs in the menu rather than only in
// config.toml.
func (a *App) buildStyleMenu(parent *systray.MenuItem) {
	menu := parent.AddSubMenuItem("Style des notes", "Densité et ton des notes produites par Claude")
	for _, style := range assets.Styles {
		a.styleItems = append(a.styleItems, &styleItem{
			item:  menu.AddSubMenuItemCheckbox(styleLabels[style], "", style == a.cfg.Claude.Style),
			style: style,
		})
	}
}

// styleLabels names the styles in the interface's language; the config keys
// themselves stay in English, like every other value in config.toml.
var styleLabels = map[string]string{
	assets.StyleBullets:  "Télégraphique",
	assets.StyleNormal:   "Standard",
	assets.StyleDetailed: "Détaillé",
	assets.StyleELI5:     "Vulgarisé",
}

// selectStyle applies a radio-style choice and persists it, since a style is a
// lasting preference rather than a per-meeting one.
func (a *App) selectStyle(chosen *styleItem) {
	a.mu.Lock()
	a.cfg.Claude.Style = chosen.style
	cfg := a.cfg
	a.markStyleLocked()
	a.mu.Unlock()

	if err := cfg.Save(); err != nil {
		a.fail(err)
		return
	}
	a.mStatus.SetTitle("Style des notes : " + styleLabels[chosen.style])
}

// markStyleLocked ticks the item matching the configuration. The caller must
// hold a.mu.
func (a *App) markStyleLocked() {
	for _, it := range a.styleItems {
		if it.style == a.cfg.Claude.Style {
			it.item.Check()
			continue
		}
		it.item.Uncheck()
	}
}

func addDeviceItems(parent *systray.MenuItem, devs []audio.Device, selected string) []*deviceItem {
	chosen, err := audio.Find(devs, selected)
	items := make([]*deviceItem, 0, len(devs))
	for _, d := range devs {
		label := d.Name
		if d.IsDefault {
			label += " (défaut)"
		}
		checked := err == nil && d.Name == chosen.Name
		items = append(items, &deviceItem{
			item: parent.AddSubMenuItemCheckbox(label, "", checked),
			name: d.Name,
		})
	}
	return items
}

// loop is the tray's event loop: menu clicks plus the icon refresh ticker.
func (a *App) loop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	clicks := make(chan func(), 8)
	watch := func(m *systray.MenuItem, fn func()) {
		go func() {
			for range m.ClickedCh {
				clicks <- fn
			}
		}()
	}

	watch(a.mToggle, a.toggle)
	watch(a.mVault, a.chooseVault)
	watch(a.mEdit, a.editConfig)
	watch(a.mReload, a.reloadConfig)
	watch(a.mSetup, func() { go a.runSetup() })
	watch(a.mNote, func() { open(a.cfg.DailyNotePath(time.Now())) })
	watch(a.mFolder, a.openSessionFolder)
	watch(a.mQuit, func() { systray.Quit() })

	for _, it := range a.styleItems {
		it := it
		watch(it.item, func() { a.selectStyle(it) })
	}
	for _, it := range a.micItems {
		it := it
		watch(it.item, func() { a.selectDevice(a.micItems, it, &a.cfg.Audio.MicDevice) })
	}
	for _, it := range a.outItems {
		it := it
		watch(it.item, func() { a.selectDevice(a.outItems, it, &a.cfg.Audio.LoopbackDevice) })
	}

	for {
		select {
		case fn := <-clicks:
			fn()
		case <-ticker.C:
			a.refresh()
		}
	}
}

// selectDevice applies a radio-style choice within one submenu.
func (a *App) selectDevice(group []*deviceItem, chosen *deviceItem, target *string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rec != nil && a.rec.Running() {
		// Changing device mid-recording would silently split the track.
		a.mStatus.SetTitle("Arrêtez l'enregistrement pour changer de périphérique")
		chosen.item.Uncheck()
		return
	}
	for _, it := range group {
		if it == chosen {
			it.item.Check()
			continue
		}
		it.item.Uncheck()
	}
	*target = chosen.name
}

func (a *App) toggle() {
	a.mu.Lock()
	busy, running := a.working, a.rec != nil && a.rec.Running()
	a.mu.Unlock()

	if busy {
		return
	}
	if running {
		a.stop()
		return
	}
	a.start()
}

func (a *App) start() {
	actx, err := audio.NewContext()
	if err != nil {
		a.fail(err)
		return
	}

	capture, err := actx.CaptureDevices()
	if err != nil {
		actx.Close()
		a.fail(err)
		return
	}
	mic, err := audio.Find(capture, a.cfg.Audio.MicDevice)
	if err != nil {
		actx.Close()
		a.fail(fmt.Errorf("micro : %w", err))
		return
	}
	playback, err := actx.PlaybackDevices()
	if err != nil {
		actx.Close()
		a.fail(err)
		return
	}
	out, err := audio.Find(playback, a.cfg.Audio.LoopbackDevice)
	if err != nil {
		actx.Close()
		a.fail(fmt.Errorf("sortie : %w", err))
		return
	}

	root, err := config.SessionsDir()
	if err != nil {
		actx.Close()
		a.fail(err)
		return
	}
	sess, err := pipeline.NewSession(root, time.Now())
	if err != nil {
		actx.Close()
		a.fail(err)
		return
	}
	sess.Meta.MicDevice, sess.Meta.OutputDevice = mic.Name, out.Name

	rec := audio.NewRecorder(actx, sess.Dir)
	err = rec.Start(audio.Options{
		MicDevice:      mic,
		PlaybackDevice: out,
		SilenceDBFS:    a.cfg.Audio.SilenceDBFS,
		ActivityWindow: a.cfg.Audio.ActivityWindow.Duration,
	})
	if err != nil {
		actx.Close()
		os.RemoveAll(sess.Dir) // nothing worth keeping from a failed start
		a.fail(err)
		return
	}

	a.mu.Lock()
	a.actx, a.rec, a.sess = actx, rec, sess
	a.autoWarned = false
	a.mu.Unlock()

	a.mToggle.SetTitle("Arrêter l'enregistrement")
}

func (a *App) stop() {
	a.mu.Lock()
	rec, actx, sess := a.rec, a.actx, a.sess
	a.rec, a.actx = nil, nil
	a.working = true
	a.mu.Unlock()

	a.mToggle.SetTitle("Traitement en cours…")
	a.mToggle.Disable()
	a.setIcon(assets.IconProcessing)

	res, err := rec.Stop()
	actx.Close()
	if err != nil {
		a.finish(pipeline.Result{}, fmt.Errorf("arrêt de l'enregistrement : %w", err))
		return
	}
	sess.RecordResult(res)
	if err := sess.Save(); err != nil {
		a.finish(pipeline.Result{}, err)
		return
	}

	// Processing runs off the event loop so the menu stays responsive while
	// Whisper works through what may be an hour of audio.
	go func() {
		p := pipeline.Pipeline{
			Cfg: a.cfg,
			Log: func(s string) { a.mStatus.SetTitle(s) },
			OnProgress: func(stage string, pc int) {
				a.mStatus.SetTitle(fmt.Sprintf("%s %d%%", stage, pc))
				systray.SetTooltip(fmt.Sprintf("godio — %s %d%%", stage, pc))
			},
		}
		res, err := p.Process(context.Background(), sess)
		a.finish(res, err)
	}()
}

func (a *App) finish(res pipeline.Result, err error) {
	a.mu.Lock()
	a.working = false
	a.mu.Unlock()

	a.mToggle.SetTitle("Démarrer l'enregistrement")
	a.mToggle.Enable()

	if err != nil {
		a.fail(err)
		a.notifier.Failed(truncate(err.Error(), 200))
		return
	}

	a.setIcon(assets.IconIdle)
	summary := "Notes ajoutées à la note du jour"
	if res.Actions > 0 {
		summary = fmt.Sprintf("Notes ajoutées — %d action(s)", res.Actions)
	}
	a.mStatus.SetTitle(summary)
	systray.SetTooltip("godio — prêt")

	// The whole point of a notification here is that transcription takes long
	// enough to walk away from.
	a.notifier.Done("godio — réunion traitée",
		fmt.Sprintf("%s\nTraitée en %s.", summary, res.Elapsed.Round(time.Second)))

	a.purge()
}

// purge applies the retention policy, once a meeting has been processed.
//
// Here rather than at startup: the one moment a deletion is certainly safe is
// just after a successful run, when nothing is recording and the session that
// matters most is the youngest one. It is silent when it frees nothing, which
// is the usual case.
func (a *App) purge() {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

	p := pipeline.Pipeline{Cfg: cfg}
	policy := p.Retention()
	if !policy.Enabled() {
		return
	}

	root, err := config.SessionsDir()
	if err != nil {
		return
	}
	res, err := pipeline.Purge(root, policy, time.Now(), false)
	if err != nil {
		// A failed purge must never look like a failed meeting: the notes are
		// already in the vault by this point.
		a.mStatus.SetTitle("Purge : " + truncate(err.Error(), 50))
		return
	}
	if res.Empty() {
		return
	}
	a.mStatus.SetTitle(fmt.Sprintf("Purge : %d session(s), %.0f Mo libérés",
		len(res.Audio)+len(res.Sessions), float64(res.Bytes)/(1<<20)))
}

func (a *App) fail(err error) {
	a.setIcon(assets.IconError)
	a.mStatus.SetTitle(truncate(err.Error(), 60))
	systray.SetTooltip("godio — " + truncate(err.Error(), 100))
}

// refresh updates the icon and tooltip from the live levels.
func (a *App) refresh() {
	a.mu.Lock()
	rec, working := a.rec, a.working
	a.mu.Unlock()

	if working {
		return
	}
	if rec == nil || !rec.Running() {
		return
	}

	levels := rec.Levels()
	me, them := levels[audio.TrackMe], levels[audio.TrackThem]

	// Upper dot = microphone, lower dot = system sound, each independent, so a
	// single glance says *which* side went quiet rather than just "something".
	a.setIcon(assets.Recording(me.Active, them.Active))

	// Showing both levels makes a dead stream obvious before the meeting ends,
	// rather than after the transcript comes back half empty.
	systray.SetTooltip(fmt.Sprintf("Micro %s %.0f dB\nSystème %s %.0f dB",
		mark(me.Active), me.DBFS, mark(them.Active), them.DBFS))
	a.mStatus.SetTitle(fmt.Sprintf("Micro %s · Système %s", mark(me.Active), mark(them.Active)))

	a.checkAutoStop(rec)
}

// checkAutoStop ends a recording that nobody is feeding any more.
//
// It runs on the tray's 500 ms tick, so the countdown is accurate to half a
// second and any sound at all resets it — the warning is a reprieve, not a
// prompt that has to be answered.
func (a *App) checkAutoStop(rec *audio.Recorder) {
	watchdog := a.autoStop()
	if !watchdog.Enabled() {
		return
	}

	silentFor := rec.SilentFor()
	switch watchdog.State(silentFor) {
	case audio.AutoStopWarn:
		left := watchdog.Remaining(silentFor)
		a.mStatus.SetTitle(fmt.Sprintf("Silence — arrêt automatique dans %s", left))
		systray.SetTooltip(fmt.Sprintf("godio — silence, arrêt dans %s", left))

		a.mu.Lock()
		first := !a.autoWarned
		a.autoWarned = true
		a.mu.Unlock()
		if first {
			// A toast, not a modal: the reason the room went quiet is usually
			// that nobody is at the machine, and a dialog waiting for a click
			// would keep the recording running forever — the exact failure
			// this feature exists to prevent.
			a.notifier.Done("godio — silence détecté",
				fmt.Sprintf("Arrêt automatique de l'enregistrement dans %s.\nParlez pour l'annuler.", left))
		}

	case audio.AutoStopFire:
		a.mStatus.SetTitle("Arrêt automatique après " + silentFor.Round(time.Second).String() + " de silence")
		a.notifier.Done("godio — arrêt automatique",
			fmt.Sprintf("Silence des deux côtés pendant %s : l'enregistrement est arrêté et traité.",
				watchdog.After))
		a.stop()

	default:
		// Someone spoke: forget the warning so a later silence warns again.
		a.mu.Lock()
		a.autoWarned = false
		a.mu.Unlock()
	}
}

// autoStop reads the watchdog settings under the lock, since « Recharger »
// can swap the configuration mid-recording.
func (a *App) autoStop() audio.AutoStop {
	a.mu.Lock()
	defer a.mu.Unlock()
	return audio.AutoStop{
		After:   a.cfg.Audio.AutoStop.Duration,
		Warning: a.cfg.Audio.AutoStopWarning.Duration,
	}
}

func mark(active bool) string {
	if active {
		return "●"
	}
	return "○"
}

// chooseVault asks for the Obsidian vault with a native folder picker and
// saves it. This is the one setting that has to be right on first run, and the
// one nobody wants to type a Windows path for.
func (a *App) chooseVault() {
	go func() {
		path, err := winui.PickFolder("Choisissez votre coffre Obsidian")
		if err != nil {
			if _, cancelled := err.(winui.ErrCancelled); !cancelled {
				a.fail(err)
			}
			return
		}

		a.mu.Lock()
		a.cfg.Obsidian.Vault = path
		cfg := a.cfg
		a.mu.Unlock()

		if err := cfg.Save(); err != nil {
			a.fail(err)
			return
		}
		a.mStatus.SetTitle("Coffre : " + filepath.Base(path))
	}()
}

func (a *App) editConfig() {
	path, err := config.Path()
	if err != nil {
		a.fail(err)
		return
	}
	open(path)
}

// reloadConfig re-reads config.toml so edits take effect without restarting.
func (a *App) reloadConfig() {
	cfg, _, err := config.Load()
	if err != nil {
		a.fail(err)
		return
	}
	a.mu.Lock()
	// Device choices live in the menu, which is built once at startup, so they
	// are kept rather than clobbered by whatever the file says.
	cfg.Audio.MicDevice = a.cfg.Audio.MicDevice
	cfg.Audio.LoopbackDevice = a.cfg.Audio.LoopbackDevice
	a.cfg = cfg
	// The style *is* read from the file, so the ticks have to follow it.
	a.markStyleLocked()
	a.mu.Unlock()
	a.mStatus.SetTitle("Configuration rechargée")
}

// runSetup downloads whisper.cpp and the models, reporting progress in the menu.
func (a *App) runSetup() {
	a.mu.Lock()
	busy := a.working || (a.rec != nil && a.rec.Running())
	if !busy {
		a.working = true
	}
	cfg := a.cfg
	a.mu.Unlock()

	if busy {
		a.mStatus.SetTitle("Occupé — réessayez après l'enregistrement")
		return
	}
	defer func() {
		a.mu.Lock()
		a.working = false
		a.mu.Unlock()
		a.mSetup.Enable()
	}()

	a.mSetup.Disable()
	a.setIcon(assets.IconProcessing)
	a.mStatus.SetTitle("Recherche des dépendances…")

	ctx := context.Background()
	dir := filepath.Dir(cfg.Resolve(cfg.Whisper.Exe))
	root := filepath.Dir(dir)

	plan, err := deps.MakePlan(ctx, root, deps.Backend(cfg.Whisper.Backend))
	if err != nil {
		a.fail(err)
		a.notifier.Failed(truncate(err.Error(), 200))
		return
	}
	if !plan.NeedsDownload() {
		a.setIcon(assets.IconIdle)
		a.mStatus.SetTitle("Dépendances déjà installées (" + string(plan.Backend) + ")")
		return
	}

	err = deps.Install(ctx, root, plan, func(stage string, done, total int64) {
		if total > 0 {
			a.mStatus.SetTitle(fmt.Sprintf("%s %d%%", stage, done*100/total))
			return
		}
		a.mStatus.SetTitle(fmt.Sprintf("%s %d Mo", stage, done/(1<<20)))
	})
	if err != nil {
		a.fail(err)
		a.notifier.Failed(truncate(err.Error(), 200))
		return
	}

	// Point the config at what was actually installed: the model name depends
	// on the backend that was resolved.
	a.mu.Lock()
	a.cfg.Whisper.Model = filepath.Join("models", plan.Speech.Name)
	a.cfg.Whisper.VADModel = filepath.Join("models", plan.VAD.Name)
	saved := a.cfg
	a.mu.Unlock()
	_ = saved.Save()

	a.setIcon(assets.IconIdle)
	a.mStatus.SetTitle("Dépendances installées (" + string(plan.Backend) + ")")
	a.notifier.Done("godio — installation terminée",
		"whisper.cpp "+plan.Release+" ("+string(plan.Backend)+") et les modèles sont prêts.")
}

// setIcon avoids redundant redraws, which flicker on some Windows builds.
func (a *App) setIcon(s assets.IconState) {
	if a.lastState == s {
		return
	}
	a.lastState = s
	systray.SetIcon(assets.Icon(s))
}

func (a *App) openSessionFolder() {
	a.mu.Lock()
	sess := a.sess
	a.mu.Unlock()

	if sess != nil {
		open(sess.Dir)
		return
	}
	if root, err := config.SessionsDir(); err == nil {
		open(root)
	}
}

// open shows a file or folder in the system file manager.
func open(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	_ = cmd.Start()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
