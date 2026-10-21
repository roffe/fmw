//go:build (linux || windows) && projectm && !novulkan

package fmw

/*
#include "milkdrop.h"
#include "share_vulkan.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"unsafe"

	"fyne.io/fyne/v2/driver/webgpu"
)

// vulkanMemory is the painter's Vulkan device, whose memory OpenGL imports
// through an fd, or a Win32 handle on Windows, and whose timelines are
// timeline semaphores.
type vulkanMemory struct {
	id [32]byte // device and driver UUIDs
}

// newVulkanMemory returns the painter's Vulkan device, or nil when it draws
// with something else, or its device cannot export memory.
func newVulkanMemory() gpuMemory {
	in, ok := webgpu.VulkanDevice()
	if !ok || !slices.Contains(in.DeviceExtensions, externalMemory("VK_KHR_external_memory")) || in.Features12.TimelineSemaphore == 0 {
		return nil
	}
	m := &vulkanMemory{}
	if C.vk_share_init(C.uintptr_t(in.GetInstanceProcAddr), C.uintptr_t(in.Instance), C.uintptr_t(in.PhysicalDevice),
		C.uintptr_t(in.Device), (*C.uint8_t)(unsafe.Pointer(&m.id[0]))) == 0 {
		return nil
	}
	return m
}

// externalMemory is the name of the extension prefix has for the handles
// memory is shared through on this system.
func externalMemory(prefix string) string {
	if runtime.GOOS == "windows" {
		return prefix + "_win32"
	}
	return prefix + "_fd"
}

func (m *vulkanMemory) deviceID() []byte {
	return m.id[:]
}

func (m *vulkanMemory) glExtension() string {
	return externalMemory("GL_EXT_memory_object")
}

func (m *vulkanMemory) alloc(size int) (sharedBuffer, error) {
	b := &vulkanBuffer{}
	if r := C.vk_shared_make(&b.vk, C.uint64_t(size)); r != C.VK_SUCCESS {
		return nil, fmt.Errorf("vulkan: no buffer to share with OpenGL: VkResult %d", r)
	}
	if b.buf = C.vk_shared_import(&b.vk, C.uint64_t(size), &b.mem); b.buf == 0 {
		C.vk_shared_free(&b.vk)
		return nil, errors.New("OpenGL did not import Vulkan's memory")
	}
	return b, nil
}

type vulkanBuffer struct {
	vk       C.vk_shared
	buf, mem C.uint
}

func (b *vulkanBuffer) gl() (C.uint, C.uint) {
	return b.buf, b.mem
}

func (b *vulkanBuffer) handles() (uintptr, uintptr) {
	return uintptr(unsafe.Pointer(b.vk.buffer)), uintptr(unsafe.Pointer(b.vk.semaphore))
}

func (b *vulkanBuffer) wait(value uint64) bool {
	return C.vk_wait(&b.vk, C.uint64_t(value), 100_000_000) == C.VK_SUCCESS
}

func (b *vulkanBuffer) reached(value uint64) bool {
	return C.vk_wait(&b.vk, C.uint64_t(value), 0) == C.VK_SUCCESS
}

func (b *vulkanBuffer) signal(value uint64) bool {
	return C.vk_signal(&b.vk, C.uint64_t(value)) == C.VK_SUCCESS
}

func (b *vulkanBuffer) free() {
	C.vk_shared_free(&b.vk)
}
