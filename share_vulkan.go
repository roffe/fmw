//go:build (linux || windows) && projectm && !novulkan

package fmw

/*
#include <stdlib.h>
#include "milkdrop.h"
#include "share_vulkan.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/webgpu"
)

// share is where projectM's frames go when Fyne's painter draws with Vulkan:
// buffers on its device that OpenGL draws into and the painter copies from,
// so frames never leave the GPU. A timeline semaphore for each orders the
// painter's copy after the drawing, and the next drawing after the copy.
type share struct {
	id      [32]byte // device and driver UUIDs
	mu      sync.Mutex
	slots   []*slot
	retired []*slot // of an earlier size, freed once nothing reads them
	shown   *slot   // the player's, which the painter copies from as it draws
	next    int

	// Only the visualizer's thread uses these.
	copying *slot // being copied into, until fence
	fence   unsafe.Pointer
}

type slot struct {
	vk       C.vk_shared
	buf, mem C.uint
	w, h     int
	stride   int
	value    uint64 // the semaphore value its next user waits for
	pending  bool   // handed to the main goroutine to show
	image    *canvas.VideoImage
}

// newShare returns the painter's Vulkan device as OpenGL can draw into it,
// or nil when there is none, or it cannot export memory.
func newShare() *share {
	in, ok := webgpu.VulkanDevice()
	if !ok || !slices.Contains(in.DeviceExtensions, externalMemory("VK_KHR_external_memory")) || in.Features12.TimelineSemaphore == 0 {
		return nil
	}
	sh := &share{}
	if C.vk_share_init(C.uintptr_t(in.GetInstanceProcAddr), C.uintptr_t(in.Instance), C.uintptr_t(in.PhysicalDevice),
		C.uintptr_t(in.Device), (*C.uint8_t)(unsafe.Pointer(&sh.id[0]))) == 0 {
		return nil
	}
	return sh
}

// deviceID is the device's and its driver's UUIDs, as gl_device_id has them.
func (sh *share) deviceID() []byte {
	return sh.id[:]
}

// resize makes the slots frames of w by h go to. The GL context must be
// current.
func (sh *share) resize(w, h int) error {
	if !glHas(externalMemory("GL_EXT_memory_object")) {
		return errors.New("OpenGL cannot import memory from Vulkan")
	}
	sh.drop()
	slots := make([]*slot, 3) // copied into, shown and handed over to show
	for i := range slots {
		s, err := newSlot(w, h)
		if err != nil {
			for _, s := range slots[:i] {
				s.free()
			}
			return err
		}
		slots[i] = s
	}
	sh.mu.Lock()
	sh.retired = append(sh.retired, sh.slots...)
	sh.slots = slots
	sh.mu.Unlock()
	return nil
}

func newSlot(w, h int) (*slot, error) {
	s := &slot{w: w, h: h, stride: (w*4 + 255) &^ 255} // as Direct3D 12 copies them
	size := C.uint64_t(s.stride * h)
	if r := C.vk_shared_make(&s.vk, size); r != C.VK_SUCCESS {
		return nil, fmt.Errorf("vulkan: no buffer to share with OpenGL: VkResult %d", r)
	}
	if s.buf = C.vk_shared_import(&s.vk, size, &s.mem); s.buf == 0 {
		s.free()
		return nil, errors.New("OpenGL did not import Vulkan's memory")
	}
	return s, nil
}

// free frees s, which nothing may read any longer. The GL context must be
// current.
func (s *slot) free() {
	C.gl_free_shared(s.buf, s.mem)
	C.vk_shared_free(&s.vk)
}

// externalMemory is the name of the extension prefix has for the handles
// memory is shared through on this system.
func externalMemory(prefix string) string {
	if runtime.GOOS == "windows" {
		return prefix + "_win32"
	}
	return prefix + "_fd"
}

// draw has render draw a frame and starts copying it into a slot, and hands
// the frame copied last time to the main goroutine to show in frame. Waiting
// for the copy a frame later, it is done by then, where waiting at once
// would spin the driver as the GPU draws. Without a slot to copy into the
// frame is drawn all the same, so presets go on the same.
func (sh *share) draw(render func(), frame *canvas.VideoFrame) {
	sh.collect()
	sh.finish(frame)
	s := sh.free()
	render()
	if s == nil {
		return
	}
	sh.mu.Lock()
	value := s.value
	sh.mu.Unlock()
	if C.vk_wait(&s.vk, C.uint64_t(value), 100_000_000) != C.VK_SUCCESS { // the painter's copy of what it held
		return
	}
	sh.fence = C.gl_read_shared(s.buf, C.int(s.w), C.int(s.h), C.int(s.stride/4))
	sh.mu.Lock()
	s.pending = true
	sh.mu.Unlock()
	sh.copying = s
}

// drop forgets the frame being copied, of a size no longer shown.
func (sh *share) drop() {
	s := sh.copying
	if s == nil {
		return
	}
	C.gl_done(sh.fence, 100_000_000)
	sh.copying = nil
	sh.mu.Lock()
	s.pending = false
	sh.mu.Unlock()
}

// finish shows the frame copied last time in frame, once it is there.
func (sh *share) finish(frame *canvas.VideoFrame) {
	s := sh.copying
	if s == nil {
		return
	}
	sh.copying = nil
	sh.mu.Lock()
	value := s.value
	sh.mu.Unlock()
	if C.gl_done(sh.fence, 100_000_000) == 0 || C.vk_signal(&s.vk, C.uint64_t(value+1)) != C.VK_SUCCESS {
		sh.mu.Lock()
		s.pending = false
		sh.mu.Unlock()
		return
	}
	if s.image == nil {
		s.image = &canvas.VideoImage{
			Buffer:    uintptr(unsafe.Pointer(s.vk.buffer)),
			Semaphore: uintptr(unsafe.Pointer(s.vk.semaphore)),
			Stride:    s.stride,
			Use: func(read func(uint32, uint64) (uint32, bool)) {
				sh.mu.Lock()
				defer sh.mu.Unlock()
				if _, ok := read(0, s.value); ok {
					s.value++
				}
			},
		}
	}
	sh.mu.Lock()
	s.value = value + 1
	sh.mu.Unlock()
	fyne.Do(func() {
		frame.Width, frame.Height, frame.Image = s.w, s.h, s.image
		frame.Refresh()
		sh.mu.Lock()
		sh.shown, s.pending = s, false
		sh.mu.Unlock()
	})
}

// free returns a slot that is neither shown nor about to be, or nil.
func (sh *share) free() *slot {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	for range sh.slots {
		s := sh.slots[sh.next]
		sh.next = (sh.next + 1) % len(sh.slots)
		if s != sh.shown && !s.pending {
			return s
		}
	}
	return nil
}

// collect frees the retired slots nothing shows or still copies from.
func (sh *share) collect() {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	kept := sh.retired[:0]
	for _, s := range sh.retired {
		if s == sh.shown || s.pending || C.vk_wait(&s.vk, C.uint64_t(s.value), 0) != C.VK_SUCCESS {
			kept = append(kept, s)
			continue
		}
		s.free()
	}
	sh.retired = kept
}
