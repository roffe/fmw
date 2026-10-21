//go:build !windows

package fmw

// #include <libavcodec/avcodec.h>
import "C"

import "fyne.io/fyne/v2/canvas"

// useSharedD3D12 reports that there is no Direct3D 12 device off Windows.
func (*track) useSharedD3D12(*C.AVCodec) bool { return false }

func (*picture) showD3D12(v *canvas.VideoFrame) { v.Width = 0 }

func waitD3D12(*C.AVFrame) {}
