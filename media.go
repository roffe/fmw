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
#include <libavutil/mastering_display_metadata.h>
#include <libavutil/pixdesc.h>
#include <libswresample/swresample.h>
#include <libswscale/swscale.h>

enum { eagain = AVERROR(EAGAIN) };

static AVStream *stream(AVFormatContext *f, int i) { return f->streams[i]; }

// rotation is how many degrees clockwise, 0, 90, 180 or 270, st has to be
// turned to show the right way up, as phones record it.
static int rotation(const AVStream *st) {
	const AVPacketSideData *sd = av_packet_side_data_get(st->codecpar->coded_side_data,
		st->codecpar->nb_coded_side_data, AV_PKT_DATA_DISPLAYMATRIX);
	if (!sd)
		return 0;
	double d = av_display_rotation_get((const int32_t *)sd->data);
	int r = -(int)(d < 0 ? d - 0.5 : d + 0.5);
	return (r % 360 + 360) % 360 / 90 * 90;
}

// to_planes points dst at f's planes when painters draw them as they are, as
// I420, NV12, yuv420p10 and P010, or else at f converted into own: to one of
// those with interleaved chroma if f has it, which swscale converts to fastest,
// and of 10 bits if f has more than 8.
static int to_planes(SwsContext **s, AVFrame *dst, AVFrame *own, const AVFrame *f) {
	av_frame_unref(dst);
	int fmt = f->format;
	int nv = fmt == AV_PIX_FMT_NV12 || fmt == AV_PIX_FMT_P010LE;
	if ((nv || fmt == AV_PIX_FMT_YUV420P || fmt == AV_PIX_FMT_YUVJ420P || fmt == AV_PIX_FMT_YUV420P10LE) &&
		f->linesize[0] > 0 && f->linesize[1] > 0 && (nv || f->linesize[2] == f->linesize[1]))
		return av_frame_ref(dst, f);
	const AVPixFmtDescriptor *d = av_pix_fmt_desc_get(fmt);
	int deep = d->comp[0].depth > 8;
	int to = deep ? AV_PIX_FMT_YUV420P10LE : AV_PIX_FMT_YUV420P;
	if (d->nb_components >= 3 && d->comp[1].plane == d->comp[2].plane && !(d->flags & AV_PIX_FMT_FLAG_RGB))
		to = deep ? AV_PIX_FMT_P010LE : AV_PIX_FMT_NV12;
	if (own->width != f->width || own->height != f->height || own->format != to) {
		av_frame_unref(own);
		own->format = to;
		own->width = f->width;
		own->height = f->height;
		int ret = av_frame_get_buffer(own, 0);
		if (ret < 0) {
			av_frame_unref(own);
			return ret;
		}
	}
	*s = sws_getCachedContext(*s, f->width, f->height, fmt, f->width, f->height, to, SWS_BILINEAR, NULL, NULL, NULL);
	if (!*s)
		return AVERROR(EINVAL);
	sws_scale(*s, (const uint8_t *const *)f->data, f->linesize, 0, f->height, own->data, own->linesize);
	own->colorspace = f->colorspace;
	own->color_range = f->color_range;
	own->color_trc = f->color_trc;
	if (d->flags & AV_PIX_FMT_FLAG_RGB) {
		own->colorspace = AVCOL_SPC_BT470BG; // what swscale encodes RGB with
		own->color_range = AVCOL_RANGE_MPEG;
	}
	return av_frame_ref(dst, own);
}

// peak returns the brightest light, in cd/m², that f's HDR metadata says the
// video has: its brightest pixel's, else its mastering display's, else 0. A
// brightest pixel past the display's peak is a mistake, as discs that say
// 10000 make.
static double peak(const AVFrame *f) {
	double cll = 0, display = 0;
	const AVFrameSideData *sd = av_frame_get_side_data(f, AV_FRAME_DATA_CONTENT_LIGHT_LEVEL);
	if (sd)
		cll = ((const AVContentLightMetadata *)sd->data)->MaxCLL;
	sd = av_frame_get_side_data(f, AV_FRAME_DATA_MASTERING_DISPLAY_METADATA);
	if (sd && ((const AVMasteringDisplayMetadata *)sd->data)->has_luminance)
		display = av_q2d(((const AVMasteringDisplayMetadata *)sd->data)->max_luminance);
	return cll > 0 && (display <= 0 || cll <= display) ? cll : display;
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
	"math"
	"os"
	"sync"
	"unsafe"

	"fyne.io/fyne/v2/canvas"
)

