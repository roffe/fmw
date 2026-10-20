//go:build !((linux || windows) && !novulkan)

package fmw

// #include <libavcodec/avcodec.h>
import "C"

import "fyne.io/fyne/v2/canvas"

// useSharedVulkan reports that there is no device shared with Fyne's
// painter to decode on: macOS's painter draws with Metal, and builds tagged
// novulkan are for an FFmpeg without Vulkan.
func (*track) useSharedVulkan(*C.AVCodec) bool { return false }

func (*picture) showImage(v *canvas.VideoFrame) { v.Width = 0 }

func waitDecoded(*C.AVFrame) {}
