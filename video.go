// Package fmw is a Fyne video widget. It plays whatever the system's FFmpeg
// libraries (libavformat, libavcodec) open, with sound through oto.
//
// Video decodes on the GPU where FFmpeg and the drivers can (NVDEC, VA-API,
// Vulkan Video, D3D11, VideoToolbox), else on the CPU. The FMW_HWDEC
// environment variable picks the kind of device by FFmpeg's name for it
// (cuda, vaapi, vulkan, d3d11va, videotoolbox, ...), or none.
package fmw

import (
	"errors"
	"image/color"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/ebitengine/oto/v3"
)

// Video is a widget that shows a video, its subtitles, and a visualization
// for music without a picture. It has no controls and takes no input: an app
// lays its own over or around it and drives it through its methods, which are
// safe to call from any goroutine.
type Video struct {
	widget.BaseWidget

	// OnEnded is called on the main goroutine when playback reaches the end
	// of what is loaded, as a playlist moves on to the next file.
	OnEnded func()
	// OnEmpty is called on the main goroutine when Play is asked with nothing
	// loaded, as a playlist starts its next file.
	OnEmpty func()
	// OnChanged is called on the main goroutine when what the video shows
	// changes: a new frame, playback starting or pausing, a seek, a load, the
	// volume. Controls update from it.
	OnChanged func()

	screen        *canvas.VideoFrame
	subs          *fyne.Container // over the screen, see subtitleLayout
	vis           *visualizer
	subLayout     *subtitleLayout
	shownSubs     []*subEvent
	shownGeometry [4]float32 // of the picture the subtitles were laid out over

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
	queued  atomic.Bool // a publish is on its way to the main goroutine
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

// NewVideo returns a Video with nothing loaded.
func NewVideo() *Video {
	v := &Video{vol: 1, seekTo: -1}
	v.screen = &canvas.VideoFrame{FillMode: canvas.ImageFillContain}
	v.front, v.back, v.spare = newPicture(), newPicture(), newPicture()
	runtime.AddCleanup(v, func(pics [3]*picture) {
		for _, pic := range pics {
			pic.free()
		}
	}, [3]*picture{v.front, v.back, v.spare})
	v.subLayout = &subtitleLayout{v: v, box: canvas.NewRectangle(color.NRGBA{A: 160})}
	v.subLayout.box.CornerRadius = 4
	v.subs = container.New(v.subLayout, v.subLayout.box)
	v.vis = newVisualizer(v.played)
	v.ExtendBaseWidget(v)
	return v
}

// CreateRenderer is a private method to Fyne which links this widget to its renderer.
func (v *Video) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(canvas.NewRectangle(color.Black), v.screen, v.vis.raster, v.vis.frame, v.subs))
}

// Load opens a file, or a URL that FFmpeg streams, in place of the current one
// and shows its first frame. A stream's server can take its time: a later
// Load or Open interrupts this one, which then fails.
func (v *Video) Load(path string) error {
	return v.load(path, v.loads.Add(1))
}

// Open loads path as Load does, without waiting for it, and calls done, if
// not nil, on the main goroutine with what Load returns, unless a later Load
// or Open came first.
func (v *Video) Open(path string, done func(error)) {
	gen := v.loads.Add(1)
	go func() {
		if err := v.load(path, gen); done != nil && !errors.Is(err, errReplaced) {
			fyne.Do(func() { done(err) })
		}
	}()
}

var errReplaced = errors.New("fmw: another file was loaded meanwhile")

// load loads path for the gen'th call of Load or Open, if no later one came.
func (v *Video) load(path string, gen uint64) error {
	ctx := audioContext()
	v.mu.Lock()
	hwdec := v.hwdec
	v.mu.Unlock()
	var mine *media
	m, err := openMedia(path, ctx != nil, hwdec, func(m *media) {
		v.openMu.Lock()
		defer v.openMu.Unlock()
		mine = m
		if v.loads.Load() != gen {
			m.interrupt()
			return
		}
		if v.opening != nil {
			v.opening.interrupt()
		}
		v.opening = m
	})
	v.openMu.Lock()
	if v.opening == mine {
		v.opening = nil
	}
	v.openMu.Unlock()
	if v.loads.Load() != gen {
		if m != nil {
			m.close()
		}
		return errReplaced
	}
	if err != nil {
		return err
	}
	s := &stream{m: m, skipTo: -1, vis: v.vis}
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

	v.mu.Lock()
	if v.loads.Load() != gen { // a later one came while this one was set up
		v.mu.Unlock()
		if s.out != nil {
			s.out.Close()
		}
		m.close()
		return errReplaced
	}
	paused := v.pause()
	old := v.s
	v.s = s
	if s.out != nil {
		s.out.SetVolume(v.vol)
	}
	v.seekTo, v.turn = 0, 0
	v.kick()
	v.mu.Unlock()
	if paused {
		fyne.Do(v.publish)
	}
	if old != nil {
		old.m.close()
	}
	return nil
}

