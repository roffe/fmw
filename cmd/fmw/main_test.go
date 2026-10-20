package main

import (
	"fmt"
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
	video, subs := sortDrop([]string{"a.srt", "movie.mkv", "other.mp4", "m.idx", "m.sub", "x.SUB"})
	if want := []string{"a.srt", "m.idx", "x.SUB"}; video != "movie.mkv" || !slices.Equal(subs, want) {
		t.Errorf("dropped files sorted to %q and %q, want movie.mkv and %q", video, subs, want)
	}
}
