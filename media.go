package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	koloaudio "github.com/ciranda-dev/kolo-audio"
	kolovideo "github.com/ciranda-dev/kolo-video"
)

// How a voice is told from silence. They are the app's choices: no library defines them, and none was tuned on real
// devices. The pictures' sizes and rates are in tiles.go.
const (
	levelInterval   = 100 * time.Millisecond // meters and speaking indicators refresh 10 times a second
	speakingFloor   = -50.0                  // dBFS; an RMS above it is a voice
	speakingHold    = 300 * time.Millisecond // a voice stays "speaking" this long after its last loud frame
	meterFloor      = -60.0                  // dBFS shown as an empty meter
	mixQueueFrames  = 20                     // 400 ms of 20 ms frames: kolo-media's playout MaxDelay
	captureShutdown = 2 * time.Second
)

// zeroMicAfter is how long a microphone must send only zeros before the window is told. A variable so a test can
// shorten it. App choice: a quiet room is never exactly zero, so this is a sign of a blocked microphone.
var zeroMicAfter = 4 * time.Second

// videoMode is the size and rate asked of the camera (-video-size; the rate is kolo-video's default) and the VP8
// bitrate at that size (-video-bitrate; 0 is kolo-video's rule).
type videoMode struct {
	w, h    int
	fps     float64
	bitrate int
}

var defaultVideo = videoMode{w: defaultVideoWidth, h: defaultVideoHeight, fps: kolovideo.DefaultFrameRate}

// MediaState is the state of one device, in the lobby and in a call.
type MediaState struct {
	State    string     `json:"state"` // "off", "on" or "error"
	DeviceID string     `json:"deviceId"`
	Error    *MeetError `json:"error"` // set with "error"
	// Hint is set while the microphone is "on" but has sent only digital silence (all-zero samples) for
	// zeroMicAfter: the usual sign of a refused permission on macOS, which gives silent buffers instead of an error.
	// Its reason is "microphone-silent".
	Hint *MeetError `json:"hint"`
}

