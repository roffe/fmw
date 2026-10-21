//go:build windows && projectm

#include <string.h>
#include <windows.h>
#include <GL/glew.h>
#include <GL/wglext.h>
#include "milkdrop.h"

static HWND window;
static HDC window_dc, dc;
static HGLRC ctx;
static HPBUFFERARB pbuffer;
static int format;
static HMODULE opengl32;
static PFNWGLCREATEPBUFFERARBPROC create_pbuffer;
static PFNWGLGETPBUFFERDCARBPROC pbuffer_dc;
static PFNWGLRELEASEPBUFFERDCARBPROC release_pbuffer_dc;
static PFNWGLDESTROYPBUFFERARBPROC destroy_pbuffer;

void *gl_proc(const char *name) {
	void *p = (void *)wglGetProcAddress(name);
	if (p == NULL || p == (void *)1 || p == (void *)2 || p == (void *)3 || p == (void *)-1)
		p = (void *)GetProcAddress(opengl32, name); // what OpenGL 1.1 has
	return p;
}

int gl_resize(int w, int h) {
	HPBUFFERARB old = pbuffer;
	HDC old_dc = dc;
	const int none[] = {0};
	pbuffer = create_pbuffer(old_dc ? old_dc : window_dc, format, w, h, none);
	if (!pbuffer) {
		pbuffer = old;
		return 0;
	}
	dc = pbuffer_dc(pbuffer);
	if (ctx && !wglMakeCurrent(dc, ctx))
		return 0;
	if (old) {
		release_pbuffer_dc(old, old_dc);
		destroy_pbuffer(old);
	}
	return 1;
}

// open_context makes the context, its functions loaded, or tells why not.
static const char *open_context(int w, int h) {
	opengl32 = LoadLibraryA("opengl32.dll");
	WNDCLASSA wc = {.lpfnWndProc = DefWindowProcA, .hInstance = GetModuleHandleA(NULL), .lpszClassName = "fmw-projectm"};
	RegisterClassA(&wc);
	window = CreateWindowA(wc.lpszClassName, "", WS_POPUP, 0, 0, 1, 1, NULL, NULL, wc.hInstance, NULL);
	window_dc = window ? GetDC(window) : NULL;
	PIXELFORMATDESCRIPTOR pfd = {
		.nSize = sizeof(pfd),
		.nVersion = 1,
		.dwFlags = PFD_DRAW_TO_WINDOW | PFD_SUPPORT_OPENGL,
		.iPixelType = PFD_TYPE_RGBA,
		.cColorBits = 24,
	};
	if (!window_dc || !SetPixelFormat(window_dc, ChoosePixelFormat(window_dc, &pfd), &pfd))
		return "no window for OpenGL";

	// a context of any version, which the functions making the right one need
	HGLRC first = wglCreateContext(window_dc);
	if (!first || !wglMakeCurrent(window_dc, first))
		return "no OpenGL";
	PFNWGLCHOOSEPIXELFORMATARBPROC choose_format = (PFNWGLCHOOSEPIXELFORMATARBPROC)wglGetProcAddress("wglChoosePixelFormatARB");
	PFNWGLCREATECONTEXTATTRIBSARBPROC create_context = (PFNWGLCREATECONTEXTATTRIBSARBPROC)wglGetProcAddress("wglCreateContextAttribsARB");
	create_pbuffer = (PFNWGLCREATEPBUFFERARBPROC)wglGetProcAddress("wglCreatePbufferARB");
	pbuffer_dc = (PFNWGLGETPBUFFERDCARBPROC)wglGetProcAddress("wglGetPbufferDCARB");
	release_pbuffer_dc = (PFNWGLRELEASEPBUFFERDCARBPROC)wglGetProcAddress("wglReleasePbufferDCARB");
	destroy_pbuffer = (PFNWGLDESTROYPBUFFERARBPROC)wglGetProcAddress("wglDestroyPbufferARB");
	const char *err = NULL;
	const int fa[] = {WGL_DRAW_TO_PBUFFER_ARB, TRUE, WGL_SUPPORT_OPENGL_ARB, TRUE, WGL_ACCELERATION_ARB, WGL_FULL_ACCELERATION_ARB,
		WGL_PIXEL_TYPE_ARB, WGL_TYPE_RGBA_ARB, WGL_COLOR_BITS_ARB, 24, 0};
	UINT n = 0;
	if (!choose_format || !create_context || !create_pbuffer || !pbuffer_dc || !release_pbuffer_dc || !destroy_pbuffer)
		err = "no OpenGL pbuffers";
	else if (!choose_format(window_dc, fa, NULL, 1, &format, &n) || n == 0 || !gl_resize(w, h))
		err = "no OpenGL pbuffer";
	else {
		const int ca[] = {WGL_CONTEXT_MAJOR_VERSION_ARB, 3, WGL_CONTEXT_MINOR_VERSION_ARB, 3,
			WGL_CONTEXT_PROFILE_MASK_ARB, WGL_CONTEXT_CORE_PROFILE_BIT_ARB, 0};
		ctx = create_context(dc, NULL, ca);
		if (!ctx || !wglMakeCurrent(dc, ctx))
			err = "no OpenGL 3.3 context";
	}
	wglDeleteContext(first);
	// a window this thread does not pump would hold up what other programs
	// send every window, and pbuffers come from each other from now on
	ReleaseDC(window, window_dc);
	DestroyWindow(window);
	window = NULL;
	window_dc = NULL;
	if (err)
		return err;
	glewExperimental = GL_TRUE; // for a core context
	if (glewInit() != GLEW_OK)
		return "GLEW cannot load OpenGL's functions";
	glGetError(); // glewInit asks for the extensions the way core contexts lack
	if (!gl_load())
		return "OpenGL lacks functions fmw calls";
	return NULL;
}

const char *gl_open(int w, int h, const uint8_t *id, int id_len, int *matched) {
	*matched = 0;
	const char *err = open_context(w, h);
	if (err)
		return err;
	uint8_t got[32];
	*matched = id && id_len <= 32 && gl_device_id(got, id_len) && memcmp(got, id, id_len) == 0;
	return NULL;
}
