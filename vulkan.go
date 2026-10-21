//go:build (linux || windows) && !novulkan

package fmw

/*
#include <stdint.h>
#include <stdlib.h>
#include <libavcodec/avcodec.h>
#include <libavutil/hwcontext_vulkan.h>

enum { access_transfer_read = 0x800 }; // VK_ACCESS_2_TRANSFER_READ_BIT, a static const in the header

// vulkan_device gives FFmpeg the Vulkan device Fyne's painter draws with,
// copying what it is told into memory of its own, kept for the life of the
// process as the device is.
static AVBufferRef *vulkan_device(uintptr_t gipa, uintptr_t inst, uintptr_t pd, uintptr_t dev,
	const char **inst_ext, int ninst, const char **dev_ext, int ndev,
	const VkPhysicalDeviceFeatures *f10, const VkPhysicalDeviceVulkan11Features *f11,
	const VkPhysicalDeviceVulkan12Features *f12, const VkPhysicalDeviceVulkan13Features *f13,
	const int *qf, int nqf) {
	AVBufferRef *ref = av_hwdevice_ctx_alloc(AV_HWDEVICE_TYPE_VULKAN);
	struct { VkPhysicalDeviceVulkan11Features f11; VkPhysicalDeviceVulkan12Features f12; VkPhysicalDeviceVulkan13Features f13; } *f = calloc(1, sizeof(*f));
	if (!ref || !f || nqf > 64) {
		av_buffer_unref(&ref);
		free(f);
		return NULL;
	}
	f->f11 = *f11;
	f->f12 = *f12;
	f->f13 = *f13;
	f->f11.pNext = &f->f12;
	f->f12.pNext = &f->f13;
	f->f13.pNext = NULL;
	AVVulkanDeviceContext *vk = ((AVHWDeviceContext *)ref->data)->hwctx;
	vk->get_proc_addr = (PFN_vkGetInstanceProcAddr)gipa;
	vk->inst = (VkInstance)inst;
	vk->phys_dev = (VkPhysicalDevice)pd;
	vk->act_dev = (VkDevice)dev;
	vk->device_features = (VkPhysicalDeviceFeatures2){.sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_FEATURES_2, .pNext = &f->f11, .features = *f10};
	vk->enabled_inst_extensions = inst_ext;
	vk->nb_enabled_inst_extensions = ninst;
	vk->enabled_dev_extensions = dev_ext;
	vk->nb_enabled_dev_extensions = ndev;
	for (int i = 0; i < nqf; i++) {
		vk->qf[i].idx = qf[3 * i];
		vk->qf[i].num = qf[3 * i + 1];
		vk->qf[i].flags = qf[3 * i + 2];
	}
	vk->nb_qf = nqf;
	if (av_hwdevice_ctx_init(ref) < 0)
		av_buffer_unref(&ref);
	return ref;
}

static AVVkFrame *vk_frame(const AVFrame *f) { return (AVVkFrame *)f->data[0]; }

// wait_decoded holds the calling thread until the GPU is done writing f, a
// frame FFmpeg returns as soon as it has queued its decoding.
static void wait_decoded(const AVFrame *f) {
	static PFN_vkWaitSemaphores wait;
	AVHWFramesContext *fc = (AVHWFramesContext *)f->hw_frames_ctx->data;
	AVVulkanDeviceContext *vk = fc->device_ctx->hwctx;
	AVVulkanFramesContext *vkfc = fc->hwctx;
	if (!wait) {
		PFN_vkGetDeviceProcAddr device_proc = (PFN_vkGetDeviceProcAddr)vk->get_proc_addr(vk->inst, "vkGetDeviceProcAddr");
		wait = (PFN_vkWaitSemaphores)device_proc(vk->act_dev, "vkWaitSemaphores");
	}
	AVVkFrame *vkf = vk_frame(f);
	vkfc->lock_frame(fc, vkf);
	uint64_t value = vkf->sem_value[0];
	VkSemaphore sem = vkf->sem[0];
	vkfc->unlock_frame(fc, vkf);
	VkSemaphoreWaitInfo info = {.sType = VK_STRUCTURE_TYPE_SEMAPHORE_WAIT_INFO, .semaphoreCount = 1, .pSemaphores = &sem, .pValues = &value};
	wait(vk->act_dev, &info, UINT64_MAX);
}

static int sw_format(const AVFrame *f) { return ((AVHWFramesContext *)f->hw_frames_ctx->data)->sw_format; }

static void lock_frame(const AVFrame *f, int lock) {
	AVHWFramesContext *fc = (AVHWFramesContext *)f->hw_frames_ctx->data;
	AVVulkanFramesContext *vk = fc->hwctx;
	if (lock)
		vk->lock_frame(fc, vk_frame(f));
	else
		vk->unlock_frame(fc, vk_frame(f));
}
*/
import "C"

import (
	"slices"
	"strings"
	"sync"
	"unsafe"

	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/webgpu"
	"github.com/roffe/gowgpu/hal/vulkan"
)

