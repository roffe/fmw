//go:build (linux || windows) && projectm

package fmw

/*
#cgo LDFLAGS: -lprojectM-4-playlist -lprojectM-4
#cgo linux LDFLAGS: -lEGL
#cgo android LDFLAGS: -lGLESv3
#cgo windows LDFLAGS: -lglew32 -lopengl32 -lgdi32 -luser32
#include <stdlib.h>
#include <projectM-4/projectM.h>
#include <projectM-4/playlist.h>
#include "milkdrop.h"
*/
import "C"

import (
	"errors"
	"image"
	"path/filepath"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

const projectM = true

// milkdrop is projectM drawing its presets in shuffled order. Only the
// thread that made it may use it, as its GL context is current there.
type milkdrop struct {
	pm    C.projectm_handle
	pl    C.projectm_playlist_handle
	w, h  int
	dir   string // the presets are from
	rows  []byte // as GL reads them, bottom up
	share *share // where frames go on the painter's GPU, nil to read them back
}

// ponytail: one GL context per process, a second Video's visualizer fails;
// keep the context per milkdrop if two have to show at once.
func newMilkdrop(w, h int) (*milkdrop, error) {
	sh := newShare()
	var id []byte
	if sh != nil {
		id = sh.deviceID()
	}
	var matched C.int
	if e := C.gl_open(C.int(w), C.int(h), (*C.uint8_t)(unsafe.SliceData(id)), C.int(len(id)), &matched); e != nil {
		return nil, errors.New(C.GoString(e))
	}
	if sh != nil && matched == 0 {
		sh = nil // OpenGL is on another GPU, or cannot say which
	}
	if sh != nil {
		if err := sh.resize(w, h); err != nil {
			fyne.LogError("fmw: visualizations go through RAM", err)
			sh = nil
		}
	}
	m := &milkdrop{pm: C.projectm_create(), w: w, h: h, share: sh}
	if m.pm == nil {
		return nil, errors.New("projectM needs OpenGL 3.3")
	}
	C.projectm_set_window_size(m.pm, C.size_t(w), C.size_t(h))
	C.projectm_set_fps(m.pm, visualFPS)
	m.pl = C.projectm_playlist_create(m.pm)
	C.projectm_playlist_set_shuffle(m.pl, true)
	return m, nil
}

func glHas(name string) bool {
	c := C.CString(name)
	defer C.free(unsafe.Pointer(c))
	return C.gl_has(c) != 0
}

// shared reports whether frames stay on the GPU, drawn with drawTo.
func (m *milkdrop) shared() bool {
	return m.share != nil
}

func (m *milkdrop) resize(w, h int) error {
	if C.gl_resize(C.int(w), C.int(h)) == 0 {
		return errors.New("no OpenGL pbuffer")
	}
	if m.share != nil {
		if err := m.share.resize(w, h); err != nil {
			return err
		}
	}
	m.w, m.h = w, h
	C.projectm_set_window_size(m.pm, C.size_t(w), C.size_t(h))
	return nil
}

// configure shows each preset for seconds, cut short by a loud beat with
// hardCuts, taking them from dir, and the textures they use from dir and a
// textures folder next to it.
func (m *milkdrop) configure(seconds float64, hardCuts bool, dir string) error {
	C.projectm_set_preset_duration(m.pm, C.double(seconds))
	C.projectm_set_hard_cut_enabled(m.pm, C.bool(hardCuts))
	if dir == m.dir {
		return nil
	}
	m.dir = dir
	paths := []*C.char{C.CString(dir), C.CString(filepath.Join(filepath.Dir(dir), "textures"))}
	defer func() {
		for _, p := range paths {
			C.free(unsafe.Pointer(p))
		}
	}()
	C.projectm_set_texture_search_paths(m.pm, &paths[0], C.size_t(len(paths)))
	C.projectm_playlist_clear(m.pl)
	if C.projectm_playlist_add_path(m.pl, paths[0], true, false) == 0 {
		return errors.New("no presets in " + dir)
	}
	m.next()
	return nil
}

// render draws the next frame to the sound in pcm, interleaved stereo.
func (m *milkdrop) render(pcm []float32) {
	for max := int(C.projectm_pcm_get_max_samples()); len(pcm) > 0; {
		n := min(len(pcm)/2, max)
		C.projectm_pcm_add_float(m.pm, (*C.float)(&pcm[0]), C.uint(n), C.PROJECTM_STEREO)
		pcm = pcm[n*2:]
	}
	C.projectm_opengl_render_frame(m.pm)
}

// draw draws the next frame and puts the crop of it into dst, of the crop's
// size, if the crop is not empty.
func (m *milkdrop) draw(pcm []float32, dst *image.RGBA, crop image.Rectangle) {
	m.render(pcm)
	if crop.Empty() {
		return
	}
	w, h := crop.Dx(), crop.Dy()
	stride := w * 4
	if len(m.rows) < stride*h {
		m.rows = make([]byte, stride*h)
	}
	C.gl_read(C.int(crop.Min.X), C.int(m.h-crop.Max.Y), C.int(w), C.int(h), unsafe.Pointer(&m.rows[0]))
	for y := range h {
		copy(dst.Pix[y*dst.Stride:y*dst.Stride+stride], m.rows[(h-1-y)*stride:])
	}
}

// drawTo draws the next frame into frame, on the GPU.
func (m *milkdrop) drawTo(pcm []float32, frame *canvas.VideoFrame) {
	m.share.draw(func() { m.render(pcm) }, frame)
}

// next cuts to another preset.
func (m *milkdrop) next() {
	C.projectm_playlist_play_next(m.pl, true)
}
