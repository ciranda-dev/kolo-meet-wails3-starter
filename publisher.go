package main

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	koloaudio "github.com/ciranda-dev/kolo-audio"
	"github.com/ciranda-dev/kolo-audio/opus"
	kolomedia "github.com/ciranda-dev/kolo-media"
	"github.com/ciranda-dev/kolo-moq/secobj"
	kolovideo "github.com/ciranda-dev/kolo-video"
	"github.com/ciranda-dev/kolo-video/vpx"
)

// Names of the two media tracks of a participant.
const (
	audioTrackName = "mic"
	videoTrackName = "camera"
)

// keyRequestEvery is the least time between two keyframes of a track, apart from the first one of a new encoder: whether a
// watcher asked, the key changed or the path lost a group. A keyframe is several times the size of the frames around it,
// so asking cannot be a way to flood a thin uplink. It is kolo-moq's NewGroupRequestInterval, and kolo-media asks the
// publisher to answer with at most one a second.
const keyRequestEvery = time.Second

// noKeyRequests is a test seam: a test sets it to see a call whose publisher ignores the requests for a keyframe.
var noKeyRequests bool

// opusBitrate is the constant bitrate of kolo-media's group-call tests (secure_scale_test.go): protected audio
// must not reveal speech through packet sizes, so it is constant and DTX is off (kolo-media guide, Protected media).
const opusBitrate = 32000

// publisher turns the local capture into the tracks of the participant's protected broadcast. A track exists only
// while its device is on, so a viewer sees "microphone off" and "camera off" from the catalog, which is sealed.
type publisher struct {
	b      *kolomedia.Broadcast
	sender *secobj.Sender
	clock  *kolomedia.Clock
	vid    videoMode
	others *atomic.Int32 // the people who watch this broadcast
	lim    vpx.Limits

	// amu guards the microphone's state: audio, aenc, abase, asamples and lossPct. A camera frame is encoded under mu,
	// which takes longer than the 20 ms between two audio frames when the machine is busy, and the microphone must
	// not wait for it. close takes amu, then mu.
	amu      sync.Mutex
	audio    *kolomedia.TrackPublisher
	aenc     *opus.Encoder
	abase    uint64 // timestamp of the first frame since the track was added, moved forward when the timeline falls behind
	asamples uint64
	lossPct  int // the loss the microphone's encoder expects, 0 with its forward error correction off

	mu      sync.Mutex
	video   *kolomedia.TrackPublisher
	venc    *vpx.Encoder
	vw, vh  int
	vlast   uint64
	kid     uint64
	needKey bool
	lastKey time.Time // when the last keyframe was written
	nkey    uint64    // keyframes written
	scale   float64   // the share of the size's full bitrate the path carries, set by the adapter
	skip    bool      // the path is far behind: no video frame is encoded
	closed  bool
	scratch *kolovideo.Frame // the camera's frame scaled to the rung, reused
	rungs   []rung           // the ladder for the newest camera frame and the people in the call
	allowed int              // the highest rung the engine and the receivers allow
	rungH   int              // the height of the rung the ladder chose; 0 before it chose: the lowest
	cost    float64          // nanoseconds the encoder takes for a pixel of a delta frame, a running mean
	costN   int              // the frames in cost
	nenc    uint64           // camera frames encoded
	nskip   uint64           // camera frames not encoded because the path is far behind
}

func newPublisher(b *kolomedia.Broadcast, sender *secobj.Sender, vid videoMode, others *atomic.Int32) *publisher {
	return &publisher{b: b, sender: sender, clock: kolomedia.NewClock(), vid: vid, others: others, lim: videoLimits(),
		kid: sender.KeyID(), scale: 1}
}

// scaled returns f scaled down to w x h, a size of a rung; f itself when it already is that size.
func (p *publisher) scaled(f *kolovideo.Frame, w, h int) *kolovideo.Frame {
	if f.Width == w && f.Height == h {
		return f
	}
	if p.scratch == nil || p.scratch.Width != w || p.scratch.Height != h {
		nf, err := kolovideo.NewFrame(w, h)
		if err != nil {
			return nil
		}
		p.scratch = nf
	}
	if f.Width/2 == w && f.Height/2 == h {
		halve(p.scratch, f)
	} else {
		shrink(p.scratch, f)
	}
	return p.scratch
}

// audioEncoderConfig is the Opus encoder of every microphone: constant bitrate with DTX off, so the size of a packet
// does not follow speech (kolo-media guide, Protected media, item 3).
func audioEncoderConfig() opus.Config {
	return opus.Config{SampleRate: sampleRate, Channels: audioChannels, Application: opus.VoIP, Bitrate: opusBitrate, CBR: true,
		DTX: false, FrameDuration: 20 * time.Millisecond}
}

func audioTrackDef() kolomedia.Track {
	return kolomedia.Track{
		Name: audioTrackName, Kind: kolomedia.Audio, Codec: kolomedia.Opus, Timescale: sampleRate, Bitrate: opusBitrate,
		SampleRate: sampleRate, Channels: audioChannels, FrameDuration: 20 * time.Millisecond,
	}
}

