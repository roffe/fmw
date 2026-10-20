package fmw

import (
	"encoding/binary"
	"image"
	"math"
	"runtime"
	"testing"
)

func TestVisualizerSound(t *testing.T) {
	v := &visualizer{ring: make([]byte, ringSize)}
	floats := func(from, n int) []byte {
		b := make([]byte, n*4)
		for i := range n {
			binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(float32(from+i)))
		}
		return b
	}
	const total = ringSize/4 + 3000 // wraps the ring
	for at := 0; at < total; at += 256 {
		v.write(int64(at)*4, floats(at, 256))
	}
	pcm, fed := v.sound(nil, 0, total*4)
	if len(pcm) != feedMax/4 || fed != total*4 || pcm[0] != total-feedMax/4 || pcm[len(pcm)-1] != total-1 {
		t.Fatalf("behind: got %d floats %v..%v to %d", len(pcm), pcm[0], pcm[len(pcm)-1], fed)
	}

	if pcm, fed = v.sound(pcm, fed, -500); len(pcm) != 0 || fed != 0 {
		t.Fatalf("right after a seek: got %d floats to %d", len(pcm), fed)
	}
	v.write(0, floats(0, 64))
	if pcm, fed = v.sound(pcm, fed, 100); len(pcm) != 24 || fed != 96 || pcm[0] != 0 || pcm[23] != 23 {
		t.Fatalf("after a seek: got %d floats to %d", len(pcm), fed)
	}
}

func TestMilkdropDraws(t *testing.T) {
	if !projectM {
		t.Skip("built without projectm")
	}
	runtime.LockOSThread()
	m, err := newMilkdrop(320, 180)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 320, 180))
	pcm := make([]float32, 2*800)
	for f := range 120 {
		for i := range pcm {
			pcm[i] = float32(math.Sin(float64(f*800+i/2) * 2 * math.Pi * 110 / sampleRate))
		}
		m.draw(pcm, img)
	}
	lit := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i+3] != 255 {
			t.Fatalf("pixel %d has alpha %d", i/4, img.Pix[i+3])
		}
		if img.Pix[i]|img.Pix[i+1]|img.Pix[i+2] != 0 {
			lit++
		}
	}
	if lit < len(img.Pix)/4/10 {
		t.Fatalf("only %d of %d pixels drawn", lit, len(img.Pix)/4)
	}
}
