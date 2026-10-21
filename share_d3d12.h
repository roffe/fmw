// Buffers on the painter's Direct3D 12 device that OpenGL draws into, see
// share_d3d12.c.

#include <stdint.h>
#include <windows.h>

typedef struct ID3D12Resource ID3D12Resource;
typedef struct ID3D12Fence ID3D12Fence;

typedef struct {
	ID3D12Resource *buffer;
	ID3D12Fence *fence;
	HANDLE handle;       // exporting the buffer
	HANDLE event;        // for waiting on fence
	uint64_t allocation; // bytes of memory
} d3d_shared;

// d3d_share_init takes the device to share and puts its adapter's LUID in
// luid.
void d3d_share_init(uintptr_t device, uint8_t luid[8]);

// d3d_shared_make makes s: a buffer of size bytes exported for OpenGL, and a
// fence. It returns S_OK or why not.
HRESULT d3d_shared_make(d3d_shared *s, uint64_t size);

// d3d_shared_import imports s's buffer into OpenGL as a buffer of size bytes
// and returns it and, in *memory, the memory object, or 0.
unsigned d3d_shared_import(d3d_shared *s, uint64_t size, unsigned *memory);

// d3d_shared_free frees what d3d_shared_make made.
void d3d_shared_free(d3d_shared *s);

// d3d_signal signals s's fence value from the host.
int d3d_signal(d3d_shared *s, uint64_t value);

// d3d_wait waits up to ms milliseconds for s's fence to reach value.
int d3d_wait(d3d_shared *s, uint64_t value, uint32_t ms);
