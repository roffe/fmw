//go:build windows

package fmw

/*
#define COBJMACROS
#include <libavcodec/avcodec.h>
#include <libavutil/hwcontext_d3d12va.h>

// d3d12_device gives FFmpeg the Direct3D 12 device Fyne's painter draws
// with, which it holds a reference to and releases with its own.
static AVBufferRef *d3d12_device(uintptr_t device) {
	AVBufferRef *ref = av_hwdevice_ctx_alloc(AV_HWDEVICE_TYPE_D3D12VA);
	if (!ref)
		return NULL;
	AVD3D12VADeviceContext *hw = ((AVHWDeviceContext *)ref->data)->hwctx;
	hw->device = (ID3D12Device *)device;
	ID3D12Device_AddRef(hw->device);
	if (av_hwdevice_ctx_init(ref) < 0)
		av_buffer_unref(&ref);
	return ref;
}

static AVD3D12VAFrame *d3d12_frame(const AVFrame *f) { return (AVD3D12VAFrame *)f->data[0]; }

// lock_device locks f's device, which FFmpeg holds while it decodes and
// touches the frames' fence values.
static void lock_device(const AVFrame *f, int lock) {
	AVHWFramesContext *fc = (AVHWFramesContext *)f->hw_frames_ctx->data;
	AVD3D12VADeviceContext *hw = fc->device_ctx->hwctx;
	if (lock)
		hw->lock(hw->lock_ctx);
	else
		hw->unlock(hw->lock_ctx);
}

// wait_d3d12 holds the calling thread until the GPU is done decoding f.
static void wait_d3d12(const AVFrame *f) {
	AVD3D12VASyncContext *sync = &d3d12_frame(f)->sync_ctx;
	if (ID3D12Fence_GetCompletedValue(sync->fence) < sync->fence_value &&
		SUCCEEDED(ID3D12Fence_SetEventOnCompletion(sync->fence, sync->fence_value, sync->event)))
		WaitForSingleObject(sync->event, INFINITE);
}
*/
import "C"

import (
	"sync"
	"unsafe"

	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/webgpu"
)

var sharedD3D12 struct {
	mu  sync.Mutex
	dev *C.AVBufferRef
}

// d3d12Device returns FFmpeg's device for the Direct3D 12 device Fyne's
// painter draws with, or nil without one. Until a window shows there is
// none, so only a device made is kept.
func d3d12Device() *C.AVBufferRef {
	sharedD3D12.mu.Lock()
	defer sharedD3D12.mu.Unlock()
	if sharedD3D12.dev == nil {
		if dev, ok := webgpu.D3D12Device(); ok {
			sharedD3D12.dev = C.d3d12_device(C.uintptr_t(dev))
		}
	}
	return sharedD3D12.dev
}

// useSharedD3D12 has t decode with D3D12 video on the device Fyne's painter
// draws with, so its frames stay on the GPU, when the codec can and there is
// such a device.
func (t *track) useSharedD3D12(codec *C.AVCodec) bool {
	for i := C.int(0); ; i++ {
		cfg := C.avcodec_get_hw_config(codec, i)
		if cfg == nil {
			return false
		}
		if cfg.device_type != C.AV_HWDEVICE_TYPE_D3D12VA || cfg.methods&C.AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX == 0 {
			continue
		}
		dev := d3d12Device()
		if dev == nil {
			return false
		}
		t.useDevice(dev, cfg.pix_fmt) // with t.hw to download the formats painters do not take
		t.gpu = true
		return true
	}
}

// showD3D12 points v at pic's frame on the GPU, a Direct3D 12 texture of
// NV12, in the COMMON state, that the painter copies from.
func (pic *picture) showD3D12(v *canvas.VideoFrame) {
	f := pic.f
	df := C.d3d12_frame(f)
	v.Y, v.U, v.V, v.UV = nil, nil, nil, nil
	v.Depth = 8
	v.Image = &canvas.VideoImage{
		Image:     uintptr(unsafe.Pointer(df.texture)),
		Semaphore: uintptr(unsafe.Pointer(df.sync_ctx.fence)),
		Layer:     int(df.subresource_index),
		Use: func(read func(uint32, uint64) (uint32, bool)) {
			C.lock_device(f, 1)
			defer C.lock_device(f, 0)
			if _, ok := read(0, uint64(df.sync_ctx.fence_value)); ok {
				df.sync_ctx.fence_value++
			}
		},
	}
}

// waitD3D12 holds the goroutine until the GPU is done decoding f, so the
// painter's copy of it, and the frame it draws, need not wait.
func waitD3D12(f *C.AVFrame) { C.wait_d3d12(f) }
