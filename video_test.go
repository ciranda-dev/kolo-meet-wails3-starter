//go:build finished

package main

import (
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	kolomedia "github.com/ciranda-dev/kolo-media"
	kolomoq "github.com/ciranda-dev/kolo-moq"
	"github.com/ciranda-dev/kolo-moq/secobj"
	kolovideo "github.com/ciranda-dev/kolo-video"
	"github.com/ciranda-dev/kolo-video/vpx"
)

// The engines' real-time sizes as kolo-video reports them (vpx.Capabilities).
var (
	nativeLimits   = vpx.Limits{Engine: vpx.EngineNative, Encode: vpx.RealTime{Width: 1920, Height: 1080, FrameRate: 30}, Decode: vpx.RealTime{Width: 1920, Height: 1080, FrameRate: 30}}
	portableLimits = vpx.Limits{Engine: vpx.EnginePortable, Encode: vpx.RealTime{Width: 640, Height: 480, FrameRate: 30}, Decode: vpx.RealTime{Width: 1920, Height: 1080, FrameRate: 30}}
)

// What is sent steps down by halves as the call grows, so each receiver decodes at most one real-time decoder's
// pixels a second in all.
func TestSendSizeSteps(t *testing.T) {
	hd := videoMode{w: 1280, h: 720, fps: 30}
	full := videoMode{w: 1920, h: 1080, fps: 30}
	for _, tc := range []struct {
		name   string
		fw, fh int
		v      videoMode
		lim    vpx.Limits
		others int
		w, h   int
	}{
		{"alone", 1280, 720, hd, nativeLimits, 0, 1280, 720},
		{"two people", 1280, 720, hd, nativeLimits, 1, 1280, 720},
		{"three people", 1280, 720, hd, nativeLimits, 2, 1280, 720},
		{"four people", 1280, 720, hd, nativeLimits, 3, 640, 360},
		{"eight people", 1280, 720, hd, nativeLimits, 7, 640, 360},
		{"ten people", 1280, 720, hd, nativeLimits, 9, 640, 360},
		{"eleven people", 1280, 720, hd, nativeLimits, 10, 320, 180},
		{"twenty people", 1280, 720, hd, nativeLimits, 19, 320, 180},
		{"a camera larger than asked", 1920, 1080, hd, nativeLimits, 1, 960, 540},
		{"1080p for two", 1920, 1080, full, nativeLimits, 1, 1920, 1080},
		{"1080p for three", 1920, 1080, full, nativeLimits, 2, 960, 540},
		{"the portable engine encodes up to 640x480", 1280, 720, hd, portableLimits, 1, 640, 360},
		{"no engine: only the setting", 1920, 1080, hd, vpx.Limits{}, 5, 960, 540},
		{"a 4:3 camera", 640, 480, hd, nativeLimits, 6, 640, 480},
		{"alone with a decoder smaller than the camera", 160, 120, videoMode{w: 160, h: 120, fps: 30},
			vpx.Limits{Decode: vpx.RealTime{Width: 80, Height: 60, FrameRate: 30}}, 0, 80, 60},
		{"a 4:3 camera, eight people", 640, 480, hd, nativeLimits, 7, 320, 240},
	} {
		w, h := sendSize(tc.fw, tc.fh, tc.v, tc.lim, tc.others)
		if w != tc.w || h != tc.h {
			t.Errorf("%s: %dx%d, want %dx%d", tc.name, w, h, tc.w, tc.h)
		}
	}
}

// Whatever the camera gives and however many people are in the call, a receiver's decoders get at most one real-time
// decoder's pixels a second, and no stream is larger than the engine encodes in real time or than the setting.
func TestSendSizeWorstCase(t *testing.T) {
	for _, lim := range []vpx.Limits{nativeLimits, portableLimits} {
		dec := float64(lim.Decode.Width*lim.Decode.Height) * lim.Decode.FrameRate
		for _, v := range []videoMode{defaultVideo, {w: 640, h: 480, fps: 30}, {w: 1920, h: 1080, fps: 30}, testVideo} {
			for _, f := range [][2]int{{v.w, v.h}, {1920, 1080}, {1280, 720}, {640, 480}, {1552, 1552}, {1, 1080}} {
				for others := 1; others < maxParticipantsCeiling; others++ {
					w, h := sendSize(f[0], f[1], v, lim, others)
					if float64(others*w*h)*v.fps > dec && w > 1 && h > 1 {
						t.Errorf("%v %v %d others: %dx%d is %.0f pixels a second", lim.Engine, f, others, w, h, float64(others*w*h)*v.fps)
					}
					if w*h > max(1, lim.Encode.Width*lim.Encode.Height) && w > 1 && h > 1 || w*h > v.w*v.h && w > 1 && h > 1 {
						t.Errorf("%v %v: %dx%d over the encoder or the setting", lim.Engine, f, w, h)
					}
					if w > f[0] || h > f[1] || w < 1 || h < 1 {
						t.Errorf("%v: %dx%d", f, w, h)
					}
				}
			}
		}
	}
}

