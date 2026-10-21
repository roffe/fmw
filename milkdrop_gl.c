//go:build (linux || windows) && projectm

#include <string.h>
#include "milkdrop.h"

#ifdef _WIN32
#define APIENTRY __stdcall
#else
#define APIENTRY
#endif

typedef unsigned int GLenum, GLuint, GLbitfield;
typedef int GLint, GLsizei;
typedef unsigned char GLboolean, GLubyte;
typedef float GLfloat;
typedef uint64_t GLuint64;
typedef ptrdiff_t GLsizeiptr;
typedef struct __GLsync *GLsync;

enum {
	GL_COLOR_BUFFER_BIT = 0x4000,
	GL_EXTENSIONS = 0x1F03,
	GL_NUM_EXTENSIONS = 0x821D,
	GL_COLOR_CLEAR_VALUE = 0x0C22,
	GL_PACK_ROW_LENGTH = 0x0D02,
	GL_PACK_ALIGNMENT = 0x0D05,
	GL_TEXTURE_2D = 0x0DE1,
	GL_UNSIGNED_BYTE = 0x1401,
	GL_RGBA = 0x1908,
	GL_NEAREST = 0x2600,
	GL_RGBA8 = 0x8058,
	GL_PIXEL_PACK_BUFFER = 0x88EB,
	GL_READ_FRAMEBUFFER = 0x8CA8,
	GL_DRAW_FRAMEBUFFER = 0x8CA9,
	GL_COLOR_ATTACHMENT0 = 0x8CE0,
	GL_FRAMEBUFFER = 0x8D40,
	GL_DEDICATED_MEMORY_OBJECT_EXT = 0x9581,
	GL_HANDLE_TYPE_OPAQUE_FD_EXT = 0x9586,
	GL_SYNC_GPU_COMMANDS_COMPLETE = 0x9117,
	GL_ALREADY_SIGNALED = 0x911A,
	GL_CONDITION_SATISFIED = 0x911C,
	GL_SYNC_FLUSH_COMMANDS_BIT = 0x0001,
	GL_DEVICE_UUID_EXT = 0x9597,
	GL_DRIVER_UUID_EXT = 0x9598,
	GL_DEVICE_LUID_EXT = 0x9599,
};

static struct {
	GLenum (APIENTRY *GetError)(void);
	void (APIENTRY *GetIntegerv)(GLenum, GLint *);
	void (APIENTRY *GetFloatv)(GLenum, GLfloat *);
	const GLubyte *(APIENTRY *GetStringi)(GLenum, GLuint);
	void (APIENTRY *BindFramebuffer)(GLenum, GLuint);
	void (APIENTRY *GenFramebuffers)(GLsizei, GLuint *);
	void (APIENTRY *FramebufferTexture2D)(GLenum, GLenum, GLenum, GLuint, GLint);
	void (APIENTRY *BlitFramebuffer)(GLint, GLint, GLint, GLint, GLint, GLint, GLint, GLint, GLbitfield, GLenum);
	void (APIENTRY *GenTextures)(GLsizei, GLuint *);
	void (APIENTRY *DeleteTextures)(GLsizei, const GLuint *);
	void (APIENTRY *BindTexture)(GLenum, GLuint);
	void (APIENTRY *TexStorage2D)(GLenum, GLsizei, GLenum, GLsizei, GLsizei);
	void (APIENTRY *ColorMask)(GLboolean, GLboolean, GLboolean, GLboolean);
	void (APIENTRY *ClearColor)(GLfloat, GLfloat, GLfloat, GLfloat);
	void (APIENTRY *Clear)(GLbitfield);
	void (APIENTRY *GenBuffers)(GLsizei, GLuint *);
	void (APIENTRY *DeleteBuffers)(GLsizei, const GLuint *);
	void (APIENTRY *BindBuffer)(GLenum, GLuint);
	void (APIENTRY *PixelStorei)(GLenum, GLint);
	void (APIENTRY *ReadPixels)(GLint, GLint, GLsizei, GLsizei, GLenum, GLenum, void *);
	void (APIENTRY *Finish)(void);
	GLsync (APIENTRY *FenceSync)(GLenum, GLbitfield);
	GLenum (APIENTRY *ClientWaitSync)(GLsync, GLbitfield, GLuint64);
	void (APIENTRY *DeleteSync)(GLsync);