// Play starts playback, from the start if it has ended.
func (v *Video) Play() {
	v.mu.Lock()
	s := v.s
	if s == nil {
		v.mu.Unlock()
		if v.OnEmpty != nil {
			fyne.Do(v.OnEmpty) // runs right here on some drivers, so never under mu
		}
		return
	}
	if v.playing {
		v.mu.Unlock()
		return
	}
	if s.videoEOF && (s.out == nil || s.audioEOF.Load()) {
		v.seekTo = 0
	}
	v.playing, v.at = true, time.Now()
	if s.out != nil && v.seekTo < 0 {
		s.out.Play() // a seek starts it when it lands
	}
	v.kick()
	v.mu.Unlock()
	fyne.Do(v.publish) // runs right here on some drivers, so never under mu
}

// Pause holds playback where it is.
func (v *Video) Pause() {
	v.mu.Lock()
	paused := v.pause()
	v.mu.Unlock()
	if paused {
		fyne.Do(v.publish)
	}
}

// pause holds playback where it is and reports whether it played. Called
// with mu held.
func (v *Video) pause() bool {
	if !v.playing {
		return false
	}
	v.pos = v.position()
	v.playing = false
	if v.s.out != nil {
		v.s.out.PauseAndStopReading() // which also lets oto drop the player
	}
	return true
}

// end pauses s, which played to its end, and calls OnEnded, unless another
// file was loaded or playback paused before either.
func (v *Video) end(s *stream) {
	v.mu.Lock()
	if v.s != s || !v.pause() {
		v.mu.Unlock()
		return
	}
	v.mu.Unlock()
	fyne.Do(func() {
		v.publish()
		v.mu.Lock()
		same := v.s == s
		v.mu.Unlock()
		if same && v.OnEnded != nil {
			v.OnEnded()
		}
	})
}

// Stop ends playback and goes back to the start.
func (v *Video) Stop() {
	v.Pause()
	v.Seek(0)
}

// Seek moves playback to t. It returns at once and the picture follows when
// the frame at t is decoded.
func (v *Video) Seek(t time.Duration) {
	v.mu.Lock()
	if v.s != nil && !v.s.m.live {
		v.seekTo = max(t.Seconds(), 0)
		if d := v.s.m.dur; d > 0 {
			v.seekTo = min(v.seekTo, d)
		}
		v.kick()
	}
	v.mu.Unlock()
	fyne.Do(v.publish)
}

// AddSubtitles reads a subtitle file, such as SRT, ASS or WebVTT, for the
// video loaded and shows it.
func (v *Video) AddSubtitles(path string) error {
	v.mu.Lock()
	s := v.s
	v.mu.Unlock()
	if s == nil {
		return errors.New("fmw: no video to add subtitles to")
	}
	i, err := s.m.addSubtitles(path)
	if err != nil {
		return err
	}
	v.ShowSubtitles(i)
	return nil
}

// AudioTracks returns the names of the audio tracks of what is loaded and
// which of them plays, none without a sound device.
func (v *Video) AudioTracks() (names []string, playing int) {
	v.mu.Lock()
	s := v.s
	v.mu.Unlock()
	if s == nil || s.out == nil {
		return nil, -1
	}
	return s.m.audioTracks()
}

// SetAudioTrack switches to audio track i, from where playback is.
func (v *Video) SetAudioTrack(i int) {
	v.mu.Lock()
	s := v.s
	v.mu.Unlock()
	if s != nil && s.out != nil && s.m.playAudio(i) {
		v.Seek(v.CurrentTime())
	}
}

