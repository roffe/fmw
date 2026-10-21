//go:build linux && projectm

package fmw

// newD3D12Memory is nil off Windows.
func newD3D12Memory() gpuMemory { return nil }
