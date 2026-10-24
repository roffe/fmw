package main

import (
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/roffe/fmw"
)

// player is an fmw.Video with play/pause, stop, skip, seek, volume and full
// screen controls under it, and a menu on the secondary button. Scrolling
// over it turns the volume up or down, and a double tap toggles full screen,
// where the controls, the window's main menu and the pointer hide while the
// pointer rests on the picture. On a touch screen they hide while a video
// plays untouched, and a tap on the picture shows or hides them.
type player struct {
	widget.BaseWidget
	video *fmw.Video

	play, stop, full *widget.Button
	prev, next       *widget.Button // see setSkip
	onPrev, onNext   func()
	seek, volume     *widget.Slider
	times            *widget.Label
	shownPlaying     bool
	shownFull        bool

	bar        fyne.CanvasObject // the controls
	idle       *time.Timer       // hides them in full screen
	moved      time.Time         // when the pointer last moved over the picture
	overScreen bool              // the pointer is on the picture rather than the controls
	windowMenu *fyne.MainMenu    // the window's, while hidden
}

func newPlayer() *player {
	p := &player{video: fmw.NewVideo()}
	v := p.video
	v.OnChanged = p.changed
	p.play = widget.NewButtonWithIcon("", theme.MediaPlayIcon(), v.TogglePlay)
	p.stop = widget.NewButtonWithIcon("", theme.MediaStopIcon(), v.Stop)
	p.prev = widget.NewButtonWithIcon("", theme.MediaSkipPreviousIcon(), func() { skip(p.onPrev) })
	p.next = widget.NewButtonWithIcon("", theme.MediaSkipNextIcon(), func() { skip(p.onNext) })
	p.prev.Disable()
	p.next.Disable()
	p.seek = widget.NewSlider(0, 1)
	p.seek.Step = 0
	p.seek.OnChanged = func(s float64) {
		p.wake()
		v.Seek(seconds(s))
	}
	p.volume = widget.NewSlider(0, 1)
	p.volume.Step = 0
	p.volume.Value = 1
	p.volume.OnChanged = func(vol float64) {
		p.wake()
		v.SetVolume(vol)
	}
	unfocus := func(float64) { // so keys such as Space reach the window, the sliders do nothing with them
		if c := fyne.CurrentApp().Driver().CanvasForObject(p); c != nil {
			c.Unfocus()
		}
	}
	p.seek.OnChangeEnded, p.volume.OnChangeEnded = unfocus, unfocus
	p.times = widget.NewLabelWithStyle("0:00 / 0:00", fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	p.full = widget.NewButtonWithIcon("", theme.ViewFullScreenIcon(), p.toggleFullScreen)
	p.ExtendBaseWidget(p)
	return p
}

// CreateRenderer is a private method to Fyne which links this widget to its renderer.
func (p *player) CreateRenderer() fyne.WidgetRenderer {
	vol := container.NewGridWrap(fyne.Size{}, p.volume)
	p.bar = container.NewBorder(nil, nil,
		container.NewHBox(p.prev, p.play, p.stop, p.next, container.NewCenter(p.times)),
		container.NewHBox(widget.NewIcon(theme.VolumeUpIcon()), vol, p.full),
		p.seek)
	if fyne.CurrentDevice().IsMobile() {
		p.bar = container.NewThemeOverride(p.bar, touchTheme{})
	}
	vol.Layout = layout.NewGridWrapLayout(fyne.NewSize(100, p.volume.MinSize().Height)) // as themed
	return widget.NewSimpleRenderer(container.NewBorder(nil, p.bar, nil, nil, p.video))
}

// changed puts the video's state on the controls.
func (p *player) changed() {
	v := p.video
	if w := p.window(); w != nil && w.FullScreen() != p.shownFull {
		p.shownFull = !p.shownFull
		if p.shownFull {
			p.full.SetIcon(theme.ViewRestoreIcon())
		} else {
			p.full.SetIcon(theme.ViewFullScreenIcon())
		}
		p.wake() // which in full screen starts the wait to hide the controls
	}
	if playing := v.Playing(); playing != p.shownPlaying {
		p.shownPlaying = playing
		if fyne.CurrentDevice().IsMobile() {
			p.wake() // the controls hide while a video plays, and show when it stops
		}
		if playing {
			p.play.SetIcon(theme.MediaPauseIcon())
		} else {
			p.play.SetIcon(theme.MediaPlayIcon())
		}
	}
	live := v.Live()
	if live != p.seek.Disabled() {
		if live {
			p.seek.Disable()
		} else {
			p.seek.Enable()
		}
	}
	now, dur := v.CurrentTime().Seconds(), v.Duration().Seconds()
	p.seek.Max, p.seek.Value = max(dur, 1), now
	t := clock(now)
	if live {
		p.seek.Value = 0
	} else {
		t += " / " + clock(dur)
	}
	p.seek.Refresh()
	if t != p.times.Text {
		p.times.SetText(t)
	}
	if vol := v.Volume(); vol != p.volume.Value {
		p.volume.Value = vol
		p.volume.Refresh()
	}
}

// Tapped on the picture of a touch screen hides the controls, and elsewhere
// shows them for a while.
func (p *player) Tapped(ev *fyne.PointEvent) {
	if fyne.CurrentDevice().IsMobile() && p.bar.Visible() && ev.Position.Y < p.bar.Position().Y {
		p.showControls(false)
		return
	}
	p.wake()
}

// DoubleTapped toggles full screen.
func (p *player) DoubleTapped(*fyne.PointEvent) {
	p.toggleFullScreen()
}

// MouseIn shows the controls, as moving over the picture does.
func (p *player) MouseIn(*desktop.MouseEvent) {
	p.overScreen = true
	p.wake()
}

// MouseMoved shows the controls, which hide again in full screen once the
// pointer rests on the picture.
func (p *player) MouseMoved(*desktop.MouseEvent) {
	p.overScreen = true
	p.wake()
}

// Cursor hides the pointer while the controls are hidden in full screen.
func (p *player) Cursor() desktop.Cursor {
	if p.bar != nil && !p.bar.Visible() {
		return desktop.HiddenCursor
	}
	return desktop.DefaultCursor
}

// MouseOut keeps the controls showing, as the pointer went to them or away.
func (p *player) MouseOut() {
	p.overScreen = false
}

// Scrolled turns the volume up or down, 2% a notch of a mouse wheel.
func (p *player) Scrolled(ev *fyne.ScrollEvent) {
	const notch = 25 // what fyne scrolls a notch on Linux and Windows
	p.video.SetVolume(min(max(p.video.Volume()+float64(ev.Scrolled.DY)/notch*0.02, 0), 1))
}

// TappedSecondary shows the player's menu.
func (p *player) TappedSecondary(ev *fyne.PointEvent) {
	if c := fyne.CurrentApp().Driver().CanvasForObject(p); c != nil {
		widget.ShowPopUpMenuAtPosition(p.menu(), c, ev.AbsolutePosition)
	}
}

const restDelay = 2 * time.Second

// wake shows the controls and, in full screen or on a touch screen, has them
// hide once the pointer or the finger rests for restDelay.
func (p *player) wake() {
	p.moved = time.Now()
	p.showControls(true)
	if p.idle != nil {
		p.idle.Stop()
	}
	if w := p.window(); fyne.CurrentDevice().IsMobile() || w != nil && w.FullScreen() {
		p.idle = time.AfterFunc(restDelay, func() { fyne.Do(p.rest) })
	}
}

func (p *player) rest() {
	if time.Since(p.moved) < restDelay {
		return
	}
	if fyne.CurrentDevice().IsMobile() {
		if p.video.Playing() && !p.video.Music() {
			p.showControls(false)
		}
	} else if w := p.window(); w != nil && w.FullScreen() && p.overScreen {
		p.showControls(false)
	}
}

// showControls shows or hides the controls and the main menu of the window.
func (p *player) showControls(show bool) {
	if p.bar == nil || p.bar.Visible() == show {
		return
	}
	w := p.window()
	if show {
		p.bar.Show()
		if w != nil && p.windowMenu != nil {
			w.SetMainMenu(p.windowMenu)
		}
		p.windowMenu = nil
	} else {
		p.bar.Hide()
		if w != nil {
			p.windowMenu = w.MainMenu()
			w.SetMainMenu(nil)
		}
	}
	p.Refresh() // as showing or hiding lays out nothing, and showing draws nothing
}

// setSkip gives the button that skips back prev to call and the one that
// skips forward next, as a playlist has entries before and after the one
// playing; a button with nil is disabled. Call it on the main goroutine.
func (p *player) setSkip(prev, next func()) {
	p.onPrev, p.onNext = prev, next
	for _, b := range []struct {
		button *widget.Button
		on     func()
	}{{p.prev, prev}, {p.next, next}} {
		if b.on != nil {
			b.button.Enable()
		} else {
			b.button.Disable()
		}
	}
}

func skip(to func()) {
	if to != nil {
		to()
	}
}

// toggleFullScreen puts the window showing the player in full screen, or
// back from it.
func (p *player) toggleFullScreen() {
	fyne.Do(func() {
		if w := p.window(); w != nil {
			w.SetFullScreen(!w.FullScreen())
			p.changed()
		}
	})
}

// window returns the window showing the player, or nil.
func (p *player) window() fyne.Window {
	c := fyne.CurrentApp().Driver().CanvasForObject(p)
	for _, w := range fyne.CurrentApp().Driver().AllWindows() {
		if w.Canvas() == c {
			return w
		}
	}
	return nil
}

// menu is the player's menu, for things as they are now.
func (p *player) menu() *fyne.Menu {
	v := p.video
	play := fyne.NewMenuItem("Play", v.Play)
	if v.Playing() {
		play = fyne.NewMenuItem("Pause", v.Pause)
	}
	full := fyne.NewMenuItem("Full Screen", p.toggleFullScreen)
	if w := p.window(); w != nil {
		full.Checked = w.FullScreen()
	}
	turn := v.Rotation()
	var turns []*fyne.MenuItem
	for _, t := range []struct {
		label string
		deg   int
	}{{"None", 0}, {"90° Clockwise", 90}, {"180°", 180}, {"90° Counterclockwise", 270}} {
		item := fyne.NewMenuItem(t.label, func() { v.SetRotation(t.deg) })
		item.Checked = t.deg == turn
		turns = append(turns, item)
	}
	rotate := fyne.NewMenuItem("Rotate", nil)
	rotate.ChildMenu = fyne.NewMenu("", turns...)

	names, showing := v.Subtitles()
	off := fyne.NewMenuItem("Off", func() { v.ShowSubtitles(-1) })
	off.Checked = showing < 0
	items := []*fyne.MenuItem{off}
	for i, name := range names {
		item := fyne.NewMenuItem(name, func() { v.ShowSubtitles(i) })
		item.Checked = i == showing
		items = append(items, item)
	}
	subs := fyne.NewMenuItem("Subtitles", nil)
	subs.ChildMenu = fyne.NewMenu("", items...)

	names, playing := v.AudioTracks()
	var tracks []*fyne.MenuItem
	for i, name := range names {
		item := fyne.NewMenuItem(name, func() { v.SetAudioTrack(i) })
		item.Checked = i == playing
		tracks = append(tracks, item)
	}
	audio := fyne.NewMenuItem("Audio", nil)
	audio.ChildMenu, audio.Disabled = fyne.NewMenu("", tracks...), len(tracks) == 0

	menu := fyne.NewMenu("", play, fyne.NewMenuItem("Stop", v.Stop), fyne.NewMenuItemSeparator(), full, rotate, audio, subs)
	if v.ShowsVisualization() {
		menu.Items = append(menu.Items, fyne.NewMenuItem("Next Visualization", v.NextVisualization))
	}
	return menu
}

func seconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

func clock(s float64) string {
	t := int(s)
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
	}
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}

// touchTheme is the app's theme with the inline icons, which size buttons and
// the thumbs of sliders and how tall they are, big enough for a finger. It
// asks the app's settings, as theme.Current is this theme while it renders.
type touchTheme struct{}

func appTheme() fyne.Theme { return fyne.CurrentApp().Settings().Theme() }

func (touchTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	return appTheme().Color(n, v)
}

func (touchTheme) Font(s fyne.TextStyle) fyne.Resource { return appTheme().Font(s) }

func (touchTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return appTheme().Icon(n) }

func (touchTheme) Size(n fyne.ThemeSizeName) float32 {
	if n == theme.SizeNameInlineIcon {
		return 1.6 * appTheme().Size(n)
	}
	return appTheme().Size(n)
}