// Subtitles returns the names of the subtitle tracks of what is loaded,
// AddSubtitles' included, and which of them shows, or -1.
func (v *Video) Subtitles() (names []string, showing int) {
	v.mu.Lock()
	s := v.s
	v.mu.Unlock()
	if s == nil {
		return nil, -1
	}
	return s.m.subtitles()
}

// ShowSubtitles shows subtitle track i, or none for -1.
func (v *Video) ShowSubtitles(i int) {
	v.mu.Lock()
	if v.s != nil {
		v.s.m.showSubtitles(i)
	}
	v.mu.Unlock()
	fyne.Do(v.publish)
}

// Rotation returns how many degrees clockwise the picture turns past how the
// video says to show it.
func (v *Video) Rotation() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.turn
}

// SetRotation turns the picture deg degrees clockwise, 0, 90, 180 or 270,
// past how the video says to show it, until the next Load.
func (v *Video) SetRotation(deg int) {
	fyne.Do(func() {
		v.mu.Lock()
		v.turn = deg
		v.front.show(v.screen, deg)
		v.mu.Unlock()
		v.screen.Refresh()
		v.publish()
	})
}

// ShowsVisualization reports whether a visualization shows, for music
// without a picture.
func (v *Video) ShowsVisualization() bool {
	return v.vis.showing()
}

// NextVisualization moves the visualization on to another preset.
func (v *Video) NextVisualization() {
	v.vis.next()
}

// SetVolume sets the audio volume, from 0 for silent to 1 for as recorded.
func (v *Video) SetVolume(vol float64) {
	v.mu.Lock()
	v.vol = vol
	if v.s != nil && v.s.out != nil {
		v.s.out.SetVolume(vol)
	}
	v.mu.Unlock()
	fyne.Do(v.publish)
}

// SetVisualization sets how music without a picture is shown.
func (v *Video) SetVisualization(vis Visualization) {
	v.vis.configure(vis)
	fyne.Do(v.publish)
}

// SetHardwareDecoding picks the kind of device the videos loaded from now on
// decode on, by FFmpeg's name for it as HardwareDecoders lists them, or none
// for the CPU. Empty picks the best there is, unless FMW_HWDEC names one.
func (v *Video) SetHardwareDecoding(kind string) {
	v.mu.Lock()
	v.hwdec = kind
	v.mu.Unlock()
}

// Volume returns the audio volume, from 0 for silent to 1 for as recorded.
func (v *Video) Volume() float64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.vol
}

// CurrentTime returns how far into the video playback is.
func (v *Video) CurrentTime() time.Duration {
	v.mu.Lock()
	defer v.mu.Unlock()
	return seconds(v.now())
}

// Duration returns the length of the video, 0 if it is not known.
func (v *Video) Duration() time.Duration {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.s == nil {
		return 0
	}
	return seconds(v.s.m.dur)
}

// Playing reports whether the video is playing.
func (v *Video) Playing() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.playing
}

// Music reports whether what is loaded is sound without a picture of its own,
// which shows a visualization, or its cover.
func (v *Video) Music() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.s != nil && v.s.m.music()
}

// Live reports whether what is loaded is a live stream, one without an end,
// which plays without seeking.
func (v *Video) Live() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.s != nil && v.s.m.live
}

// TogglePlay pauses the video if it is playing and plays it otherwise.
func (v *Video) TogglePlay() {
	if v.Playing() {
		v.Pause()
	} else {
		v.Play()
	}
}

// kick starts the work goroutine unless it runs. Called with mu held.
func (v *Video) kick() {
	if !v.busy {
		v.busy = true
		go v.work()
	}
}

// work carries out seeks and plays until there is neither to do, keeping
// decoding off the main goroutine.
func (v *Video) work() {
	for {
		v.mu.Lock()
		s, to, spare := v.s, v.seekTo, v.spare
		if to < 0 && !v.playing {
			v.busy = false
			v.mu.Unlock()
			return
		}
		v.mu.Unlock()
		if to >= 0 {
			v.land(s, to, spare)
		} else if wait, ended := v.advance(s, spare); ended {
			v.end(s)
		} else {
			// shown now, as it is due, not after the wait for the next one,
			// which would take its place before the screen had it
			v.show()
			time.Sleep(wait)
			continue
		}
		v.show()
	}
}

