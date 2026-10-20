<p align="center"><img src="logo.png" width="160" alt="fmw"></p>

# fmw

A video player widget for [Fyne](https://fyne.io), and a small player app built on it.

It plays whatever FFmpeg can open, decodes on the GPU when there is one, and on Linux the frames never leave the GPU. 8K60 HEVC 10-bit plays at 4% of one CPU core :)

## What it does

- Plays pretty much anything FFmpeg opens, files and the URLs FFmpeg can stream
- Hardware decoding through FFmpeg: NVDEC, VA-API, VDPAU, Vulkan Video, D3D11, VideoToolbox. Falls back to the CPU for what the GPU can't do
- Zero copy on Linux. When Fyne's WebGPU painter runs on Vulkan, FFmpeg decodes with Vulkan Video on the same GPU device the window draws with, and the painter copies the picture GPU side. No trip through RAM
- YUV to RGB on the GPU, 8 and 10-bit, BT.601, BT.709 and BT.2020
- HDR10 and HLG tone mapped to SDR (BT.2390), using the peak brightness in the file
- Sound through [oto](https://github.com/ebitengine/oto), every audio track in the file selectable
- Subtitles: embedded text, PGS and DVD tracks, and external SRT, ASS/SSA, WebVTT, PGS `.sup` and VobSub `.idx`/`.sub` files. Subtitle files named like the video, next to it or in a `Subs` folder, load by themselves
- Rotation: what phones store in the file, plus whatever you pick in the menu
- Full screen, with the controls and the window menu hiding when the mouse stops moving

## Controls

The widget handles the mouse by itself:

| | |
|---|---|
| Double click | full screen |
| Mouse wheel | volume, 2% a notch |
| Right click | menu: play/pause, stop, full screen, rotate, audio track, subtitles |

Keys are up to the app, the fmw app sets these up (see [cmd/fmw/main.go](cmd/fmw/main.go)):

| | |
|---|---|
| Space | play / pause |
| Left / Right | 5 seconds back / forward |
| Alt+Enter | full screen, keypad Enter works too |

The app also has File > Open, Open Subtitles and Open Recent with native file dialogs, drag and drop (a video plays, subtitle files get added), remembers the volume and puts the file name in the title.

## Using the widget

```go
package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"github.com/roffe/fmw"
)

func main() {
	a := app.NewWithID("com.example.player")
	w := a.NewWindow("Player")
	p := fmw.NewPlayer()
	w.SetContent(p)
	w.Resize(fyne.NewSize(960, 600))

	// Load once the window shows, that's when the GPU device the decoder
	// shares with the painter gets made. Loaded before that it still plays,
	// just with the frames going through RAM.
	a.Lifecycle().SetOnStarted(func() {
		if err := p.Load("movie.mkv"); err != nil {
			panic(err)
		}
		p.Play()
	})
	w.ShowAndRun()
}
```

The rest is what you'd expect: `Play`, `Pause`, `TogglePlay`, `Stop`, `Seek`, `CurrentTime`, `Duration`, `Playing`, `SetVolume`, `Volume`, `AddSubtitles` and `ToggleFullScreen`. All of them are safe to call from any goroutine.

## Building

You need:

1. Go 1.27
2. FFmpeg 6.1 or newer with its development files, found through pkg-config (libavformat, libavcodec, libavutil, libswscale, libswresample)
3. The Vulkan headers on Linux and Windows, for the zero copy path. On Arch that's `vulkan-headers`, on Debian and Ubuntu `libvulkan-dev`. Without them, or with an FFmpeg built without Vulkan, build with `-tags novulkan`
4. My Fyne fork on the `wgpu` branch, which has the `canvas.VideoFrame` the player draws with and the WebGPU painter, and [gowgpu](https://github.com/roffe/gowgpu) under it. Upstream Fyne doesn't have `VideoFrame`

The `go.work` expects them next to fmw:

```sh
git clone https://github.com/roffe/fmw
git clone -b wgpu https://github.com/roffe/fyne
git clone https://github.com/roffe/gowgpu
cd fmw
go build -tags wayland,migrated_fynedo ./cmd/fmw
./fmw movie.mkv
```

`wayland` builds GLFW for native Wayland, leave it out on X11. `migrated_fynedo` skips Fyne's thread checks.

Or build with the fyne tool, which picks up [cmd/fmw/FyneApp.toml](cmd/fmw/FyneApp.toml) and adds `migrated_fynedo` and the icon by itself:

```sh
cd cmd/fmw
fyne build --tags wayland
fyne install --tags wayland
```

On Wayland the window icon comes from the installed `.desktop` file, so the icon only shows up after `fyne install` (or `fyne package` and `make user-install` in what it makes).

## Hardware decoding

By default fmw decodes with Vulkan Video on the window's own GPU device when the painter runs on Vulkan, and otherwise on the first kind of device FFmpeg's decoder knows that the machine has. `FMW_HWDEC` picks one by FFmpeg's name for it:

```sh
FMW_HWDEC=none ./fmw movie.mkv    # CPU only
FMW_HWDEC=cuda ./fmw movie.mkv    # NVDEC, frames copied through RAM
FMW_HWDEC=vaapi ./fmw movie.mkv
```

## How fast

Measured on Linux with an RTX 4080 SUPER, CPU use in % of one core:

| Video | Zero copy | GPU decode, frames through RAM | CPU decode |
|---|---|---|---|
| 8K60 HEVC 10-bit | 4%, 587 MB | 42%, 1.9 GB | 476%, 2.6 GB |
| 4K60 HEVC 10-bit | 3% | 15% | 81% |
| 1080p HEVC 10-bit film | 2%, 377 MB | 3%, 610 MB | 9%, 489 MB |

At 8K60 zero copy shows all 60 frames a second, going through RAM only manages 20-30 of them.

## Status

Tested on Linux, KDE Plasma on Wayland with NVIDIA. Windows and macOS haven't been tried yet. They should decode on the GPU there too (D3D11, VideoToolbox), with the frames going through RAM.

Known limits:

- ASS/SSA subtitles show as plain text, their styles and positions are dropped
- HDR is tone mapped to SDR, there's no HDR output yet
- Zero copy is Linux only for now. Windows paints with DX12 by default, which needs D3D12 video decoding, that's next on the list
- AV1 decodes on the CPU with dav1d

## Tests

```sh
go test ./...
```

They need FFmpeg, the clips they play are in [testdata](testdata).
