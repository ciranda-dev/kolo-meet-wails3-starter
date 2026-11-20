package main

import (
	"context"
	"errors"

	koloaudio "github.com/ciranda-dev/kolo-audio"
	kolovideo "github.com/ciranda-dev/kolo-video"
)

// Device is a camera, a microphone or a speaker. ID is opaque and stays on this computer.
type Device struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Default bool   `json:"default"`
}

// Devices lists what can be chosen. A kind without devices is an empty list, not an error.
type Devices struct {
	Cameras     []Device `json:"cameras"`
	Microphones []Device `json:"microphones"`
	Speakers    []Device `json:"speakers"`
}

// Names of the three kinds of device in reasons ("camera-denied") and in Lobby.
const (
	devCamera     = "camera"
	devMicrophone = "microphone"
	devSpeaker    = "speaker"
)

type cameraStream interface {
	Read(ctx context.Context) (*kolovideo.Frame, error)
	Close() error
}

type micStream interface {
	Read(ctx context.Context, p []int16) (int, error)
	Close() error
}

type speakerStream interface {
	Write(ctx context.Context, p []int16) (int, error)
	Close() error
}

// hardware is the part of the machine the app uses. The app's comes from kolo-video and kolo-audio, and tests have
// fakes: a test never opens a real device.
type hardware interface {
	Cameras() ([]Device, error)
	Microphones() ([]Device, error)
	Speakers() ([]Device, error)
	// MicrophoneAccess is the system's microphone permission, one of the mic* values below. It never asks and never
	// opens a device. An error means the platform cannot say (nothing to ask).
	MicrophoneAccess() (string, error)
	// RequestMicrophone asks for microphone access when the person has not been asked (macOS: the system prompt) and
	// returns when it is decided or ctx ends. It never opens a device and has no time limit of its own.
	RequestMicrophone(ctx context.Context) error
	// RequestCamera asks for camera access where the system requires it (macOS). It may show a system prompt.
	RequestCamera(ctx context.Context) error
	OpenCamera(id string, w, h int, fps float64) (cameraStream, error)
	OpenMicrophone(id string) (micStream, error)
	OpenSpeaker(id string) (speakerStream, error)
}

// The microphone permissions MicrophoneAccess reports, and the status the window gets.
const (
	micNotDetermined = "not-determined"
	micRestricted    = "restricted"
	micDenied        = "denied"
	micAuthorized    = "authorized"
	micUnknown       = "unknown" // the platform cannot say: opening the microphone tells
)

// Audio runs at 48 kHz mono in 20 ms frames, the Opus defaults of kolo-audio (opus.Config) and what kolo-media's
// group-call tests send (secure_scale_test.go).
const (
	sampleRate    = koloaudio.DefaultSampleRate
	audioChannels = koloaudio.DefaultChannels
	frameSamples  = sampleRate / 50
)

// realHardware is kolo-video and kolo-audio on the machine. Until step 2 of the guide it is the made-up devices of
// starter.go.
type realHardware struct{}

func (realHardware) Cameras() ([]Device, error)     { return starterDevices.Cameras() }
func (realHardware) Microphones() ([]Device, error) { return starterDevices.Microphones() }
func (realHardware) Speakers() ([]Device, error)    { return starterDevices.Speakers() }

func (realHardware) MicrophoneAccess() (string, error) { return starterDevices.MicrophoneAccess() }

func (realHardware) RequestMicrophone(ctx context.Context) error {
	return starterDevices.RequestMicrophone(ctx)
}

func (realHardware) RequestCamera(ctx context.Context) error {
	return starterDevices.RequestCamera(ctx)
}

func (realHardware) OpenCamera(id string, w, h int, fps float64) (cameraStream, error) {
	return starterDevices.OpenCamera(id, w, h, fps)
}

func (realHardware) OpenMicrophone(id string) (micStream, error) {
	return starterDevices.OpenMicrophone(id)
}

func (realHardware) OpenSpeaker(id string) (speakerStream, error) {
	return starterDevices.OpenSpeaker(id)
}

// deviceError turns the error of a device into the MeetError of its kind. Step 2 of the guide tells a refused
// permission, a missing device and a lost one apart.
func deviceError(kind string, err error) *MeetError {
	var me *MeetError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &me):
		return me
	}
	return meetErr(kind+"-failed", err, "")
}
