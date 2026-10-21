//go:build linux && projectm && novulkan

package fmw

import "fyne.io/fyne/v2/canvas"

// share is nil in builds without Vulkan, where frames are read back.
type share struct{}

func newShare() *share                         { return nil }
func (*share) uuid() []byte                    { return nil }
func (*share) resize(int, int) error           { return nil }
func (*share) draw(func(), *canvas.VideoFrame) {}
