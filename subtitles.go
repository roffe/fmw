package fmw

/*
#include <stdlib.h>
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>

static AVSubtitleRect *rect(const AVSubtitle *s, unsigned i) { return s->rects[i]; }
*/
import "C"

import (
	"errors"
	"image"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"
)

// subTrack is a subtitle stream: one of the media's own, or one in a file.
type subTrack struct {
	name   string
	index  C.int             // the stream's in the media, -1 for one in a file
	cc     *C.AVCodecContext // its decoder
	tb     float64           // seconds per timestamp unit of its packets
	offset float64           // stream time where playback time is zero
	bitmap bool              // events are pictures rather than text
	shown  bool              // flagged to show by default, or forced

	// A file is read as playback gets to it, as a film's worth of pictures
	// takes hundreds of megabytes: its stream in fc, and how far it is read,
	// in playback time.
	fc     *C.AVFormatContext
	pkt    *C.AVPacket
	stream C.int
	read   float64

	events []*subEvent     // guarded by media.subMu
	seen   map[subKey]bool // text events already had, as a seek reads them again
}

type subKey struct {
	ms   int64
	text string
}

// subEvent is what a subtitle track shows from start to end: text, or a
// picture at x, y on a canvas of cw by ch, the video's frame where those are 0.
type subEvent struct {
	start, end   float64
	text         string
	img          *image.NRGBA
	x, y, cw, ch int
}

// openSubtitles opens a decoder for each subtitle stream of the media.
func (m *media) openSubtitles() {
	for i, st := range unsafe.Slice(m.fc.streams, m.fc.nb_streams) {
		par := st.codecpar
		if par.codec_type != C.AVMEDIA_TYPE_SUBTITLE {
			continue
		}
		t := newSubTrack(st, par, C.int(i))
		if t == nil {
			continue
		}
		t.offset = m.start
		t.shown = st.disposition&(C.AV_DISPOSITION_DEFAULT|C.AV_DISPOSITION_FORCED) != 0
		t.name = streamName(st, len(m.subs)+1)
		m.subs = append(m.subs, t)
	}
	m.subActive = -1
	for i, t := range m.subs {
		if t.shown {
			m.subActive = i
			break
		}
	}
}

func newSubTrack(st *C.AVStream, par *C.AVCodecParameters, index C.int) *subTrack {
	codec := C.avcodec_find_decoder(par.codec_id)
	if codec == nil {
		return nil
	}
	t := &subTrack{index: index, cc: C.avcodec_alloc_context3(codec), tb: float64(st.time_base.num) / float64(st.time_base.den), seen: map[subKey]bool{}}
	if d := C.avcodec_descriptor_get(par.codec_id); d != nil {
		t.bitmap = d.props&C.AV_CODEC_PROP_BITMAP_SUB != 0
	}
	if C.avcodec_parameters_to_context(t.cc, par) < 0 {
		C.avcodec_free_context(&t.cc)
		return nil
	}
	t.cc.pkt_timebase = st.time_base
	if C.avcodec_open2(t.cc, codec, nil) < 0 {
		C.avcodec_free_context(&t.cc)
		return nil
	}
	return t
}

// streamName names a stream for the menu by its language and title.
func streamName(st *C.AVStream, n int) string {
	var parts []string
	for _, key := range []string{"language", "title"} {
		ckey := C.CString(key)
		if e := C.av_dict_get(st.metadata, ckey, nil, 0); e != nil {
			parts = append(parts, C.GoString(e.value))
		}
		C.free(unsafe.Pointer(ckey))
	}
	if len(parts) == 0 {
		return "Track " + strconv.Itoa(n)
	}
	return strings.Join(parts, " - ")
}

// subtitlePacket decodes p when it belongs to the subtitle track showing, and
// reports whether it was a subtitle packet at all. Called with mu held.
func (m *media) subtitlePacket(p *C.AVPacket) bool {
	m.subMu.Lock()
	var t *subTrack
	found := false
	for i, s := range m.subs {
		if s.index == p.stream_index {
			found = true
			if i == m.subActive {
				t = s
			}
			break
		}
	}
	m.subMu.Unlock()
	if t != nil {
		m.decodeSubtitle(t, p)
	}
	return found
}

// decodeSubtitle adds the event in p to t.
func (m *media) decodeSubtitle(t *subTrack, p *C.AVPacket) {
	if e := t.decode(p); e != nil {
		m.subMu.Lock()
		t.add(e)
		m.subMu.Unlock()
	}
}

