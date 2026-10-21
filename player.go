// Package fmw is a Fyne video player widget. It plays whatever the system's
// FFmpeg libraries (libavformat, libavcodec) open, with sound through oto.
//
// Video decodes on the GPU where FFmpeg and the drivers can (NVDEC, VA-API,
// Vulkan Video, D3D11, VideoToolbox), else on the CPU. The FMW_HWDEC
// environment variable picks the kind of device by FFmpeg's name for it
// (cuda, vaapi, vulkan, d3d11va, videotoolbox, ...), or none.
package fmw

import (
	"errors"
	"fmt"
	"image/color"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/ebitengine/oto/v3"
)

// Player is a widget that shows a video with play/pause, stop, seek, volume
// and full screen controls, subtitles, and a menu on the secondary button.
// Scrolling over it turns the volume up or down, and a double tap toggles
// full screen, where the controls, the window's main menu and the pointer
// hide while the pointer rests on the picture.
// Its methods are safe to call from any goroutine.
type Player struct {
	widget.BaseWidget

	// OnEnded is called on the main goroutine when playback reaches the end
	// of what is loaded, as a playlist moves on to the next file.
	OnEnded func()

	screen           *canvas.VideoFrame
	play, stop, full *widget.Button
	seek, volume     *widget.Slider
	times            *widget.Label
	subs             *fyne.Container // over the screen, see subtitleLayout
	vis              *visualizer
	subLayout        *subtitleLayout
	shownPlaying     bool
	shownFull        bool
	shownSubs        []*subEvent
	shownGeometry    [4]float32 // of the picture the subtitles were laid out over

	// Only the main goroutine uses these.
	bar        fyne.CanvasObject // the controls
	idle       *time.Timer       // hides them in full screen
	moved      time.Time         // when the pointer last moved over the picture
	overScreen bool              // the pointer is on the picture rather than the controls
	windowMenu *fyne.MainMenu    // the window's, while hidden

	loads   atomic.Uint64 // calls of Load and Open, the latest of which wins
	openMu  sync.Mutex
	opening *media // the latest one's, while it opens

	mu      sync.Mutex
	s       *stream
	playing bool
	seekTo  float64 // where a seek that has not landed yet goes, or -1
	busy    bool    // the work goroutine runs
	pos     float64 // the playback clock as of at
	at      time.Time
	heard   float64  // the audio clock when position last read it
	front   *picture // on screen
	back    *picture // next on screen, when fresh
	spare   *picture // the work goroutine takes frames into it
	fresh   bool
	vol     float64
	turn    int    // degrees the picture turns clockwise past how the video says to show it
	hwdec   string // see SetHardwareDecoding
}

// stream is a loaded file and how far its playback got.
type stream struct {
	m        *media
	out      *oto.Player // nil without sound
	vis      *visualizer
	sent     atomic.Int64 // bytes oto has read since the last seek
	start    float64      // stream time of the first of them
	mixed    float64      // seconds of them the device had taken, as position last saw
	read     float64      // the most seconds the device has taken at once
	videoEOF bool
	audioEOF atomic.Bool

	// Only oto's reads use these, and seeks while oto is stopped.
	pcm     []byte  // decoded audio oto has not read yet
	silence int     // bytes of silence to play before pcm
	skipTo  float64 // where the sound starts after a seek, or -1
}