// setAudio adds or removes the microphone track. Step 3 of the guide writes it.
func (p *publisher) setAudio(on bool) error { return notInStarter(3) }

// audioComplexity is a test seam: the Opus complexity of the microphone's encoder, or -1 for the library's default (10).
// Under the race detector an encoder that carries the forward error correction takes 29 ms for a 20 ms frame, so the
// tests lower it there.
var audioComplexity = -1

// setLoss tells the encoder to expect pct percent loss and to carry the previous frame in each packet, or not at 0.
func setLoss(enc *opus.Encoder, pct int) {
	enc.SetFEC(pct > 0)
	enc.SetPacketLossPercent(pct)
}

// setAudioLoss sets the loss the microphone's encoder expects, from what the sender saw of its path; 0 turns the
// forward error correction off. A microphone turned on later starts with the same.
func (p *publisher) setAudioLoss(pct int) {
	p.amu.Lock()
	defer p.amu.Unlock()
	if p.closed || pct == p.lossPct {
		return
	}
	p.lossPct = pct
	if p.aenc != nil {
		setLoss(p.aenc, pct)
	}
}

// setVideo removes the camera track; it is added again by the first frame of the camera, which tells its size.
func (p *publisher) setVideo(on bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if on {
		return nil
	}
	return p.dropVideoLocked()
}

// dropVideoLocked removes the camera track and closes the encoder, whichever of the two exists: the encoder is made
// before the track and outlives a track that could not be added.
func (p *publisher) dropVideoLocked() error {
	var err error
	if p.video != nil {
		err = p.video.Close()
		p.video = nil
	}
	if p.venc != nil {
		p.venc.Close()
		p.venc = nil
	}
	return err
}

// onAudio encodes one 20 ms frame of the microphone. Step 3 of the guide writes it.
func (p *publisher) onAudio(pcm []int16) {}

// onVideo encodes one camera frame, at the rung the ladder chose, within what the engine and the receivers allow; a change of size adds the track again with the new size, and its encoder starts with a keyframe. A new key
// (someone joined or left) forces a keyframe, so a joiner does not wait for the next one (kolo-media guide, Protected
// media, step 4).
func (p *publisher) onVideo(f *kolovideo.Frame, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	others := 0
	if p.others != nil {
		others = int(p.others.Load())
	}
	if p.skip {
		p.needKey = true
		p.nskip++
		return
	}
	p.rungs, p.allowed = ladderRungs(p.vid, f.Width, f.Height, p.lim, others)
	i := min(p.rungIndexLocked(), p.allowed)
	if noAdapt {
		i = p.allowed // no controller to climb: the largest picture allowed, as the test seam promises
	}
	p.rungH = p.rungs[i].h
	if j := p.sendingLocked(); j >= 0 && i > j && now.Sub(p.lastKey) < vpx.DefaultKeyframeInterval-frameEvery(p.vid.fps) {
		i = j // a step up waits for the key frame the encoder is about to make, so it costs none of its own
	}
	r := p.rungs[i]
	if f = p.scaled(f, r.w, r.h); f == nil {
		return
	}
	if p.venc != nil && (f.Width != p.vw || f.Height != p.vh) {
		p.dropVideoLocked()
	}
	fresh := false // a new encoder starts with a keyframe whatever is asked
	if p.venc == nil {
		enc, err := vpx.NewEncoder(vpx.Config{Codec: vpx.VP8, Width: f.Width, Height: f.Height, FrameRate: p.vid.fps,
			Bitrate: p.rateLocked(f.Width, f.Height)})
		if err != nil {
			return
		}
		p.venc, p.vw, p.vh, p.needKey, fresh = enc, f.Width, f.Height, true, true
	}
	if p.video == nil {
		tp, err := p.b.AddTrack(kolomedia.Track{
			Name: videoTrackName, Kind: kolomedia.Video, Codec: kolomedia.VP8, Timescale: 90000,
			Width: uint32(f.Width), Height: uint32(f.Height), FrameRate: p.vid.fps,
		}, kolomedia.PackagerOptions{})
		if err != nil {
			return
		}
		p.video, p.needKey, fresh = tp, true, true
	}
	if kid := p.sender.KeyID(); kid != p.kid {
		p.kid, p.needKey = kid, true
	}
	select {
	case <-p.video.KeyframeRequests():
		if !noKeyRequests && now.Sub(p.lastKey) >= keyRequestEvery {
			p.needKey = true
		}
	default:
	}
	key, began := p.needKey && (fresh || now.Sub(p.lastKey) >= keyRequestEvery), time.Now()
	pkts, err := p.venc.Encode(f, key)
	if err != nil {
		return
	}
	p.nenc++
	if !key {
		p.noteCost(f.Width*f.Height, time.Since(began))
	}
	for _, pk := range pkts {
		ts, err := p.clock.At(now, 90000)
		if err != nil {
			return
		}
		if ts <= p.vlast {
			ts = p.vlast + 1
		}
		p.vlast = ts
		err = p.video.WriteFrame(kolomedia.Frame{Timestamp: ts, Keyframe: pk.Keyframe, Data: pk.Data})
		switch {
		case err == nil:
			if pk.Keyframe {
				p.needKey, p.lastKey = false, now
				p.nkey++
			}
		case errors.Is(err, kolomedia.ErrNeedKeyframe):
			p.needKey = true
		}
	}
}