// halve averages each 2x2 block, odd sizes included, and keeps the times.
func TestHalve(t *testing.T) {
	src, _ := kolovideo.NewFrame(5, 3)
	for i := range src.Y {
		src.Y[i] = byte(i*10 + i%3)
	}
	for i := range src.U {
		src.U[i], src.V[i] = byte(i*20), byte(200-i*20)
	}
	src.Timestamp, src.Duration = time.Second, time.Second/30
	dst, _ := kolovideo.NewFrame(2, 1)
	halve(dst, src)
	// Y rows of 5: 0 11 22 30 41 and 52 60 71 82 90; (0+11+52+60)/4 = 30.75 and (22+30+71+82)/4 = 51.25, rounded.
	if dst.Y[0] != 31 || dst.Y[1] != 51 {
		t.Errorf("Y %v", dst.Y)
	}
	// Chroma is 3x2 in the source and 1x1 here: the mean of (0,20,60,80) is 40.
	if dst.U[0] != 40 || dst.V[0] != 160 {
		t.Errorf("U %v V %v", dst.U, dst.V)
	}
	if dst.Timestamp != time.Second || dst.Duration != time.Second/30 || dst.Validate() != nil {
		t.Errorf("frame %+v", dst)
	}
	// The last chroma column of an odd width has no right neighbour: it is read, not past the plane.
	for _, n := range []int{6, 7} {
		odd, _ := kolovideo.NewFrame(n, n)
		half, _ := kolovideo.NewFrame(n/2, n/2)
		halve(half, odd)
	}
}

// A bitrate given for the setting's size keeps its bits per pixel at a smaller size; none means kolo-video's rule.
func TestVideoBitrate(t *testing.T) {
	v := videoMode{w: 1280, h: 720, fps: 30}
	if b := videoBitrate(v, 640, 360); b != 0 {
		t.Errorf("default: %d", b)
	}
	v.bitrate = 2000000
	if b := videoBitrate(v, 1280, 720); b != 2000000 {
		t.Errorf("full size: %d", b)
	}
	if b := videoBitrate(v, 640, 360); b != 500000 {
		t.Errorf("half: %d", b)
	}
	v.bitrate = 1
	if b := videoBitrate(v, 1, 1); b != 1 {
		t.Errorf("never zero: %d", b)
	}
}

