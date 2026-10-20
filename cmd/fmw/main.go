// Command fmw plays a video: fmw [file or URL]
package main

import (
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"
	"github.com/roffe/fmw"
)

func main() {
	a := app.NewWithID("nu.roffe.fmw")
	w := a.NewWindow("fmw")
	p := fmw.NewPlayer()

	play := func(path string) {
		if err := p.Load(path); err != nil {
			dialog.ShowError(err, w)
			return
		}
		p.Play()
	}

	open := fyne.NewMenuItem("Open…", func() {
		dialog.ShowFileOpen(func(rc fyne.URIReadCloser, err error) {
			if err != nil || rc == nil {
				return
			}
			rc.Close()
			play(rc.URI().Path())
		}, w)
	})
	w.SetMainMenu(fyne.NewMainMenu(fyne.NewMenu("File", open)))

	if len(os.Args) > 1 {
		a.Lifecycle().SetOnStarted(func() { play(os.Args[1]) })
	}
	w.SetContent(p)
	w.Resize(fyne.NewSize(960, 600))
	w.ShowAndRun()
}