var sharedVulkan struct {
	mu  sync.Mutex
	dev *C.AVBufferRef
}

// vulkanDevice returns FFmpeg's device for the Vulkan device Fyne's painter
// draws with, or nil without one that decodes video. Until a window shows
// there is none, so only a device made is kept.
func vulkanDevice() *C.AVBufferRef {
	sharedVulkan.mu.Lock()
	defer sharedVulkan.mu.Unlock()
	if sharedVulkan.dev == nil {
		if in, ok := webgpu.VulkanDevice(); ok && len(in.QueueFamilies) > 0 {
			sharedVulkan.dev = newVulkanDevice(&in)
		}
	}
	return sharedVulkan.dev
}

func newVulkanDevice(in *vulkan.Interop) *C.AVBufferRef {
	if unsafe.Sizeof(in.Features) != C.sizeof_VkPhysicalDeviceFeatures ||
		unsafe.Sizeof(in.Features11) != C.sizeof_VkPhysicalDeviceVulkan11Features ||
		unsafe.Sizeof(in.Features12) != C.sizeof_VkPhysicalDeviceVulkan12Features ||
		unsafe.Sizeof(in.Features13) != C.sizeof_VkPhysicalDeviceVulkan13Features {
		return nil // gowgpu's bindings do not match these headers
	}
	cstrings := func(names []string) **C.char { // kept, as the device is
		a := (*[64]*C.char)(C.calloc(64, C.size_t(unsafe.Sizeof((*C.char)(nil)))))
		for i, n := range names[:min(len(names), 64)] {
			a[i] = C.CString(n)
		}
		return &a[0]
	}
	var qf []C.int
	for _, q := range in.QueueFamilies {
		qf = append(qf, C.int(q.Index), C.int(q.Count), C.int(q.Flags))
	}
	// without the ones exporting memory, which FFmpeg would make every frame
	// exportable for, which it was not tried with
	exts := slices.DeleteFunc(slices.Clone(in.DeviceExtensions), func(e string) bool {
		return strings.HasPrefix(e, "VK_KHR_external_memory_")
	})
	return C.vulkan_device(C.uintptr_t(in.GetInstanceProcAddr),
		C.uintptr_t(in.Instance), C.uintptr_t(in.PhysicalDevice), C.uintptr_t(in.Device),
		cstrings(in.InstanceExtensions), C.int(min(len(in.InstanceExtensions), 64)),
		cstrings(exts), C.int(min(len(exts), 64)),
		(*C.VkPhysicalDeviceFeatures)(unsafe.Pointer(&in.Features)),
		(*C.VkPhysicalDeviceVulkan11Features)(unsafe.Pointer(&in.Features11)),
		(*C.VkPhysicalDeviceVulkan12Features)(unsafe.Pointer(&in.Features12)),
		(*C.VkPhysicalDeviceVulkan13Features)(unsafe.Pointer(&in.Features13)),
		&qf[0], C.int(len(in.QueueFamilies)))
}

// useSharedVulkan has t decode with Vulkan Video on the device Fyne's
// painter draws with, so its frames stay on the GPU, when the codec can and
// there is such a device.
func (t *track) useSharedVulkan(codec *C.AVCodec) bool {
	for i := C.int(0); ; i++ {
		cfg := C.avcodec_get_hw_config(codec, i)
		if cfg == nil {
			return false
		}
		if cfg.device_type != C.AV_HWDEVICE_TYPE_VULKAN || cfg.methods&C.AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX == 0 {
			continue
		}
		dev := vulkanDevice()
		if dev == nil {
			return false
		}
		t.useDevice(dev, cfg.pix_fmt) // with t.hw to download the formats painters do not take
		t.gpu = true
		return true
	}
}

// showImage points v at pic's frame on the GPU, a Vulkan image of NV12 or
// P010 that the painter copies from.
func (pic *picture) showImage(v *canvas.VideoFrame) {
	f := pic.f
	vkf := C.vk_frame(f)
	v.Y, v.U, v.V, v.UV = nil, nil, nil, nil
	if v.Depth = 8; C.sw_format(f) == C.AV_PIX_FMT_P010LE {
		v.Depth = 16 // its 10 bits are the high ones
	}
	v.Image = &canvas.VideoImage{
		Image:     uintptr(unsafe.Pointer(vkf.img[0])),
		Semaphore: uintptr(unsafe.Pointer(vkf.sem[0])),
		Use: func(read func(uint32, uint64) (uint32, bool)) {
			C.lock_frame(f, 1)
			defer C.lock_frame(f, 0)
			if layout, ok := read(uint32(vkf.layout[0]), uint64(vkf.sem_value[0])); ok {
				vkf.layout[0] = C.VkImageLayout(layout)
				vkf.access[0] = C.access_transfer_read
				vkf.sem_value[0]++
			}
		},
	}
}

// waitDecoded holds the goroutine until the GPU is done decoding f, so the
// painter's copy of it, and the frame it draws, need not wait.
func waitDecoded(f *C.AVFrame) { C.wait_decoded(f) }
