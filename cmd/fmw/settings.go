package main

import (
	"errors"
	"fmt"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/roffe/browse"
	"github.com/roffe/fmw"
)

var resolutions = [][2]int{{640, 360}, {854, 480}, {960, 540}, {1280, 720}, {1600, 900}, {1920, 1080}, {2560, 1440}, {3840, 2160}}

// settings keeps the player's settings in the app's preferences, where the
// settings window changes them.
type settings struct {
	prefs fyne.Preferences
	p     *fmw.Player
	main  fyne.Window // for dialogs while the settings window is closed
	win   fyne.Window
}

// parent is the window for the settings' dialogs.
func (s *settings) parent() fyne.Window {
	if s.win != nil {
		return s.win
	}
	return s.main
}

func (s *settings) visualization() fmw.Visualization {
	return fmw.Visualization{
		Off:      s.prefs.Bool("visualization.off"),
		Width:    s.prefs.IntWithFallback("visualization.width", 1920),
		Height:   s.prefs.IntWithFallback("visualization.height", 1080),
		Seconds:  s.prefs.FloatWithFallback("visualization.seconds", 30),
		HardCuts: s.prefs.Bool("visualization.hardcuts"),
		Presets:  s.prefs.String("visualization.presets"),
	}
}

// apply hands the player the settings.
func (s *settings) apply() {
	s.p.SetHardwareDecoding(s.prefs.String("hwdec"))
	s.p.SetVisualization(s.visualization())
}

// show shows the settings window, which changes the settings as they are
// picked.
func (s *settings) show(a fyne.App) {
	if s.win == nil {
		s.win = a.NewWindow("Settings")
		s.win.SetContent(s.content())
		s.win.SetOnClosed(func() { s.win = nil })
		s.win.Resize(fyne.NewSize(520, 0))
	}
	s.win.Show()
	s.win.RequestFocus()
}

func (s *settings) content() fyne.CanvasObject {
	kinds := append([]string{"Auto", "Off"}, fmw.HardwareDecoders()...)
	hw := widget.NewSelect(kinds, func(kind string) {
		switch kind {
		case "Auto":
			kind = ""
		case "Off":
			kind = "none"
		}
		s.prefs.SetString("hwdec", kind)
		s.apply()
	})
	switch kind := s.prefs.String("hwdec"); kind {
	case "":
		hw.Selected = "Auto"
	case "none":
		hw.Selected = "Off"
	default:
		if !slices.Contains(kinds, kind) {
			hw.Options = append(hw.Options, kind)
		}
		hw.Selected = kind
	}
	hwItem := widget.NewFormItem("Hardware decoding", hw)
	hwItem.HintText = "For the next video loaded. Auto picks the best there is."
	video := widget.NewCard("Video", "", widget.NewForm(hwItem))
	if !fmw.HasVisualizations() {
		return container.NewVBox(video)
	}
	return container.NewVBox(video, s.visualizationCard())
}

func (s *settings) visualizationCard() fyne.CanvasObject {
	v := s.visualization()
	update := func(change func()) {
		change()
		s.apply()
	}

	show := widget.NewCheck("For music without a picture", func(on bool) {
		update(func() { s.prefs.SetBool("visualization.off", !on) })
	})
	show.Checked = !v.Off

	names := make([]string, len(resolutions))
	for i, r := range resolutions {
		names[i] = fmt.Sprintf("%d × %d", r[0], r[1])
	}
	current := fmt.Sprintf("%d × %d", v.Width, v.Height)
	if !slices.Contains(names, current) {
		names = append(names, current)
	}
	resolution := widget.NewSelect(names, func(name string) {
		var w, h int
		if _, err := fmt.Sscanf(name, "%d × %d", &w, &h); err == nil {
			update(func() {
				s.prefs.SetInt("visualization.width", w)
				s.prefs.SetInt("visualization.height", h)
			})
		}
	})
	resolution.Selected = current

	secondsLabel := widget.NewLabel("")
	seconds := widget.NewSlider(5, 120)
	seconds.Step = 5
	seconds.Value = v.Seconds
	secondsLabel.SetText(fmt.Sprintf("%.0f s", v.Seconds))
	seconds.OnChanged = func(f float64) { secondsLabel.SetText(fmt.Sprintf("%.0f s", f)) }
	seconds.OnChangeEnded = func(f float64) {
		update(func() { s.prefs.SetFloat("visualization.seconds", f) })
	}

	cuts := widget.NewCheck("Cut to the next preset on a loud beat", func(on bool) {
		update(func() { s.prefs.SetBool("visualization.hardcuts", on) })
	})
	cuts.Checked = v.HardCuts

	folder := widget.NewLabel("")
	folder.Truncation = fyne.TextTruncateEllipsis
	showFolder := func() {
		if dir := s.prefs.String("visualization.presets"); dir != "" {
			folder.SetText(dir)
		} else {
			folder.SetText("Those that came with fmw, or the system's")
		}
	}
	showFolder()
	choose := widget.NewButton("Choose…", func() {
		o := browse.Options{Title: "Folder of MilkDrop presets", Parent: nativeHandle(s.win)}
		go func() { // the native dialog blocks until it closes
			dir, err := browse.OpenFolder(o)
			fyne.Do(func() {
				switch {
				case err == nil:
					update(func() { s.prefs.SetString("visualization.presets", dir) })
					showFolder()
				case errors.Is(err, browse.ErrUnavailable):
					dialog.ShowFolderOpen(func(u fyne.ListableURI, err error) {
						if err == nil && u != nil {
							update(func() { s.prefs.SetString("visualization.presets", u.Path()) })
							showFolder()
						}
					}, s.parent())
				case !errors.Is(err, browse.ErrCancelled):
					dialog.ShowError(err, s.parent())
				}
			})
		}()
	})
	reset := widget.NewButton("Default", func() {
		update(func() { s.prefs.SetString("visualization.presets", "") })
		showFolder()
	})

	form := widget.NewForm(
		widget.NewFormItem("Show", show),
		widget.NewFormItem("Resolution", resolution),
		widget.NewFormItem("Each preset", container.NewBorder(nil, nil, nil, secondsLabel, seconds)),
		widget.NewFormItem("Hard cuts", cuts),
		widget.NewFormItem("Presets", container.NewBorder(nil, nil, nil, container.NewHBox(choose, reset), folder)),
	)
	form.Items[1].HintText = "Drawn at this size, then scaled to cover the player."
	return widget.NewCard("Visualizations", "MilkDrop presets by projectM", form)
}
