//go:build linux && projectm

package fmw

/*
#cgo pkg-config: egl opengl
#cgo LDFLAGS: -lprojectM-4-playlist -lprojectM-4
#include <stdlib.h>
#include <string.h>
#include <EGL/egl.h>
#include <EGL/eglext.h>
#define GL_GLEXT_PROTOTYPES
#include <GL/gl.h>
#include <GL/glext.h>
#include <projectM-4/projectM.h>
#include <projectM-4/playlist.h>

static EGLDisplay dpy;
static EGLConfig cfg;
static EGLContext ctx;
static EGLSurface surf = EGL_NO_SURFACE;

// gl_resize draws into a w by h pbuffer from now on, the context current on
// the calling thread.
static int gl_resize(int w, int h) {
	if (surf != EGL_NO_SURFACE)
		eglDestroySurface(dpy, surf);
	const EGLint a[] = {EGL_WIDTH, w, EGL_HEIGHT, h, EGL_NONE};
	surf = eglCreatePbufferSurface(dpy, cfg, a);
	return surf != EGL_NO_SURFACE && eglMakeCurrent(dpy, surf, surf, ctx);
}

// gl_open makes an OpenGL 3.3 core context on the first GPU that is not a
// software one, without a window, as Fyne's painter may not draw with GL.
static const char *gl_open(int w, int h) {
	PFNEGLQUERYDEVICESEXTPROC devices = (PFNEGLQUERYDEVICESEXTPROC)eglGetProcAddress("eglQueryDevicesEXT");
	PFNEGLQUERYDEVICESTRINGEXTPROC device_string = (PFNEGLQUERYDEVICESTRINGEXTPROC)eglGetProcAddress("eglQueryDeviceStringEXT");
	EGLDeviceEXT dev[8];
	EGLint n = 0;
	if (!devices || !device_string || !devices(8, dev, &n) || n == 0)
		return "no EGL devices";
	int i = 0;
	for (; i < n - 1; i++) {
		const char *ext = device_string(dev[i], EGL_EXTENSIONS);
		if (!ext || !strstr(ext, "EGL_MESA_device_software"))
			break;
	}
	dpy = eglGetPlatformDisplay(EGL_PLATFORM_DEVICE_EXT, dev[i], NULL);
	if (dpy == EGL_NO_DISPLAY || !eglInitialize(dpy, NULL, NULL))
		return "eglInitialize failed";
	const EGLint ca[] = {EGL_SURFACE_TYPE, EGL_PBUFFER_BIT, EGL_RENDERABLE_TYPE, EGL_OPENGL_BIT,
		EGL_RED_SIZE, 8, EGL_GREEN_SIZE, 8, EGL_BLUE_SIZE, 8, EGL_NONE};
	if (!eglChooseConfig(dpy, ca, &cfg, 1, &n) || n == 0)
		return "no EGL config for OpenGL pbuffers";
	const EGLint xa[] = {EGL_CONTEXT_MAJOR_VERSION, 3, EGL_CONTEXT_MINOR_VERSION, 3,
		EGL_CONTEXT_OPENGL_PROFILE_MASK, EGL_CONTEXT_OPENGL_CORE_PROFILE_BIT, EGL_NONE};
	if (!eglBindAPI(EGL_OPENGL_API) || (ctx = eglCreateContext(dpy, cfg, EGL_NO_CONTEXT, xa)) == EGL_NO_CONTEXT)
		return "no OpenGL 3.3 context";
	if (!gl_resize(w, h))
		return "no OpenGL pbuffer";
	return NULL;
}

// gl_read reads w by h pixels of the picture drawn from x, y up, opaque,
// bottom row first.
static void gl_read(int x, int y, int w, int h, void *dst) {
	glBindFramebuffer(GL_FRAMEBUFFER, 0);
	glColorMask(GL_FALSE, GL_FALSE, GL_FALSE, GL_TRUE);
	glClearColor(0, 0, 0, 1);
	glClear(GL_COLOR_BUFFER_BIT);
	glColorMask(GL_TRUE, GL_TRUE, GL_TRUE, GL_TRUE);
	glBindBuffer(GL_PIXEL_PACK_BUFFER, 0);
	glPixelStorei(GL_PACK_ALIGNMENT, 4);
	glReadPixels(x, y, w, h, GL_RGBA, GL_UNSIGNED_BYTE, dst);
}
*/
import "C"

import (
	"errors"
	"image"
	"path/filepath"
	"unsafe"
)

const projectM = true

// milkdrop is projectM drawing its presets in shuffled order. Only the
// thread that made it may use it, as its GL context is current there.
type milkdrop struct {
	pm   C.projectm_handle
	pl   C.projectm_playlist_handle
	w, h int
	dir  string // the presets are from
	rows []byte // as GL reads them, bottom up
}

// ponytail: one GL context per process, a second Player's visualizer fails;
// keep dpy/ctx/surf per milkdrop if two have to show at once.
func newMilkdrop(w, h int) (*milkdrop, error) {
	if e := C.gl_open(C.int(w), C.int(h)); e != nil {
		return nil, errors.New(C.GoString(e))
	}
	m := &milkdrop{pm: C.projectm_create(), w: w, h: h}
	if m.pm == nil {
		return nil, errors.New("projectM needs OpenGL 3.3")
	}
	C.projectm_set_window_size(m.pm, C.size_t(w), C.size_t(h))
	C.projectm_set_fps(m.pm, visualFPS)
	m.pl = C.projectm_playlist_create(m.pm)
	C.projectm_playlist_set_shuffle(m.pl, true)
	return m, nil
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

func (m *milkdrop) resize(w, h int) error {
	if C.gl_resize(C.int(w), C.int(h)) == 0 {
		return errors.New("no OpenGL pbuffer")
	}
	m.w, m.h = w, h
	C.projectm_set_window_size(m.pm, C.size_t(w), C.size_t(h))
	return nil
}

// draw draws the next frame to the sound in pcm, interleaved stereo, and
// puts the crop of it into dst, of the crop's size.
func (m *milkdrop) draw(pcm []float32, dst *image.RGBA, crop image.Rectangle) {
	for max := int(C.projectm_pcm_get_max_samples()); len(pcm) > 0; {
		n := min(len(pcm)/2, max)
		C.projectm_pcm_add_float(m.pm, (*C.float)(&pcm[0]), C.uint(n), C.PROJECTM_STEREO)
		pcm = pcm[n*2:]
	}
	C.projectm_opengl_render_frame(m.pm)
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

// next cuts to another preset.
func (m *milkdrop) next() {
	C.projectm_playlist_play_next(m.pl, true)
}
