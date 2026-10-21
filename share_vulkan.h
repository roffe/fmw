// Buffers on the painter's Vulkan device that OpenGL draws into, see
// share_vulkan.c.

#include <stdint.h>
#include <vulkan/vulkan.h>

typedef struct {
	VkBuffer buffer;
	VkDeviceMemory memory;
	VkSemaphore semaphore;
	uint64_t allocation; // bytes of memory
	int fd;              // exporting it, or -1
	void *handle;        // on Windows
} vk_shared;

// vk_share_init takes the device to share, puts its device and driver UUIDs
// in uuid and reports whether it can export memory OpenGL imports.
int vk_share_init(uintptr_t gipa, uintptr_t inst, uintptr_t pd, uintptr_t dev, uint8_t uuid[32]);

// vk_shared_make makes s: a buffer of size bytes in memory exported for
// OpenGL, and a timeline semaphore. It returns VK_SUCCESS or why not.
VkResult vk_shared_make(vk_shared *s, uint64_t size);

// vk_shared_free frees what vk_shared_make made.
void vk_shared_free(vk_shared *s);

// vk_signal signals s's semaphore value from the host.
VkResult vk_signal(vk_shared *s, uint64_t value);

// vk_wait waits up to timeout nanoseconds for s's semaphore to reach value.
VkResult vk_wait(vk_shared *s, uint64_t value, uint64_t timeout);
