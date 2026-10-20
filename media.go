package fmw

/*
#cgo pkg-config: libavformat libavcodec libavutil libswscale libswresample
#include <errno.h>
#include <stdlib.h>
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/channel_layout.h>
#include <libavutil/display.h>
#include <libavutil/hwcontext.h>
#include <libavutil/pixdesc.h>
#include <libswresample/swresample.h>
#include <libswscale/swscale.h>

enum { eagain = AVERROR(EAGAIN) };

static AVStream *stream(AVFormatContext *f, int i) { return f->streams[i]; }

// rotation is how far clockwise, in quarter turns of 90, st has to be turned
// to show the right way up, as phones record it.
static int rotation(const AVStream *st) {
	const AVPacketSideData *sd = av_packet_side_data_get(st->codecpar->coded_side_data,
		st->codecpar->nb_coded_side_data, AV_PKT_DATA_DISPLAYMATRIX);
	if (!sd)
		return 0;
	double d = av_display_rotation_get((const int32_t *)sd->data);
	int r = -(int)(d < 0 ? d - 0.5 : d + 0.5);
	return (r % 360 + 360) % 360 / 90 * 90;
}

// rotate turns the w by h pixels at src clockwise by rot degrees into dst.
static void rotate(const uint32_t *src, int w, int h, uint32_t *dst, int rot) {
	for (int y = 0; y < h; y++)
		for (int x = 0; x < w; x++) {
			uint32_t p = src[y * w + x];
			if (rot == 90)
				dst[x * h + h - 1 - y] = p;
			else if (rot == 180)
				dst[(h - 1 - y) * w + w - 1 - x] = p;
			else
				dst[(w - 1 - x) * h + y] = p;
		}
}

// planar returns f as planar 8-bit 4:2:0, converted into mid if it is any other
// 4:2:0 layout. swscale only goes to RGBA fast from that, and is ten times slower
// from the NV12 and P010 that hardware decoders hand over.
static const AVFrame *planar(SwsContext **pre, AVFrame *mid, const AVFrame *f) {
	const AVPixFmtDescriptor *d = av_pix_fmt_desc_get(f->format);
	if (f->format == AV_PIX_FMT_YUV420P || f->format == AV_PIX_FMT_YUVJ420P || d->nb_components != 3 ||
		d->log2_chroma_w != 1 || d->log2_chroma_h != 1 || d->flags & AV_PIX_FMT_FLAG_RGB)
		return f;
	*pre = sws_getCachedContext(*pre, f->width, f->height, f->format, f->width, f->height, AV_PIX_FMT_YUV420P, SWS_BILINEAR, NULL, NULL, NULL);
	if (!*pre)
		return f;
	if (mid->width != f->width || mid->height != f->height) {
		av_frame_unref(mid);
		mid->format = AV_PIX_FMT_YUV420P;
		mid->width = f->width;
		mid->height = f->height;
		if (av_frame_get_buffer(mid, 0) < 0) {
			av_frame_unref(mid);
			return f;
		}
	}
	sws_scale(*pre, (const uint8_t *const *)f->data, f->linesize, 0, f->height, mid->data, mid->linesize);
	mid->colorspace = f->colorspace;
	mid->color_range = f->color_range;
	return mid;
}

// to_rgba scales f into the w by h RGBA pixels at dst, with the colour matrix f
// is tagged with, or BT.709 for untagged HD and BT.601 below that.
static void to_rgba(SwsContext **s, SwsContext **pre, AVFrame *mid, const AVFrame *f, uint8_t *dst, int w, int h) {
	f = planar(pre, mid, f);
	SwsContext *n = sws_getCachedContext(*s, f->width, f->height, f->format, w, h, AV_PIX_FMT_RGBA, SWS_BILINEAR, NULL, NULL, NULL);
	if (n && n != *s) {
		int cs = f->colorspace;
		if (cs == AVCOL_SPC_UNSPECIFIED)
			cs = f->height >= 720 ? SWS_CS_ITU709 : SWS_CS_DEFAULT;
		sws_setColorspaceDetails(n, sws_getCoefficients(cs), f->color_range == AVCOL_RANGE_JPEG,
			sws_getCoefficients(SWS_CS_DEFAULT), 1, 0, 1 << 16, 1 << 16);
	}
	*s = n;
	if (!n)
		return;
	uint8_t *d[4] = {dst};
	int ls[4] = {w * 4};
	sws_scale(n, (const uint8_t *const *)f->data, f->linesize, 0, f->height, d, ls);
}

// pcm_converter makes a converter from what c decodes to interleaved stereo float at rate.
static SwrContext *pcm_converter(const AVCodecContext *c, int rate) {
	AVChannelLayout in = c->ch_layout, out = AV_CHANNEL_LAYOUT_STEREO;
	if (in.order == AV_CHANNEL_ORDER_UNSPEC)
		av_channel_layout_default(&in, in.nb_channels);
	SwrContext *s = NULL;
	if (swr_alloc_set_opts2(&s, &out, AV_SAMPLE_FMT_FLT, rate, &in, c->sample_fmt, c->sample_rate, 0, NULL) < 0 || swr_init(s) < 0)
		swr_free(&s);
	return s;
}

static int to_pcm(SwrContext *s, uint8_t *out, int n, const AVFrame *f) {
	return swr_convert(s, &out, n, (const uint8_t *const *)f->extended_data, f->nb_samples);
}

// hw_format picks the hardware pixel format use_hw asked for, or lets the
// decoder fall back to the CPU for a stream the device cannot do.
static enum AVPixelFormat hw_format(AVCodecContext *c, const enum AVPixelFormat *fmts) {
	for (const enum AVPixelFormat *p = fmts; *p != AV_PIX_FMT_NONE; p++)
		if (*p == (enum AVPixelFormat)(intptr_t)c->opaque)
			return *p;
	return avcodec_default_get_format(c, fmts);
}

static void use_hw(AVCodecContext *c, AVBufferRef *dev, enum AVPixelFormat fmt) {
	c->hw_device_ctx = av_buffer_ref(dev);
	c->opaque = (void *)(intptr_t)fmt;
	c->get_format = hw_format;
}

// download replaces f, a frame in video memory, by a copy in system memory, using tmp.
static int download(AVFrame *f, AVFrame *tmp) {
	int ret = av_hwframe_transfer_data(tmp, f, 0);
	if (ret >= 0)
		ret = av_frame_copy_props(tmp, f);
	av_frame_unref(f);
	av_frame_move_ref(f, tmp);
	return ret;
}
*/
import "C"

