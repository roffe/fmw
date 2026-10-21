//go:build linux && projectm

#include <string.h>
#include <EGL/egl.h>
#include <EGL/eglext.h>
#include "milkdrop.h"

static EGLDisplay dpy = EGL_NO_DISPLAY;
static EGLConfig cfg;
static EGLContext ctx = EGL_NO_CONTEXT;
static EGLSurface surf = EGL_NO_SURFACE;

void *gl_proc(const char *name) {
	return (void *)eglGetProcAddress(name);
}

int gl_resize(int w, int h) {
	if (surf != EGL_NO_SURFACE)
		eglDestroySurface(dpy, surf);
	const EGLint a[] = {EGL_WIDTH, w, EGL_HEIGHT, h, EGL_NONE};
	surf = eglCreatePbufferSurface(dpy, cfg, a);
	return surf != EGL_NO_SURFACE && eglMakeCurrent(dpy, surf, surf, ctx);
}

static void gl_close(void) {
	if (dpy == EGL_NO_DISPLAY)
		return;
	eglMakeCurrent(dpy, EGL_NO_SURFACE, EGL_NO_SURFACE, EGL_NO_CONTEXT);
	if (surf != EGL_NO_SURFACE)
		eglDestroySurface(dpy, surf);
	if (ctx != EGL_NO_CONTEXT)
		eglDestroyContext(dpy, ctx);
	eglTerminate(dpy);
	dpy = EGL_NO_DISPLAY;
	ctx = EGL_NO_CONTEXT;
	surf = EGL_NO_SURFACE;
}

// open_device makes the context on dev, or reports that it cannot.
static int open_device(EGLDeviceEXT dev, int w, int h) {
	dpy = eglGetPlatformDisplay(EGL_PLATFORM_DEVICE_EXT, dev, NULL);
	if (dpy == EGL_NO_DISPLAY || !eglInitialize(dpy, NULL, NULL)) {
		dpy = EGL_NO_DISPLAY;
		return 0;
	}
	const EGLint ca[] = {EGL_SURFACE_TYPE, EGL_PBUFFER_BIT, EGL_RENDERABLE_TYPE, EGL_OPENGL_BIT,
		EGL_RED_SIZE, 8, EGL_GREEN_SIZE, 8, EGL_BLUE_SIZE, 8, EGL_NONE};
	const EGLint xa[] = {EGL_CONTEXT_MAJOR_VERSION, 3, EGL_CONTEXT_MINOR_VERSION, 3,
		EGL_CONTEXT_OPENGL_PROFILE_MASK, EGL_CONTEXT_OPENGL_CORE_PROFILE_BIT, EGL_NONE};
	EGLint n = 0;
	if (eglChooseConfig(dpy, ca, &cfg, 1, &n) && n > 0 && eglBindAPI(EGL_OPENGL_API) &&
		(ctx = eglCreateContext(dpy, cfg, EGL_NO_CONTEXT, xa)) != EGL_NO_CONTEXT &&
		gl_resize(w, h) && gl_load())
		return 1;
	gl_close();
	return 0;
}

const char *gl_open(int w, int h, const uint8_t *uuid, int *matched) {
	PFNEGLQUERYDEVICESEXTPROC devices = (PFNEGLQUERYDEVICESEXTPROC)eglGetProcAddress("eglQueryDevicesEXT");
	PFNEGLQUERYDEVICESTRINGEXTPROC device_string = (PFNEGLQUERYDEVICESTRINGEXTPROC)eglGetProcAddress("eglQueryDeviceStringEXT");
	EGLDeviceEXT dev[8];
	EGLint n = 0;
	*matched = 0;
	if (!devices || !device_string || !devices(8, dev, &n) || n == 0)
		return "no EGL devices";
	int first = -1;
	for (int i = 0; i < n; i++) {
		const char *ext = device_string(dev[i], EGL_EXTENSIONS);
		if (ext && strstr(ext, "EGL_MESA_device_software"))
			continue;
		if (first < 0)
			first = i;
		if (!uuid)
			break;
		uint8_t got[32];
		if (open_device(dev[i], w, h)) {
			if (gl_device_uuid(got) && memcmp(got, uuid, 32) == 0) {
				*matched = 1;
				return NULL;
			}
			gl_close();
		}
	}
	if (!open_device(dev[first < 0 ? 0 : first], w, h))
		return "no OpenGL 3.3 context";
	return NULL;
}
