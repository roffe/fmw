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
	p := fmw.NewPlayer()
	p.SetVolume(0)
	w := test.NewWindow(p)
	defer w.Close()
	started := make(chan string, 4)
	pl := &playlist{app: a, main: w, current: -1, selected: -1, load: func(e entry) error {
		if err := p.Load(e.Path); err != nil {
			return err
		}
		p.Play()
		started <- e.Path
		return nil
	}}
	// The test driver calls OnEnded on the player's goroutine, the real ones
	// on the main goroutine, which this test stands in for.
	ended := make(chan struct{}, 1)
	p.OnEnded = func() { ended <- struct{}{} }

	pl.add([]string{"../../testdata/test.mp4", "missing.mp3", "../../testdata/test.mpg"}) // the first plays 3 seconds
	pl.playFrom(0)
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