import (
	"errors"
	"image"
	"math"
	"os"
	"sync"
	"unsafe"
)

const noPTS = math.MinInt64 // AV_NOPTS_VALUE

func init() {
	C.av_log_set_level(C.AV_LOG_FATAL) // decoders complain on stderr about every seek
}

// media is a file or stream open in libav. Its methods lock it, as the
// playback loop takes video from it while oto's reads take audio.
type media struct {
	mu    sync.Mutex
	fc    *C.AVFormatContext // nil once closed
	pkt   *C.AVPacket
	video *track
	audio *track
	sws   *C.SwsContext
	pre   *C.SwsContext // and its way to planar 4:2:0
	mid   *C.AVFrame
	swr   *C.SwrContext
	pcm   []byte
	start float64 // stream time where playback time is zero
	dur   float64
	w, h  int    // the video's display size
	sw    int    // and the size it is scaled to before turning,
	sh    int    // which is the same unless it is on its side
	rot   C.int  // clockwise degrees to turn it
	turn  []byte // the scaled frame, when it has to be turned
}

// track is one stream of a media and its decoder.
type track struct {
	index C.int
	cc    *C.AVCodecContext
	frame *C.AVFrame
	last  *C.AVFrame    // the frame take moved past last
	hw    *C.AVFrame    // for downloading frames when decoding on the GPU
	tb    float64       // seconds per timestamp unit
	queue []*C.AVPacket // read while looking for the other track's
	ready bool          // frame holds a frame not taken yet
	t     float64       // and its time
}

// openMedia opens path, which can be anything libavformat reads, with its
// best video track and, if withAudio, the audio track that goes with it.
func openMedia(path string, withAudio bool) (*media, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	m := &media{}
	if ret := C.avformat_open_input(&m.fc, cpath, nil, nil); ret < 0 {
		return nil, averror(ret)
	}
	if ret := C.avformat_find_stream_info(m.fc, nil); ret < 0 {
		m.close()
		return nil, averror(ret)
	}
	m.pkt, m.mid = C.av_packet_alloc(), C.av_frame_alloc()
	m.video = m.open(C.AVMEDIA_TYPE_VIDEO, -1)
	if withAudio {
		related := C.int(-1)
		if m.video != nil {
			related = m.video.index
		}
		if m.audio = m.open(C.AVMEDIA_TYPE_AUDIO, related); m.audio != nil {
			if m.swr = C.pcm_converter(m.audio.cc, sampleRate); m.swr == nil {
				m.audio.free()
				m.audio = nil
			}
		}
	}
	if m.video == nil && m.audio == nil {
		m.close()
		return nil, errors.New("fmw: nothing to play in " + path)
	}

	if m.fc.start_time != noPTS {
		m.start = float64(m.fc.start_time) / C.AV_TIME_BASE
	}
	if m.fc.duration != noPTS {
		m.dur = float64(m.fc.duration) / C.AV_TIME_BASE
	}
	if v := m.video; v != nil {
		st := C.stream(m.fc, v.index)
		m.sw, m.sh = int(v.cc.width), int(v.cc.height)
		if sar := C.av_guess_sample_aspect_ratio(m.fc, st, nil); sar.num > 0 && sar.den > 0 {
			m.sw = m.sw * int(sar.num) / int(sar.den)
		}
		m.w, m.h, m.rot = m.sw, m.sh, C.rotation(st)
		if m.rot == 90 || m.rot == 270 {
			m.w, m.h = m.sh, m.sw
		}
	}
	return m, nil
}

