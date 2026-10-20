// Command fmw plays videos and music: fmw [file, folder, URL or playlist ...]
package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"github.com/roffe/browse"
	"github.com/roffe/fmw"
)

const (
	maxRecent = 10
	author    = "roffe"
)

var (
	media = []browse.Filter{
		{Name: "Media", Extensions: []string{"mp4", "m4v", "mkv", "webm", "mov", "avi", "wmv", "flv", "mpg", "mpeg", "ts", "m2ts", "3gp", "ogv", "mp3", "m4a", "flac", "ogg", "opus", "wav", "m3u", "m3u8"}},
		{Name: "All files"},
	}
	subtitles = []browse.Filter{
		{Name: "Subtitles", Extensions: []string{"srt", "ass", "ssa", "vtt", "sup", "idx", "sub"}},
		{Name: "All files"},
	}
)

func main() {
	a := app.NewWithID("com.roffe.fmw")
	prefs := a.Preferences()
	w := a.NewWindow("fmw")
	p := fmw.NewPlayer()
	p.SetVolume(prefs.FloatWithFallback("volume", 1))
	a.Lifecycle().SetOnStopped(func() { prefs.SetFloat("volume", p.Volume()) })
	set := &settings{prefs: prefs, p: p}
	set.apply()
	load := func(e entry) error {
		if err := p.Load(e.Path); err != nil {
			return err
		}
		w.SetTitle(title(e))
		p.Play()
		return nil
	}
	pl := newPlaylist(a, w, load)
	p.OnEnded = pl.ended

	recent := fyne.NewMenuItem("Open Recent", nil)
	recent.ChildMenu = fyne.NewMenu("")
	var play func(path string)
	var mainMenu *fyne.MainMenu // not w.MainMenu(), which the player takes away in full screen
	listRecent := func(paths []string) {
		recent.ChildMenu.Items = nil
		for _, path := range paths {
			recent.ChildMenu.Items = append(recent.ChildMenu.Items, fyne.NewMenuItem(filepath.Base(path), func() { play(path) }))
		}
		recent.Disabled = len(paths) == 0
	}
	play = func(path string) {
		var err error
		if isPlaylist(path) {
			if err = pl.open(path); err == nil {
				pl.playFrom(0)
			}
		} else if err = load(entry{Path: path}); err == nil {
			pl.playingOther()
		}
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		paths := addRecent(prefs.StringList("recent"), path)
		prefs.SetStringList("recent", paths)
		listRecent(paths)
		mainMenu.Refresh()
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
	var about fyne.Window
	showAbout := fyne.NewMenuItem("About", func() {
		if about == nil {
			about = newAbout(a)
			about.SetOnClosed(func() { about = nil })
		}
		about.Show()
		about.RequestFocus()
	})
	showPlaylist := fyne.NewMenuItem("Playlist", pl.show)
	showSettings := fyne.NewMenuItem("Settings…", func() { set.show(a) })
	mainMenu = fyne.NewMainMenu(
		fyne.NewMenu("File", open, openSubs, recent, fyne.NewMenuItemSeparator(), showPlaylist, showSettings),
		fyne.NewMenu("Help", showAbout))
	w.SetMainMenu(mainMenu)
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
		case fyne.KeyN:
			pl.next()
		case fyne.KeyP:
			pl.previous()
		}
	})

	// one file plays, more and folders go on the playlist
	openAll := func(paths []string) {
		switch {
		case len(paths) == 1 && !isFolder(paths[0]):
			play(paths[0])
		case len(paths) > 0:
			pl.playFrom(pl.add(paths))
		}
	}
	w.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		files, subs := sortDrop(pathsOf(uris))
		openAll(files)
		for _, sub := range subs {
			if err := p.AddSubtitles(sub); err != nil {
				dialog.ShowError(err, w)
			}
		}
	})

	if args := os.Args[1:]; len(args) > 0 {
		a.Lifecycle().SetOnStarted(func() { openAll(args) })
	}
	w.SetPadded(false) // the picture goes edge to edge, in full screen too
	w.SetContent(p)
	w.Resize(fyne.NewSize(960, 600))
	w.ShowAndRun()
}

// newAbout makes a window telling which version of fmw this is, the Fyne it
// is built with and who wrote it. The version is FyneApp.toml's when the app
// was built with it, else the one Go took from git.
func newAbout(a fyne.App) fyne.Window {
	info, _ := debug.ReadBuildInfo()
	version, toolkit := versions(info)
	if m := a.Metadata(); m.ID != "" {
		version = m.Version
	}
	w := a.NewWindow("About fmw")
	w.SetContent(container.NewVBox(
		widget.NewLabelWithStyle("fmw", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewForm(
			widget.NewFormItem("Version", widget.NewLabel(version)),
			widget.NewFormItem("Fyne", widget.NewLabel(toolkit)),
			widget.NewFormItem("Author", widget.NewLabel(author)),
		),
	))
	w.CenterOnScreen()
	return w
}

// versions returns the version of fmw and of the Fyne it is built with, as
// the Go toolchain recorded them. Go records none for a module go.work uses
// from a directory.
func versions(info *debug.BuildInfo) (app, toolkit string) {
	if info == nil {
		return "unknown", "unknown"
	}
	app, toolkit = info.Main.Version, "unknown"
	for _, dep := range info.Deps {
		switch {
		case dep.Path != "fyne.io/fyne/v2":
		case dep.Replace != nil:
			toolkit = strings.TrimSpace(dep.Replace.Path + " " + dep.Replace.Version)
		case dep.Version == "(devel)":
			toolkit = "local copy (go.work)"
		default:
			toolkit = dep.Version
		}
	}
	return app, toolkit
}

// sortDrop splits dropped files into those that are not subtitles, to play,
// and the subtitles, to add to what plays, leaving out a VobSub .sub dropped
// with its .idx, which opens through that.
func sortDrop(paths []string) (files, subs []string) {
	for _, path := range paths {
		ext := strings.ToLower(filepath.Ext(path))
		switch {
		case !slices.Contains(subtitles[0].Extensions, strings.TrimPrefix(ext, ".")):
			files = append(files, path)
		case ext == ".sub" && slices.Contains(paths, strings.TrimSuffix(path, filepath.Ext(path))+".idx"):
		default:
			subs = append(subs, path)
		}
	}
	return files, subs
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
