package fmw

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

// testdata/subs.srt is two cues, and testdata/test.mkv is test.mp4 with them
// twice, as SubRip (eng, shown by default) and as ASS (swe, titled Styled),
// and a second audio track, an 880Hz tone in AC-3 (swe):
//
//	ffmpeg -i test.mp4 -i subs.srt -i subs.srt -f lavfi -i sine=frequency=880:duration=3:sample_rate=48000 \
//		-map 0 -map 3 -map 1 -map 2 -c:v copy -c:a:0 copy -c:a:1 ac3 -ac:a:1 1 -b:a:1 32k -c:s:0 srt -c:s:1 ass \
//		-metadata:s:a:0 language=eng -metadata:s:a:1 language=swe -metadata:s:s:0 language=eng -metadata:s:s:1 language=swe \
//		-metadata:s:s:1 title=Styled -disposition:a:0 default -disposition:s:0 default test.mkv
func TestSubtitles(t *testing.T) {
	test.NewTempApp(t)
	p := NewPlayer()
	p.SetVolume(0)
	if err := p.Load("testdata/test.mkv"); err != nil {
		t.Fatal(err)
	}
	m := p.s.m
	if names, showing := m.subtitles(); !slices.Equal(names, []string{"eng", "swe - Styled"}) || showing != 0 {
		t.Fatalf("tracks %q, showing %d", names, showing)
	}
	shown := func() []string {
		p.mu.Lock()
		defer p.mu.Unlock()
		var text []string
		for _, e := range p.shownSubs {
			text = append(text, e.text)
		}
		return text
	}

	for track := range 2 { // SubRip, then ASS, which both decode to ASS
		p.showSubtitles(track)
		p.Seek(0)
		p.Play()
		for end := time.Now().Add(3 * time.Second); p.CurrentTime() < time.Second; time.Sleep(5 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatalf("track %d: playback stuck at %v", track, p.CurrentTime())
			}
		}
		if got := shown(); !slices.Equal(got, []string{"First line\nsecond line"}) {
			t.Errorf("track %d shows %q at 1s", track, got)
		}
		p.Pause()
	}

	if err := p.AddSubtitles("testdata/subs.srt"); err != nil {
		t.Fatal(err)
	}
	if _, showing := m.subtitles(); showing != 2 {
		t.Errorf("added subtitles are not showing, %d is", showing)
	}
	if on := m.subtitlesAt(2); len(on) != 1 || on[0].text != "Ünïcödé tagged" {
		t.Errorf("added subtitles show %v at 2s", on)
	}

	// a file of two tracks, read as playback gets to them
	if err := p.AddSubtitles("testdata/test.mkv"); err != nil {
		t.Fatal(err)
	}
	if names, showing := m.subtitles(); !slices.Equal(names[3:], []string{"test.mkv - eng", "test.mkv - swe - Styled"}) || showing != 3 {
		t.Fatalf("tracks %q, showing %d", names, showing)
	}
	for _, at := range []float64{2, 1, 2} { // and back, as after a seek
		m.seekSubtitles()
		want := map[float64]string{1: "First line\nsecond line", 2: "Ünïcödé tagged"}[at]
		if on := m.subtitlesAt(at); len(on) != 1 || on[0].text != want {
			t.Errorf("the file's first track shows %v at %vs", on, at)
		}
	}
}

func TestSidecars(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "Subs"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"movie.mkv", "movie.srt", "movie.en.srt", "movie2.srt", "other.ass", "movie.txt", "movie.sub", "Subs/movie.idx", "Subs/movie.sub"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := sidecars(filepath.Join(dir, "movie.mkv"))
	if want := []string{filepath.Join(dir, "movie.en.srt"), filepath.Join(dir, "movie.srt"), filepath.Join(dir, "Subs", "movie.idx")}; !slices.Equal(got, want) {
		t.Errorf("sidecars %q, want %q", got, want)
	}
}