// decode returns the event in p, which has neither text nor a picture when it
// only ends the last one, or nil.
func (t *subTrack) decode(p *C.AVPacket) *subEvent {
	var sub C.AVSubtitle
	var got C.int
	if C.avcodec_decode_subtitle2(t.cc, &sub, &got, p) < 0 || got == 0 {
		return nil
	}
	defer C.avsubtitle_free(&sub)

	at := float64(p.pts) * t.tb
	if sub.pts != noPTS {
		at = float64(sub.pts) / C.AV_TIME_BASE
	}
	at -= t.offset
	e := &subEvent{start: at + float64(sub.start_display_time)/1000, end: math.Inf(1)}
	if sub.end_display_time != 0 && sub.end_display_time != math.MaxUint32 {
		e.end = at + float64(sub.end_display_time)/1000
	}
	var lines []string
	for i := range C.uint(sub.num_rects) {
		r := C.rect(&sub, C.uint(i))
		switch r._type {
		case C.SUBTITLE_ASS:
			lines = append(lines, assText(C.GoString(r.ass)))
		case C.SUBTITLE_TEXT:
			lines = append(lines, C.GoString(r.text))
		case C.SUBTITLE_BITMAP:
			if e.img == nil { // ponytail: the first picture of an event, as nearly all have one
				e.img, e.x, e.y = bitmap(r), int(r.x), int(r.y)
				e.cw, e.ch = int(t.cc.width), int(t.cc.height)
			}
		}
	}
	e.text = strings.TrimSpace(strings.Join(lines, "\n"))
	return e
}

// add puts e among t's events. Called with subMu held.
func (t *subTrack) add(e *subEvent) {
	if t.bitmap {
		// a picture shows until the next event, which an empty one only ends,
		// and the old ones are dropped as they would only take memory
		kept := t.events[:0]
		for _, old := range t.events {
			if old.end > e.start {
				old.end = min(old.end, e.start)
			}
			if old.end > e.start-60 {
				kept = append(kept, old)
			}
		}
		t.events = kept
		if e.img == nil {
			return
		}
	} else {
		key := subKey{int64(e.start * 1000), e.text}
		if e.text == "" || t.seen[key] {
			return
		}
		t.seen[key] = true
	}
	t.events = append(t.events, e)
}

// addSubtitles adds a track for each subtitle stream of the file at path, as
// VobSub has one a language, and returns the index of the first. A VobSub
// .sub opens through the .idx next to it.
func (m *media) addSubtitles(path string) (int, error) {
	if idx := strings.TrimSuffix(path, filepath.Ext(path)) + ".idx"; strings.EqualFold(filepath.Ext(path), ".sub") {
		if _, err := os.Stat(idx); err == nil {
			path = idx
		}
	}
	fc, err := openFile(path, true)
	if err != nil {
		return 0, err
	}
	defer C.avformat_close_input(&fc)
	var tracks []*subTrack
	streams := unsafe.Slice(fc.streams, fc.nb_streams)
	for i, st := range streams {
		if st.codecpar.codec_type != C.AVMEDIA_TYPE_SUBTITLE {
			continue
		}
		t := newSubTrack(st, st.codecpar, -1)
		if t == nil {
			continue
		}
		if t.fc, err = openFile(path, false); err != nil {
			C.avcodec_free_context(&t.cc)
			continue
		}
		for j, other := range unsafe.Slice(t.fc.streams, t.fc.nb_streams) {
			if j != i {
				other.discard = C.AVDISCARD_ALL
			}
		}
		t.stream, t.pkt, t.read = C.int(i), C.av_packet_alloc(), math.Inf(-1)
		t.name = filepath.Base(path)
		tracks = append(tracks, t)
	}
	if len(tracks) == 0 {
		return 0, errors.New("fmw: no subtitles to decode in " + path)
	}
	for n, t := range tracks {
		if len(tracks) > 1 {
			t.name += " - " + streamName(streams[t.stream], n+1)
		}
	}

	m.subMu.Lock()
	defer m.subMu.Unlock()
	m.subs = append(m.subs, tracks...)
	return len(m.subs) - len(tracks), nil
}

// openFile opens path in libavformat, probing what its streams hold if asked.
func openFile(path string, probe bool) (*C.AVFormatContext, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var fc *C.AVFormatContext
	if ret := C.avformat_open_input(&fc, cpath, nil, nil); ret < 0 {
		return nil, averror(ret)
	}
	if probe {
		if ret := C.avformat_find_stream_info(fc, nil); ret < 0 {
			C.avformat_close_input(&fc)
			return nil, averror(ret)
		}
	}
	return fc, nil
}

