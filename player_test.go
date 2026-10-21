package fmw

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"slices"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
)

// The test files are 3s of test pattern at 320x240 and 25fps with a 440Hz tone:
// an MPEG program stream, whose clock starts at 0.5s, and an MP4 with a single
// key frame, so seeking decodes up to the target.
//
//	ffmpeg -f lavfi -i testsrc=duration=3:size=320x240:rate=25 -f lavfi -i sine=frequency=440:duration=3:sample_rate=44100 \
//		-c:v mpeg1video -q:v 8 -c:a mp2 -b:a 64k -format mpeg testdata/test.mpg
//	ffmpeg -f lavfi -i testsrc=duration=3:size=320x240:rate=25 -f lavfi -i sine=frequency=440:duration=3:sample_rate=48000 \
//		-c:v libx264 -preset veryfast -g 75 -pix_fmt yuv420p -c:a aac -b:a 64k -movflags +faststart testdata/test.mp4
func TestPlayer(t *testing.T) {
	test.NewTempApp(t)
	for _, name := range []string{"testdata/test.mpg", "testdata/test.mp4"} {
		t.Run(name, func(t *testing.T) {
			p := NewPlayer()
			p.SetVolume(0)
			near := func(want, slack time.Duration) {
				t.Helper()
				if got := p.CurrentTime(); got < want-slack || got > want+slack {
					t.Fatalf("at %v, want %v±%v", got, want, slack)
				}
			}
			landed := func() {
				t.Helper()
				for end := time.Now().Add(2 * time.Second); ; time.Sleep(time.Millisecond) {
					p.mu.Lock()
					done := p.seekTo < 0 && !p.fresh
					p.mu.Unlock()
					if done {
						return
					}
					if time.Now().After(end) {
						t.Fatal("seek did not land")
					}
				}
			}

			if err := p.Load(name); err != nil {
				t.Fatal(err)
			}
			landed()
			if d := p.Duration(); d < 2900*time.Millisecond || d > 3100*time.Millisecond {
				t.Fatalf("duration %v, want 3s", d)
			}
			p.mu.Lock()
			first := bytes.Clone(p.screen.Y)
			p.mu.Unlock()

			p.Seek(2 * time.Second)
			landed()
			near(2*time.Second, 0)
			p.mu.Lock()
			moved := !bytes.Equal(first, p.screen.Y)
			m := p.s.m
			p.mu.Unlock()
			if !moved {
				t.Fatal("seek did not change the picture")
			}
			m.mu.Lock()
			next := m.video.t
			m.mu.Unlock()
			if next <= 2 || next > 2.04 {
				t.Fatalf("seek to 2s has the frame at %.3fs next", next)
			}

			p.Play()
			time.Sleep(100 * time.Millisecond) // the sound device starting
			from := p.CurrentTime()
			time.Sleep(300 * time.Millisecond)
			p.Pause()
			if d := p.CurrentTime() - from; d < 200*time.Millisecond || d > 400*time.Millisecond {
				t.Fatalf("played %v in 300ms", d)
			}
			paused := p.CurrentTime()
			time.Sleep(100 * time.Millisecond)
			near(paused, 0)

			p.Play()
			for end := time.Now().Add(3 * time.Second); p.Playing(); time.Sleep(10 * time.Millisecond) {
				if time.Now().After(end) {
					t.Fatalf("still playing at %v", p.CurrentTime())
				}
			}
			near(p.Duration(), 300*time.Millisecond) // how far ahead the device's buffer puts the clock varies

			p.Stop()
			landed()
			near(0, 0)
		})
	}
}

