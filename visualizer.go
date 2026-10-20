package fmw

import (
	"image"
	"math"
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

	// ponytail: every frame is read back from the GPU and uploaded again,
	// so frames bigger than this are drawn smaller and scaled up; share the
	// texture with the painter's device to draw them at full size
	visualPixels = 1920 * 1080
)

// visualizer shows MilkDrop presets, by projectM, dancing to music that has
// no picture. projectM draws on a thread of its own, from the sound oto
// read, a device buffer late so the beats land as they are heard.
type visualizer struct {
	raster *canvas.Raster
	heard  func() (int64, bool) // bytes of sound out of the speakers since the last seek, while music plays
	wake   chan struct{}
	skip   atomic.Bool
	failed atomic.Bool
	once   sync.Once

	mu    sync.Mutex
	ring  []byte // the sound oto read, at its offset since the last seek mod ringSize
	w, h  int    // pixels the raster was last drawn at
	shown *image.RGBA
	ready *image.RGBA // drawn, for the raster's next draw
	pool  []*image.RGBA
}

func newVisualizer(heard func() (int64, bool)) *visualizer {
	v := &visualizer{heard: heard, wake: make(chan struct{}, 1), shown: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	if projectM {
		v.ring = make([]byte, ringSize)
	}
	v.raster = canvas.NewRaster(v.generate)
	v.raster.Hide()
	return v
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
	music = music && projectM && !v.failed.Load()
	if music != v.raster.Visible() {
		if music {
			v.raster.Show()
		} else {
			v.raster.Hide()
		}
	}
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

// next cuts to another preset.
func (v *visualizer) next() {
	v.skip.Store(true)
}

func (v *visualizer) run() {
	runtime.LockOSThread() // where projectM's GL context is current
	var (
		m   *milkdrop
		fed int64
		pcm []float32
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
		w, h := v.size()
		if w == 0 || h == 0 {
			continue
		}
		var err error
		if m == nil {
			m, err = newMilkdrop(w, h)
		} else if m.w != w || m.h != h {
			err = m.resize(w, h)
		}
		if err != nil {
			fyne.LogError("fmw: no visualization", err)
			v.failed.Store(true)
			fyne.Do(v.raster.Hide)
			return
		}
		if v.skip.Swap(false) {
			m.next()
		}
		pcm, fed = v.sound(pcm, fed, heard)
		img := v.take(w, h)
		m.draw(pcm, img)
		v.put(img)
		fyne.Do(v.raster.Refresh)
	}
}

// size returns the size to draw frames at: the raster's, unless that has
// more than visualPixels.
func (v *visualizer) size() (int, int) {
	v.mu.Lock()
	w, h := v.w, v.h
	v.mu.Unlock()
	if w*h > visualPixels {
		f := math.Sqrt(visualPixels / float64(w*h))
		w, h = int(float64(w)*f), int(float64(h)*f)
	}
	return w, h
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
