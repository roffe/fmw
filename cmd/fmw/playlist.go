package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/roffe/browse"
)

var playlists = []browse.Filter{
	{Name: "Playlists", Extensions: []string{"m3u8", "m3u"}},
	{Name: "All files"},
}

// playlist is the files to play one after the other, shown in a window of
// its own. Only the main goroutine uses it.
type playlist struct {
	app      fyne.App
	load     func(entry, func(error)) // loads an entry into the player, plays it and says how that went
	entries  []entry
	current  int         // the entry playing, -1 for none
	selected int         // in the window, -1 for none
	kept     string      // where the entries are kept between runs
	main     fyne.Window // for dialogs while the playlist window is closed
	win      fyne.Window
	list     *widget.List
}

// newPlaylist returns the playlist kept from the last run.
func newPlaylist(a fyne.App, main fyne.Window, load func(entry, func(error))) *playlist {
	pl := &playlist{app: a, main: main, load: load, current: -1, selected: -1}
	if root := a.Storage().RootURI(); root != nil {
		pl.kept = filepath.Join(root.Path(), "playlist.m3u8")
		pl.entries, _ = readM3U(pl.kept)
	}
	return pl
}

// changed shows the entries as they are now and keeps them for the next run.
func (pl *playlist) changed() {
	pl.refresh()
	if pl.kept == "" {
		return
	}
	err := os.MkdirAll(filepath.Dir(pl.kept), 0o755)
	if err == nil {
		err = writeM3U(pl.kept, pl.entries)
	}
	if err != nil {
		fyne.LogError("fmw: keeping the playlist", err)
	}
}

// parent is the window for the playlist's dialogs.
func (pl *playlist) parent() fyne.Window {
	if pl.win != nil {
		return pl.win
	}
	return pl.main
}

func (pl *playlist) refresh() {
	if pl.list != nil {
		pl.list.Refresh()
	}
}

// add adds the files at paths to the end, a playlist's entries and a
// folder's media files in their place, and returns where the first went.
func (pl *playlist) add(paths []string) int {
	first := len(pl.entries)
	for _, path := range paths {
		switch {
		case isPlaylist(path):
			list, err := readM3U(path)
			if err != nil {
				dialog.ShowError(err, pl.parent())
				continue
			}
			pl.entries = append(pl.entries, list...)
		case isFolder(path):
			pl.entries = append(pl.entries, mediaIn(path)...)
		default:
			pl.entries = append(pl.entries, entry{Path: path, Seconds: -1})
		}
	}
	pl.changed()
	return first
}

func isFolder(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// mediaIn returns the media files in dir and the folders in it, by name.
func mediaIn(dir string) []entry {
	var list []entry
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		if err == nil && !d.IsDir() && !isPlaylist(path) && slices.Contains(media[0].Extensions, ext) {
			list = append(list, entry{Path: path, Seconds: -1})
		}
		return nil
	})
	return list
}

// open puts the entries of the playlist file at path in place of these.
func (pl *playlist) open(path string) error {
	list, err := readM3U(path)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return errors.New(filepath.Base(path) + " lists nothing to play")
	}
	pl.entries, pl.current = list, -1
	pl.unselect()
	pl.changed()
	return nil
}

// play plays entry i.
func (pl *playlist) play(i int) {
	e := pl.entries[i]
	pl.load(e, func(err error) {
		if err != nil {
			dialog.ShowError(err, pl.parent())
			return
		}
		pl.current = pl.where(i, e)
		pl.refresh()
	})
}

// playFrom plays entry i, or the first that loads from it on, going step at a
// time.
func (pl *playlist) playFrom(i, step int) {
	if i < 0 || i >= len(pl.entries) {
		return
	}
	e := pl.entries[i]
	pl.load(e, func(err error) {
		at := pl.where(i, e) // the entries can have changed while e loaded
		if err != nil {
			fyne.LogError("fmw: skipping "+e.Path, err)
			if at >= 0 {
				pl.playFrom(at+step, step)
			}
			return
		}
		pl.current = at
		pl.refresh()
	})
}

