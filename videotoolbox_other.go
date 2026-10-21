//go:build !darwin

package fmw

// #include <libavcodec/avcodec.h>
import "C"

import "fyne.io/fyne/v2/canvas"

type copyFence struct{}

func (*copyFence) wait() {}
func (*copyFence) free() {}

// useSharedVideoToolbox reports that there is no VideoToolbox off macOS.
func (*track) useSharedVideoToolbox(*C.AVCodec) bool { return false }

func (*picture) showSurface(v *canvas.VideoFrame) { v.Width = 0 }
