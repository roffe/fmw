//go:build windows && projectm

#define COBJMACROS
#define WIDL_C_INLINE_WRAPPERS // for the methods returning structs
#include <string.h>
#include <initguid.h>
#include <windows.h>
#include <d3d12.h>
#include "milkdrop.h"
#include "share_d3d12.h"

static ID3D12Device *dev;

void d3d_share_init(uintptr_t device, uint8_t luid[8]) {
	dev = (ID3D12Device *)device;
	LUID id = ID3D12Device_GetAdapterLuid(dev);
	memcpy(luid, &id, 8);
}

HRESULT d3d_shared_make(d3d_shared *s, uint64_t size) {
	memset(s, 0, sizeof(*s));
	D3D12_HEAP_PROPERTIES heap = {.Type = D3D12_HEAP_TYPE_DEFAULT};
	D3D12_RESOURCE_DESC desc = {
		.Dimension = D3D12_RESOURCE_DIMENSION_BUFFER,
		.Width = size,
		.Height = 1,
		.DepthOrArraySize = 1,
		.MipLevels = 1,
		.SampleDesc = {.Count = 1},
		.Layout = D3D12_TEXTURE_LAYOUT_ROW_MAJOR,
	};
	HRESULT hr = ID3D12Device_CreateCommittedResource(dev, &heap, D3D12_HEAP_FLAG_SHARED, &desc,
		D3D12_RESOURCE_STATE_COMMON, NULL, &IID_ID3D12Resource, (void **)&s->buffer);
	if (FAILED(hr))
		return hr;
	s->allocation = ID3D12Device_GetResourceAllocationInfo(dev, 0, 1, &desc).SizeInBytes;
	if (FAILED(hr = ID3D12Device_CreateSharedHandle(dev, (ID3D12DeviceChild *)s->buffer, NULL, GENERIC_ALL, NULL, &s->handle)) ||
		FAILED(hr = ID3D12Device_CreateFence(dev, 0, D3D12_FENCE_FLAG_NONE, &IID_ID3D12Fence, (void **)&s->fence))) {
		d3d_shared_free(s);
		return hr;
	}
	if (!(s->event = CreateEventA(NULL, FALSE, FALSE, NULL))) {
		d3d_shared_free(s);
		return E_FAIL;
	}
	return S_OK;
}

unsigned d3d_shared_import(d3d_shared *s, uint64_t size, unsigned *memory) {
	unsigned buffer = gl_import_win32(s->handle, 0x958A /* GL_HANDLE_TYPE_D3D12_RESOURCE_EXT */, s->allocation, size, memory);
	CloseHandle(s->handle); // OpenGL keeps its own
	s->handle = NULL;
	return buffer;
}

void d3d_shared_free(d3d_shared *s) {
	if (s->fence)
		ID3D12Fence_Release(s->fence);
	if (s->buffer)
		ID3D12Resource_Release(s->buffer);
	if (s->handle)
		CloseHandle(s->handle);
	if (s->event)
		CloseHandle(s->event);
	memset(s, 0, sizeof(*s));
}

int d3d_signal(d3d_shared *s, uint64_t value) {
	return SUCCEEDED(ID3D12Fence_Signal(s->fence, value));
}

int d3d_wait(d3d_shared *s, uint64_t value, uint32_t ms) {
	if (ID3D12Fence_GetCompletedValue(s->fence) >= value)
		return 1;
	if (ms == 0 || FAILED(ID3D12Fence_SetEventOnCompletion(s->fence, value, s->event)))
		return 0;
	return WaitForSingleObject(s->event, ms) == WAIT_OBJECT_0;
}
