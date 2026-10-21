//go:build linux && projectm && !novulkan

#include <string.h>
#include "share_vulkan.h"

#ifdef _WIN32
#include <windows.h>
#include <vulkan/vulkan_win32.h>
#define HANDLE_TYPE VK_EXTERNAL_MEMORY_HANDLE_TYPE_OPAQUE_WIN32_BIT
#else
#define HANDLE_TYPE VK_EXTERNAL_MEMORY_HANDLE_TYPE_OPAQUE_FD_BIT
#endif

static VkDevice dev;
static VkPhysicalDeviceMemoryProperties mem_props;
static struct {
	PFN_vkCreateBuffer CreateBuffer;
	PFN_vkDestroyBuffer DestroyBuffer;
	PFN_vkGetBufferMemoryRequirements GetBufferMemoryRequirements;
	PFN_vkAllocateMemory AllocateMemory;
	PFN_vkFreeMemory FreeMemory;
	PFN_vkBindBufferMemory BindBufferMemory;
	PFN_vkCreateSemaphore CreateSemaphore;
	PFN_vkDestroySemaphore DestroySemaphore;
	PFN_vkSignalSemaphore SignalSemaphore;
	PFN_vkWaitSemaphores WaitSemaphores;
#ifdef _WIN32
	PFN_vkGetMemoryWin32HandleKHR GetMemoryWin32HandleKHR;
#else
	PFN_vkGetMemoryFdKHR GetMemoryFdKHR;
#endif
} vk;

int vk_share_init(uintptr_t gipa, uintptr_t inst, uintptr_t pd, uintptr_t device, uint8_t uuid[32]) {
	PFN_vkGetInstanceProcAddr instance_proc = (PFN_vkGetInstanceProcAddr)gipa;
	VkInstance instance = (VkInstance)inst;
	VkPhysicalDevice phys = (VkPhysicalDevice)pd;
	dev = (VkDevice)device;
	PFN_vkGetDeviceProcAddr device_proc = (PFN_vkGetDeviceProcAddr)instance_proc(instance, "vkGetDeviceProcAddr");
	PFN_vkGetPhysicalDeviceMemoryProperties memory_properties =
		(PFN_vkGetPhysicalDeviceMemoryProperties)instance_proc(instance, "vkGetPhysicalDeviceMemoryProperties");
	PFN_vkGetPhysicalDeviceProperties2 properties2 =
		(PFN_vkGetPhysicalDeviceProperties2)instance_proc(instance, "vkGetPhysicalDeviceProperties2");
	if (!device_proc || !memory_properties || !properties2)
		return 0;

	vk.CreateBuffer = (PFN_vkCreateBuffer)device_proc(dev, "vkCreateBuffer");
	vk.DestroyBuffer = (PFN_vkDestroyBuffer)device_proc(dev, "vkDestroyBuffer");
	vk.GetBufferMemoryRequirements = (PFN_vkGetBufferMemoryRequirements)device_proc(dev, "vkGetBufferMemoryRequirements");
	vk.AllocateMemory = (PFN_vkAllocateMemory)device_proc(dev, "vkAllocateMemory");
	vk.FreeMemory = (PFN_vkFreeMemory)device_proc(dev, "vkFreeMemory");
	vk.BindBufferMemory = (PFN_vkBindBufferMemory)device_proc(dev, "vkBindBufferMemory");
	vk.CreateSemaphore = (PFN_vkCreateSemaphore)device_proc(dev, "vkCreateSemaphore");
	vk.DestroySemaphore = (PFN_vkDestroySemaphore)device_proc(dev, "vkDestroySemaphore");
	vk.SignalSemaphore = (PFN_vkSignalSemaphore)device_proc(dev, "vkSignalSemaphore");
	if (!vk.SignalSemaphore)
		vk.SignalSemaphore = (PFN_vkSignalSemaphore)device_proc(dev, "vkSignalSemaphoreKHR");
	vk.WaitSemaphores = (PFN_vkWaitSemaphores)device_proc(dev, "vkWaitSemaphores");
	if (!vk.WaitSemaphores)
		vk.WaitSemaphores = (PFN_vkWaitSemaphores)device_proc(dev, "vkWaitSemaphoresKHR");
#ifdef _WIN32
	vk.GetMemoryWin32HandleKHR = (PFN_vkGetMemoryWin32HandleKHR)device_proc(dev, "vkGetMemoryWin32HandleKHR");
#else
	vk.GetMemoryFdKHR = (PFN_vkGetMemoryFdKHR)device_proc(dev, "vkGetMemoryFdKHR");
#endif
	void **fns = (void **)&vk;
	for (size_t i = 0; i < sizeof(vk) / sizeof(void *); i++)
		if (!fns[i])
			return 0;

	memory_properties(phys, &mem_props);
	VkPhysicalDeviceIDProperties id = {.sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_ID_PROPERTIES};
	VkPhysicalDeviceProperties2 props = {.sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_PROPERTIES_2, .pNext = &id};
	properties2(phys, &props);
	memcpy(uuid, id.deviceUUID, 16);
	memcpy(uuid + 16, id.driverUUID, 16);
	return 1;
}