	// GL_EXT_memory_object, _fd and _win32, which gl_has checks for first
	void (APIENTRY *GetUnsignedBytevEXT)(GLenum, GLubyte *);
	void (APIENTRY *GetUnsignedBytei_vEXT)(GLenum, GLuint, GLubyte *);
	void (APIENTRY *CreateMemoryObjectsEXT)(GLsizei, GLuint *);
	void (APIENTRY *DeleteMemoryObjectsEXT)(GLsizei, const GLuint *);
	void (APIENTRY *MemoryObjectParameterivEXT)(GLuint, GLenum, const GLint *);
	void (APIENTRY *BufferStorageMemEXT)(GLenum, GLsizeiptr, GLuint, GLuint64);
	void (APIENTRY *ImportMemoryFdEXT)(GLuint, GLuint64, GLenum, GLint);
	void (APIENTRY *ImportMemoryWin32HandleEXT)(GLuint, GLuint64, GLenum, void *);
} gl;

// names are gl's functions in its order, the first required ones before
// the extensions'.
static const char *names[] = {
	"glGetError", "glGetIntegerv", "glGetFloatv", "glGetStringi",
	"glBindFramebuffer", "glGenFramebuffers", "glFramebufferTexture2D", "glBlitFramebuffer",
	"glGenTextures", "glDeleteTextures", "glBindTexture", "glTexStorage2D",
	"glColorMask", "glClearColor", "glClear",
	"glGenBuffers", "glDeleteBuffers", "glBindBuffer", "glPixelStorei", "glReadPixels", "glFinish",
	"glFenceSync", "glClientWaitSync", "glDeleteSync",
	"glGetUnsignedBytevEXT", "glGetUnsignedBytei_vEXT", "glCreateMemoryObjectsEXT", "glDeleteMemoryObjectsEXT",
	"glMemoryObjectParameterivEXT", "glBufferStorageMemEXT", "glImportMemoryFdEXT", "glImportMemoryWin32HandleEXT",
};
enum { required = 24 };
_Static_assert(sizeof(gl) == sizeof(names) / sizeof(names[0]) * sizeof(void *), "names and gl differ");

int gl_load(void) {
	void **fns = (void **)&gl;
	for (size_t i = 0; i < sizeof(names) / sizeof(names[0]); i++) {
		fns[i] = gl_proc(names[i]);
		if (!fns[i] && i < required)
			return 0;
	}
	return 1;
}

int gl_has(const char *name) {
	GLint n = 0;
	gl.GetIntegerv(GL_NUM_EXTENSIONS, &n);
	for (GLint i = 0; i < n; i++) {
		const GLubyte *ext = gl.GetStringi(GL_EXTENSIONS, i);
		if (ext && strcmp((const char *)ext, name) == 0)
			return 1;
	}
	return 0;
}

int gl_device_id(uint8_t *id, int id_len) {
	if (!gl_has("GL_EXT_memory_object") || !gl.GetUnsignedBytei_vEXT || !gl.GetUnsignedBytevEXT)
		return 0;
	gl.GetError();
	switch (id_len) {
	case 32:
		gl.GetUnsignedBytei_vEXT(GL_DEVICE_UUID_EXT, 0, id);
		gl.GetUnsignedBytevEXT(GL_DRIVER_UUID_EXT, id + 16);
		break;
	case 8:
		if (!gl_has("GL_EXT_memory_object_win32"))
			return 0;
		gl.GetUnsignedBytevEXT(GL_DEVICE_LUID_EXT, id);
		break;
	default:
		return 0;
	}
	return gl.GetError() == 0;
}

void gl_read(int x, int y, int w, int h, void *dst) {
	GLfloat clear[4];
	gl.GetFloatv(GL_COLOR_CLEAR_VALUE, clear);
	gl.BindFramebuffer(GL_FRAMEBUFFER, 0);
	gl.ColorMask(0, 0, 0, 1);
	gl.ClearColor(0, 0, 0, 1);
	gl.Clear(GL_COLOR_BUFFER_BIT);
	gl.ColorMask(1, 1, 1, 1);
	gl.ClearColor(clear[0], clear[1], clear[2], clear[3]);
	gl.BindBuffer(GL_PIXEL_PACK_BUFFER, 0);
	gl.PixelStorei(GL_PACK_ALIGNMENT, 4);
	gl.ReadPixels(x, y, w, h, GL_RGBA, GL_UNSIGNED_BYTE, dst);
}

