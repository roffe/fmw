package main

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"github.com/roffe/fmw"
)

func TestPlaylistPlaysThrough(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	p := fmw.NewVideo()
	p.SetVolume(0)
	w := test.NewWindow(p)
	defer w.Close()
	started := make(chan string, 4)
	pl := &playlist{app: a, main: w, current: -1, selected: -1, load: func(e entry, done func(error)) {
		err := p.Load(e.Path)
		if err == nil {
			p.Play()
			started <- e.Path
		}
		done(err)
	}}
	// The test driver calls OnEnded on the player's goroutine, the real ones
	// on the main goroutine, which this test stands in for.
	ended := make(chan struct{}, 1)
	p.OnEnded = func() { ended <- struct{}{} }

	pl.add([]string{"../../testdata/test.mp4", "missing.mp3", "../../testdata/test.mpg"}) // the first plays 3 seconds
	pl.playFrom(0, 1)
	if got := <-started; got != "../../testdata/test.mp4" {
		t.Fatalf("%s started first", got)
	}
	select {
	case <-ended:
	case <-time.After(15 * time.Second):
		t.Fatal("the first entry did not end")
	}
	pl.ended()
	if got := <-started; got != "../../testdata/test.mpg" || pl.current != 2 {
		t.Errorf("%s started next, entry %d, want the third past the missing one", got, pl.current)
	}
}

func TestPlaylistSkips(t *testing.T) {
	var prev, next bool
	pl := &playlist{current: -1, selected: -1, skip: func(p, n func()) { prev, next = p != nil, n != nil }}
	pl.add([]string{"a.mp4", "b.mp4", "c.mp4"})
	for _, c := range []struct {
		current    int
		prev, next bool
	}{{-1, false, false}, {0, false, true}, {1, true, true}, {2, true, false}} {
		pl.current = c.current
		pl.refresh()
		if prev != c.prev || next != c.next {
			t.Errorf("entry %d: prev %v next %v, want %v %v", c.current, prev, next, c.prev, c.next)
		}
	}
}
