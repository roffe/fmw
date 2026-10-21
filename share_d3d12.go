//go:build windows && projectm

package fmw

/*
#include "milkdrop.h"
#include "share_d3d12.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	"fyne.io/fyne/v2/driver/webgpu"
)

// d3d12Memory is the painter's Direct3D 12 device, whose buffers OpenGL
// imports through shared handles, and whose timelines are fences.
type d3d12Memory struct {
	luid [8]byte
}

// newD3D12Memory returns the painter's Direct3D 12 device, or nil when it
// draws with something else.
func newD3D12Memory() gpuMemory {
	dev, ok := webgpu.D3D12Device()
	if !ok {
		return nil
	}
	m := &d3d12Memory{}
	C.d3d_share_init(C.uintptr_t(dev), (*C.uint8_t)(unsafe.Pointer(&m.luid[0])))
	return m
}

func (m *d3d12Memory) deviceID() []byte {
	return m.luid[:]
}

func (m *d3d12Memory) glExtension() string {
	return "GL_EXT_memory_object_win32"
}

func (m *d3d12Memory) alloc(size int) (sharedBuffer, error) {
	b := &d3d12Buffer{}
	if hr := C.d3d_shared_make(&b.d, C.uint64_t(size)); hr != 0 {
		return nil, fmt.Errorf("direct3d 12: no buffer to share with OpenGL: HRESULT %#x", uint32(hr))
	}
	if b.buf = C.d3d_shared_import(&b.d, C.uint64_t(size), &b.mem); b.buf == 0 {
		C.d3d_shared_free(&b.d)
		return nil, errors.New("OpenGL did not import Direct3D 12's buffer")
	}
	return b, nil
}

type d3d12Buffer struct {
	d        C.d3d_shared
	buf, mem C.uint
}

func (b *d3d12Buffer) gl() (C.uint, C.uint) {
	return b.buf, b.mem
}

func (b *d3d12Buffer) handles() (uintptr, uintptr) {
	return uintptr(unsafe.Pointer(b.d.buffer)), uintptr(unsafe.Pointer(b.d.fence))
}

func (b *d3d12Buffer) wait(value uint64) bool {
	return C.d3d_wait(&b.d, C.uint64_t(value), 100) != 0
}

func (b *d3d12Buffer) reached(value uint64) bool {
	return C.d3d_wait(&b.d, C.uint64_t(value), 0) != 0
}

func (b *d3d12Buffer) signal(value uint64) bool {
	return C.d3d_signal(&b.d, C.uint64_t(value)) != 0
}

func (b *d3d12Buffer) free() {
	C.d3d_shared_free(&b.d)
}
