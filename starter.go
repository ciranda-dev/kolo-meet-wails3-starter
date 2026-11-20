package main

// This file is only in the starter: the finished app has neither the made-up devices nor ErrNotInStarter. Step 6 of
// the guide deletes it.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	koloaudio "github.com/ciranda-dev/kolo-audio"
	_ "github.com/ciranda-dev/kolo-store" // host.go uses it from step 3; until then this keeps it in go.mod
	kolovideo "github.com/ciranda-dev/kolo-video"
)

// ErrNotInStarter is the cause of every error from a part of the app that the guide has not written yet.
var ErrNotInStarter = errors.New("kolo-meet: not implemented in the starter")

// notInStarter is the error of a part that step of the guide writes.
func notInStarter(step int) error {
	return fmt.Errorf("%w (step %d of the guide)", ErrNotInStarter, step)
}

// madeUpDevices is a camera, a microphone and a speaker made of numbers, so the window runs before step 2 of the
// guide. Nothing real is opened.
type madeUpDevices struct{}

var starterDevices madeUpDevices

func (madeUpDevices) Cameras() ([]Device, error) {
	return []Device{{ID: "starter-camera", Name: "Made-up camera (starter)", Default: true}}, nil
}

func (madeUpDevices) Microphones() ([]Device, error) {
	return []Device{{ID: "starter-microphone", Name: "Made-up microphone (starter)", Default: true}}, nil
}

func (madeUpDevices) Speakers() ([]Device, error) {
	return []Device{{ID: "starter-speaker", Name: "Made-up speaker (starter)", Default: true}}, nil
}

func (madeUpDevices) MicrophoneAccess() (string, error) { return micAuthorized, nil }

func (madeUpDevices) RequestMicrophone(context.Context) error { return nil }

func (madeUpDevices) RequestCamera(context.Context) error { return nil }

func (madeUpDevices) OpenCamera(_ string, w, h int, fps float64) (cameraStream, error) {
	return &madeUpCamera{w: w, h: h, every: time.Duration(float64(time.Second) / fps), closed: make(chan struct{})}, nil
}

func (madeUpDevices) OpenMicrophone(string) (micStream, error) {
	return &madeUpMic{closed: make(chan struct{})}, nil
}

func (madeUpDevices) OpenSpeaker(string) (speakerStream, error) { return madeUpSpeaker{}, nil }

// madeUpCamera draws moving grey stripes.
type madeUpCamera struct {
	w, h   int
	every  time.Duration
	next   time.Time
	n      int
	closed chan struct{}
	once   sync.Once
}

func (c *madeUpCamera) Read(ctx context.Context) (*kolovideo.Frame, error) {
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
	f, err := kolovideo.NewFrame(c.w, c.h)
	if err != nil {
		return nil, err
	}
	for i := range f.Y {
		f.Y[i] = byte(16 + (i/c.w+i%c.w+4*c.n)%220)
	}
	c.n++
	return f, nil
}

func (c *madeUpCamera) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

// madeUpMic plays a quiet 440 Hz tone for half of every second, so the meter moves.
type madeUpMic struct {
	next   time.Time
	n      int
	closed chan struct{}
	once   sync.Once
}

func (m *madeUpMic) Read(ctx context.Context, p []int16) (int, error) {
	if m.next.IsZero() {
		m.next = time.Now()
	}
	m.next = m.next.Add(time.Duration(len(p)) * time.Second / sampleRate)
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-m.closed:
		return 0, koloaudio.ErrClosed
	case <-time.After(time.Until(m.next)):
	}
	for i := range p {
		p[i] = 0
		if m.n%sampleRate < sampleRate/2 {
			p[i] = int16(3000 * math.Sin(2*math.Pi*440*float64(m.n)/sampleRate))
		}
		m.n++
	}
	return len(p), nil
}

func (m *madeUpMic) Close() error {
	m.once.Do(func() { close(m.closed) })
	return nil
}

// madeUpSpeaker drops what it gets.
type madeUpSpeaker struct{}

func (madeUpSpeaker) Write(_ context.Context, p []int16) (int, error) { return len(p), nil }
func (madeUpSpeaker) Close() error                                    { return nil }
