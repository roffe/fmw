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
- Full screen, with the controls, the window menu and the mouse pointer hiding when the mouse stops moving
- MilkDrop visualizations for music, through projectM (Linux, opt in, see [Visualizations](#visualizations))
- A playlist window: drop files, folders and M3U playlists on it, open and save playlists, old Winamp ones too

## Controls

The widget handles the mouse by itself:

| | |
|---|---|
| Double click | full screen |
| Mouse wheel | volume, 2% a notch |
| Right click | menu: play/pause, stop, full screen, rotate, audio track, subtitles, next visualization |

Keys are up to the app, the fmw app sets these up (see [cmd/fmw/main.go](cmd/fmw/main.go)):

| | |
|---|---|
| Space | play / pause |
| Left / Right | 5 seconds back / forward |
| Alt+Enter | full screen, keypad Enter works too |
| N / P | next / previous on the playlist |

The app also has File > Open, Open Subtitles and Open Recent with native file dialogs, drag and drop (one file plays, several go on the playlist, an M3U replaces it, subtitle files get added to what plays), remembers the volume and puts the file name in the title.

File > Playlist opens the playlist window. Drop files, folders or M3U/M3U8 playlists on it to add them, a folder's music and videos in name order, double click or Enter plays one, Delete removes it. It plays through by itself, skipping files that are gone, and it's still there next time you start fmw. Old M3U playlists made on Windows work: they're read as Windows-1252 when they aren't UTF-8, with backslashes in their paths. Playlists are saved as M3U8, with paths relative to the playlist where the files are next to it or below.

File > Settings has hardware decoding and the visualization settings. They take effect right away.

## Download

Builds are on the [releases](https://github.com/roffe/fmw/releases) page: an AppImage and a tarball for Linux, a zip for Windows. FFmpeg 9.0 comes with them, so there's nothing else to install. The [nightly](https://github.com/roffe/fmw/releases/tag/nightly) gets built from every push to master.

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

The rest is what you'd expect: `Play`, `Pause`, `TogglePlay`, `Stop`, `Seek`, `CurrentTime`, `Duration`, `Playing`, `SetVolume`, `Volume`, `AddSubtitles` and `ToggleFullScreen`. All of them are safe to call from any goroutine. `OnEnded` gets called when what's loaded has played to the end, that's how the app's playlist moves on.

`SetHardwareDecoding` and `SetVisualization` take what the settings window sets, `HardwareDecoders` lists the kinds of device FFmpeg can decode on and `HasVisualizations` says if fmw was built with projectM.

## Building

You need:

1. Go 1.27
2. FFmpeg 6.1 or newer with its development files, found through pkg-config (libavformat, libavcodec, libavutil, libswscale, libswresample)
3. The Vulkan headers on Linux and Windows, for the zero copy path. On Arch that's `vulkan-headers`, on Debian and Ubuntu `libvulkan-dev`. Without them, or with an FFmpeg built without Vulkan, build with `-tags novulkan`
Fyne comes from my fork's `wgpu` branch, go.mod points there. It has the `canvas.VideoFrame` the player draws with and the WebGPU painter, with [gowgpu](https://github.com/roffe/gowgpu) under it. Upstream Fyne doesn't have `VideoFrame`.

```sh
git clone https://github.com/roffe/fmw
cd fmw
go build -tags wayland,migrated_fynedo ./cmd/fmw
./fmw movie.mkv
```

`wayland` builds GLFW for Wayland only. Leave it out and GLFW gets both X11 and Wayland and picks one at start, that's how the releases are built. `migrated_fynedo` skips Fyne's thread checks.

Or build with the fyne tool, which picks up [cmd/fmw/FyneApp.toml](cmd/fmw/FyneApp.toml) and adds `migrated_fynedo` and the icon by itself:

```sh
cd cmd/fmw
fyne build --tags wayland
fyne install --tags wayland
```

On Wayland the window icon comes from the installed `.desktop` file, so the icon only shows up after `fyne install` (or `fyne package` and `make user-install` in what it makes).

To work on Fyne or gowgpu at the same time, clone them next to fmw and `go work init . ../fyne ../gowgpu`.

`make linux`, `make appimage` and `make windows` build what the releases have, into `dist`. They build against FFmpeg 9.0 from [BtbN's builds](https://github.com/BtbN/FFmpeg-Builds) and bundle it, it gets downloaded to `.tmp` the first time along with the Vulkan headers. `make windows` cross compiles with llvm-mingw's `x86_64-w64-mingw32-clang`, `WINCC` picks another compiler.

## Hardware decoding

By default fmw decodes with Vulkan Video on the window's own GPU device when the painter runs on Vulkan, and otherwise on the first kind of device FFmpeg's decoder knows that the machine has. `FMW_HWDEC` picks one by FFmpeg's name for it:

```sh
FMW_HWDEC=none ./fmw movie.mkv    # CPU only
FMW_HWDEC=cuda ./fmw movie.mkv    # NVDEC, frames copied through RAM
FMW_HWDEC=vaapi ./fmw movie.mkv
```

## Visualizations

Music without a picture, or with just the album cover, gets MilkDrop presets dancing to it through [projectM](https://github.com/projectM-visualizer/projectm). It's opt in and Linux only for now:

```sh
go build -tags wayland,migrated_fynedo,projectm ./cmd/fmw
```

It needs libprojectM 4 (`libprojectm` on Arch) and presets, by default from `/usr/share/projectM/presets`, where Arch's `projectm` package puts 4000+ of them. Settings or `FMW_PRESETS` point it somewhere else. Right click > Next Visualization cuts to another one, otherwise projectM moves on by itself.

Presets get drawn at a fixed size, 1920 x 1080 unless you pick another in Settings, and scaled to cover the player, cut at the sides or the top and bottom when the window has another shape. projectM starts a preset over from black whenever the size it draws at changes, so this way resizing the window or going full screen doesn't.

projectM only speaks OpenGL, so it draws in its own headless EGL context and every frame is read back and uploaded to the painter. That's around 25% of a core at 1080p. Handing the texture straight to the painter's GPU device is next.

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
