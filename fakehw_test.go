//go:build finished

package main

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"sync"
	"sync/atomic"
	"time"

	koloaudio "github.com/ciranda-dev/kolo-audio"
	kolovideo "github.com/ciranda-dev/kolo-video"
)

// fakeHW is a camera, a microphone and a speaker made of numbers: a flat picture of one brightness and a sine of one
// frequency, so a test can tell who sent what. No test opens a real device.
type fakeHW struct {
	luma byte    // brightness of the picture
	freq float64 // pitch of the tone, Hz
	// texture makes the picture a moving pattern with noise instead of a flat one, so its JPEG and VP8 sizes are like
	// a camera's rather than near zero.
	texture bool

	denyCamera atomic.Bool
	denyMic    atomic.Bool
	noSpeaker  atomic.Bool
	micSilent  atomic.Bool // the microphone sends only zeros from the start
	// micAccess is the permission the fake reports: 0 authorized, 1 not determined, 2 denied, 3 restricted, 4 the
	// platform cannot say. micAnswer answers a prompt: "allow" or "deny"; with no answer the prompt waits for ctx.
	micAccess atomic.Int32
	micAnswer chan string
	logMu     sync.Mutex
	order     []string     // "status", "request" and "open-mic" in the order they happened
	opened    atomic.Int32 // devices open now
	opens     atomic.Int32 // devices opened so far, of any kind
	closes    atomic.Int32 // devices closed so far, each counted once
	camOpens  atomic.Int32
	micOpens  atomic.Int32

	mu     sync.Mutex
	played []int16 // what the speaker got, newest last, kept to the last second
	cam    *fakeCamera
	mic    *fakeMic
}

func (f *fakeHW) note(s string) { f.logMu.Lock(); f.order = append(f.order, s); f.logMu.Unlock() }

func (f *fakeHW) calls() []string {
	f.logMu.Lock()
	defer f.logMu.Unlock()
	return append([]string(nil), f.order...)
}

func (f *fakeHW) MicrophoneAccess() (string, error) {
	f.note("status")
	switch f.micAccess.Load() {
	case 1:
		return micNotDetermined, nil
	case 2:
		return micDenied, nil
	case 3:
		return micRestricted, nil
	case 4:
		return "", koloaudio.ErrUnsupported
	}
	return micAuthorized, nil
}

func (f *fakeHW) RequestMicrophone(ctx context.Context) error {
	f.note("request")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case a := <-f.micAnswer:
		if a == "allow" {
			f.micAccess.Store(0)
			return nil
		}
		f.micAccess.Store(2)
		return fmt.Errorf("%w (denied): %w", koloaudio.ErrPermission, fs.ErrPermission)
	}
}

func newFakeHW(luma byte, freq float64) *fakeHW {
	return &fakeHW{luma: luma, freq: freq}
}

func (f *fakeHW) Cameras() ([]Device, error) {
	return []Device{{ID: "cam-a", Name: "Camera A", Default: true}, {ID: "cam-b", Name: "Camera B"}}, nil
}
func (f *fakeHW) Microphones() ([]Device, error) {
	return []Device{{ID: "mic-a", Name: "Microphone A", Default: true}}, nil
}
func (f *fakeHW) Speakers() ([]Device, error) {
	return []Device{{ID: "spk-a", Name: "Speaker A", Default: true}}, nil
}

func (f *fakeHW) RequestCamera(context.Context) error {
	if f.denyCamera.Load() {
		return kolovideo.ErrPermission
	}
	return nil
}

func (f *fakeHW) OpenCamera(id string, w, h int, fps float64) (cameraStream, error) {
	if f.denyCamera.Load() {
		return nil, kolovideo.ErrPermission
	}
	f.opened.Add(1)
	f.opens.Add(1)
	f.camOpens.Add(1)
	c := &fakeCamera{hw: f, w: w, h: h, every: time.Duration(float64(time.Second) / fps), closed: make(chan struct{})}
	f.mu.Lock()
	f.cam = c
	f.mu.Unlock()
	return c, nil
}

func (f *fakeHW) OpenMicrophone(id string) (micStream, error) {
	f.note("open-mic")
	if f.denyMic.Load() {
		return nil, fsPermission{}
	}
	f.opened.Add(1)
	f.opens.Add(1)
	f.micOpens.Add(1)
	m := &fakeMic{hw: f, closed: make(chan struct{}), next: time.Now()}
	m.silent.Store(f.micSilent.Load())
	f.mu.Lock()
	f.mic = m
	f.mu.Unlock()
	return m, nil
}

func (f *fakeHW) OpenSpeaker(id string) (speakerStream, error) {
	if f.noSpeaker.Load() {
		return nil, koloaudio.ErrNoDevice
	}
	f.opened.Add(1)
	f.opens.Add(1)
	return &fakeSpeaker{hw: f}, nil
}