func (m *media) open(kind C.enum_AVMediaType, related C.int) *track {
	var codec *C.AVCodec
	i := C.av_find_best_stream(m.fc, kind, -1, related, &codec, 0)
	if i < 0 {
		return nil
	}
	st := C.stream(m.fc, i)
	t := &track{index: i, cc: C.avcodec_alloc_context3(codec), frame: C.av_frame_alloc(), last: C.av_frame_alloc(), tb: float64(st.time_base.num) / float64(st.time_base.den)}
	if C.avcodec_parameters_to_context(t.cc, st.codecpar) < 0 {
		t.free()
		return nil
	}
	t.cc.pkt_timebase = st.time_base
	t.cc.thread_count = 0 // one per core
	if kind == C.AVMEDIA_TYPE_VIDEO {
		t.useHardware(codec)
	}
	if C.avcodec_open2(t.cc, codec, nil) < 0 {
		t.free()
		return nil
	}
	return t
}

// useHardware has t decode on the first kind of device its decoder knows that
// this machine has, or on the kind FMW_HWDEC names. AV1 stays on the CPU: its
// default decoder, dav1d, has no hardware support, and FFmpeg's own one has
// no CPU fallback.
func (t *track) useHardware(codec *C.AVCodec) {
	want := os.Getenv("FMW_HWDEC")
	for i := C.int(0); want != "none"; i++ {
		cfg := C.avcodec_get_hw_config(codec, i)
		if cfg == nil {
			return
		}
		name := C.GoString(C.av_hwdevice_get_type_name(cfg.device_type))
		if cfg.methods&C.AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX == 0 || want != "" && want != name {
			continue
		}
		if dev := hwDevice(cfg.device_type); dev != nil {
			C.use_hw(t.cc, dev, cfg.pix_fmt)
			t.hw = C.av_frame_alloc()
			return
		}
	}
}

var (
	hwMu      sync.Mutex
	hwDevices = map[C.enum_AVHWDeviceType]*C.AVBufferRef{} // nil where there is no such device
)

// hwDevice returns the process's device of type typ, made the first time as
// that takes up to a quarter of a second.
func hwDevice(typ C.enum_AVHWDeviceType) *C.AVBufferRef {
	hwMu.Lock()
	defer hwMu.Unlock()
	dev, ok := hwDevices[typ]
	if !ok {
		if C.av_hwdevice_ctx_create(&dev, typ, nil, nil, 0) < 0 {
			dev = nil
		}
		hwDevices[typ] = dev
	}
	return dev
}

// next returns the time of the next video frame, decoding it unless it waits
// already, or false at the end.
func (m *media) next() (float64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.video
	if m.fc == nil || v == nil {
		return 0, false
	}
	if !v.ready {
		if !m.decode(v) {
			return 0, false
		}
		v.ready, v.t = true, m.time(v)
	}
	return v.t, true
}

// take moves past the waiting video frame, converting it into dst, which is w
// by h, unless dst is nil.
func (m *media) take(dst *image.RGBA) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fc == nil || m.video == nil || !m.video.ready {
		return
	}
	v := m.video
	v.ready = false
	C.av_frame_unref(v.last)
	C.av_frame_move_ref(v.last, v.frame)
	if dst != nil {
		m.convert(dst)
	}
}

// show converts the frame take moved past last into dst, false if there is
// none since the last seek.
func (m *media) show(dst *image.RGBA) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fc == nil || m.video == nil || m.video.last.data[0] == nil {
		return false
	}
	m.convert(dst)
	return true
}