// TestMenu checks what the secondary button's menu offers and that its
// subtitle and full screen items act.
func TestMenu(t *testing.T) {
	test.NewTempApp(t)
	p := NewPlayer()
	p.SetVolume(0)
	w := test.NewWindow(p)
	defer w.Close()
	if err := p.Load("testdata/test.mkv"); err != nil {
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
	if got, want := labels(m.Items[5].ChildMenu.Items), []string{"*eng (aac mono)", "swe (ac3 mono)"}; p.s.out != nil && !slices.Equal(got, want) {
		t.Errorf("audio %q, want %q", got, want)
	}
	subs := m.Items[6].ChildMenu.Items
	if got, want := labels(subs), []string{"Off", "*eng", "swe - Styled"}; !slices.Equal(got, want) {
		t.Fatalf("subtitles %q, want %q", got, want)
	}

	subs[2].Action()
	m.Items[3].Action()
	m = p.menu()
	if got, want := labels(m.Items[6].ChildMenu.Items), []string{"Off", "eng", "*swe - Styled"}; !slices.Equal(got, want) {
		t.Errorf("after choosing swe, subtitles %q", got)
	}
	if !w.FullScreen() || !m.Items[3].Checked {
		t.Error("Full Screen did not put the window in full screen")
	}

	// the test driver runs fyne.Do where it is called, so the UI is only
	// touched here while the player's goroutine is not publishing
	idle := func() {
		for p.mu.Lock(); p.busy; p.mu.Lock() {
			p.mu.Unlock()
			time.Sleep(time.Millisecond)
		}
		p.mu.Unlock()
	}
	idle()
	test.TapAt(p.seek, fyne.NewPos(p.seek.Size().Width/2, p.seek.Size().Height/2))
	idle()
	if f := w.Canvas().Focused(); f != nil {
		t.Errorf("seeking left %T focused, so Space would not reach the window", f)
	}

	p.SetVolume(0.5)
	test.Scroll(w.Canvas(), fyne.NewPos(10, 10), 0, 25) // a notch up, over the picture
	if v := p.Volume(); v < 0.519 || v > 0.521 {
		t.Errorf("scrolling a notch up set the volume to %v", v)
	}

	idle()
	p.menu().Items[4].ChildMenu.Items[1].Action()
	p.mu.Lock()
	turned := p.screen.Rotation
	p.mu.Unlock()
	if got := labels(p.menu().Items[4].ChildMenu.Items); turned != 90 || got[1] != "*90° Clockwise" {
		t.Errorf("rotating clockwise shows the picture at %d°, the menu as %q", turned, got)
	}
}

// testdata/hdr10.mkv is a second of HEVC test pattern in 10-bit PQ with HDR10
// metadata: a 4000 cd/m² mastering display and a brightest pixel of 1000.
//
//	ffmpeg -f lavfi -i testsrc=duration=1:size=192x144:rate=5 -c:v libx265 -pix_fmt yuv420p10le -preset ultrafast \
//		-x265-params "hdr10=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(40000000,50):max-cll=1000,400" \
//		testdata/hdr10.mkv
func TestHDR(t *testing.T) {
	test.NewTempApp(t)
	p := NewPlayer()
	p.SetVolume(0)
	if err := p.Load("testdata/hdr10.mkv"); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		p.mu.Lock()
		f := p.screen
		width, depth, space, transfer, peak := f.Width, f.Depth, f.ColorSpace, f.Transfer, f.Peak
		p.mu.Unlock()
		if width == 0 {
			if time.Now().After(end) {
				t.Fatal("no frame shown")
			}
			continue
		}
		if depth != 10 && depth != 16 { // yuv420p10 from the CPU, P010 from a GPU
			t.Errorf("shown at depth %d", depth)
		}
		if space != canvas.VideoColorSpaceBT2020 || transfer != canvas.VideoTransferPQ || peak != 1000 {
			t.Errorf("shown as colour space %d, transfer %d, peak %v", space, transfer, peak)
		}
		return
	}
}

// TestAudioTracks switches test.mkv to its second audio track, an 880Hz tone,
// and checks that the tone is what decodes from then on.
func TestAudioTracks(t *testing.T) {
	m, err := openMedia("testdata/test.mkv", true, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	pitch := func() float64 { // from the zero crossings of the first half second
		m.seek(0)
		var left []float32
		for len(left) < sampleRate/2 {
			pcm, _, ok := m.samples()
			if !ok {
				break
			}
			for i := 0; i+8 <= len(pcm); i += 8 {
				left = append(left, math.Float32frombits(binary.LittleEndian.Uint32(pcm[i:])))
			}
		}
		crossings := 0
		for i := 1; i < len(left); i++ {
			if (left[i-1] < 0) != (left[i] < 0) {
				crossings++
			}
		}
		return float64(crossings) / 2 / (float64(len(left)) / sampleRate)
	}
	if f := pitch(); f < 400 || f > 480 {
		t.Errorf("the first track plays at %.0fHz, want 440", f)
	}
	if !m.playAudio(1) {
		t.Fatal("cannot switch to the second track")
	}
	if f := pitch(); f < 800 || f > 960 {
		t.Errorf("the second track plays at %.0fHz, want 880", f)
	}
	if _, playing := m.audioTracks(); playing != 1 {
		t.Errorf("track %d plays", playing)
	}
}

// TestControlsRest checks that in full screen the controls, the window's menu
// and the pointer hide while it rests on the picture, and come back as it moves.
func TestControlsRest(t *testing.T) {
	test.NewTempApp(t)
	p := NewPlayer()
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

// TestLoadReplaced checks that a later Load interrupts one waiting for a
// stream's server, and wins.
func TestLoadReplaced(t *testing.T) {
	test.NewTempApp(t)
	p := NewPlayer()
	w := test.NewWindow(p)
	defer w.Close()
	server, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() { // takes connections and never answers
		for {
			c, err := server.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	stream := make(chan error, 1)
	go func() { stream <- p.Load("http://" + server.Addr().String() + "/radio.mp3") }()
	time.Sleep(300 * time.Millisecond) // for it to wait on the server
	if err := p.Load("testdata/test.mp4"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stream:
		if !errors.Is(err, errReplaced) {
			t.Errorf("the stream's Load returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream's Load went on waiting")
	}
	if d := p.Duration(); d < 2*time.Second || d > 4*time.Second {
		t.Errorf("what plays is %v long, not test.mp4", d)
	}
}