VkResult vk_shared_make(vk_shared *s, uint64_t size) {
	memset(s, 0, sizeof(*s));
	s->fd = -1;
	VkExternalMemoryBufferCreateInfo external = {.sType = VK_STRUCTURE_TYPE_EXTERNAL_MEMORY_BUFFER_CREATE_INFO, .handleTypes = HANDLE_TYPE};
	VkBufferCreateInfo bi = {
		.sType = VK_STRUCTURE_TYPE_BUFFER_CREATE_INFO,
		.pNext = &external,
		.size = size,
		.usage = VK_BUFFER_USAGE_TRANSFER_SRC_BIT | VK_BUFFER_USAGE_TRANSFER_DST_BIT,
		.sharingMode = VK_SHARING_MODE_EXCLUSIVE,
	};
	VkResult r = vk.CreateBuffer(dev, &bi, NULL, &s->buffer);
	if (r != VK_SUCCESS)
		return r;
	VkMemoryRequirements req;
	vk.GetBufferMemoryRequirements(dev, s->buffer, &req);
	uint32_t type = UINT32_MAX;
	for (uint32_t i = 0; i < mem_props.memoryTypeCount && type == UINT32_MAX; i++)
		if (req.memoryTypeBits & (1u << i) && mem_props.memoryTypes[i].propertyFlags & VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT)
			type = i;
	if (type == UINT32_MAX) {
		vk_shared_free(s);
		return VK_ERROR_FEATURE_NOT_PRESENT;
	}
	VkExportMemoryAllocateInfo export = {.sType = VK_STRUCTURE_TYPE_EXPORT_MEMORY_ALLOCATE_INFO, .handleTypes = HANDLE_TYPE};
	VkMemoryDedicatedAllocateInfo dedicated = {.sType = VK_STRUCTURE_TYPE_MEMORY_DEDICATED_ALLOCATE_INFO, .pNext = &export, .buffer = s->buffer};
	VkMemoryAllocateInfo ai = {.sType = VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO, .pNext = &dedicated, .allocationSize = req.size, .memoryTypeIndex = type};
	if ((r = vk.AllocateMemory(dev, &ai, NULL, &s->memory)) != VK_SUCCESS ||
		(r = vk.BindBufferMemory(dev, s->buffer, s->memory, 0)) != VK_SUCCESS) {
		vk_shared_free(s);
		return r;
	}
	s->allocation = req.size;
#ifdef _WIN32
	VkMemoryGetWin32HandleInfoKHR gi = {.sType = VK_STRUCTURE_TYPE_MEMORY_GET_WIN32_HANDLE_INFO_KHR, .memory = s->memory, .handleType = HANDLE_TYPE};
	r = vk.GetMemoryWin32HandleKHR(dev, &gi, (HANDLE *)&s->handle);
#else
	VkMemoryGetFdInfoKHR gi = {.sType = VK_STRUCTURE_TYPE_MEMORY_GET_FD_INFO_KHR, .memory = s->memory, .handleType = HANDLE_TYPE};
	r = vk.GetMemoryFdKHR(dev, &gi, &s->fd);
#endif
	if (r != VK_SUCCESS) {
		vk_shared_free(s);
		return r;
	}
	VkSemaphoreTypeCreateInfo timeline = {.sType = VK_STRUCTURE_TYPE_SEMAPHORE_TYPE_CREATE_INFO, .semaphoreType = VK_SEMAPHORE_TYPE_TIMELINE};
	VkSemaphoreCreateInfo si = {.sType = VK_STRUCTURE_TYPE_SEMAPHORE_CREATE_INFO, .pNext = &timeline};
	if ((r = vk.CreateSemaphore(dev, &si, NULL, &s->semaphore)) != VK_SUCCESS)
		vk_shared_free(s);
	return r;
}

void vk_shared_free(vk_shared *s) {
	if (s->semaphore)
		vk.DestroySemaphore(dev, s->semaphore, NULL);
	if (s->buffer)
		vk.DestroyBuffer(dev, s->buffer, NULL);
	if (s->memory)
		vk.FreeMemory(dev, s->memory, NULL);
#ifdef _WIN32
	if (s->handle)
		CloseHandle(s->handle);
#endif
	memset(s, 0, sizeof(*s));
	s->fd = -1;
}

VkResult vk_signal(vk_shared *s, uint64_t value) {
	VkSemaphoreSignalInfo info = {.sType = VK_STRUCTURE_TYPE_SEMAPHORE_SIGNAL_INFO, .semaphore = s->semaphore, .value = value};
	return vk.SignalSemaphore(dev, &info);
}

VkResult vk_wait(vk_shared *s, uint64_t value, uint64_t timeout) {
	VkSemaphoreWaitInfo info = {.sType = VK_STRUCTURE_TYPE_SEMAPHORE_WAIT_INFO, .semaphoreCount = 1, .pSemaphores = &s->semaphore, .pValues = &value};
	return vk.WaitSemaphores(dev, &info, timeout);
}