// NewPlayer returns a Player with nothing loaded.
func NewPlayer() *Player {
	p := &Player{vol: 1, seekTo: -1}
	p.screen = &canvas.VideoFrame{FillMode: canvas.ImageFillContain}
	p.front, p.back, p.spare = newPicture(), newPicture(), newPicture()
	runtime.AddCleanup(p, func(pics [3]*picture) {
		for _, pic := range pics {
			pic.free()
		}
	}, [3]*picture{p.front, p.back, p.spare})
	p.play = widget.NewButtonWithIcon("", theme.MediaPlayIcon(), p.TogglePlay)
	p.stop = widget.NewButtonWithIcon("", theme.MediaStopIcon(), p.Stop)
	p.seek = widget.NewSlider(0, 1)
	p.seek.Step = 0
	p.seek.OnChanged = func(v float64) { p.Seek(seconds(v)) }
	p.volume = widget.NewSlider(0, 1)
	p.volume.Step = 0
	p.volume.Value = 1
	p.volume.OnChanged = p.SetVolume
	unfocus := func(float64) { // so keys such as Space reach the window, the sliders do nothing with them
		if c := fyne.CurrentApp().Driver().CanvasForObject(p); c != nil {
			c.Unfocus()
		}
	}
	p.seek.OnChangeEnded, p.volume.OnChangeEnded = unfocus, unfocus
	p.times = widget.NewLabelWithStyle("0:00 / 0:00", fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	p.full = widget.NewButtonWithIcon("", theme.ViewFullScreenIcon(), p.ToggleFullScreen)
	p.subLayout = &subtitleLayout{p: p, box: canvas.NewRectangle(color.NRGBA{A: 160})}
	p.subLayout.box.CornerRadius = 4
	p.subs = container.New(p.subLayout, p.subLayout.box)
	p.vis = newVisualizer(p.played)
	p.ExtendBaseWidget(p)
	return p
}

// CreateRenderer is a private method to Fyne which links this widget to its renderer.
func (p *Player) CreateRenderer() fyne.WidgetRenderer {
	vol := container.NewGridWrap(fyne.NewSize(100, p.volume.MinSize().Height), p.volume)
	p.bar = container.NewBorder(nil, nil,
		container.NewHBox(p.play, p.stop, p.times),
		container.NewHBox(widget.NewIcon(theme.VolumeUpIcon()), vol, p.full),
		p.seek)
	screen := container.NewStack(canvas.NewRectangle(color.Black), p.screen, p.vis.raster, p.vis.frame, p.subs)
	return widget.NewSimpleRenderer(container.NewBorder(nil, p.bar, nil, nil, screen))
}

// DoubleTapped toggles full screen.
func (p *Player) DoubleTapped(*fyne.PointEvent) {
	p.ToggleFullScreen()
}

// MouseIn shows the controls, as moving over the picture does.
func (p *Player) MouseIn(*desktop.MouseEvent) {
	p.overScreen = true
	p.wake()
}

// MouseMoved shows the controls, which hide again in full screen once the
// pointer rests on the picture.
func (p *Player) MouseMoved(*desktop.MouseEvent) {
	p.overScreen = true
	p.wake()
}

// Cursor hides the pointer while the controls are hidden in full screen.
func (p *Player) Cursor() desktop.Cursor {
	if p.bar != nil && !p.bar.Visible() {
		return desktop.HiddenCursor
	}
	return desktop.DefaultCursor
}

// MouseOut keeps the controls showing, as the pointer went to them or away.
func (p *Player) MouseOut() {
	p.overScreen = false
}

const restDelay = 2 * time.Second

// wake shows the controls and, in full screen, has them hide once the pointer
// rests for restDelay.
func (p *Player) wake() {
	p.moved = time.Now()
	p.showControls(true)
	if p.idle != nil {
		p.idle.Stop()
	}
	if w := p.window(); w != nil && w.FullScreen() {
		p.idle = time.AfterFunc(restDelay, func() { fyne.Do(p.rest) })
	}
}

func (p *Player) rest() {
	if w := p.window(); w != nil && w.FullScreen() && p.overScreen && time.Since(p.moved) >= restDelay {
		p.showControls(false)
	}
}

// showControls shows or hides the controls and the main menu of the window.
func (p *Player) showControls(show bool) {
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

// Load opens a file, or a URL that FFmpeg streams, in place of the current one
// and shows its first frame. A stream's server can take its time: a later
// Load or Open interrupts this one, which then fails.
func (p *Player) Load(path string) error {
	return p.load(path, p.loads.Add(1))
}

// Open loads path as Load does, without waiting for it, and calls done, if
// not nil, on the main goroutine with what Load returns, unless a later Load
// or Open came first.
func (p *Player) Open(path string, done func(error)) {
	gen := p.loads.Add(1)
	go func() {
		if err := p.load(path, gen); done != nil && !errors.Is(err, errReplaced) {
			fyne.Do(func() { done(err) })
		}
	}()
}

var errReplaced = errors.New("fmw: another file was loaded meanwhile")

// load loads path for the gen'th call of Load or Open, if no later one came.
func (p *Player) load(path string, gen uint64) error {
	ctx := audioContext()
	p.mu.Lock()
	hwdec := p.hwdec
	p.mu.Unlock()
	var mine *media
	m, err := openMedia(path, ctx != nil, hwdec, func(m *media) {
		p.openMu.Lock()
		defer p.openMu.Unlock()
		mine = m
		if p.loads.Load() != gen {
			m.interrupt()
			return
		}
		if p.opening != nil {
			p.opening.interrupt()
		}
		p.opening = m
	})
	p.openMu.Lock()
	if p.opening == mine {
		p.opening = nil
	}
	p.openMu.Unlock()
	if p.loads.Load() != gen {
		if m != nil {
			m.close()
		}
		return errReplaced
	}
	if err != nil {
		return err
	}
	s := &stream{m: m, skipTo: -1, vis: p.vis}
	if m.audio != nil {
		s.out = ctx.NewPlayer(s)
	}
	shown := false
	for _, sub := range sidecars(path) {
		if n, err := m.addSubtitles(sub); err == nil && !shown {
			m.showSubtitles(n) // put next to the video, so wanted
			shown = true
		}
	}
	runtime.AddCleanup(s, (*media).close, m)

	p.mu.Lock()
	if p.loads.Load() != gen { // a later one came while this one was set up
		p.mu.Unlock()
		if s.out != nil {
			s.out.Close()
		}
		m.close()
		return errReplaced
	}
	paused := p.pause()
	old := p.s
	p.s = s
	if s.out != nil {
		s.out.SetVolume(p.vol)
	}
	p.seekTo, p.turn = 0, 0
	p.kick()
	p.mu.Unlock()
	if paused {
		fyne.Do(p.publish)
	}
	if old != nil {
		old.m.close()
	}
	return nil
}

// Play starts playback, from the start if it has ended.
func (p *Player) Play() {
	p.mu.Lock()
	s := p.s
	if s == nil || p.playing {
		p.mu.Unlock()
		return
	}
	if s.videoEOF && (s.out == nil || s.audioEOF.Load()) {
		p.seekTo = 0
	}
	p.playing, p.at = true, time.Now()
	if s.out != nil && p.seekTo < 0 {
		s.out.Play() // a seek starts it when it lands
	}
	p.kick()
	p.mu.Unlock()
	fyne.Do(p.publish) // runs right here on some drivers, so never under mu
}

// Pause holds playback where it is.
func (p *Player) Pause() {
	p.mu.Lock()
	paused := p.pause()
	p.mu.Unlock()
	if paused {
		fyne.Do(p.publish)
	}
}

// pause holds playback where it is and reports whether it played. Called
// with mu held.
func (p *Player) pause() bool {
	if !p.playing {
		return false
	}
	p.pos = p.position()
	p.playing = false
	if p.s.out != nil {
		p.s.out.PauseAndStopReading() // which also lets oto drop the player
	}
	return true
}

// end pauses s, which played to its end, and calls OnEnded, unless another
// file was loaded or playback paused before either.
func (p *Player) end(s *stream) {
	p.mu.Lock()
	if p.s != s || !p.pause() {
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	fyne.Do(func() {
		p.publish()
		p.mu.Lock()
		same := p.s == s
		p.mu.Unlock()
		if same && p.OnEnded != nil {
			p.OnEnded()
		}
	})
}

// Stop ends playback and goes back to the start.
func (p *Player) Stop() {
	p.Pause()
	p.Seek(0)
}

// Seek moves playback to t. It returns at once and the picture follows when
// the frame at t is decoded.
func (p *Player) Seek(t time.Duration) {
	p.mu.Lock()
	if p.s != nil && !p.s.m.live {
		p.seekTo = max(t.Seconds(), 0)
		if d := p.s.m.dur; d > 0 {
			p.seekTo = min(p.seekTo, d)
		}
		p.kick()
	}
	p.mu.Unlock()
	fyne.Do(p.publish)
}

// AddSubtitles reads a subtitle file, such as SRT, ASS or WebVTT, for the
// video loaded and shows it.
func (p *Player) AddSubtitles(path string) error {
	p.mu.Lock()
	s := p.s
	p.mu.Unlock()
	if s == nil {
		return errors.New("fmw: no video to add subtitles to")
	}
	i, err := s.m.addSubtitles(path)
	if err != nil {
		return err
	}
	p.showSubtitles(i)
	return nil
}

// playAudio switches to audio track i, from where playback is.
func (p *Player) playAudio(i int) {
	p.mu.Lock()
	s := p.s
	p.mu.Unlock()
	if s != nil && s.out != nil && s.m.playAudio(i) {
		p.Seek(p.CurrentTime())
	}
}

func (p *Player) showSubtitles(i int) {
	p.mu.Lock()
	if p.s != nil {
		p.s.m.showSubtitles(i)
	}
	p.mu.Unlock()
	fyne.Do(p.publish)
}

// ToggleFullScreen puts the window showing the player in full screen, or
// back from it.
func (p *Player) ToggleFullScreen() {
	fyne.Do(func() {
		if w := p.window(); w != nil {
			w.SetFullScreen(!w.FullScreen())
			p.publish()
		}
	})
}

// window returns the window showing the player, or nil.
func (p *Player) window() fyne.Window {
	c := fyne.CurrentApp().Driver().CanvasForObject(p)
	for _, w := range fyne.CurrentApp().Driver().AllWindows() {
		if w.Canvas() == c {
			return w
		}
	}
	return nil
}

// TappedSecondary shows the player's menu.
func (p *Player) TappedSecondary(ev *fyne.PointEvent) {
	if c := fyne.CurrentApp().Driver().CanvasForObject(p); c != nil {
		widget.ShowPopUpMenuAtPosition(p.menu(), c, ev.AbsolutePosition)
	}
}

// menu is the player's menu, for things as they are now.
func (p *Player) menu() *fyne.Menu {
	play := fyne.NewMenuItem("Play", p.Play)
	if p.Playing() {
		play = fyne.NewMenuItem("Pause", p.Pause)
	}
	full := fyne.NewMenuItem("Full Screen", p.ToggleFullScreen)
	if w := p.window(); w != nil {
		full.Checked = w.FullScreen()
	}
	p.mu.Lock()
	turn := p.turn
	p.mu.Unlock()
	var turns []*fyne.MenuItem
	for _, t := range []struct {
		label string
		deg   int
	}{{"None", 0}, {"90° Clockwise", 90}, {"180°", 180}, {"90° Counterclockwise", 270}} {
		item := fyne.NewMenuItem(t.label, func() { p.rotate(t.deg) })
		item.Checked = t.deg == turn
		turns = append(turns, item)
	}
	rotate := fyne.NewMenuItem("Rotate", nil)
	rotate.ChildMenu = fyne.NewMenu("", turns...)
	off := fyne.NewMenuItem("Off", func() { p.showSubtitles(-1) })
	items := []*fyne.MenuItem{off}
	var tracks []*fyne.MenuItem
	p.mu.Lock()
	s := p.s
	p.mu.Unlock()
	if s != nil {
		names, showing := s.m.subtitles()
		off.Checked = showing < 0
		for i, name := range names {
			item := fyne.NewMenuItem(name, func() { p.showSubtitles(i) })
			item.Checked = i == showing
			items = append(items, item)
		}
		if s.out != nil {
			names, playing := s.m.audioTracks()
			for i, name := range names {
				item := fyne.NewMenuItem(name, func() { p.playAudio(i) })
				item.Checked = i == playing
				tracks = append(tracks, item)
			}
		}
	}
	audio := fyne.NewMenuItem("Audio", nil)
	audio.ChildMenu, audio.Disabled = fyne.NewMenu("", tracks...), len(tracks) == 0
	subs := fyne.NewMenuItem("Subtitles", nil)
	subs.ChildMenu = fyne.NewMenu("", items...)
	menu := fyne.NewMenu("", play, fyne.NewMenuItem("Stop", p.Stop), fyne.NewMenuItemSeparator(), full, rotate, audio, subs)
	if p.vis.showing() {
		menu.Items = append(menu.Items, fyne.NewMenuItem("Next Visualization", p.vis.next))
	}
	return menu
}

// rotate turns the picture deg degrees clockwise past how the video says to
// show it. Called on the main goroutine.
func (p *Player) rotate(deg int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.turn = deg
	p.front.show(p.screen, deg)
	p.screen.Refresh()
}

// Scrolled turns the volume up or down, 2% a notch of a mouse wheel.
func (p *Player) Scrolled(ev *fyne.ScrollEvent) {
	const notch = 25 // what fyne scrolls a notch on Linux and Windows
	p.SetVolume(min(max(p.Volume()+float64(ev.Scrolled.DY)/notch*0.02, 0), 1))
}

// SetVolume sets the audio volume, from 0 for silent to 1 for as recorded.
func (p *Player) SetVolume(v float64) {
	p.mu.Lock()
	p.vol = v
	if p.s != nil && p.s.out != nil {
		p.s.out.SetVolume(v)
	}
	p.mu.Unlock()
	fyne.Do(func() {
		p.volume.Value = v
		p.volume.Refresh()
	})
}

// SetVisualization sets how music without a picture is shown.
func (p *Player) SetVisualization(v Visualization) {
	p.vis.configure(v)
	fyne.Do(p.publish)
}

// SetHardwareDecoding picks the kind of device the videos loaded from now on
// decode on, by FFmpeg's name for it as HardwareDecoders lists them, or none
// for the CPU. Empty picks the best there is, unless FMW_HWDEC names one.
func (p *Player) SetHardwareDecoding(kind string) {
	p.mu.Lock()
	p.hwdec = kind
	p.mu.Unlock()
}

// Volume returns the audio volume, from 0 for silent to 1 for as recorded.
func (p *Player) Volume() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.vol
}

// CurrentTime returns how far into the video playback is.
func (p *Player) CurrentTime() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return seconds(p.now())
}

// Duration returns the length of the video, 0 if it is not known.
func (p *Player) Duration() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.s == nil {
		return 0
	}
	return seconds(p.s.m.dur)
}

