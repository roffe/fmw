// What fmw's OpenGL code for projectM shares between its files: the context
// (milkdrop_egl.c, milkdrop_wgl.c) and the calls made in it (milkdrop_gl.c).

#include <stddef.h>
#include <stdint.h>

// gl_open makes an OpenGL 3.3 core context current on the calling thread,
// drawing into a w by h pbuffer, on the GPU id of id_len bytes names, as
// gl_device_id gives it, where there is a choice, or else on the first that
// is not a software one. *matched says whether it is the GPU id names.
const char *gl_open(int w, int h, const uint8_t *id, int id_len, int *matched);

// gl_resize has the context draw into a w by h pbuffer from now on.
int gl_resize(int w, int h);

// gl_proc returns the context's function name, or NULL.
void *gl_proc(const char *name);

// gl_load loads the functions fmw calls, after gl_open, and reports whether
// it found them all.
int gl_load(void);

// gl_device_id puts what names the context's GPU in id and reports whether
// the context can tell: for an id_len of 32 its device UUID then its
// driver's, as Vulkan has them, for 8 its LUID, as Direct3D has it.
int gl_device_id(uint8_t *id, int id_len);

// gl_has reports whether the context has extension name.
int gl_has(const char *name);

// gl_read reads w by h pixels of the picture drawn from x, y up, opaque,
// bottom row first.
void gl_read(int x, int y, int w, int h, void *dst);

// gl_import_fd makes a buffer of size bytes in memory of allocation bytes
// that another API exported as an opaque fd, which it takes, and returns it
// and, in *memory, the memory object, or 0.
unsigned gl_import_fd(int fd, uint64_t allocation, uint64_t size, unsigned *memory);

// gl_import_win32 is gl_import_fd for a Win32 handle of the given GL handle
// type, which the caller keeps.
unsigned gl_import_win32(void *handle, unsigned type, uint64_t allocation, uint64_t size, unsigned *memory);

// gl_free_shared deletes what gl_import_fd or gl_import_win32 made.
void gl_free_shared(unsigned buffer, unsigned memory);

// gl_read_shared has the w by h picture drawn copied into buffer, top row
// first, rows row_length pixels apart, and returns the fence gl_done waits for
// before it is there.
void *gl_read_shared(unsigned buffer, int w, int h, int row_length);

// gl_done waits up to timeout nanoseconds for fence, deletes it and reports
// whether it was reached.
int gl_done(void *fence, uint64_t timeout);
