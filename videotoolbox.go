//go:build darwin

package fmw

/*
#cgo LDFLAGS: -framework CoreVideo -lobjc
#include <unistd.h>
#include <CoreVideo/CoreVideo.h>
#include <objc/message.h>
#include <objc/runtime.h>
#include <libavcodec/avcodec.h>
#include <libavutil/hwcontext.h>

static int sw_format(const AVFrame *f) { return ((AVHWFramesContext *)f->hw_frames_ctx->data)->sw_format; }

static uintptr_t surface(const AVFrame *f) { return (uintptr_t)CVPixelBufferGetIOSurface((CVPixelBufferRef)f->data[3]); }

static uintptr_t new_event(uintptr_t device) {
	return (uintptr_t)((id (*)(id, SEL))objc_msgSend)((id)device, sel_registerName("newSharedEvent"));
}

static void release_event(uintptr_t event) {
	((void (*)(id, SEL))objc_msgSend)((id)event, sel_registerName("release"));
}

// wait_event holds the calling thread until event reaches value, a second at
// most, as a copy the painter dropped never signals.
static void wait_event(uintptr_t event, uint64_t value) {
	SEL signaled = sel_registerName("signaledValue");
	for (int i = 0; i < 1000 && ((uint64_t (*)(id, SEL))objc_msgSend)((id)event, signaled) < value; i++)
		usleep(1000);
}
*/
import "C"

import (
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/webgpu"
)

// copyFence is the id<MTLSharedEvent> the painter signals once it copied a
// picture's IOSurface, and the value it signals then. VideoToolbox's pool
// knows nothing of it, so the picture keeps the frame until then.
type copyFence struct {
	event uintptr
	value uint64
}

// wait holds the goroutine until the painter is done copying the frame.
func (c *copyFence) wait() {
	if c.event != 0 {
		C.wait_event(C.uintptr_t(c.event), C.uint64_t(c.value))
	}
}

func (c *copyFence) free() {
	if c.event != 0 {
		c.wait()
		C.release_event(C.uintptr_t(c.event))
		c.event = 0
	}
}

// useSharedVideoToolbox has t decode on VideoToolbox into IOSurfaces the
// painter copies from, when it draws with Metal and the codec can.
func (t *track) useSharedVideoToolbox(codec *C.AVCodec) bool {
	if _, ok := webgpu.MetalDevice(); !ok {
		return false
	}
	for i := C.int(0); ; i++ {
		cfg := C.avcodec_get_hw_config(codec, i)
		if cfg == nil {
			return false
		}
		if cfg.device_type != C.AV_HWDEVICE_TYPE_VIDEOTOOLBOX || cfg.methods&C.AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX == 0 {
			continue
		}
		dev := hwDevice(cfg.device_type)
		if dev == nil {
			return false
		}
		t.useDevice(dev, cfg.pix_fmt) // with t.hw to download the formats painters do not take
		t.gpu = true
		return true
	}
}

// showSurface points v at pic's frame on the GPU, the IOSurface of NV12 or
// P010 behind VideoToolbox's CVPixelBuffer, that the painter copies from.
func (pic *picture) showSurface(v *canvas.VideoFrame) {
	f := pic.f
	if pic.fence.event == 0 {
		dev, _ := webgpu.MetalDevice()
		pic.fence.event = uintptr(C.new_event(C.uintptr_t(dev)))
	}
	v.Y, v.U, v.V, v.UV = nil, nil, nil, nil
	if v.Depth = 8; C.sw_format(f) == C.AV_PIX_FMT_P010LE {
		v.Depth = 16 // its 10 bits are the high ones
	}
	v.Image = &canvas.VideoImage{
		Image:     uintptr(C.surface(f)),
		Semaphore: pic.fence.event,
		Use: func(read func(uint32, uint64) (uint32, bool)) {
			if _, ok := read(0, pic.fence.value); ok {
				pic.fence.value++
			}
		},
	}
}
