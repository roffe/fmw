// Package fmw is a Fyne video player widget. It plays whatever the system's
// FFmpeg libraries (libavformat, libavcodec) open, with sound through oto.
//
// Video decodes on the GPU where FFmpeg and the drivers can (NVDEC, VA-API,
// Vulkan Video, D3D11, VideoToolbox), else on the CPU. The FMW_HWDEC
// environment variable picks the kind of device by FFmpeg's name for it
// (cuda, vaapi, vulkan, d3d11va, videotoolbox, ...), or none.
package fmw

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/ebitengine/oto/v3"
)

// Player is a widget that shows a video with play/pause, stop, seek and volume
// controls. Its methods are safe to call from any goroutine.
type Player struct {
	widget.BaseWidget

	screen       *canvas.Image
	play, stop   *widget.Button
	seek, volume *widget.Slider
	times        *widget.Label
	shownPlaying bool

	mu      sync.Mutex
	s       *stream
	playing bool
	seekTo  float64 // where a seek that has not landed yet goes, or -1
	busy    bool    // the work goroutine runs
	pos     float64 // the playback clock as of at
	at      time.Time
	heard   float64     // the audio clock when position last read it
	front   *image.RGBA // on screen
	back    *image.RGBA // next on screen, when fresh
	spare   *image.RGBA // the work goroutine draws here
	fresh   bool
	vol     float64
}

// stream is a loaded file and how far its playback got.
type stream struct {
	m        *media
	out      *oto.Player  // nil without sound
	sent     atomic.Int64 // bytes oto has read since the last seek
	start    float64      // stream time of the first of them
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
	p.screen = &canvas.Image{FillMode: canvas.ImageFillContain, ScaleMode: canvas.ImageScaleFastest}
	p.play = widget.NewButtonWithIcon("", theme.MediaPlayIcon(), p.toggle)
	p.stop = widget.NewButtonWithIcon("", theme.MediaStopIcon(), p.Stop)
	p.seek = widget.NewSlider(0, 1)
	p.seek.Step = 0
	p.seek.OnChanged = func(v float64) { p.Seek(seconds(v)) }
	p.volume = widget.NewSlider(0, 1)
	p.volume.Step = 0
	p.volume.Value = 1
	p.volume.OnChanged = p.SetVolume
	p.times = widget.NewLabelWithStyle("0:00 / 0:00", fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	p.ExtendBaseWidget(p)
	return p
}

// CreateRenderer is a private method to Fyne which links this widget to its renderer.
func (p *Player) CreateRenderer() fyne.WidgetRenderer {
	vol := container.NewGridWrap(fyne.NewSize(100, p.volume.MinSize().Height), p.volume)
	bar := container.NewBorder(nil, nil,
		container.NewHBox(p.play, p.stop, p.times),
		container.NewHBox(widget.NewIcon(theme.VolumeUpIcon()), vol),
		p.seek)
	screen := container.NewStack(canvas.NewRectangle(color.Black), p.screen)
	return widget.NewSimpleRenderer(container.NewBorder(nil, bar, nil, nil, screen))
}

// Load opens a file, or a URL that FFmpeg streams, in place of the current one
// and shows its first frame.
func (p *Player) Load(path string) error {
	ctx := audioContext()
	m, err := openMedia(path, ctx != nil)
	if err != nil {
		return err
	}
	s := &stream{m: m, skipTo: -1}
	if m.audio != nil {
		s.out = ctx.NewPlayer(s)
	}
	runtime.AddCleanup(s, (*media).close, m)

	p.Pause()
	p.mu.Lock()
	old := p.s
	p.s = s
	if s.out != nil {
		s.out.SetVolume(p.vol)
	}
	p.front, p.back, p.spare = nil, nil, nil
	if m.video != nil {
		r := image.Rect(0, 0, m.w, m.h)
		p.front, p.back, p.spare = image.NewRGBA(r), image.NewRGBA(r), image.NewRGBA(r)
	}
	p.seekTo = 0
	p.kick()
	p.mu.Unlock()
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
	if !p.playing {
		p.mu.Unlock()
		return
	}
	p.pos = p.position()
	p.playing = false
	if p.s.out != nil {
		p.s.out.PauseAndStopReading() // which also lets oto drop the player
	}
	p.mu.Unlock()
	fyne.Do(p.publish)
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
	if p.s != nil {
		p.seekTo = max(t.Seconds(), 0)
		if d := p.s.m.dur; d > 0 {
			p.seekTo = min(p.seekTo, d)
		}
		p.kick()
	}
	p.mu.Unlock()
	fyne.Do(p.publish)
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

func (p *Player) toggle() {
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
			p.Pause()
		} else {
			time.Sleep(wait)
		}
		fyne.Do(p.publish)
	}
}

// land seeks s to the frame showing at t, or to wherever it got when a newer
// seek comes in.
func (p *Player) land(s *stream, t float64, spare *image.RGBA) {
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
func (p *Player) advance(s *stream, spare *image.RGBA) (time.Duration, bool) {
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
// takes a chunk, so in between it runs on wall time, stopping a device buffer
// ahead if the audio stalls. Called with mu held.
func (p *Player) position() float64 {
	if !p.playing {
		return p.pos
	}
	now := time.Now()
	p.pos += now.Sub(p.at).Seconds()
	p.at = now
	if s := p.s; s.out != nil && !s.audioEOF.Load() {
		mixed := float64(int(s.sent.Load())-s.out.BufferedSize()) / (sampleRate * 8)
		if heard := s.start + mixed - deviceBuffer.Seconds(); heard != p.heard {
			p.pos, p.heard = heard, heard
		}
		p.pos = min(p.pos, p.heard+deviceBuffer.Seconds())
	}
	return p.pos
}

// publish puts the latest frame and the playback state on screen. The work
// goroutine only draws into spare, and the painter only reads front on the
// main goroutine, so decoding never races with an upload.
func (p *Player) publish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fresh {
		p.front, p.back, p.fresh = p.back, p.front, false
		resized := p.screen.Image == nil || p.screen.Image.Bounds() != p.front.Bounds()
		p.screen.Image = p.front
		if resized {
			p.screen.Refresh()
		} else {
			p.screen.RefreshRegion(p.front.Rect)
		}
	} else if p.front == nil && p.screen.Image != nil { // sound only
		p.screen.Image = nil
		p.screen.Refresh()
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
	p.seek.Max, p.seek.Value = max(dur, 1), now
	p.seek.Refresh()
	if t := clock(now) + " / " + clock(dur); t != p.times.Text {
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