// show runs publish on the main goroutine once, however many times it is
// asked before publish gets there, so a main loop that stalls, as while a
// window edge is dragged, is left one publish to catch up with, not one a
// frame.
func (v *Video) show() {
	if v.queued.CompareAndSwap(false, true) {
		fyne.Do(func() {
			v.queued.Store(false)
			v.publish()
		})
	}
}

// land seeks s to the frame showing at t, or to wherever it got when a newer
// seek comes in.
func (v *Video) land(s *stream, t float64, spare *picture) {
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
	for ok && ft <= t+0.001 && !v.superseded(t) {
		s.m.take(nil)
		ft, ok = s.m.next()
	}
	shown := s.m.show(spare)
	if !shown && ok { // t comes before the first frame
		s.m.take(spare)
		shown = true
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.s != s {
		return
	}
	if shown {
		v.spare, v.back, v.fresh = v.back, spare, true
	}
	if v.seekTo == t {
		v.seekTo = -1
	}
	v.pos, v.at, v.heard = t, time.Now(), t-deviceBuffer.Seconds()
	s.start, s.videoEOF = t, !ok
	s.sent.Store(0)
	s.mixed = 0
	s.audioEOF.Store(false)
	s.pcm, s.silence, s.skipTo = nil, 0, t
	if s.out != nil && v.playing && v.seekTo < 0 {
		s.out.Play()
	}
}

func (v *Video) superseded(t float64) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.seekTo != t
}

// advance shows the frames that are due and returns how long until the next
// one, or that the video and its sound have run out.
func (v *Video) advance(s *stream, spare *picture) (time.Duration, bool) {
	v.mu.Lock()
	now, videoEOF := v.position(), s.videoEOF
	v.mu.Unlock()
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

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.s != s || v.seekTo >= 0 {
		return 0, false // a load or seek came in meanwhile
	}
	if shown {
		v.spare, v.back, v.fresh = v.back, spare, true
	}
	s.videoEOF = videoEOF
	ended := videoEOF && (s.out == nil || s.audioEOF.Load() && s.out.BufferedSize() == 0)
	return min(max(seconds(next-now), time.Millisecond), 50*time.Millisecond), ended
}

// now is the playback position, or where a seek that has not landed goes.
// Called with mu held.
func (v *Video) now() float64 {
	if v.seekTo >= 0 {
		return v.seekTo
	}
	return v.position()
}

// position returns the playback clock: the audio heard so far while there is
// audio, the wall clock otherwise. The audio clock only moves when the device
// takes a chunk, so in between it runs on wall time, stopping a device buffer,
// or the biggest chunk, ahead if the audio stalls. Called with mu held.
func (v *Video) position() float64 {
	if !v.playing {
		return v.pos
	}
	now := time.Now()
	v.pos += now.Sub(v.at).Seconds()
	v.at = now
	if s := v.s; s.out != nil && !s.audioEOF.Load() {
		mixed := float64(int(s.sent.Load())-s.out.BufferedSize()) / (sampleRate * 8)
		if mixed != s.mixed {
			s.read = max(s.read, mixed-s.mixed)
			s.mixed = mixed
		}
		if heard := s.start + mixed - latency(s); heard != v.heard {
			v.pos, v.heard = heard, heard
		}
		v.pos = min(v.pos, v.heard+max(deviceBuffer.Seconds(), s.read))
	}
	return v.pos
}

// played returns how many bytes of sound since the last seek have come out of
// the speakers, while music without a picture plays.
func (v *Video) played() (int64, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s := v.s
	if s == nil || s.out == nil || !v.playing || !s.m.music() {
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
func (v *Video) publish() {
	v.mu.Lock()
	if v.fresh {
		v.front, v.back, v.fresh = v.back, v.front, false
		v.front.show(v.screen, v.turn)
		v.screen.Refresh()
	} else if v.s != nil && v.s.m.video == nil && v.screen.Width != 0 { // sound only
		v.screen.Width = 0
		v.screen.Refresh()
	}
	v.vis.show(v.s != nil && v.s.m.music())
	v.updateSubtitles(v.now())
	v.mu.Unlock()
	if v.OnChanged != nil {
		v.OnChanged()
	}
}

func seconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}
