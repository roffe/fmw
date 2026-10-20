// Command fmw plays a video: fmw [file or URL]
package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/driver/desktop"
	"github.com/roffe/browse"
	"github.com/roffe/fmw"
)

const maxRecent = 10

var (
	media = []browse.Filter{
		{Name: "Media", Extensions: []string{"mp4", "m4v", "mkv", "webm", "mov", "avi", "wmv", "flv", "mpg", "mpeg", "ts", "m2ts", "3gp", "ogv", "mp3", "m4a", "flac", "ogg", "opus", "wav"}},
		{Name: "All files"},
	}
	subtitles = []browse.Filter{
		{Name: "Subtitles", Extensions: []string{"srt", "ass", "ssa", "vtt", "sup", "idx", "sub"}},
		{Name: "All files"},
	}
)

func main() {
	a := app.NewWithID("nu.roffe.fmw")
	prefs := a.Preferences()
	w := a.NewWindow("fmw")
	p := fmw.NewPlayer()
	p.SetVolume(prefs.FloatWithFallback("volume", 1))
	a.Lifecycle().SetOnStopped(func() { prefs.SetFloat("volume", p.Volume()) })

	recent := fyne.NewMenuItem("Open Recent", nil)
	recent.ChildMenu = fyne.NewMenu("")
	var play func(path string)
	listRecent := func(paths []string) {
		recent.ChildMenu.Items = nil
		for _, path := range paths {
			recent.ChildMenu.Items = append(recent.ChildMenu.Items, fyne.NewMenuItem(filepath.Base(path), func() { play(path) }))
		}
		recent.Disabled = len(paths) == 0
	}
	play = func(path string) {
		if err := p.Load(path); err != nil {
			dialog.ShowError(err, w)
			return
		}
		w.SetTitle(filepath.Base(path))
		paths := addRecent(prefs.StringList("recent"), path)
		prefs.SetStringList("recent", paths)
		listRecent(paths)
		w.MainMenu().Refresh()
		p.Play()
	}

	open := fyne.NewMenuItem("Open…", func() {
		openFile(w, "Open media", media, play)
	})
	openSubs := fyne.NewMenuItem("Open Subtitles…", func() {
		openFile(w, "Open subtitles", subtitles, func(path string) {
			if err := p.AddSubtitles(path); err != nil {
				dialog.ShowError(err, w)
			}
		})
	})
	listRecent(prefs.StringList("recent"))
	w.SetMainMenu(fyne.NewMainMenu(fyne.NewMenu("File", open, openSubs, recent)))
	for _, enter := range []fyne.KeyName{fyne.KeyReturn, fyne.KeyEnter} { // Enter is the keypad's
		w.Canvas().AddShortcut(&desktop.CustomShortcut{KeyName: enter, Modifier: fyne.KeyModifierAlt}, func(fyne.Shortcut) {
			p.ToggleFullScreen()
		})
	}
	w.Canvas().SetOnTypedKey(func(e *fyne.KeyEvent) {
		switch e.Name {
		case fyne.KeySpace:
			p.TogglePlay()
		case fyne.KeyLeft:
			p.Seek(p.CurrentTime() - 5*time.Second)
		case fyne.KeyRight:
			p.Seek(p.CurrentTime() + 5*time.Second)
		}
	})

	w.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		paths := make([]string, len(uris))
		for i, u := range uris {
			paths[i] = u.Path()
		}
		video, subs := sortDrop(paths)
		if video != "" {
			play(video)
		}
		for _, sub := range subs {
			if err := p.AddSubtitles(sub); err != nil {
				dialog.ShowError(err, w)
			}
		}
	})

	if len(os.Args) > 1 {
		a.Lifecycle().SetOnStarted(func() { play(os.Args[1]) })
	}
	w.SetContent(p)
	w.Resize(fyne.NewSize(960, 600))
	w.ShowAndRun()
}

// sortDrop splits dropped files into the first that is not subtitles, to play,
// and the subtitles, to add to it, leaving out a VobSub .sub dropped with its
// .idx, which opens through that.
func sortDrop(paths []string) (video string, subs []string) {
	for _, path := range paths {
		ext := strings.ToLower(filepath.Ext(path))
		switch {
		case !slices.Contains(subtitles[0].Extensions, strings.TrimPrefix(ext, ".")):
			if video == "" {
				video = path
			}
		case ext == ".sub" && slices.Contains(paths, strings.TrimSuffix(path, filepath.Ext(path))+".idx"):
		default:
			subs = append(subs, path)
		}
	}
	return video, subs
}

// openFile asks for a file with the system's own dialog, or Fyne's where it
// has none, and gives open its path on the main goroutine.
func openFile(w fyne.Window, title string, filters []browse.Filter, open func(string)) {
	o := browse.Options{Title: title, Filters: filters, Parent: nativeHandle(w)}
	go func() { // the native dialog blocks until it closes
		path, err := browse.OpenFile(o)
		fyne.Do(func() {
			switch {
			case err == nil:
				open(path)
			case errors.Is(err, browse.ErrUnavailable):
				dialog.ShowFileOpen(func(rc fyne.URIReadCloser, err error) {
					if err != nil || rc == nil {
						return
					}
					rc.Close()
					open(rc.URI().Path())
				}, w)
			case !errors.Is(err, browse.ErrCancelled):
				dialog.ShowError(err, w)
			}
		})
	}()
}

// nativeHandle is w's window as browse wants it for a parent, or 0 where it
// can not have one, as on Wayland.
func nativeHandle(w fyne.Window) (h uintptr) {
	if nw, ok := w.(driver.NativeWindow); ok {
		nw.RunNative(func(ctx any) {
			switch c := ctx.(type) {
			case driver.WindowsWindowContext:
				h = c.HWND
			case driver.MacWindowContext:
				h = c.NSWindow
			case driver.X11WindowContext:
				h = c.WindowHandle
			}
		})
	}
	return h
}

// addRecent puts path first in the recent paths, once, keeping maxRecent.
func addRecent(paths []string, path string) []string {
	out := []string{path}
	for _, old := range paths {
		if old != path && len(out) < maxRecent {
			out = append(out, old)
		}
	}
	return out
}