// where returns where e is on the playlist, i if it still is there, or -1.
func (pl *playlist) where(i int, e entry) int {
	if i < len(pl.entries) && pl.entries[i] == e {
		return i
	}
	return slices.Index(pl.entries, e)
}

func (pl *playlist) next() {
	pl.playFrom(pl.current+1, 1)
}

func (pl *playlist) previous() {
	pl.playFrom(pl.current-1, -1)
}

// ended moves on to the next entry when the one playing ends.
func (pl *playlist) ended() {
	if pl.current >= 0 {
		pl.next()
	}
}

// playingOther marks that what plays is not from the playlist.
func (pl *playlist) playingOther() {
	pl.current = -1
	pl.refresh()
}

func (pl *playlist) remove(i int) {
	pl.entries = append(pl.entries[:i], pl.entries[i+1:]...)
	switch {
	case i == pl.current:
		pl.current = -1
	case i < pl.current:
		pl.current--
	}
	pl.unselect()
	pl.changed()
}

func (pl *playlist) clear() {
	pl.entries, pl.current = nil, -1
	pl.unselect()
	pl.changed()
}

func (pl *playlist) unselect() {
	pl.selected = -1
	if pl.list != nil {
		pl.list.UnselectAll()
	}
}

// show shows the playlist window, where files and playlists dropped are added.
func (pl *playlist) show() {
	if pl.win == nil {
		pl.win = pl.makeWindow()
	}
	pl.win.Show()
	pl.win.RequestFocus()
}

func (pl *playlist) makeWindow() fyne.Window {
	w := pl.app.NewWindow("Playlist")
	pl.list = widget.NewList(
		func() int { return len(pl.entries) },
		func() fyne.CanvasObject { return newRow(pl) },
		func(id widget.ListItemID, o fyne.CanvasObject) { o.(*row).show(id) },
	)
	pl.list.OnSelected = func(id widget.ListItemID) { pl.selected = id }
	pl.list.OnUnselected = func(widget.ListItemID) { pl.selected = -1 }
	if pl.current >= 0 {
		pl.list.ScrollTo(pl.current)
	}
	bar := widget.NewToolbar(
		widget.NewToolbarAction(theme.MediaSkipPreviousIcon(), pl.previous),
		widget.NewToolbarAction(theme.MediaSkipNextIcon(), pl.next),
		widget.NewToolbarSeparator(),
		widget.NewToolbarAction(theme.ContentAddIcon(), pl.browseAdd),
		widget.NewToolbarAction(theme.ContentRemoveIcon(), func() {
			if pl.selected >= 0 {
				pl.remove(pl.selected)
			}
		}),
		widget.NewToolbarAction(theme.DeleteIcon(), pl.clear),
		widget.NewToolbarSpacer(),
		widget.NewToolbarAction(theme.FolderOpenIcon(), pl.browseOpen),
		widget.NewToolbarAction(theme.DocumentSaveIcon(), pl.browseSave),
	)
	w.SetContent(container.NewBorder(bar, nil, nil, nil, pl.list))
	w.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		pl.add(pathsOf(uris))
	})
	w.Canvas().SetOnTypedKey(func(e *fyne.KeyEvent) {
		if pl.selected < 0 {
			return
		}
		switch e.Name {
		case fyne.KeyReturn, fyne.KeyEnter:
			pl.play(pl.selected)
		case fyne.KeyDelete, fyne.KeyBackspace:
			pl.remove(pl.selected)
		}
	})
	w.SetOnClosed(func() { pl.win, pl.list, pl.selected = nil, nil, -1 })
	w.Resize(fyne.NewSize(400, 500))
	return w
}