const noPTS = math.MinInt64 // AV_NOPTS_VALUE

func init() {
	C.av_log_set_level(C.AV_LOG_FATAL) // decoders complain on stderr about every seek
}

// media is a file or stream open in libav. Its methods lock it, as the
// playback loop takes video from it while oto's reads take audio.
type media struct {
	mu     sync.Mutex
	fc     *C.AVFormatContext // nil once closed
	pkt    *C.AVPacket
	video  *track
	audio  *track
	audios []C.int       // the streams audio can play from
	sws    *C.SwsContext // for frames in formats painters do not draw
	swr    *C.SwrContext
	pcm    []byte
	start  float64 // stream time where playback time is zero
	dur    float64

	// how the video is shown: its width over height when its pixels are not
	// square, how far to turn it clockwise, and its HDR peak in cd/m², which
	// the frames that have metadata say
	aspect   float32
	rotation int
	peak     float64

	subMu     sync.Mutex // guards subs, subActive and their events
	subs      []*subTrack
	subActive int // the track showing, -1 for none
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
	m.pkt = C.av_packet_alloc()
	if i := C.av_find_best_stream(m.fc, C.AVMEDIA_TYPE_VIDEO, -1, -1, nil, 0); i >= 0 {
		m.video = m.open(i)
	}
	if withAudio {
		related := C.int(-1)
		if m.video != nil {
			related = m.video.index
		}
		if i := C.av_find_best_stream(m.fc, C.AVMEDIA_TYPE_AUDIO, -1, related, nil, 0); i >= 0 {
			m.audio, m.swr = m.openAudio(i)
		}
		for i, st := range unsafe.Slice(m.fc.streams, m.fc.nb_streams) {
			if st.codecpar.codec_type == C.AVMEDIA_TYPE_AUDIO {
				m.audios = append(m.audios, C.int(i))
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
	m.openSubtitles()
	if v := m.video; v != nil {
		st := C.stream(m.fc, v.index)
		if sar := C.av_guess_sample_aspect_ratio(m.fc, st, nil); sar.num > 0 && sar.den > 0 && sar.num != sar.den && v.cc.height > 0 {
			m.aspect = float32(v.cc.width) * float32(sar.num) / float32(sar.den) / float32(v.cc.height)
		}
		m.rotation = int(C.rotation(st))
	}
	return m, nil
}

// open opens a decoder for stream i.
func (m *media) open(i C.int) *track {
	st := C.stream(m.fc, i)
	codec := C.avcodec_find_decoder(st.codecpar.codec_id)
	if codec == nil {
		return nil
	}
	t := &track{index: i, cc: C.avcodec_alloc_context3(codec), frame: C.av_frame_alloc(), last: C.av_frame_alloc(), tb: float64(st.time_base.num) / float64(st.time_base.den)}
	if C.avcodec_parameters_to_context(t.cc, st.codecpar) < 0 {
		t.free()
		return nil
	}
	t.cc.pkt_timebase = st.time_base
	t.cc.thread_count = 0 // one per core
	if st.codecpar.codec_type == C.AVMEDIA_TYPE_VIDEO {
		t.useHardware(codec)
	}
	if C.avcodec_open2(t.cc, codec, nil) < 0 {
		t.free()
		return nil
	}
	return t
}

// openAudio opens a decoder for audio stream i and a converter from what it
// decodes, or returns nils.
func (m *media) openAudio(i C.int) (*track, *C.SwrContext) {
	t := m.open(i)
	if t == nil {
		return nil, nil
	}
	swr := C.pcm_converter(t.cc, sampleRate)
	if swr == nil {
		t.free()
		return nil, nil
	}
	return t, swr
}

// audioTracks returns the names of the audio tracks and which one plays, -1 for none.
func (m *media) audioTracks() ([]string, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fc == nil {
		return nil, -1
	}
	names, playing := make([]string, len(m.audios)), -1
	for n, i := range m.audios {
		par := C.stream(m.fc, i).codecpar
		var layout [64]C.char
		C.av_channel_layout_describe(&par.ch_layout, &layout[0], C.size_t(len(layout)))
		names[n] = streamName(C.stream(m.fc, i), n+1) + " (" + C.GoString(C.avcodec_get_name(par.codec_id)) + " " + C.GoString(&layout[0]) + ")"
		if m.audio != nil && m.audio.index == i {
			playing = n
		}
	}
	return names, playing
}

// playAudio has audio play from track n, reporting whether it could. Packets
// read before it switched are gone, so the track is heard after a seek.
func (m *media) playAudio(n int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fc == nil || n < 0 || n >= len(m.audios) {
		return false
	}
	t, swr := m.openAudio(m.audios[n])
	if t == nil {
		return false
	}
	if m.audio != nil {
		m.audio.free()
		C.swr_free(&m.swr)
	}
	m.audio, m.swr = t, swr
	return true
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

// take moves past the waiting video frame, holding it in dst unless dst is nil.
func (m *media) take(dst *picture) {
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
		m.hold(dst)
	}
}

// show holds the frame take moved past last in dst, false if there is none
// since the last seek.
func (m *media) show(dst *picture) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fc == nil || m.video == nil || m.video.last.data[0] == nil {
		return false
	}
	m.hold(dst)
	return true
}

func (m *media) hold(dst *picture) {
	if C.to_planes(&m.sws, dst.f, dst.own, m.video.last) < 0 {
		C.av_frame_unref(dst.f)
	}
	if p := float64(C.peak(m.video.last)); p > 0 {
		m.peak = p
	}
	dst.aspect, dst.rotation, dst.peak = m.aspect, m.rotation, m.peak
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
	m.seekSubtitles()
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
	C.swr_free(&m.swr)
	m.closeSubtitles()
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
			m.subtitlePacket(m.pkt)
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

// picture is a decoded frame kept for the screen: a reference to the decoder's
// planes, or to own when they had to be converted, and how it is shown.
type picture struct {
	f, own   *C.AVFrame
	aspect   float32
	rotation int
	peak     float64
}

func newPicture() *picture {
	return &picture{f: C.av_frame_alloc(), own: C.av_frame_alloc()}
}

func (pic *picture) free() {
	C.av_frame_free(&pic.f)
	C.av_frame_free(&pic.own)
}

// show points v at pic's planes, which stay put until pic holds another
// frame. A pic holding nothing leaves v without a picture.
func (pic *picture) show(v *canvas.VideoFrame) {
	f := pic.f
	if f.data[0] == nil {
		v.Width = 0
		return
	}
	w, h := int(f.width), int(f.height)
	ch := (h + 1) / 2
	plane := func(i, rows int) []byte {
		return unsafe.Slice((*byte)(unsafe.Pointer(f.data[i])), int(f.linesize[i])*rows)
	}
	v.Width, v.Height, v.Y, v.YStride, v.CStride = w, h, plane(0, h), int(f.linesize[0]), int(f.linesize[1])
	v.U, v.V, v.UV = nil, nil, nil
	switch f.format {
	case C.AV_PIX_FMT_NV12, C.AV_PIX_FMT_P010LE:
		v.UV = plane(1, ch)
	default:
		v.U, v.V = plane(1, ch), plane(2, ch)
	}
	switch v.Depth = 8; f.format {
	case C.AV_PIX_FMT_P010LE:
		v.Depth = 16 // its 10 bits are the high ones
	case C.AV_PIX_FMT_YUV420P10LE:
		v.Depth = 10
	}
	switch f.colorspace {
	case C.AVCOL_SPC_BT709:
		v.ColorSpace = canvas.VideoColorSpaceBT709
	case C.AVCOL_SPC_BT2020_NCL, C.AVCOL_SPC_BT2020_CL:
		v.ColorSpace = canvas.VideoColorSpaceBT2020
	case C.AVCOL_SPC_UNSPECIFIED:
		v.ColorSpace = canvas.VideoColorSpaceBT601
		if h >= 720 {
			v.ColorSpace = canvas.VideoColorSpaceBT709
		}
	default:
		v.ColorSpace = canvas.VideoColorSpaceBT601
	}
	v.FullRange = f.color_range == C.AVCOL_RANGE_JPEG || f.format == C.AV_PIX_FMT_YUVJ420P
	switch f.color_trc {
	case C.AVCOL_TRC_SMPTE2084:
		v.Transfer = canvas.VideoTransferPQ
	case C.AVCOL_TRC_ARIB_STD_B67:
		v.Transfer = canvas.VideoTransferHLG
	default:
		v.Transfer = canvas.VideoTransferSDR
	}
	v.Aspect, v.Rotation, v.Peak = pic.aspect, pic.rotation, float32(pic.peak)
}
