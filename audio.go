package fmw

import (
	"io"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"github.com/ebitengine/oto/v3"
)

// sampleRate is what every file's audio is converted to for oto.
const sampleRate = 48000

// deviceBuffer is how much mixed audio the device queues before it is heard.
// Every oto backend is asked for this much so Player.position can take it off.
//
// ponytail: a fixed figure, so sinks that add their own delay (Bluetooth), or
// PipeWire growing the buffer when the process falls behind, put the sound
// late; ask the backend for the sink latency if that matters.
const deviceBuffer = 100 * time.Millisecond

var (
	audioOnce sync.Once
	audioCtx  *oto.Context
)

// audioContext returns the process's one oto context, or nil without an audio device.
func audioContext() *oto.Context {
	audioOnce.Do(func() {
		ctx, ready, err := oto.NewContext(&oto.NewContextOptions{SampleRate: sampleRate, ChannelCount: 2, Format: oto.FormatFloat32LE, BufferSize: deviceBuffer})
		if err == nil {
			<-ready
			err = ctx.Err()
		}
		if err != nil {
			fyne.LogError("fmw: playing without sound", err)
			return
		}
		audioCtx = ctx
	})
	return audioCtx
}

// Read hands oto the stream's audio from where the last seek landed. Bytes
// count as read before oto buffers them, so Player.position runs ahead by
// what a read in flight carries: it hands over a few milliseconds at a time.
func (s *stream) Read(b []byte) (int, error) {
	for len(s.pcm) == 0 && s.silence == 0 {
		chunk, t, ok := s.m.samples()
		if !ok {
			s.audioEOF.Store(true)
			return 0, io.EOF
		}
		if s.skipTo >= 0 {
			if gap := t - s.skipTo; gap > 0 {
				s.silence = int(gap*sampleRate) * 8 // the sound starts after the picture
			} else {
				chunk = chunk[min(len(chunk), int(-gap*sampleRate)*8):]
			}
			if len(chunk) > 0 || s.silence > 0 {
				s.skipTo = -1
			}
		}
		s.pcm = chunk
	}

	var n int
	if s.silence > 0 {
		n = min(len(b), s.silence, 1024)
		clear(b[:n])
		s.silence -= n
	} else {
		n = copy(b[:min(len(b), 1024)], s.pcm)
		s.pcm = s.pcm[n:]
	}
	s.vis.write(s.sent.Load(), b[:n])
	s.sent.Add(int64(n))
	return n, nil
}

// Seek only exists so oto drops what it buffered; Player.seek moves the stream.
func (*stream) Seek(int64, int) (int64, error) {
	return 0, nil
}