// fsPermission is what kolo-audio returns for a denied microphone: an error that matches fs.ErrPermission.
type fsPermission struct{}

func (fsPermission) Error() string        { return "koloaudio: audio backend failed: permission denied" }
func (fsPermission) Is(target error) bool { return target == fs.ErrPermission }

type fakeCamera struct {
	hw     *fakeHW
	w, h   int
	every  time.Duration
	next   time.Time
	closed chan struct{}
	once   sync.Once
	lose   atomic.Bool
	frames int
}

func (c *fakeCamera) Read(ctx context.Context) (*kolovideo.Frame, error) {
	if c.next.IsZero() {
		c.next = time.Now()
	}
	c.next = c.next.Add(c.every)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, kolovideo.ErrClosed
	case <-time.After(time.Until(c.next)):
	}
	if c.lose.Load() {
		return nil, kolovideo.ErrDeviceLost
	}
	f, err := kolovideo.NewFrame(c.w, c.h)
	if err != nil {
		return nil, err
	}
	c.frames++
	f.Timestamp, f.Duration = time.Duration(c.frames)*c.every, c.every
	if c.hw.texture {
		paintTexture(f, c.frames)
		return f, nil
	}
	for i := range f.Y {
		f.Y[i] = c.hw.luma
	}
	return f, nil
}

// paintTexture draws picture number n of a slow pan over blocks and stripes, with noise like a camera sensor's.
func paintTexture(f *kolovideo.Frame, n int) {
	seed := uint32(n)*2654435761 + 1
	for y := range f.Height {
		for x := range f.Width {
			seed = seed*1664525 + 1013904223
			u := x + 2*n
			v := 60 + (u/24+y/24)%2*90 + (u+y)/16 + int(seed>>30)
			f.Y[y*f.YStride+x] = byte(min(v, 255))
		}
	}
	for y := range (f.Height + 1) / 2 {
		for x := range (f.Width + 1) / 2 {
			f.U[y*f.UVStride+x] = byte(100 + (x+n)/16%2*40)
			f.V[y*f.UVStride+x] = byte(140 - y/16%2*30)
		}
	}
}

func (c *fakeCamera) Close() error {
	c.once.Do(func() { c.hw.opened.Add(-1); c.hw.closes.Add(1); close(c.closed) })
	return nil
}

type fakeMic struct {
	hw     *fakeHW
	next   time.Time
	phase  float64
	closed chan struct{}
	once   sync.Once
	lose   atomic.Bool
	silent atomic.Bool
}

func (m *fakeMic) Read(ctx context.Context, p []int16) (int, error) {
	m.next = m.next.Add(time.Duration(len(p)) * time.Second / sampleRate)
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-m.closed:
		return 0, koloaudio.ErrClosed
	case <-time.After(time.Until(m.next)):
	}
	if m.lose.Load() {
		return 0, koloaudio.ErrDeviceLost
	}
	for i := range p {
		if m.silent.Load() {
			p[i] = 0
		} else {
			p[i] = int16(8000 * math.Sin(m.phase))
		}
		m.phase += 2 * math.Pi * m.hw.freq / sampleRate
	}
	return len(p), nil
}

func (m *fakeMic) Close() error {
	m.once.Do(func() { m.hw.opened.Add(-1); m.hw.closes.Add(1); close(m.closed) })
	return nil
}

type fakeSpeaker struct {
	hw   *fakeHW
	once sync.Once
}

func (s *fakeSpeaker) Write(_ context.Context, p []int16) (int, error) {
	s.hw.mu.Lock()
	defer s.hw.mu.Unlock()
	s.hw.played = append(s.hw.played, p...)
	if len(s.hw.played) > sampleRate {
		s.hw.played = s.hw.played[len(s.hw.played)-sampleRate:]
	}
	return len(p), nil
}

func (s *fakeSpeaker) Close() error {
	s.once.Do(func() { s.hw.opened.Add(-1); s.hw.closes.Add(1) })
	return nil
}

// heard returns the last second the speaker played.
func (f *fakeHW) heard() []int16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int16(nil), f.played...)
}

// tone is the strength of freq in pcm (Goertzel), as a fraction of a full-scale sine at that pitch.
func tone(pcm []int16, freq float64) float64 {
	if len(pcm) == 0 {
		return 0
	}
	w := 2 * math.Pi * freq / sampleRate
	coeff := 2 * math.Cos(w)
	var s1, s2 float64
	for _, v := range pcm {
		s0 := float64(v) + coeff*s1 - s2
		s2, s1 = s1, s0
	}
	power := s1*s1 + s2*s2 - coeff*s1*s2
	return 2 * math.Sqrt(power) / float64(len(pcm)) / 32768
}
