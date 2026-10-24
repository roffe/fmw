//go:build (linux || windows) && projectm

package fmw

/*
#include "milkdrop.h"
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// share is where projectM's frames go when Fyne's painter draws on the same
// GPU with Vulkan or Direct3D 12: buffers on its device that OpenGL draws
// into and the painter copies from, so frames never leave the GPU. Each has a
// timeline, which orders the painter's copy after the drawing, and the next
// drawing after the copy.
type share struct {
	gpu     gpuMemory
	mu      sync.Mutex
	slots   []*slot
	retired []*slot // of an earlier size, freed once nothing reads them
	shown   *slot   // the video's, which the painter copies from as it draws
	next    int

	// Only the visualizer's thread uses these.
	copying *slot // being copied into, until fence
	fence   unsafe.Pointer
}

// gpuMemory is the API the painter draws with, as frames are shared with it.
type gpuMemory interface {
	// deviceID names the GPU as gl_device_id does.
	deviceID() []byte

	// glExtension is the OpenGL extension that imports its memory.
	glExtension() string

	// alloc makes a buffer of size bytes and its timeline, at 0, and imports
	// the buffer into OpenGL, whose context must be current.
	alloc(size int) (sharedBuffer, error)
}

type sharedBuffer interface {
	gl() (buffer, memory C.uint)
	handles() (buffer, timeline uintptr) // as canvas.VideoImage has them
	wait(value uint64) bool              // for the timeline to reach value, a while at most
	reached(value uint64) bool           // without waiting
	signal(value uint64) bool            // from the host
	free()
}

type slot struct {
	sharedBuffer
	w, h    int
	stride  int
	value   uint64 // the timeline value its next user waits for
	pending bool   // handed to the main goroutine to show
	image   *canvas.VideoImage
}

// newShare returns the painter's device as OpenGL can draw into it, or nil.
func newShare() *share {
	if gpu := newVulkanMemory(); gpu != nil {
		return &share{gpu: gpu}
	}
	if gpu := newD3D12Memory(); gpu != nil {
		return &share{gpu: gpu}
	}
	return nil
}

func (sh *share) deviceID() []byte {
	return sh.gpu.deviceID()
}

// resize makes the slots frames of w by h go to. The GL context must be
// current.
func (sh *share) resize(w, h int) error {
	if !glHas(sh.gpu.glExtension()) {
		return errors.New("OpenGL cannot import the painter's memory")
	}
	sh.drop()
	stride := (w*4 + 255) &^ 255 // as Direct3D 12 copies them
	slots := make([]*slot, 3)    // copied into, shown and handed over to show
	for i := range slots {
		buf, err := sh.gpu.alloc(stride * h)
		if err != nil {
			for _, s := range slots[:i] {
				s.release()
			}
			return err
		}
		slots[i] = &slot{sharedBuffer: buf, w: w, h: h, stride: stride}
	}
	sh.mu.Lock()
	sh.retired = append(sh.retired, sh.slots...)
	sh.slots = slots
	sh.mu.Unlock()
	return nil
}

// release frees s, which nothing may read any longer. The GL context must be
// current.
func (s *slot) release() {
	buf, mem := s.gl()
	C.gl_free_shared(buf, mem)
	s.free()
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
	if !s.wait(value) { // the painter's copy of what it held
		return
	}
	buf, _ := s.gl()
	sh.fence = C.gl_read_shared(buf, C.int(s.w), C.int(s.h), C.int(s.stride/4))
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
	if C.gl_done(sh.fence, 100_000_000) == 0 || !s.signal(value+1) {
		sh.mu.Lock()
		s.pending = false
		sh.mu.Unlock()
		return
	}
	if s.image == nil {
		buffer, timeline := s.handles()
		s.image = &canvas.VideoImage{
			Buffer:    buffer,
			Semaphore: timeline,
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
		if s == sh.shown || s.pending || !s.reached(s.value) {
			kept = append(kept, s)
			continue
		}
		s.release()
	}
	sh.retired = kept
}