// Playing reports whether the video is playing.
func (p *Player) Playing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing
}

// TogglePlay pauses the video if it is playing and plays it otherwise.
func (p *Player) TogglePlay() {
	if p.Playing() {
		p.Pause()
	} else {
		p.Play()
	}
}

// kick starts the work goroutine unless it runs. Called with mu held.
func (p *Player) kick() {
	if !p.busy {
		p.busy = true
		go p.work()
	}
}

// work carries out seeks and plays until there is neither to do, keeping
// decoding off the main goroutine.
func (p *Player) work() {
	for {
		p.mu.Lock()
		s, to, spare := p.s, p.seekTo, p.spare
		if to < 0 && !p.playing {
			p.busy = false
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()
		if to >= 0 {
			p.land(s, to, spare)
		} else if wait, ended := p.advance(s, spare); ended {
			p.end(s)
		} else {
			// shown now, as it is due, not after the wait for the next one,
			// which would take its place before the screen had it
			fyne.Do(p.publish)
			time.Sleep(wait)
			continue
		}
		fyne.Do(p.publish)
	}
}

// land seeks s to the frame showing at t, or to wherever it got when a newer
// seek comes in.
func (p *Player) land(s *stream, t float64, spare *picture) {
	if s.out != nil {
		s.out.PauseAndStopReading()
		_, _ = s.out.Seek(0, io.SeekStart) // drops what oto buffered
	}
	s.m.seek(t)
	ft, ok := s.m.next()
	for back := 1.0; t > 0 && (!ok || ft > t+0.001) && back <= 16; back *= 2 {
		at := max(t-back, 0)
		s.m.seek(at) // without an index a seek can land after the key frame
		ft, ok = s.m.next()
		if at == 0 {
			break
		}
	}
	for ok && ft <= t+0.001 && !p.superseded(t) {
		s.m.take(nil)
		ft, ok = s.m.next()
	}
	shown := s.m.show(spare)
	if !shown && ok { // t comes before the first frame
		s.m.take(spare)
		shown = true
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.s != s {
		return
	}
	if shown {
		p.spare, p.back, p.fresh = p.back, spare, true
	}
	if p.seekTo == t {
		p.seekTo = -1
	}
	p.pos, p.at, p.heard = t, time.Now(), t-deviceBuffer.Seconds()
	s.start, s.videoEOF = t, !ok
	s.sent.Store(0)
	s.mixed = 0
	s.audioEOF.Store(false)
	s.pcm, s.silence, s.skipTo = nil, 0, t
	if s.out != nil && p.playing && p.seekTo < 0 {
		s.out.Play()
	}
}

func (p *Player) superseded(t float64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seekTo != t
}

// advance shows the frames that are due and returns how long until the next
// one, or that the video and its sound have run out.
func (p *Player) advance(s *stream, spare *picture) (time.Duration, bool) {
	p.mu.Lock()
	now, videoEOF := p.position(), s.videoEOF
	p.mu.Unlock()
	next, shown := now+0.05, false
	for !videoEOF {
		t, ok := s.m.next()
		if !ok {
			videoEOF = true
		} else if t > now {
			next = t
			break
		} else {
			s.m.take(spare)
			shown = true
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.s != s || p.seekTo >= 0 {
		return 0, false // a load or seek came in meanwhile
	}
	if shown {
		p.spare, p.back, p.fresh = p.back, spare, true
	}
	s.videoEOF = videoEOF
	ended := videoEOF && (s.out == nil || s.audioEOF.Load() && s.out.BufferedSize() == 0)
	return min(max(seconds(next-now), time.Millisecond), 50*time.Millisecond), ended
}

// now is the playback position, or where a seek that has not landed goes.
// Called with mu held.
func (p *Player) now() float64 {
	if p.seekTo >= 0 {
		return p.seekTo
	}
	return p.position()
}

// position returns the playback clock: the audio heard so far while there is
// audio, the wall clock otherwise. The audio clock only moves when the device
// takes a chunk, so in between it runs on wall time, stopping a device buffer,
// or the biggest chunk, ahead if the audio stalls. Called with mu held.
func (p *Player) position() float64 {
	if !p.playing {
		return p.pos
	}
	now := time.Now()
	p.pos += now.Sub(p.at).Seconds()
	p.at = now
	if s := p.s; s.out != nil && !s.audioEOF.Load() {
		mixed := float64(int(s.sent.Load())-s.out.BufferedSize()) / (sampleRate * 8)
		if mixed != s.mixed {
			s.read = max(s.read, mixed-s.mixed)
			s.mixed = mixed
		}
		if heard := s.start + mixed - latency(s); heard != p.heard {
			p.pos, p.heard = heard, heard
		}
		p.pos = min(p.pos, p.heard+max(deviceBuffer.Seconds(), s.read))
	}
	return p.pos
}

// played returns how many bytes of sound since the last seek have come out of
// the speakers, while music without a picture plays.
func (p *Player) played() (int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.s
	if s == nil || s.out == nil || !p.playing || !s.m.music() {
		return 0, false
	}
	return s.sent.Load() - int64(s.out.BufferedSize()) - int64(latency(s)*sampleRate)*8, true
}

// latency is how long the sound s mixes takes to be heard.
func latency(s *stream) float64 {
	return deviceBuffer.Seconds() + queuedReads*s.read
}

// publish puts the latest frame and the playback state on screen. The work
// goroutine only takes frames into spare, and the painter only reads front's
// planes on the main goroutine, so decoding never races with an upload.
func (p *Player) publish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fresh {
		p.front, p.back, p.fresh = p.back, p.front, false
		p.front.show(p.screen, p.turn)
		p.screen.Refresh()
	} else if p.s != nil && p.s.m.video == nil && p.screen.Width != 0 { // sound only
		p.screen.Width = 0
		p.screen.Refresh()
	}
	p.vis.show(p.s != nil && p.s.m.music())
	if w := p.window(); w != nil && w.FullScreen() != p.shownFull {
		p.shownFull = !p.shownFull
		if p.shownFull {
			p.full.SetIcon(theme.ViewRestoreIcon())
		} else {
			p.full.SetIcon(theme.ViewFullScreenIcon())
		}
		p.wake() // which in full screen starts the wait to hide the controls
	}
	now, dur := p.now(), 0.0
	if p.s != nil {
		dur = p.s.m.dur
	}
	if p.playing != p.shownPlaying {
		p.shownPlaying = p.playing
		if p.playing {
			p.play.SetIcon(theme.MediaPauseIcon())
		} else {
			p.play.SetIcon(theme.MediaPlayIcon())
		}
	}
	p.updateSubtitles(now)
	live := p.s != nil && p.s.m.live
	if live != p.seek.Disabled() {
		if live {
			p.seek.Disable()
		} else {
			p.seek.Enable()
		}
	}
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