func TestVideoFlags(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	load := func(args []string, e map[string]string) (settings, error) {
		c, err := LoadConfig(args, env(e), io.Discard)
		if err != nil {
			return settings{}, err
		}
		return c.resolve()
	}
	dir := t.TempDir()
	s, err := load([]string{"-data", dir}, nil)
	if err != nil || s.video != (videoMode{}) {
		t.Fatalf("default: %+v %v", s.video, err)
	}
	if defaultVideo != (videoMode{w: 1920, h: 1080, fps: 30}) {
		t.Errorf("default video %+v", defaultVideo)
	}
	s, err = load([]string{"-data", dir, "-video-size", "1920x1080", "-video-bitrate", "3000000"}, nil)
	if err != nil || s.video != (videoMode{w: 1920, h: 1080, fps: 30, bitrate: 3000000}) {
		t.Errorf("flags: %+v %v", s.video, err)
	}
	s, err = load([]string{"-data", dir}, map[string]string{"KOLO_MEET_VIDEO_SIZE": "640x480", "KOLO_MEET_VIDEO_BITRATE": "500000"})
	if err != nil || s.video != (videoMode{w: 640, h: 480, fps: 30, bitrate: 500000}) {
		t.Errorf("env: %+v %v", s.video, err)
	}
	s, err = load([]string{"-data", dir, "-video-bitrate", "800000"}, nil)
	if err != nil || s.video != (videoMode{w: 1920, h: 1080, fps: 30, bitrate: 800000}) {
		t.Errorf("bitrate alone: %+v %v", s.video, err)
	}
	for _, bad := range [][]string{
		{"-video-size", "1280"}, {"-video-size", "1280x"}, {"-video-size", "x720"}, {"-video-size", "0x720"},
		{"-video-size", "1280x0"}, {"-video-size", "-2x4"}, {"-video-size", "1921x1080"}, {"-video-size", "1920x1081"},
		{"-video-size", "1280 x 720"}, {"-video-size", "abc"},
		{"-video-bitrate", "0"}, {"-video-bitrate", "-1"}, {"-video-bitrate", "4294967296000"}, {"-video-bitrate", "fast"},
	} {
		_, err := load(append([]string{"-data", dir}, bad...), nil)
		if !errors.Is(err, ErrConfig) && err == nil {
			t.Errorf("%v accepted", bad)
		}
		if err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

// A sender halves what it sends when the receivers' budget asks for it: here a decoder made to handle 160x120 at
// 15 fps, so in a call of three each sends its 160x120 camera at 80x60 (two streams of 160x120 at 10 fps would be
// more than one such decoder).
func TestCallSendsHalvedPicturesWhenTheBudgetAsks(t *testing.T) {
	old := videoLimits
	videoLimits = func() vpx.Limits {
		return vpx.Limits{Engine: vpx.EngineNative, Encode: vpx.RealTime{Width: 1920, Height: 1080, FrameRate: 30},
			Decode: vpx.RealTime{Width: 160, Height: 120, FrameRate: 15}}
	}
	t.Cleanup(func() { videoLimits = old })
	ps := network(t, 3)
	startCall(t, ps)
	a, c := ps[0], ps[2]
	idC := idOf(t, c)
	waitUntil(t, "three in the call", func() bool { return c.activeCall().others.Load() == 2 })
	waitUntil(t, "Carla sends 80x60", func() bool {
		pub := c.activeCall().publisher()
		pub.mu.Lock()
		defer pub.mu.Unlock()
		return pub.vw == 80 && pub.vh == 60
	})
	n := a.rec.frameCount(idC)
	waitUntil(t, "Carla's pictures", func() bool { return a.rec.frameCount(idC) > n+3 })
	a.rec.mu.Lock()
	size := a.rec.lastSize[idC]
	a.rec.mu.Unlock()
	if size[0] > 80 || size[1] > 60 {
		t.Errorf("Ana sees Carla at %v, more than was sent", size)
	}
}

// The video setting reaches the camera, the encoder's size and the window's boxes.
func TestVideoSettingIsUsed(t *testing.T) {
	hw := newFakeHW(100, 440)
	p := startPeer(t, "Ana", Config{First: freeUDP(t), VideoSize: "320x180", VideoBitrate: 100000}, hw, nil)
	if p.vid != (videoMode{w: 320, h: 180, fps: 30, bitrate: 100000}) {
		t.Fatalf("video %+v", p.vid)
	}
	p.tiles.mu.Lock()
	r := p.tiles.rule
	p.tiles.mu.Unlock()
	if r == nil || *r != tileRuleFor(p.vid) {
		t.Errorf("tile rule %+v", r)
	}
	if _, err := p.SetCamera(true); err != nil {
		t.Fatal(err)
	}
	hw.mu.Lock()
	cam := hw.cam
	hw.mu.Unlock()
	if cam == nil || cam.w != 320 || cam.h != 180 {
		t.Errorf("camera opened at %+v", cam)
	}
}

// The encoder gets the bitrate for the size it sends.
func TestPublisherBitrate(t *testing.T) {
	if err := loadCodecs(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	origin, err := kolomoq.NewOrigin(nil)
	if err != nil {
		t.Fatal(err)
	}
	sender, err := secobj.NewSender(origin, kolomoq.Namespace{"r", "k"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	bc, err := kolomedia.NewSecureBroadcast(origin, sender)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Close()
	var others atomic.Int32
	others.Store(1)
	p := newPublisher(bc, sender, videoMode{w: 160, h: 120, fps: 30, bitrate: 400000}, &others)
	p.lim = vpx.Limits{Decode: vpx.RealTime{Width: 80, Height: 60, FrameRate: 30}}
	defer p.close()
	f, _ := kolovideo.NewFrame(160, 120)
	p.onVideo(f, time.Now())
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.venc == nil || p.vw != 80 || p.venc.Config().Bitrate != 100000 {
		t.Errorf("encoder %dx%d, bitrate %v", p.vw, p.vh, p.venc != nil && p.venc.Config().Bitrate == 100000)
	}
}

// A keyframe is written at most once a second per encoder, whatever asks for it: a new key, a lost group or the track.
func TestPublisherWritesAtMostOneKeyframeASecond(t *testing.T) {
	p := testPublisher(t, videoMode{w: 160, h: 120, fps: 30, bitrate: 400000})
	f, _ := kolovideo.NewFrame(160, 120)
	at := time.Now()
	keys := func() (uint64, bool) {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.nkey, p.needKey
	}
	ask := func() {
		p.mu.Lock()
		p.needKey = true
		p.mu.Unlock()
	}
	p.onVideo(f, at)
	if n, _ := keys(); n != 1 {
		t.Fatalf("%d keyframes after the first frame, want 1", n)
	}
	for _, after := range []time.Duration{100, 300, 600, 900} {
		ask()
		p.onVideo(f, at.Add(after*time.Millisecond))
		if n, want := keys(); n != 1 || !want {
			t.Fatalf("%d ms: %d keyframes, still wanted %v; want 1 and wanted", after, n, want)
		}
	}
	p.onVideo(f, at.Add(1100*time.Millisecond))
	if n, want := keys(); n != 2 || want {
		t.Fatalf("after a second: %d keyframes, wanted %v; want 2 and served", n, want)
	}
	for range 20 {
		p.onVideo(f, at.Add(1200*time.Millisecond))
	}
	if n, _ := keys(); n != 2 {
		t.Errorf("%d keyframes with no request, want 2", n)
	}
}