// import_buffer makes a buffer of size bytes on memory, a memory object
// imported already, or deletes memory and returns 0.
static unsigned import_buffer(GLuint memory, uint64_t size) {
	GLuint buffer = 0;
	gl.GenBuffers(1, &buffer);
	gl.BindBuffer(GL_PIXEL_PACK_BUFFER, buffer);
	gl.BufferStorageMemEXT(GL_PIXEL_PACK_BUFFER, (GLsizeiptr)size, memory, 0);
	gl.BindBuffer(GL_PIXEL_PACK_BUFFER, 0);
	if (gl.GetError() != 0) {
		gl_free_shared(buffer, memory);
		return 0;
	}
	return buffer;
}

static GLuint dedicated_memory(void) {
	GLuint memory = 0;
	GLint dedicated = 1;
	gl.GetError();
	gl.CreateMemoryObjectsEXT(1, &memory);
	gl.MemoryObjectParameterivEXT(memory, GL_DEDICATED_MEMORY_OBJECT_EXT, &dedicated);
	return memory;
}

unsigned gl_import_fd(int fd, uint64_t allocation, uint64_t size, unsigned *memory) {
	if (!gl.ImportMemoryFdEXT || !gl.CreateMemoryObjectsEXT || !gl.BufferStorageMemEXT)
		return 0;
	*memory = dedicated_memory();
	gl.ImportMemoryFdEXT(*memory, allocation, GL_HANDLE_TYPE_OPAQUE_FD_EXT, fd);
	return import_buffer(*memory, size);
}

unsigned gl_import_win32(void *handle, unsigned type, uint64_t allocation, uint64_t size, unsigned *memory) {
	if (!gl.ImportMemoryWin32HandleEXT || !gl.CreateMemoryObjectsEXT || !gl.BufferStorageMemEXT)
		return 0;
	*memory = dedicated_memory();
	gl.ImportMemoryWin32HandleEXT(*memory, allocation, type, handle);
	return import_buffer(*memory, size);
}

void gl_free_shared(unsigned buffer, unsigned memory) {
	if (buffer)
		gl.DeleteBuffers(1, &buffer);
	if (memory && gl.DeleteMemoryObjectsEXT)
		gl.DeleteMemoryObjectsEXT(1, &memory);
}

void *gl_read_shared(unsigned buffer, int w, int h, int row_length) {
	static GLuint fbo, tex;
	static int tw, th;
	if (tw != w || th != h) {
		if (!fbo)
			gl.GenFramebuffers(1, &fbo);
		if (tex)
			gl.DeleteTextures(1, &tex);
		gl.GenTextures(1, &tex);
		gl.BindTexture(GL_TEXTURE_2D, tex);
		gl.TexStorage2D(GL_TEXTURE_2D, 1, GL_RGBA8, w, h);
		gl.BindTexture(GL_TEXTURE_2D, 0);
		gl.BindFramebuffer(GL_FRAMEBUFFER, fbo);
		gl.FramebufferTexture2D(GL_FRAMEBUFFER, GL_COLOR_ATTACHMENT0, GL_TEXTURE_2D, tex, 0);
		tw = w;
		th = h;
	}
	gl.BindFramebuffer(GL_READ_FRAMEBUFFER, 0);
	gl.BindFramebuffer(GL_DRAW_FRAMEBUFFER, fbo);
	gl.BlitFramebuffer(0, 0, w, h, 0, h, w, 0, GL_COLOR_BUFFER_BIT, GL_NEAREST); // top row first
	gl.BindFramebuffer(GL_READ_FRAMEBUFFER, fbo);
	gl.BindBuffer(GL_PIXEL_PACK_BUFFER, buffer);
	gl.PixelStorei(GL_PACK_ALIGNMENT, 4);
	gl.PixelStorei(GL_PACK_ROW_LENGTH, row_length);
	gl.ReadPixels(0, 0, w, h, GL_RGBA, GL_UNSIGNED_BYTE, 0);
	gl.PixelStorei(GL_PACK_ROW_LENGTH, 0);
	gl.BindBuffer(GL_PIXEL_PACK_BUFFER, 0);
	gl.BindFramebuffer(GL_FRAMEBUFFER, 0);
	return gl.FenceSync(GL_SYNC_GPU_COMMANDS_COMPLETE, 0);
}

int gl_done(void *fence, uint64_t timeout) {
	GLenum r = gl.ClientWaitSync((GLsync)fence, GL_SYNC_FLUSH_COMMANDS_BIT, timeout);
	gl.DeleteSync((GLsync)fence);
	return r == GL_ALREADY_SIGNALED || r == GL_CONDITION_SATISFIED;
}
