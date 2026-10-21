// What fmw's OpenGL code for projectM shares between its files: the context
// (milkdrop_egl.c, milkdrop_wgl.c) and the calls made in it (milkdrop_gl.c).

#include <stddef.h>
#include <stdint.h>

// gl_open makes an OpenGL 3.3 core context current on the calling thread,
// drawing into a w by h pbuffer, on the GPU with uuid's device and driver
// UUIDs, as gl_device_uuid gives them, or else on the first that is not a
// software one. *matched says which.
const char *gl_open(int w, int h, const uint8_t *uuid, int *matched);

// gl_resize has the context draw into a w by h pbuffer from now on.
int gl_resize(int w, int h);

// gl_proc returns the context's function name, or NULL.
void *gl_proc(const char *name);

// gl_load loads the functions fmw calls, after gl_open, and reports whether
// it found them all.
int gl_load(void);

// gl_device_uuid puts the device UUID of the context's GPU in uuid, then its
// driver's, and reports whether it has GL_EXT_memory_object to tell them.
int gl_device_uuid(uint8_t uuid[32]);

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
