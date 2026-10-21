package fmw

import (
	"image"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

const (
	visualFPS = 60
	ringSize  = 1 << 18 // bytes of sound kept, 0.68 s
	feedMax   = 2048 * 8
)

// Visualization is how music without a picture is shown: with MilkDrop
// presets that projectM draws to the sound, in builds with the projectm tag.
// The zero value shows them, at 1920 by 1080, 30 seconds each, from the
// presets that come with fmw or the system's.
type Visualization struct {
	// Off shows the album cover, or nothing, in place of presets.
	Off bool

	// Width and Height are the size presets are drawn at, which is scaled
	// to cover the player, cut at the sides or the top and bottom where its
	// shape differs. Changing it starts the preset over.
	Width, Height int

	// Seconds is how long a preset shows before the next, and HardCuts has
	// a loud beat cut to the next one sooner.
	Seconds  float64
	HardCuts bool

	// Presets is a folder of MilkDrop presets to show, its subfolders too.
	Presets string
}

// HasVisualizations reports whether fmw was built with projectM, which draws
// visualizations.
func HasVisualizations() bool {
	return projectM
}

func (v Visualization) size() (int, int) {
	if v.Width <= 0 || v.Height <= 0 {
		return 1920, 1080
	}
	return v.Width, v.Height
}

func (v Visualization) seconds() float64 {
	if v.Seconds <= 0 {
		return 30
	}
	return v.Seconds
}

// presetDir returns the folder to show presets from: v's, FMW_PRESETS, the
// presets folder next to the executable, as the releases have it, or the
// system's.
func (v Visualization) presetDir() string {
	if v.Presets != "" {
		return v.Presets
	}
	if d := os.Getenv("FMW_PRESETS"); d != "" {
		return d
	}
	if exe, err := os.Executable(); err == nil {
		if d := filepath.Join(filepath.Dir(exe), "presets"); isDir(d) {
			return d
		}
	}
	return "/usr/share/projectM/presets"
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// visualizer shows MilkDrop presets, by projectM, dancing to music that has
// no picture. projectM draws on a thread of its own, from the sound oto
// read, a device buffer late so the beats land as they are heard. Its frames
// stay on the GPU, shown in frame, where they can, or else are read back
// into the raster.
type visualizer struct {
	raster *canvas.Raster
	frame  *canvas.VideoFrame
	heard  func() (int64, bool) // bytes of sound out of the speakers since the last seek, while music plays
	wake   chan struct{}
	skip   atomic.Bool
	failed atomic.Bool
	shared atomic.Bool // frames go to frame
	once   sync.Once

	mu      sync.Mutex
	ring    []byte // the sound oto read, at its offset since the last seek mod ringSize
	cfg     Visualization
	changes int // how many times cfg changed
	w, h    int // pixels the raster was last drawn at
	shown   *image.RGBA
	ready   *image.RGBA // drawn, for the raster's next draw
	pool    []*image.RGBA
}

func newVisualizer(heard func() (int64, bool)) *visualizer {
	v := &visualizer{heard: heard, wake: make(chan struct{}, 1), shown: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	if projectM {
		v.ring = make([]byte, ringSize)
	}
	v.raster = canvas.NewRaster(v.generate)
	v.raster.Hide()
	v.frame = &canvas.VideoFrame{FillMode: canvas.ImageFillCover}
	v.frame.Hide()
	return v
}

func (v *visualizer) configure(cfg Visualization) {
	v.mu.Lock()
	v.cfg = cfg
	v.changes++
	v.mu.Unlock()
}

// write keeps b, the sound oto read at offset at since the last seek.
func (v *visualizer) write(at int64, b []byte) {
	if v.ring == nil {
		return
	}
	v.mu.Lock()
	for len(b) > 0 {
		n := copy(v.ring[at%ringSize:], b)
		b, at = b[n:], at+int64(n)
	}
	v.mu.Unlock()
}

// sound returns in dst the sound heard since offset fed, at most feedMax
// bytes of it, and the offset it ends at.
func (v *visualizer) sound(dst []float32, fed, heard int64) ([]float32, int64) {
	heard &^= 7 // whole stereo frames
	if fed > heard || heard-fed > feedMax {
		fed = heard - feedMax // a seek, or this thread fell behind
	}
	fed = max(fed, 0)
	n := int(max(heard-fed, 0)) / 4
	dst = slices.Grow(dst[:0], n)[:n]
	b := unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(dst))), n*4)
	v.mu.Lock()
	for i := 0; i < len(b); {
		i += copy(b[i:], v.ring[(fed+int64(i))%ringSize:])
	}
	v.mu.Unlock()
	return dst, max(heard, 0)
}