// FrameEvent is the payload of the "meet:frame" event: the newest picture of one participant, a JPEG.
type FrameEvent struct {
	ID     string `json:"id"` // a participant's ID, or "self"
	Seq    uint64 `json:"seq"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	JPEG   []byte `json:"jpeg"`
}

// Level is how loud one participant is: Level from 0 (silence) to 1 (full scale), and whether it is speaking.
type Level struct {
	ID       string  `json:"id"`
	Level    float64 `json:"level"`
	Speaking bool    `json:"speaking"`
}

// meter maps a level in dBFS to 0 (meterFloor or lower) to 1 (full scale).
func meter(db float64) float64 {
	if math.IsNaN(db) || db <= meterFloor {
		return 0
	}
	return min(1, (db-meterFloor)/-meterFloor)
}

// speakTracker decides "speaking" from the RMS of successive frames, with a hold so words do not flicker.
type speakTracker struct {
	last time.Time
}

func (t *speakTracker) update(rmsDB float64, now time.Time) bool {
	if rmsDB > speakingFloor {
		t.last = now
	}
	return !t.last.IsZero() && now.Sub(t.last) < speakingHold
}

// tileRenderer turns frames into JPEGs for the window, at the size and rate its tileBudget allows. It is not safe for
// concurrent use.
type tileRenderer struct {
	b    *tileBudget // nil: one tile, the window's size not known
	id   string
	next time.Time // the earliest the next picture may be drawn
	r    kolovideo.Renderer
	img  *image.RGBA
	buf  bytes.Buffer
	seq  uint64
}

// render draws f and returns the JPEG, or false when it is not the tile's turn, f is unusable, or the picture is
// larger than the tile's share of a second.
func (t *tileRenderer) render(f *kolovideo.Frame, now time.Time) (FrameEvent, bool) {
	if f == nil || f.Width <= 0 || f.Height <= 0 || f.Width > maxFrameWidth || f.Height > maxFrameHeight || now.Before(t.next.Add(-tileEarly)) {
		return FrameEvent{}, false
	}
	began := time.Now()
	size, share, rate := t.b.take(t.id, f.Width, f.Height)
	interval := time.Second / time.Duration(rate)
	w, h := size.X, size.Y
	if t.img == nil || t.img.Bounds().Dx() != w || t.img.Bounds().Dy() != h {
		t.img = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	if err := t.r.Render(t.img, f); err != nil {
		return FrameEvent{}, false
	}
	t.buf.Reset()
	if err := jpeg.Encode(&t.buf, t.img, &jpeg.Options{Quality: jpeg.DefaultQuality}); err != nil {
		return FrameEvent{}, false
	}
	t.b.noteCost(t.id, time.Since(began))
	n := frameEventBytes(t.buf.Len())
	if n > share {
		t.wait(now, interval)
		return FrameEvent{}, false
	}
	// The next picture waits until this one fits the tile's share, so a tile sends at most share bytes a second.
	t.wait(now, max(interval, time.Duration(n)*time.Second/time.Duration(share)))
	t.seq++
	return FrameEvent{Seq: t.seq, Width: w, Height: h, JPEG: bytes.Clone(t.buf.Bytes())}, true
}

// wait moves the next turn gap later. A turn taken late is counted from the time it was due, unless it is a whole
// gap late, so the rate holds on average without a burst after a pause.
func (t *tileRenderer) wait(now time.Time, gap time.Duration) {
	if now.Sub(t.next) > gap {
		t.next = now
	}
	t.next = t.next.Add(gap)
}

// micCapture reads a microphone in frames of frameSamples until stopped.
type micCapture struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func startMic(s micStream, onFrame func([]int16), onLost func(error)) *micCapture {
	ctx, cancel := context.WithCancel(context.Background())
	c := &micCapture{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(c.done)
		defer s.Close()
		buf := make([]int16, frameSamples)
		for have := 0; ; {
			n, err := s.Read(ctx, buf[have:])
			have += n
			if have == len(buf) {
				onFrame(buf)
				have = 0
			}
			if err != nil {
				if ctx.Err() == nil {
					onLost(err)
				}
				return
			}
		}
	}()
	return c
}

func (c *micCapture) stop() {
	c.cancel()
	waitDone(c.done)
}

// camCapture reads a camera until stopped.
type camCapture struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func startCam(s cameraStream, onFrame func(*kolovideo.Frame), onLost func(error)) *camCapture {
	ctx, cancel := context.WithCancel(context.Background())
	c := &camCapture{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(c.done)
		defer s.Close()
		for {
			f, err := s.Read(ctx)
			if err != nil {
				if ctx.Err() == nil {
					onLost(err)
				}
				return
			}
			onFrame(f)
		}
	}()
	return c
}

func (c *camCapture) stop() {
	c.cancel()
	waitDone(c.done)
}

// waitDone waits for a capture loop to end. A device that does not return from Read in captureShutdown is left
// behind rather than blocking the app; its loop ends when the device does.
func waitDone(done <-chan struct{}) {
	select {
	case <-done:
	case <-time.After(captureShutdown):
	}
}

// sinks are the consumers of the local capture while in a call.
type sinks struct {
	audio func(pcm []int16)
	video func(f *kolovideo.Frame, now time.Time)
}

// localMedia owns this participant's microphone and camera, in the lobby and in a call, and turns them on and off.
// A device that is off is closed, so the system's "in use" light goes out.
type localMedia struct {
	hw    hardware
	vid   videoMode
	frame func(FrameEvent) // the self-view; nil: none
	lost  func(kind string, err error)
	hint  func() // the silent-microphone hint changed

	mu   sync.Mutex
	mic  *micCapture
	cam  *camCapture
	tile tileRenderer

	sink     atomic.Pointer[sinks]
	level    atomic.Uint64 // math.Float64bits of the newest frame's level in dBFS
	speaking atomic.Bool
	speak    speakTracker
	zeros    atomic.Int64 // consecutive frames that were all zeros
	silent   atomic.Bool  // the hint is on
}

func newLocalMedia(hw hardware, vid videoMode, frame func(FrameEvent), lost func(string, error), hint func()) *localMedia {
	l := &localMedia{hw: hw, vid: vid, frame: frame, lost: lost, hint: hint, tile: tileRenderer{id: selfTile}}
	l.level.Store(math.Float64bits(math.Inf(-1)))
	return l
}

// SelfLevel returns the level of the microphone now.
func (l *localMedia) selfLevel() Level {
	db := math.Float64frombits(l.level.Load())
	return Level{ID: "self", Level: meter(db), Speaking: l.speaking.Load()}
}

func (l *localMedia) setSink(s *sinks) { l.sink.Store(s) }

// openMic opens the microphone id ("" is the default) unless it is open.
func (l *localMedia) openMic(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.mic != nil {
		return nil
	}
	s, err := l.hw.OpenMicrophone(id)
	if err != nil {
		return deviceError(devMicrophone, err)
	}
	l.mic = startMic(s, l.onAudio, func(err error) { l.deviceLost(devMicrophone, err) })
	return nil
}

func (l *localMedia) closeMic() {
	l.mu.Lock()
	c := l.mic
	l.mic = nil
	l.mu.Unlock()
	if c != nil {
		c.stop()
	}
	l.zeros.Store(0)
	l.silent.Store(false)
	l.level.Store(math.Float64bits(math.Inf(-1)))
	l.speaking.Store(false)
}

// openCamera opens the camera id ("" is the default) unless it is open. It asks the system for access first where
// that is needed (macOS), which may show the system prompt.
func (l *localMedia) openCamera(ctx context.Context, id string) error {
	l.mu.Lock()
	open := l.cam != nil
	l.mu.Unlock()
	if open {
		return nil
	}
	if err := l.hw.RequestCamera(ctx); err != nil {
		return deviceError(devCamera, err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cam != nil {
		return nil
	}
	s, err := l.hw.OpenCamera(id, l.vid.w, l.vid.h, l.vid.fps)
	if err != nil {
		return deviceError(devCamera, err)
	}
	l.cam = startCam(s, l.onVideo, func(err error) { l.deviceLost(devCamera, err) })
	return nil
}

func (l *localMedia) closeCamera() {
	l.mu.Lock()
	c := l.cam
	l.cam = nil
	l.mu.Unlock()
	if c != nil {
		c.stop()
	}
	l.tile.b.release(l.tile.id)
}

func (l *localMedia) closeAll() {
	l.closeMic()
	l.closeCamera()
}

func (l *localMedia) isOpen() (mic, cam bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.mic != nil, l.cam != nil
}

func (l *localMedia) deviceLost(kind string, err error) {
	if kind == devMicrophone {
		l.closeMicAfterLoss()
	} else {
		l.closeCameraAfterLoss()
	}
	if l.lost != nil {
		l.lost(kind, err)
	}
}

// The loop that found the loss ends by itself; only the field is cleared, without waiting for the loop.
func (l *localMedia) closeMicAfterLoss() {
	l.mu.Lock()
	l.mic = nil
	l.mu.Unlock()
	l.level.Store(math.Float64bits(math.Inf(-1)))
	l.speaking.Store(false)
}

func (l *localMedia) closeCameraAfterLoss() {
	l.mu.Lock()
	l.cam = nil
	l.mu.Unlock()
	l.tile.b.release(l.tile.id)
}

func (l *localMedia) onAudio(pcm []int16) {
	l.watchZeros(pcm)
	_, rms := koloaudio.Level(pcm)
	l.level.Store(math.Float64bits(rms))
	l.speaking.Store(l.speak.update(rms, time.Now()))
	if s := l.sink.Load(); s != nil && s.audio != nil {
		s.audio(pcm)
	}
}

// watchZeros turns the silent-microphone hint on after zeroMicAfter of all-zero frames, and off at the first sample
// that is not zero.
func (l *localMedia) watchZeros(pcm []int16) {
	if slices.ContainsFunc(pcm, func(v int16) bool { return v != 0 }) {
		l.zeros.Store(0)
		if l.silent.CompareAndSwap(true, false) && l.hint != nil {
			l.hint()
		}
		return
	}
	if time.Duration(l.zeros.Add(1))*20*time.Millisecond >= zeroMicAfter && l.silent.CompareAndSwap(false, true) && l.hint != nil {
		l.hint()
	}
}

func (l *localMedia) onVideo(f *kolovideo.Frame) {
	now := time.Now()
	if l.frame != nil {
		l.mu.Lock()
		ev, ok := l.tile.render(f, now)
		l.mu.Unlock()
		if ok {
			ev.ID = selfTile
			l.frame(ev)
		}
	}
	if s := l.sink.Load(); s != nil && s.video != nil {
		s.video(f, now)
	}
}

// mixer plays the audio of every remote participant on one speaker: each participant has a short queue of 20 ms
// frames, and every 20 ms the queues are added into one frame and written.
type mixer struct {
	out speakerStream // nil: nothing plays, the queues are only emptied

	mu     sync.Mutex
	queues map[string][][]int16
	ticks  int
}

func newMixer(out speakerStream) *mixer { return &mixer{out: out, queues: map[string][][]int16{}} }

// push queues a copy of pcm for participant id; the oldest frame goes when the queue is full.
func (m *mixer) push(id string, pcm []int16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := append(m.queues[id], append([]int16(nil), pcm...))
	if len(q) > mixQueueFrames {
		q = q[len(q)-mixQueueFrames:]
	}
	m.queues[id] = q
}

func (m *mixer) drop(id string) {
	m.mu.Lock()
	delete(m.queues, id)
	m.mu.Unlock()
}

// mix takes one frame from every queue and adds them. A queue longer than mixTarget is shortened by one frame every
// mixCatchEvery ticks. It returns false when every queue was empty.
func (m *mixer) mix(dst []int16) bool {
	clear(dst)
	m.mu.Lock()
	defer m.mu.Unlock()
	any := false
	m.ticks++
	for id, q := range m.queues {
		if len(q) > mixTarget && m.ticks%mixCatchEvery == 0 {
			// Behind by more than the target: two frames become one, so the queue catches up without a click.
			q = append([][]int16{crossfade(q[0], q[1])}, q[2:]...)
		}
		if len(q) == 0 {
			continue
		}
		koloaudio.Mix(dst, q[0])
		m.queues[id] = q[1:]
		any = true
	}
	return any
}

// run mixes and writes a frame every 20 ms until ctx ends. A speaker that fails is reported once and the mixer
// keeps emptying the queues, so a lost speaker never blocks the call.
func (m *mixer) run(ctx context.Context, lost func(error)) {
	tick := time.NewTicker(time.Second / 50)
	defer tick.Stop()
	if m.out != nil {
		defer m.out.Close()
	}
	buf := make([]int16, frameSamples)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		any := m.mix(buf)
		if m.out == nil || !any {
			continue
		}
		if _, err := m.out.Write(ctx, buf); err != nil && ctx.Err() == nil {
			if lost != nil && !errors.Is(err, koloaudio.ErrClosed) {
				lost(err)
			}
			m.out.Close()
			m.out = nil
		}
	}
}
