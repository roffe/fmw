package main

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
)

// TestMenu checks what the secondary button's menu offers and that its items
// act, and that the seek slider and the mouse wheel do theirs.
func TestMenu(t *testing.T) {
	test.NewTempApp(t)
	p := newPlayer()
	p.video.SetVolume(0)
	w := test.NewWindow(p)
	defer w.Close()

	// before anything is loaded, so no decoding races with the test driver,
	// which runs fyne.Do where it is called
	test.TapAt(p.seek, fyne.NewPos(p.seek.Size().Width/2, p.seek.Size().Height/2))
	if f := w.Canvas().Focused(); f != nil {
		t.Errorf("seeking left %T focused, so Space would not reach the window", f)
	}
	p.video.SetVolume(0.5)
	test.Scroll(w.Canvas(), fyne.NewPos(10, 10), 0, 25) // a notch up, over the picture
	if v := p.video.Volume(); v < 0.519 || v > 0.521 || p.volume.Value != v {
		t.Errorf("scrolling a notch up set the volume to %v, the slider to %v", v, p.volume.Value)
	}
	p.video.SetVolume(0)

	if err := p.video.Load("../../testdata/test.mkv"); err != nil {
		t.Fatal(err)
	}
	labels := func(items []*fyne.MenuItem) (l []string) {
		for _, item := range items {
			if item.Checked {
				l = append(l, "*"+item.Label)
			} else {
				l = append(l, item.Label)
			}
		}
		return l
	}

	m := p.menu()
	if got, want := labels(m.Items), []string{"Play", "Stop", "", "Full Screen", "Rotate", "Audio", "Subtitles"}; !slices.Equal(got, want) {
		t.Fatalf("menu %q, want %q", got, want)
	}
	if names, _ := p.video.AudioTracks(); names != nil {
		if got, want := labels(m.Items[5].ChildMenu.Items), []string{"*eng (aac mono)", "swe (ac3 mono)"}; !slices.Equal(got, want) {
			t.Errorf("audio %q, want %q", got, want)
		}
	}
	subs := m.Items[6].ChildMenu.Items
	if got, want := labels(subs), []string{"Off", "*eng", "swe - Styled"}; !slices.Equal(got, want) {
		t.Fatalf("subtitles %q, want %q", got, want)
	}

	subs[2].Action()
	m.Items[3].Action()
	m = p.menu()
	if got := labels(m.Items[6].ChildMenu.Items); !slices.Equal(got, []string{"Off", "eng", "*swe - Styled"}) {
		t.Errorf("after choosing swe, subtitles %q", got)
	}
	if !w.FullScreen() || !m.Items[3].Checked {
		t.Error("Full Screen did not put the window in full screen")
	}

	m.Items[4].ChildMenu.Items[1].Action()
	if got := labels(p.menu().Items[4].ChildMenu.Items); p.video.Rotation() != 90 || got[1] != "*90° Clockwise" {
		t.Errorf("rotating clockwise turns the picture %d°, the menu shows %q", p.video.Rotation(), got)
	}
}

// TestControlsRest checks that in full screen the controls, the window's menu
// and the pointer hide while it rests on the picture, and come back as it moves.
func TestControlsRest(t *testing.T) {
	test.NewTempApp(t)
	p := newPlayer()
	w := test.NewWindow(p)
	defer w.Close()
	menu := fyne.NewMainMenu(fyne.NewMenu("File"))
	w.SetMainMenu(menu)
	rest := func() { // as the timer would, without waiting for it
		p.idle.Stop()
		p.moved = p.moved.Add(-restDelay)
		p.rest()
	}

	test.DoubleTap(p)
	if !w.FullScreen() {
		t.Fatal("a double tap did not go full screen")
	}
	p.MouseMoved(&desktop.MouseEvent{})
	rest()
	if p.bar.Visible() || w.MainMenu() != nil || p.Cursor() != desktop.HiddenCursor {
		t.Error("resting on the picture left the controls, the menu or the pointer showing")
	}
	p.MouseMoved(&desktop.MouseEvent{})
	if !p.bar.Visible() || w.MainMenu() != menu || p.Cursor() != desktop.DefaultCursor {
		t.Error("moving did not bring the controls, the menu and the pointer back")
	}
	p.MouseOut() // onto the controls
	rest()
	if !p.bar.Visible() {
		t.Error("the controls hid under the pointer")
	}

	p.MouseIn(&desktop.MouseEvent{})
	rest()
	test.DoubleTap(p)
	if w.FullScreen() || !p.bar.Visible() || w.MainMenu() != menu {
		t.Error("leaving full screen did not show the controls and the menu")
	}
}

func TestSetSkip(t *testing.T) {
	test.NewTempApp(t)
	p := newPlayer()
	if !p.prev.Disabled() || !p.next.Disabled() {
		t.Fatal("skip buttons enabled without a playlist")
	}
	skipped := 0
	p.setSkip(nil, func() { skipped++ })
	if !p.prev.Disabled() || p.next.Disabled() {
		t.Errorf("prev disabled %v, next disabled %v, want only prev", p.prev.Disabled(), p.next.Disabled())
	}
	test.Tap(p.next)
	test.Tap(p.prev) // disabled, and with nothing to call
	if skipped != 1 {
		t.Errorf("next skipped %d times, want 1", skipped)
	}
	p.setSkip(nil, nil)
	if !p.prev.Disabled() || !p.next.Disabled() {
		t.Error("skip buttons enabled with nothing to skip to")
	}
}
