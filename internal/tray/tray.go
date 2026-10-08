// Package tray is raudio's system-tray application.
package tray

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/clementherve/raudio/internal/assets"
	"github.com/clementherve/raudio/internal/audio"
	"github.com/clementherve/raudio/internal/config"
	"github.com/clementherve/raudio/internal/pipeline"
)

// App holds the tray's state.
type App struct {
	cfg config.Config

	mu      sync.Mutex
	actx    *audio.Context
	rec     *audio.Recorder
	sess    *pipeline.Session
	working bool

	mToggle *systray.MenuItem
	mStatus *systray.MenuItem
	mNote   *systray.MenuItem
	mFolder *systray.MenuItem
	mQuit   *systray.MenuItem

	micItems  []*deviceItem
	outItems  []*deviceItem
	lastState assets.IconState
}

type deviceItem struct {
	item *systray.MenuItem
	name string
}

// Run starts the tray and blocks until the user quits.
func Run(cfg config.Config) error {
	a := &App{cfg: cfg, lastState: -1}
	systray.Run(a.onReady, a.onExit)
	return nil
}

func (a *App) onReady() {
	a.setIcon(assets.IconIdle)
	systray.SetTooltip("raudio — prêt")

	a.mToggle = systray.AddMenuItem("Démarrer l'enregistrement", "")
	a.mStatus = systray.AddMenuItem("Prêt", "")
	a.mStatus.Disable()

	systray.AddSeparator()
	a.buildDeviceMenus()

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
	watch(a.mNote, func() { open(a.cfg.DailyNotePath(time.Now())) })
	watch(a.mFolder, a.openSessionFolder)
	watch(a.mQuit, func() { systray.Quit() })

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
		a.finish(fmt.Errorf("arrêt de l'enregistrement : %w", err))
		return
	}
	sess.RecordResult(res)
	if err := sess.Save(); err != nil {
		a.finish(err)
		return
	}

	// Processing runs off the event loop so the menu stays responsive while
	// Whisper works through what may be an hour of audio.
	go func() {
		p := pipeline.Pipeline{Cfg: a.cfg, Log: func(s string) { a.mStatus.SetTitle(s) }}
		_, err := p.Process(context.Background(), sess)
		a.finish(err)
	}()
}

func (a *App) finish(err error) {
	a.mu.Lock()
	a.working = false
	a.mu.Unlock()

	a.mToggle.SetTitle("Démarrer l'enregistrement")
	a.mToggle.Enable()

	if err != nil {
		a.fail(err)
		return
	}
	a.setIcon(assets.IconIdle)
	a.mStatus.SetTitle("Notes ajoutées à la note du jour")
}

func (a *App) fail(err error) {
	a.setIcon(assets.IconError)
	a.mStatus.SetTitle(truncate(err.Error(), 60))
	systray.SetTooltip("raudio — " + truncate(err.Error(), 100))
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

	state := assets.IconSilent
	if me.Active && them.Active {
		state = assets.IconActive
	}
	a.setIcon(state)

	// Showing both levels makes a dead stream obvious before the meeting ends,
	// rather than after the transcript comes back half empty.
	systray.SetTooltip(fmt.Sprintf("Moi %.0f dB %s · Eux %.0f dB %s",
		me.DBFS, mark(me.Active), them.DBFS, mark(them.Active)))
	a.mStatus.SetTitle(fmt.Sprintf("Moi %s · Eux %s", mark(me.Active), mark(them.Active)))
}

func mark(active bool) string {
	if active {
		return "●"
	}
	return "○"
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