// show shows the visualizer over the picture while music plays, which it
// starts drawing to. Called on the main goroutine.
func (v *visualizer) show(music bool) {
	v.mu.Lock()
	off := v.cfg.Off
	v.mu.Unlock()
	music = music && projectM && !off && !v.failed.Load()
	shared := v.shared.Load()
	showIf(v.raster, music && !shared)
	showIf(v.frame, music && shared)
	if music {
		// ponytail: the thread lives as long as the process and keeps its
		// Player; stop it from a Player.Close if players come and go
		v.once.Do(func() { go v.run() })
		select {
		case v.wake <- struct{}{}:
		default:
		}
	}
}

func showIf(o fyne.CanvasObject, show bool) {
	if show == o.Visible() {
		return
	}
	if show {
		o.Show()
	} else {
		o.Hide()
	}
}

// next cuts to another preset.
func (v *visualizer) next() {
	v.skip.Store(true)
}

func (v *visualizer) run() {
	runtime.LockOSThread() // where projectM's GL context is current
	var (
		m       *milkdrop
		fed     int64
		pcm     []float32
		applied = -1
	)
	tick := time.NewTicker(time.Second / visualFPS)
	for range tick.C {
		heard, ok := v.heard()
		if !ok {
			tick.Stop()
			<-v.wake
			tick.Reset(time.Second / visualFPS)
			continue
		}
		v.mu.Lock()
		cfg, changes, vw, vh := v.cfg, v.changes, v.w, v.h
		v.mu.Unlock()
		var err error
		if m == nil {
			if m, err = newMilkdrop(cfg.size()); err == nil {
				v.shared.Store(m.shared())
			}
		} else if w, h := cfg.size(); changes != applied && (m.w != w || m.h != h) {
			err = m.resize(w, h)
		}
		if err != nil {
			fyne.LogError("fmw: no visualization", err)
			v.failed.Store(true)
			fyne.Do(func() {
				v.raster.Hide()
				v.frame.Hide()
			})
			return
		}
		if changes != applied {
			if err := m.configure(cfg.seconds(), cfg.HardCuts, cfg.presetDir()); err != nil {
				fyne.LogError("fmw: visualization presets", err)
			}
			applied = changes
		}
		if v.skip.Swap(false) {
			m.next()
		}
		pcm, fed = v.sound(pcm, fed, heard)
		if m.shared() {
			m.drawTo(pcm, v.frame)
			continue
		}
		if vw <= 0 || vh <= 0 {
			m.draw(pcm, nil, image.Rectangle{}) // until the raster says how big it is
			continue
		}
		crop := cover(m.w, m.h, vw, vh)
		img := v.take(crop.Dx(), crop.Dy())
		m.draw(pcm, img, crop)
		v.put(img)
		fyne.Do(v.raster.Refresh)
	}
}

// cover returns the middle part of a w by h picture that, scaled, covers a
// view of vw by vh.
func cover(w, h, vw, vh int) image.Rectangle {
	cw, ch := w, h
	if w*vh > vw*h {
		cw = max(1, (h*vw+vh/2)/vh)
	} else {
		ch = max(1, (w*vh+vw/2)/vw)
	}
	x, y := (w-cw)/2, (h-ch)/2
	return image.Rect(x, y, x+cw, y+ch)
}

// take returns an image of w by h pixels the raster does not show.
func (v *visualizer) take(w, h int) *image.RGBA {
	v.mu.Lock()
	defer v.mu.Unlock()
	for len(v.pool) > 0 {
		img := v.pool[len(v.pool)-1]
		v.pool = v.pool[:len(v.pool)-1]
		if img.Rect.Dx() == w && img.Rect.Dy() == h {
			return img
		}
	}
	return image.NewRGBA(image.Rect(0, 0, w, h))
}

// put has the raster draw img next, which replaces a frame drawn before
// that the raster never drew.
func (v *visualizer) put(img *image.RGBA) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ready != nil {
		v.pool = append(v.pool, v.ready)
	}
	v.ready = img
}

// generate hands the painter the latest frame. The one it had before can be
// drawn into again, as the painter uploads what generate returns before it
// calls generate again.
func (v *visualizer) generate(w, h int) image.Image {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.w, v.h = w, h
	if v.ready != nil {
		v.pool = append(v.pool, v.shown)
		v.shown, v.ready = v.ready, nil
	}
	return v.shown
}