// feed decodes t's file up to a second past playback time at, finding its
// place first after a seek or a while of showing another track. Called with
// subMu held.
func (t *subTrack) feed(at float64) {
	if t.fc == nil {
		return
	}
	if t.read < at-10 {
		// from a while before, as PGS spreads an event over packets, which a
		// seek can land in the middle of
		ts := C.int64_t((at - 10) * C.AV_TIME_BASE)
		if C.avformat_seek_file(t.fc, -1, math.MinInt64, ts, ts, 0) < 0 {
			C.av_seek_frame(t.fc, -1, 0, C.AVSEEK_FLAG_BYTE)
		}
		C.avcodec_flush_buffers(t.cc)
		if t.bitmap {
			t.events = nil
		}
		t.read = at
	}
	for t.read < at+1 {
		if C.av_read_frame(t.fc, t.pkt) < 0 {
			t.read = math.Inf(1)
			return
		}
		if t.pkt.stream_index == t.stream {
			if t.pkt.pts != noPTS {
				t.read = max(t.read, float64(t.pkt.pts)*t.tb-t.offset)
			}
			if e := t.decode(t.pkt); e != nil {
				t.add(e)
			}
		}
		C.av_packet_unref(t.pkt)
	}
}

// subtitles returns the names of the subtitle tracks and which one shows, -1 for none.
func (m *media) subtitles() ([]string, int) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	names := make([]string, len(m.subs))
	for i, t := range m.subs {
		names[i] = t.name
	}
	return names, m.subActive
}

// showSubtitles makes track i the one showing, -1 for none.
func (m *media) showSubtitles(i int) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	if i >= -1 && i < len(m.subs) {
		m.subActive = i
	}
}

// subtitlesAt returns the events of the track showing at playback time at.
func (m *media) subtitlesAt(at float64) []*subEvent {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	if m.subActive < 0 {
		return nil
	}
	t := m.subs[m.subActive]
	t.feed(at)
	var on []*subEvent
	for _, e := range t.events {
		if e.start <= at && at < e.end {
			on = append(on, e)
		}
	}
	return on
}

// seekSubtitles forgets what tracks decoded that a seek makes stale: the
// decoders' state, and pictures, which are read again. Files find their
// place again as they are fed. Called with mu held.
func (m *media) seekSubtitles() {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	for _, t := range m.subs {
		if t.fc != nil {
			t.read = math.Inf(-1)
		} else {
			C.avcodec_flush_buffers(t.cc)
			if t.bitmap {
				t.events = nil
			}
		}
	}
}

func (m *media) closeSubtitles() {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	for _, t := range m.subs {
		C.avcodec_free_context(&t.cc)
		C.av_packet_free(&t.pkt)
		C.avformat_close_input(&t.fc)
	}
}

// assText is the text of a decoded subtitle in ASS's Dialogue form, without
// its fields or override tags.
//
// ponytail: plain text only, so ASS styles, positions and effects are lost;
// libass renders them all if they matter.
func assText(s string) string {
	for range 8 { // ReadOrder, Layer, Style, Name, MarginL, MarginR, MarginV, Effect
		if i := strings.IndexByte(s, ','); i >= 0 {
			s = s[i+1:]
		}
	}
	var b strings.Builder
	for len(s) > 0 {
		switch {
		case s[0] == '{':
			if end := strings.IndexByte(s, '}'); end >= 0 {
				s = s[end+1:]
				continue
			}
		case strings.HasPrefix(s, `\N`), strings.HasPrefix(s, `\n`):
			b.WriteByte('\n')
			s = s[2:]
			continue
		case strings.HasPrefix(s, `\h`):
			b.WriteByte(' ')
			s = s[2:]
			continue
		}
		b.WriteByte(s[0])
		s = s[1:]
	}
	return b.String()
}

// bitmap turns a subtitle picture, palette indexes, into an image.
func bitmap(r *C.AVSubtitleRect) *image.NRGBA {
	w, h, stride := int(r.w), int(r.h), int(r.linesize[0])
	if w <= 0 || h <= 0 || r.data[0] == nil || r.data[1] == nil {
		return nil
	}
	idx := unsafe.Slice((*byte)(r.data[0]), stride*h)
	pal := unsafe.Slice((*uint32)(unsafe.Pointer(r.data[1])), 256) // ARGB, native endian
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := pal[idx[y*stride+x]]
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = byte(c>>16), byte(c>>8), byte(c), byte(c>>24)
		}
	}
	return img
}
