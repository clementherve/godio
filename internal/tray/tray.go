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
	"github.com/clementherve/godio/internal/lang"
	"github.com/clementherve/godio/internal/notify"
	"github.com/clementherve/godio/internal/pipeline"
	"github.com/clementherve/godio/internal/proc"
	"github.com/clementherve/godio/internal/selfupdate"
	"github.com/clementherve/godio/internal/winui"
)

// App holds the tray's state.
type App struct {
	cfg config.Config
	// version is the running build, used to decide whether a release is newer.
	version string

	mu      sync.Mutex
	actx    *audio.Context
	rec     *audio.Recorder
	sess    *pipeline.Session
	working bool
	// events carries every systray call made outside the tray loop. Menu items
	// are plain structs inside systray, not thread-safe, and updating a visible
	// Windows menu from another goroutine can crash the process.
	events chan func()
	// autoWarned keeps the silence warning to one notification per recording,
	// instead of one every 500 ms for the whole countdown.
	autoWarned bool

	mToggle *systray.MenuItem
	mStatus *systray.MenuItem
	mNote   *systray.MenuItem
	mFolder *systray.MenuItem
	mQuit   *systray.MenuItem

	mVault   *systray.MenuItem
	mEdit    *systray.MenuItem
	mReload  *systray.MenuItem
	mSetup   *systray.MenuItem
	mUpdate  *systray.MenuItem
	mRestart *systray.MenuItem

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
func Run(cfg config.Config, version string) error {
	a := &App{
		cfg:       cfg,
		version:   version,
		lastState: -1,
		notifier:  notify.New(),
		events:    make(chan func(), 64),
	}
	systray.Run(a.onReady, a.onExit)
	return nil
}

func (a *App) onReady() {
	a.setIcon(assets.IconIdle)
	systray.SetTooltip(lang.T().Tray.TooltipReady)

	a.mToggle = systray.AddMenuItem(lang.T().Tray.Start, "")
	a.mStatus = systray.AddMenuItem(lang.T().Tray.Ready, "")
	a.mStatus.Disable()

	systray.AddSeparator()
	a.buildDeviceMenus()

	systray.AddSeparator()
	cfgMenu := systray.AddMenuItem(lang.T().Tray.ConfigMenu, "")
	a.mVault = cfgMenu.AddSubMenuItem(lang.T().Tray.PickVault, "")
	a.buildStyleMenu(cfgMenu)
	a.mEdit = cfgMenu.AddSubMenuItem(lang.T().Tray.OpenConfig, "")
	a.mReload = cfgMenu.AddSubMenuItem(lang.T().Tray.Reload, "")
	a.mSetup = systray.AddMenuItem(lang.T().Tray.Setup, "")
	// The sibling of the item above: that one updates the models, this one
	// updates godio.
	a.mUpdate = systray.AddMenuItem(lang.T().Tray.Update, lang.T().Tray.UpdateTooltip)
	a.mRestart = systray.AddMenuItem(lang.T().Tray.Restart, lang.T().Tray.RestartTooltip)
	a.mRestart.Hide()

	systray.AddSeparator()
	a.mNote = systray.AddMenuItem(lang.T().Tray.OpenNote, "")
	a.mFolder = systray.AddMenuItem(lang.T().Tray.OpenFolder, "")

	systray.AddSeparator()
	a.mQuit = systray.AddMenuItem(lang.T().Tray.Quit, "")

	go a.loop()
	go a.checkUpdate()
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
	mic := systray.AddMenuItem(lang.T().Tray.MicMenu, lang.T().Tray.MicTooltip)
	out := systray.AddMenuItem(lang.T().Tray.OutMenu, lang.T().Tray.OutTooltip)

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
	menu := parent.AddSubMenuItem(lang.T().Tray.StyleMenu, lang.T().Tray.StyleTooltip)
	for _, style := range assets.Styles {
		a.styleItems = append(a.styleItems, &styleItem{
			item:  menu.AddSubMenuItemCheckbox(styleLabel(style), "", style == a.cfg.Claude.Style),
			style: style,
		})
	}
}

// styleLabel names a style in the interface's language; the config values
// themselves stay in English, like every other value in config.toml.
//
// A function rather than a map, so « Recharger » can change the language
// without the labels being stuck at whatever they were at startup.
func styleLabel(style string) string {
	switch style {
	case assets.StyleBullets:
		return lang.T().Tray.StyleBullets
	case assets.StyleDetailed:
		return lang.T().Tray.StyleDetailed
	case assets.StyleELI5:
		return lang.T().Tray.StyleELI5
	default:
		return lang.T().Tray.StyleNormal
	}
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
	a.mStatus.SetTitle(lang.T().Tray.StylePrefix + styleLabel(chosen.style))
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
			label += lang.T().Tray.DefaultSuffix
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
	watch(a.mUpdate, func() { go a.runUpdate() })
	watch(a.mRestart, a.restart)
	watch(a.mNote, func() {
		a.mu.Lock()
		cfg := a.cfg
		a.mu.Unlock()
		open(cfg.DailyNotePath(time.Now()))
	})
	watch(a.mFolder, a.openSessionFolder)
	watch(a.mQuit, func() { systray.Quit() })

	for _, it := range a.styleItems {
		it := it
		watch(it.item, func() { a.selectStyle(it) })
	}
	for _, it := range a.micItems {
		it := it
		watch(it.item, func() { a.selectDevice(a.micItems, it, &a.cfg.Audio.MicDevice, audio.TrackMe) })
	}
	for _, it := range a.outItems {
		it := it
		watch(it.item, func() { a.selectDevice(a.outItems, it, &a.cfg.Audio.LoopbackDevice, audio.TrackThem) })
	}

	for {
		select {
		case fn := <-a.events:
			fn()
		case fn := <-clicks:
			fn()
		case <-ticker.C:
			a.refresh()
		}
	}
}

// post runs fn on the tray loop. Every systray call outside onReady and loop
// must go through it: systray's MenuItem fields are unsynchronised, and
// SetMenuItemInfo on a menu that TrackPopupMenu is displaying can crash.
//
// The fallback goroutine matters because post is also called from the loop
// itself (through fail and updateFailed). Blocking there when the queue is full
// would deadlock the only goroutine able to drain it.
func (a *App) post(fn func()) {
	select {
	case a.events <- fn:
	default:
		go func() { a.events <- fn }()
	}
}

// selectDevice applies a radio-style choice within one submenu.
//
// A device can now be swapped while a recording is running: the capture
// stream is reopened against the new endpoint and keeps writing into the same
// track, so the meeting is not split (and not stopped) by a change of input
// source.
func (a *App) selectDevice(group []*deviceItem, chosen *deviceItem, target *string, track audio.Track) {
	actx, err := audio.NewContext()
	var dev audio.Device
	if err == nil {
		var devs []audio.Device
		if track == audio.TrackMe {
			devs, err = actx.CaptureDevices()
		} else {
			devs, err = actx.PlaybackDevices()
		}
		if err == nil {
			dev, err = audio.Find(devs, chosen.name)
		}
		actx.Close()
	}

	a.mu.Lock()
	recording := a.rec != nil && a.rec.Running()
	a.mu.Unlock()

	if recording {
		if err != nil {
			a.mStatus.SetTitle(lang.T().Tray.StopToChangeDevice)
			chosen.item.Uncheck()
			return
		}
		if err := a.rec.SwitchDevice(track, dev); err != nil {
			a.mStatus.SetTitle(truncate(err.Error(), 60))
			chosen.item.Uncheck()
			return
		}
		a.mu.Lock()
		for _, it := range group {
			if it == chosen {
				it.item.Check()
				continue
			}
			it.item.Uncheck()
		}
		*target = chosen.name
		a.mu.Unlock()
		return
	}

	if err != nil {
		a.mStatus.SetTitle(lang.T().Tray.StopToChangeDevice)
		chosen.item.Uncheck()
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
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
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()

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
	mic, err := audio.Find(capture, cfg.Audio.MicDevice)
	if err != nil {
		actx.Close()
		a.fail(fmt.Errorf(lang.T().Err.MicFmt, err))
		return
	}
	playback, err := actx.PlaybackDevices()
	if err != nil {
		actx.Close()
		a.fail(err)
		return
	}
	out, err := audio.Find(playback, cfg.Audio.LoopbackDevice)
	if err != nil {
		actx.Close()
		a.fail(fmt.Errorf(lang.T().Err.OutputFmt, err))
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
		SilenceDBFS:    cfg.Audio.SilenceDBFS,
		ActivityWindow: cfg.Audio.ActivityWindow.Duration,
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

	a.mToggle.SetTitle(lang.T().Tray.Stop)
}

func (a *App) stop() {
	a.mu.Lock()
	rec, actx, sess, cfg := a.rec, a.actx, a.sess, a.cfg
	a.rec, a.actx = nil, nil
	a.working = true
	a.mu.Unlock()

	a.mToggle.SetTitle(lang.T().Tray.Processing)
	a.mToggle.Disable()
	a.setIcon(assets.IconProcessing)

	res, err := rec.Stop()
	actx.Close()
	if err != nil {
		a.finish(pipeline.Result{}, fmt.Errorf(lang.T().Err.StopRecordingFmt, err))
		return
	}
	sess.RecordResult(res)
	if err := sess.Save(); err != nil {
		a.finish(pipeline.Result{}, err)
		return
	}

	// Processing runs off the event loop so the menu stays responsive while
	// Whisper works through what may be an hour of audio. Its callbacks post
	// back to the loop instead of touching systray from this goroutine.
	go func() {
		p := pipeline.Pipeline{
			Cfg: cfg,
			Log: func(s string) {
				a.post(func() { a.mStatus.SetTitle(s) })
			},
			OnProgress: func(stage string, pc int) {
				a.post(func() {
					a.mStatus.SetTitle(fmt.Sprintf("%s %d%%", stage, pc))
					systray.SetTooltip(fmt.Sprintf(lang.T().Tray.TooltipProgressFmt, stage, pc))
				})
			},
		}
		res, err := p.Process(context.Background(), sess)
		a.post(func() { a.finish(res, err) })
	}()
}

func (a *App) finish(res pipeline.Result, err error) {
	a.mu.Lock()
	a.working = false
	a.mu.Unlock()

	a.mToggle.SetTitle(lang.T().Tray.Start)
	a.mToggle.Enable()

	if err != nil {
		a.fail(err)
		a.notifier.Failed(truncate(err.Error(), 200))
		return
	}

	a.setIcon(assets.IconIdle)
	summary := lang.T().Tray.NotesAdded
	if res.Actions > 0 {
		summary = fmt.Sprintf(lang.T().Tray.NotesAddedActionsFmt, res.Actions)
	}
	a.mStatus.SetTitle(summary)
	systray.SetTooltip(lang.T().Tray.TooltipReady)

	// The whole point of a notification here is that transcription takes long
	// enough to walk away from.
	a.notifier.Done(lang.T().Tray.ProcessedTitle,
		fmt.Sprintf(lang.T().Tray.ProcessedBodyFmt, summary, res.Elapsed.Round(time.Second)))

	// Retention walks the session directory and may delete gigabytes; it must
	// not freeze the menu, and it only ever reports through the loop.
	go a.purge()
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
		msg := lang.T().Tray.PurgeErrPrefix + truncate(err.Error(), 50)
		a.post(func() { a.mStatus.SetTitle(msg) })
		return
	}
	if res.Empty() {
		return
	}
	msg := fmt.Sprintf(lang.T().Tray.PurgeDoneFmt,
		len(res.Audio)+len(res.Sessions), float64(res.Bytes)/(1<<20))
	a.post(func() { a.mStatus.SetTitle(msg) })
}

func (a *App) fail(err error) {
	msg := err.Error()
	a.post(func() {
		a.setIcon(assets.IconError)
		a.mStatus.SetTitle(truncate(msg, 60))
		systray.SetTooltip(lang.T().Tray.TooltipPrefix + truncate(msg, 100))
	})
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
	systray.SetTooltip(fmt.Sprintf(lang.T().Tray.LevelsTooltipFmt,
		mark(me.Active), me.DBFS, mark(them.Active), them.DBFS))
	a.mStatus.SetTitle(fmt.Sprintf(lang.T().Tray.LevelsFmt, mark(me.Active), mark(them.Active)))

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
		a.mStatus.SetTitle(fmt.Sprintf(lang.T().Tray.SilenceCountdownFmt, left))
		systray.SetTooltip(fmt.Sprintf(lang.T().Tray.SilenceTooltipFmt, left))

		a.mu.Lock()
		first := !a.autoWarned
		a.autoWarned = true
		a.mu.Unlock()
		if first {
			// A toast, not a modal: the reason the room went quiet is usually
			// that nobody is at the machine, and a dialog waiting for a click
			// would keep the recording running forever — the exact failure
			// this feature exists to prevent.
			a.notifier.Done(lang.T().Tray.SilenceTitle,
				fmt.Sprintf(lang.T().Tray.SilenceBodyFmt, left))
		}

	case audio.AutoStopFire:
		a.mStatus.SetTitle(lang.T().Tray.AutoStoppedPrefix + silentFor.Round(time.Second).String() + lang.T().Tray.OfSilence)
		a.notifier.Done(lang.T().Tray.AutoStopTitle,
			fmt.Sprintf(lang.T().Tray.AutoStopBodyFmt,
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
		path, err := winui.PickFolder(lang.T().Tray.PickVaultTitle)
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
		msg := lang.T().Tray.VaultPrefix + filepath.Base(path)
		a.post(func() { a.mStatus.SetTitle(msg) })
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

	lang.Use(lang.Resolve(cfg.Language))
	a.relabel()
	a.mStatus.SetTitle(lang.T().Tray.Reloaded)
}

// relabel re-reads every menu title from the catalogue, so changing the
// language in config.toml takes effect on « Recharger » rather than at the
// next launch. It runs on the click goroutine, like every other menu mutation.
func (a *App) relabel() {
	a.mu.Lock()
	recording := a.rec != nil && a.rec.Running()
	a.mu.Unlock()

	toggle := lang.T().Tray.Start
	if recording {
		toggle = lang.T().Tray.Stop
	}
	for _, it := range []struct {
		item  *systray.MenuItem
		title string
	}{
		{a.mToggle, toggle},
		{a.mVault, lang.T().Tray.PickVault},
		{a.mEdit, lang.T().Tray.OpenConfig},
		{a.mReload, lang.T().Tray.Reload},
		{a.mSetup, lang.T().Tray.Setup},
		{a.mUpdate, lang.T().Tray.Update},
		{a.mRestart, lang.T().Tray.Restart},
		{a.mNote, lang.T().Tray.OpenNote},
		{a.mFolder, lang.T().Tray.OpenFolder},
		{a.mQuit, lang.T().Tray.Quit},
	} {
		if it.item != nil {
			it.item.SetTitle(it.title)
		}
	}
	for _, it := range a.styleItems {
		it.item.SetTitle(styleLabel(it.style))
	}
	// The device submenus carry device names, not translatable text; only the
	// "(default)" suffix would change, and relabelling them would mean
	// re-reading the device list for the sake of one word.
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
		a.post(func() { a.mStatus.SetTitle(lang.T().Tray.Busy) })
		return
	}
	defer func() {
		a.mu.Lock()
		a.working = false
		a.mu.Unlock()
		a.post(func() { a.mSetup.Enable() })
	}()

	a.post(func() {
		a.mSetup.Disable()
		a.setIcon(assets.IconProcessing)
		a.mStatus.SetTitle(lang.T().Tray.LookingForDeps)
	})

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
		msg := lang.T().Tray.DepsAlreadyPrefix + string(plan.Backend) + ")"
		a.post(func() {
			a.setIcon(assets.IconIdle)
			a.mStatus.SetTitle(msg)
		})
		return
	}

	err = deps.Install(ctx, root, plan, func(stage string, done, total int64) {
		msg := fmt.Sprintf(lang.T().Tray.ProgressMBFmt, stage, done/(1<<20))
		if total > 0 {
			msg = fmt.Sprintf("%s %d%%", stage, done*100/total)
		}
		a.post(func() { a.mStatus.SetTitle(msg) })
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

	msg := lang.T().Tray.DepsInstalledPrefix + string(plan.Backend) + ")"
	a.post(func() {
		a.setIcon(assets.IconIdle)
		a.mStatus.SetTitle(msg)
	})
	a.notifier.Done(lang.T().Tray.DepsDoneTitle,
		lang.T().Tray.DepsDoneBody1+plan.Release+" ("+string(plan.Backend)+lang.T().Tray.DepsDoneBody2)
}

// checkUpdate asks GitHub, at startup, whether a newer release exists.
//
// It only ever reports: installing an executable stays an explicit click. The
// check is opt-in because a tray that phones home the moment it launches is
// not something to switch on for someone without asking.
func (a *App) checkUpdate() {
	a.mu.Lock()
	cfg, version := a.cfg, a.version
	a.mu.Unlock()

	if !cfg.Update.CheckOnStart || selfupdate.IsDev(version) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	avail, err := selfupdate.Check(ctx, version, cfg.Update.Prerelease)
	if err != nil || !avail.Newer {
		// A failed check is not worth interrupting anyone over: the menu item
		// is there whenever they want to look.
		return
	}

	updateTitle := lang.T().Tray.UpdatePrefix + avail.Latest + ")"
	status := lang.T().Tray.VersionPrefix + avail.Latest + lang.T().Tray.AvailableSuffix
	a.post(func() {
		a.mUpdate.SetTitle(updateTitle)
		a.mStatus.SetTitle(status)
	})
	a.notifier.Done(lang.T().Tray.UpdateAvailableTitle,
		fmt.Sprintf(lang.T().Tray.UpdateAvailableBodyFmt,
			version, avail.Latest, avail.HumanSize()))
}

// runUpdate downloads and installs the latest release.
func (a *App) runUpdate() {
	a.mu.Lock()
	busy := a.working || (a.rec != nil && a.rec.Running())
	if !busy {
		a.working = true
	}
	cfg, version := a.cfg, a.version
	a.mu.Unlock()

	if busy {
		// Swapping the binaries mid-meeting would be the worst possible moment.
		a.post(func() { a.mStatus.SetTitle(lang.T().Tray.Busy) })
		return
	}
	defer func() {
		a.mu.Lock()
		a.working = false
		a.mu.Unlock()
		a.post(func() { a.mUpdate.Enable() })
	}()

	a.post(func() {
		a.mUpdate.Disable()
		a.setIcon(assets.IconProcessing)
		a.mStatus.SetTitle(lang.T().Tray.LookingForUpdate)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	avail, err := selfupdate.Check(ctx, version, cfg.Update.Prerelease)
	if err != nil {
		a.updateFailed(err)
		return
	}
	if !avail.Newer {
		msg := lang.T().Tray.UpToDatePrefix + version + ")"
		a.post(func() {
			a.setIcon(assets.IconIdle)
			a.mStatus.SetTitle(msg)
		})
		return
	}

	dir, err := selfupdate.InstallDir()
	if err != nil {
		a.updateFailed(err)
		return
	}
	selfupdate.CleanupOld(dir)

	applied, err := selfupdate.Apply(ctx, avail, dir, func(stage string, done, total int64) {
		msg := fmt.Sprintf(lang.T().Tray.ProgressMBFmt, stage, done/(1<<20))
		if total > 0 {
			msg = fmt.Sprintf("%s %d%%", stage, done*100/total)
		}
		a.post(func() { a.mStatus.SetTitle(msg) })
	})
	if err != nil {
		a.updateFailed(err)
		return
	}

	// The running process is still the old code, whatever the files now say.
	updateTitle := lang.T().Tray.GodioPrefix + applied.To + lang.T().Tray.InstalledSuffix
	status := lang.T().Tray.RestartToUsePrefix + applied.To
	a.post(func() {
		a.setIcon(assets.IconIdle)
		a.mUpdate.SetTitle(updateTitle)
		a.mStatus.SetTitle(status)
		a.mRestart.Show()
	})
	a.notifier.Done(lang.T().Tray.UpdateDoneTitle,
		fmt.Sprintf(lang.T().Tray.UpdateDoneBodyFmt, applied.From, applied.To))
}

// updateFailed reports an update error without dressing it up as a failed
// meeting: nothing was recorded, and nothing was lost.
func (a *App) updateFailed(err error) {
	msg := lang.T().Tray.UpdateErrPrefix + truncate(err.Error(), 60)
	a.post(func() {
		a.setIcon(assets.IconIdle)
		a.mStatus.SetTitle(msg)
	})
	a.notifier.Failed(truncate(err.Error(), 200))
}

// restart launches the freshly installed tray and quits this one.
func (a *App) restart() {
	a.mu.Lock()
	busy := a.working || (a.rec != nil && a.rec.Running())
	a.mu.Unlock()
	if busy {
		a.mStatus.SetTitle(lang.T().Tray.Busy)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		a.updateFailed(err)
		return
	}
	if err := exec.Command(exe).Start(); err != nil {
		a.updateFailed(err)
		return
	}
	systray.Quit()
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
	proc.HideConsole(cmd)
	_ = cmd.Start()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
