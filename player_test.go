package fmw

import (
	"bytes"
	"testing"
	"time"

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
			first := bytes.Clone(p.front.Pix)
			p.mu.Unlock()

			p.Seek(2 * time.Second)
			landed()
			near(2*time.Second, 0)
			p.mu.Lock()
			moved := !bytes.Equal(first, p.front.Pix)
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
