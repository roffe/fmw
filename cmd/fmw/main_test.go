package main

import (
	"fmt"
	"runtime/debug"
	"slices"
	"testing"
)

func TestAddRecent(t *testing.T) {
	if got := addRecent([]string{"a", "b", "c"}, "b"); !slices.Equal(got, []string{"b", "a", "c"}) {
		t.Errorf("reopening b gave %v", got)
	}
	var full []string
	for i := range maxRecent {
		full = append(full, fmt.Sprint(i))
	}
	got := addRecent(full, "new")
	if len(got) != maxRecent || got[0] != "new" || got[maxRecent-1] != full[maxRecent-2] {
		t.Errorf("adding to a full list gave %v", got)
	}
}

func TestSortDrop(t *testing.T) {
	files, subs := sortDrop([]string{"a.srt", "movie.mkv", "other.mp4", "m.idx", "m.sub", "x.SUB", "list.m3u"})
	if want, wantSubs := []string{"movie.mkv", "other.mp4", "list.m3u"}, []string{"a.srt", "m.idx", "x.SUB"}; !slices.Equal(files, want) || !slices.Equal(subs, wantSubs) {
		t.Errorf("dropped files sorted to %q and %q, want %q and %q", files, subs, want, wantSubs)
	}
}

func TestVersions(t *testing.T) {
	built := debug.Module{Path: "github.com/roffe/fmw", Version: "v0.1.0"}
	for _, c := range []struct {
		fyne debug.Module
		want string
	}{
		{debug.Module{Path: "fyne.io/fyne/v2", Version: "v2.8.2"}, "v2.8.2"},
		{debug.Module{Path: "fyne.io/fyne/v2", Version: "v2.8.2", Replace: &debug.Module{Path: "github.com/roffe/fyne", Version: "v0.0.0-20261020-ce9aa7749"}}, "github.com/roffe/fyne v0.0.0-20261020-ce9aa7749"},
		{debug.Module{Path: "fyne.io/fyne/v2", Version: "v2.8.2", Replace: &debug.Module{Path: "../fyne"}}, "../fyne"},
		{debug.Module{Path: "fyne.io/fyne/v2", Version: "(devel)"}, "local copy (go.work)"},
	} {
		info := &debug.BuildInfo{Main: built, Deps: []*debug.Module{{Path: "fyne.io/systray", Version: "v1.12.3"}, &c.fyne}}
		if app, toolkit := versions(info); app != "v0.1.0" || toolkit != c.want {
			t.Errorf("versions gave %q and %q, want v0.1.0 and %q", app, toolkit, c.want)
		}
	}
	if app, toolkit := versions(nil); app != "unknown" || toolkit != "unknown" {
		t.Errorf("without build info, versions gave %q and %q", app, toolkit)
	}
}
