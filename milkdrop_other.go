//go:build !(linux && projectm)

package fmw

import (
	"errors"
	"image"
)

// projectM is false in builds without the projectm tag, which show no
// visualizations.
const projectM = false

type milkdrop struct{ w, h int }

func newMilkdrop(int, int) (*milkdrop, error) { return nil, errors.New("fmw: built without projectm") }
func (*milkdrop) resize(int, int) error       { return nil }
func (*milkdrop) draw([]float32, *image.RGBA) {}
func (*milkdrop) next()                       {}