// sendingLocked is the index in p.rungs of the rung being encoded, -1 when none is.
func (p *publisher) sendingLocked() int {
	if p.venc == nil {
		return -1
	}
	for i, r := range p.rungs {
		if r.w == p.vw && r.h == p.vh {
			return i
		}
	}
	return -1
}

// frameEvery is the time between two camera frames at fps.
func frameEvery(fps float64) time.Duration {
	return time.Duration(float64(time.Second) / max(fps, 1))
}

// rateLocked is the bitrate of a w x h stream now: the size's full rate, scaled by what the path carries.
func (p *publisher) rateLocked(w, h int) int {
	full := fullRate(p.vid, w, h)
	if p.scale >= 1 {
		return full
	}
	return max(1, int(float64(full)*p.scale))
}

// fullRate is the bitrate of a w x h stream before the path asks for less: the setting's, or kolo-video's rule up to
// maxDefaultBitrate.
func fullRate(v videoMode, w, h int) int {
	if b := videoBitrate(v, w, h); b > 0 {
		return b
	}
	return int(min(vpx.DefaultBitsPerPixel*float64(w*h)*v.fps, maxDefaultBitrate))
}

// fullRate is the full bitrate of the stream being sent, 0 before the first frame.
func (p *publisher) fullRate() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.venc == nil {
		return 0
	}
	return float64(fullRate(p.vid, p.vw, p.vh))
}

// rungIndexLocked is the index in p.rungs of the highest rung that is not above the height the ladder chose.
func (p *publisher) rungIndexLocked() int {
	for i := len(p.rungs) - 1; i > 0; i-- {
		if p.rungs[i].h <= p.rungH {
			return i
		}
	}
	return 0
}

// fakeEncodeTime is a test seam: when set, it gives the time an encode of that many pixels took, whatever the clock says.
var fakeEncodeTime func(pixels int) time.Duration

// noteCost adds the time a delta frame of px pixels took to encode to the running mean of the cost of a pixel. Key
// frames are left out: they cost several times a delta frame, once in 2 s, and do not set the load the encoder keeps.
func (p *publisher) noteCost(px int, took time.Duration) {
	if fakeEncodeTime != nil {
		took = fakeEncodeTime(px)
	}
	ns := float64(took) / float64(px)
	if p.costN == 0 {
		p.cost = ns
	} else {
		p.cost += (ns - p.cost) * costWeight
	}
	p.costN++
}

// ladder returns the rungs for the camera and the call now, the index of the one being encoded, and the index of the
// highest one the engine, the receivers and the processor allow: one whose encode time, from the cost of a pixel, is under cpuShare of a
// frame interval. The lowest rung always counts. Before the encoder has run costWarm frames nothing is ruled out.
func (p *publisher) ladder() (rs []rung, cur, top int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rs = slices.Clone(p.rungs)
	if len(rs) == 0 {
		return nil, 0, 0
	}
	top = p.allowed
	if p.costN >= costWarm {
		budget := cpuShare * float64(time.Second) / max(p.vid.fps, 1)
		for top > 0 && p.cost*float64(rs[top].w*rs[top].h) > budget {
			top--
		}
	}
	cur = p.rungIndexLocked()
	if j := p.sendingLocked(); j >= 0 {
		cur = j // a step up asked for is not sent before the next key frame
	}
	return rs, min(cur, p.allowed), top
}

// setRung makes rung i of the last ladder() the one to send; the encoder is made again, with a key frame, at the next
// camera frame.
func (p *publisher) setRung(i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i >= 0 && i < len(p.rungs) {
		p.rungH = p.rungs[i].h
	}
}

func (p *publisher) rateScale() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scale
}

func (p *publisher) skipping() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.skip
}

// setPath applies what the adapter decided: the encoder's bitrate, a key frame after a lost group, and whether to
// encode at all.
func (p *publisher) setPath(d decision) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.skip = d.skip
	if d.key && time.Since(p.lastKey) >= keyRequestEvery {
		p.needKey = true
	}
	if d.scale != p.scale {
		p.scale = d.scale
		if p.venc != nil {
			p.venc.SetBitrate(p.rateLocked(p.vw, p.vh))
		}
	}
}

func (p *publisher) close() {
	p.amu.Lock()
	defer p.amu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	if p.audio != nil {
		p.audio.Close()
		p.aenc.Close()
	}
	p.dropVideoLocked()
}

// pcmLevel is the RMS level of pcm in dBFS.
func pcmLevel(pcm []int16) float64 {
	_, rms := koloaudio.Level(pcm)
	return rms
}
