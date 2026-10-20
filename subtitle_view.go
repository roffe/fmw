package fmw

import (
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// subtitleLayout lays subtitles over the screen: text centred along the
// bottom of the picture on a dim box, pictures where their events put them on
// the frame they were made for. That frame is fitted to the screen as it is,
// not stretched over the picture: a film cropped to 2.39:1 keeps the 16:9
// subtitles of its disc, which would otherwise come out wide.
type subtitleLayout struct {
	p      *Player
	box    *canvas.Rectangle
	lines  []*canvas.Text
	images []*canvas.Image
	events []*subEvent // the images'
}

func (l *subtitleLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.Size{}
}

func (l *subtitleLayout) Layout(_ []fyne.CanvasObject, size fyne.Size) {
	pos, area := l.p.pictureArea(size)
	for i, img := range l.images {
		e := l.events[i]
		at, frame, cw, ch := pos, area, e.cw, e.ch
		if cw > 0 && ch > 0 {
			at, frame = fit(size, float32(cw)/float32(ch))
		} else {
			cw, ch = l.p.screen.Width, l.p.screen.Height
		}
		if cw == 0 || ch == 0 {
			continue
		}
		sx, sy := frame.Width/float32(cw), frame.Height/float32(ch)
		b := e.img.Bounds()
		img.Move(at.AddXY(float32(e.x)*sx, float32(e.y)*sy))
		img.Resize(fyne.NewSize(float32(b.Dx())*sx, float32(b.Dy())*sy))
	}

	l.box.Hidden = len(l.lines) == 0
	if len(l.lines) == 0 {
		return
	}
	style := fyne.TextStyle{Bold: true}
	widestAt := func(size float32) (w float32) {
		for _, t := range l.lines {
			w = max(w, fyne.MeasureText(t.Text, size, style).Width)
		}
		return w
	}
	textSize := min(max(area.Height/22, 12), 64)
	if w, limit := widestAt(textSize), area.Width*0.9; w > limit {
		textSize *= limit / w
	}
	lineHeight := fyne.MeasureText("Ag", textSize, style).Height
	pad := textSize / 3
	top := pos.Y + area.Height*0.94 - lineHeight*float32(len(l.lines))
	for i, t := range l.lines {
		if t.TextSize != textSize {
			t.TextSize = textSize
			t.Refresh()
		}
		w := fyne.MeasureText(t.Text, textSize, style).Width
		t.Move(fyne.NewPos(pos.X+(area.Width-w)/2, top+lineHeight*float32(i)))
		t.Resize(fyne.NewSize(w, lineHeight))
	}
	widest := widestAt(textSize) // text does not shrink quite in proportion
	l.box.Move(fyne.NewPos(pos.X+(area.Width-widest)/2-pad, top-pad/2))
	l.box.Resize(fyne.NewSize(widest+2*pad, lineHeight*float32(len(l.lines))+pad))
}

// pictureArea is where the picture shows on a screen of size, as it fits it.
func (p *Player) pictureArea(size fyne.Size) (fyne.Position, fyne.Size) {
	a := p.screen.Aspect
	if a <= 0 && p.screen.Height > 0 {
		a = float32(p.screen.Width) / float32(p.screen.Height)
	}
	if p.screen.Rotation%180 != 0 && a > 0 {
		a = 1 / a
	}
	return fit(size, a)
}

// fit is the biggest area of the given aspect, width over height, centred in size.
func fit(size fyne.Size, aspect float32) (fyne.Position, fyne.Size) {
	if aspect <= 0 || size.Height <= 0 {
		return fyne.Position{}, size
	}
	if size.Width/size.Height > aspect {
		w := size.Height * aspect
		return fyne.NewPos((size.Width-w)/2, 0), fyne.NewSize(w, size.Height)
	}
	h := size.Width / aspect
	return fyne.NewPos(0, (size.Height-h)/2), fyne.NewSize(size.Width, h)
}

// updateSubtitles puts the events showing at now over the picture when they
// changed, or lays them out again when the picture did. Called by publish.
func (p *Player) updateSubtitles(now float64) {
	var on []*subEvent
	if p.s != nil {
		on = p.s.m.subtitlesAt(now)
	}
	geometry := [4]float32{float32(p.screen.Width), float32(p.screen.Height), p.screen.Aspect, float32(p.screen.Rotation)}
	if slices.Equal(on, p.shownSubs) {
		if geometry != p.shownGeometry {
			p.shownGeometry = geometry
			p.subLayout.Layout(nil, p.subs.Size())
		}
		return
	}
	p.shownSubs, p.shownGeometry = on, geometry

	l := p.subLayout
	l.lines, l.images, l.events = nil, nil, nil
	objects := []fyne.CanvasObject{l.box}
	for _, e := range on {
		if e.img != nil {
			img := canvas.NewImageFromImage(e.img)
			img.ScaleMode = canvas.ImageScaleFastest
			l.images, l.events = append(l.images, img), append(l.events, e)
			objects = append(objects, img)
			continue
		}
		for _, line := range strings.Split(e.text, "\n") {
			t := canvas.NewText(line, color.White)
			t.TextStyle.Bold = true
			l.lines = append(l.lines, t)
			objects = append(objects, t)
		}
	}
	p.subs.Objects = objects
	p.subs.Refresh()
}

// sidecars returns the subtitle files next to the video at path, or in a Subs
// folder there, that share its name, as movie.srt, movie.en.srt or
// Subs/movie.idx do for movie.mkv.
func sidecars(path string) []string {
	dir, name := filepath.Split(path)
	base := strings.TrimSuffix(name, filepath.Ext(name)) + "."
	var subs []string
	for _, dir := range []string{filepath.Clean(dir), filepath.Join(dir, "Subs")} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			switch strings.ToLower(filepath.Ext(e.Name())) {
			case ".srt", ".ass", ".ssa", ".vtt", ".idx", ".sup":
				if !e.IsDir() && strings.HasPrefix(e.Name(), base) {
					subs = append(subs, filepath.Join(dir, e.Name()))
				}
			}
		}
	}
	return subs
}
