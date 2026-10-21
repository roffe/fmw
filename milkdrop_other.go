//go:build !((linux || windows) && projectm)

package fmw

import (
	"errors"
	"image"

	"fyne.io/fyne/v2/canvas"
)

// projectM is false in builds without the projectm tag, which show no
// visualizations.
const projectM = false

type milkdrop struct{ w, h int }

func newMilkdrop(int, int) (*milkdrop, error)                  { return nil, errors.New("fmw: built without projectm") }
func (*milkdrop) shared() bool                                 { return false }
func (*milkdrop) resize(int, int) error                        { return nil }
func (*milkdrop) configure(float64, bool, string) error        { return nil }
func (*milkdrop) draw([]float32, *image.RGBA, image.Rectangle) {}
func (*milkdrop) drawTo([]float32, *canvas.VideoFrame)         {}
func (*milkdrop) next()                                        {}
