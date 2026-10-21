//go:build (linux || windows) && projectm && novulkan

package fmw

// newVulkanMemory is nil in builds without Vulkan.
func newVulkanMemory() gpuMemory { return nil }