func (pl *playlist) browseAdd() {
	o := browse.Options{Title: "Add to playlist", Filters: media, Parent: nativeHandle(pl.win)}
	go func() { // the native dialog blocks until it closes
		paths, err := browse.OpenFiles(o)
		fyne.Do(func() {
			switch {
			case err == nil:
				pl.add(paths)
			case errors.Is(err, browse.ErrUnavailable):
				dialog.ShowFileOpen(func(rc fyne.URIReadCloser, err error) {
					if err == nil && rc != nil {
						rc.Close()
						pl.add([]string{rc.URI().Path()})
					}
				}, pl.parent())
			case !errors.Is(err, browse.ErrCancelled):
				dialog.ShowError(err, pl.parent())
			}
		})
	}()
}

func (pl *playlist) browseOpen() {
	openFile(pl.parent(), "Open playlist", playlists, func(path string) {
		if err := pl.open(path); err != nil {
			dialog.ShowError(err, pl.parent())
		}
	})
}

func (pl *playlist) browseSave() {
	save := func(path string) {
		if !isPlaylist(path) {
			path += ".m3u8"
		}
		if err := writeM3U(path, pl.entries); err != nil {
			dialog.ShowError(err, pl.parent())
		}
	}
	o := browse.Options{Title: "Save playlist", Name: "playlist.m3u8", Filters: playlists, Parent: nativeHandle(pl.win)}
	go func() { // the native dialog blocks until it closes
		path, err := browse.SaveFile(o)
		fyne.Do(func() {
			switch {
			case err == nil:
				save(path)
			case errors.Is(err, browse.ErrUnavailable):
				d := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
					if err == nil && wc != nil {
						wc.Close()
						save(wc.URI().Path())
					}
				}, pl.parent())
				d.SetFileName("playlist.m3u8")
				d.Show()
			case !errors.Is(err, browse.ErrCancelled):
				dialog.ShowError(err, pl.parent())
			}
		})
	}()
}

// row is an entry in the playlist window, which a double tap plays.
type row struct {
	widget.BaseWidget
	pl           *playlist
	id           widget.ListItemID
	icon         *widget.Icon
	name, length *widget.Label
	box          *fyne.Container
}

func newRow(pl *playlist) *row {
	r := &row{pl: pl, icon: widget.NewIcon(nil), name: widget.NewLabel(""), length: widget.NewLabel("")}
	r.name.Truncation = fyne.TextTruncateEllipsis
	icon := container.NewGridWrap(fyne.NewSquareSize(theme.IconInlineSize()), r.icon) // as wide when empty
	r.box = container.NewBorder(nil, nil, container.NewCenter(icon), r.length, r.name)
	r.ExtendBaseWidget(r)
	return r
}

func (r *row) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.box)
}

func (r *row) show(id widget.ListItemID) {
	r.id = id
	e := r.pl.entries[id]
	if id == r.pl.current {
		r.icon.SetResource(theme.MediaPlayIcon())
	} else {
		r.icon.SetResource(nil)
	}
	r.name.SetText(fmt.Sprintf("%d. %s", id+1, e.name()))
	r.length.SetText("")
	if e.Seconds >= 0 {
		r.length.SetText(fmt.Sprintf("%d:%02d", e.Seconds/60, e.Seconds%60))
	}
	r.box.Refresh() // lays the length out at its new width
}

// Tapped selects the entry, which the list does not do itself for a row
// that takes double taps.
func (r *row) Tapped(*fyne.PointEvent) {
	r.pl.list.Select(r.id)
}

func (r *row) DoubleTapped(*fyne.PointEvent) {
	r.pl.list.Select(r.id)
	r.pl.play(r.id)
}

// pathsOf returns the paths of dropped files, and the URLs of dropped links.
func pathsOf(uris []fyne.URI) []string {
	paths := make([]string, len(uris))
	for i, u := range uris {
		if u.Scheme() == "file" {
			paths[i] = u.Path()
		} else {
			paths[i] = u.String()
		}
	}
	return paths
}

// title is what the window shows while e plays.
func title(e entry) string {
	if e.Title != "" || isURL(e.Path) {
		return e.name()
	}
	return filepath.Base(e.Path)
}