func (m *media) convert(dst *image.RGBA) {
	if m.rot == 0 {
		C.to_rgba(&m.sws, &m.pre, m.mid, m.video.last, (*C.uint8_t)(&dst.Pix[0]), C.int(m.w), C.int(m.h))
		return
	}
	if len(m.turn) < m.sw*m.sh*4 {
		m.turn = make([]byte, m.sw*m.sh*4)
	}
	if C.to_rgba(&m.sws, &m.pre, m.mid, m.video.last, (*C.uint8_t)(&m.turn[0]), C.int(m.sw), C.int(m.sh)); m.sws != nil {
		C.rotate((*C.uint32_t)(unsafe.Pointer(&m.turn[0])), C.int(m.sw), C.int(m.sh), (*C.uint32_t)(unsafe.Pointer(&dst.Pix[0])), m.rot)
	}
}

// samples returns the next stretch of audio as interleaved stereo float32 at
// sampleRate and its time, or false at the end. The bytes are good until the
// next call.
func (m *media) samples() ([]byte, float64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.audio
	if m.fc == nil || a == nil || !m.decode(a) {
		return nil, 0, false
	}
	n := C.swr_get_out_samples(m.swr, a.frame.nb_samples)
	if n <= 0 {
		return nil, m.time(a), true
	}
	if len(m.pcm) < int(n)*8 {
		m.pcm = make([]byte, n*8)
	}
	got := C.to_pcm(m.swr, (*C.uint8_t)(&m.pcm[0]), n, a.frame)
	return m.pcm[:max(got, 0)*8], m.time(a), true
}

// seek moves to the key frame at or before t and forgets what was decoded.
func (m *media) seek(t float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fc == nil {
		return
	}
	// Without an index (MPEG-TS and PS) a seek lands past the start of the key
	// frame it found, which nothing can back off from at the start of the file.
	if t > 0 || m.fc.iformat.flags&C.AVFMT_TS_DISCONT == 0 || C.av_seek_frame(m.fc, -1, 0, C.AVSEEK_FLAG_BYTE) < 0 {
		ts := C.int64_t((t + m.start) * C.AV_TIME_BASE)
		C.avformat_seek_file(m.fc, -1, math.MinInt64, ts, ts, 0)
	}
	for _, tr := range []*track{m.video, m.audio} {
		if tr != nil {
			C.avcodec_flush_buffers(tr.cc)
			tr.drop()
		}
	}
	if m.swr != nil {
		C.swr_init(m.swr)
	}
}

func (m *media) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fc == nil {
		return
	}
	for _, tr := range []*track{m.video, m.audio} {
		if tr != nil {
			tr.free()
		}
	}
	C.av_packet_free(&m.pkt)
	C.sws_freeContext(m.sws)
	C.sws_freeContext(m.pre)
	C.av_frame_free(&m.mid)
	C.swr_free(&m.swr)
	C.avformat_close_input(&m.fc)
}

// decode puts the next frame of t in t.frame, false at the end.
func (m *media) decode(t *track) bool {
	for {
		ret := C.avcodec_receive_frame(t.cc, t.frame)
		if ret == 0 && t.frame.hw_frames_ctx != nil {
			ret = C.download(t.frame, t.hw)
		}
		if ret != C.eagain {
			return ret >= 0
		}
		p := m.packet(t)
		C.avcodec_send_packet(t.cc, p) // nil drains the decoder
		C.av_packet_free(&p)
	}
}

// packet returns the next packet of t, keeping those of the other track it
// reads past, or nil at the end.
func (m *media) packet(t *track) *C.AVPacket {
	if len(t.queue) > 0 {
		p := t.queue[0]
		t.queue = t.queue[1:]
		return p
	}
	for C.av_read_frame(m.fc, m.pkt) >= 0 {
		var to *track
		for _, tr := range []*track{m.video, m.audio} {
			if tr != nil && tr.index == m.pkt.stream_index {
				to = tr
			}
		}
		if to == nil {
			C.av_packet_unref(m.pkt)
			continue
		}
		p := C.av_packet_alloc()
		C.av_packet_move_ref(p, m.pkt)
		if to == t {
			return p
		}
		to.queue = append(to.queue, p)
	}
	return nil
}

func (m *media) time(t *track) float64 {
	if ts := t.frame.best_effort_timestamp; ts != noPTS {
		return float64(ts)*t.tb - m.start
	}
	return t.t
}

func (t *track) drop() {
	for _, p := range t.queue {
		C.av_packet_free(&p)
	}
	t.queue, t.ready = nil, false
	C.av_frame_unref(t.last)
}

func (t *track) free() {
	t.drop()
	C.av_frame_free(&t.frame)
	C.av_frame_free(&t.last)
	C.av_frame_free(&t.hw)
	C.avcodec_free_context(&t.cc)
}

func averror(ret C.int) error {
	var buf [C.AV_ERROR_MAX_STRING_SIZE]C.char
	C.av_strerror(ret, &buf[0], C.size_t(len(buf)))
	return errors.New("fmw: " + C.GoString(&buf[0]))
}
