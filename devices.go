package main

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"time"

	koloaudio "github.com/ciranda-dev/kolo-audio"
)

// Devices lists the cameras, microphones and speakers. It never opens one, so it asks for no permission.
func (s *MeetService) Devices() (Devices, error) {
	var out Devices
	var errs []error
	var err error
	if out.Cameras, err = s.hw.Cameras(); err != nil {
		errs = append(errs, deviceError(devCamera, err))
	}
	if out.Microphones, err = s.hw.Microphones(); err != nil {
		errs = append(errs, deviceError(devMicrophone, err))
	}
	if out.Speakers, err = s.hw.Speakers(); err != nil {
		errs = append(errs, deviceError(devSpeaker, err))
	}
	out.Cameras, out.Microphones, out.Speakers = orEmpty(out.Cameras), orEmpty(out.Microphones), orEmpty(out.Speakers)
	return out, errors.Join(errs...)
}

func hasDevice(ds []Device, id string) bool {
	return id == "" || slices.ContainsFunc(ds, func(d Device) bool { return d.ID == id })
}

// SetDevices saves the camera, microphone and speaker to use; "" is the system default. An open device is opened
// again on the new one. It fails with "device-unknown" for an ID that is not in the lists.
func (s *MeetService) SetDevices(camera, microphone, speaker string) (Settings, error) {
	ds, _ := s.Devices()
	if !hasDevice(ds.Cameras, camera) || !hasDevice(ds.Microphones, microphone) || !hasDevice(ds.Speakers, speaker) {
		return Settings{}, meetErr("device-unknown", ErrUnknownDevice, "")
	}
	if err := s.saveSaved(func(p *savedSettings) { p.CameraID, p.MicrophoneID, p.SpeakerID = camera, microphone, speaker }); err != nil {
		return Settings{}, meetErr("internal", err, "")
	}
	if s.media != nil {
		mic, cam := s.media.isOpen()
		if cam {
			s.media.closeCamera()
			s.SetCamera(true)
		}
		if mic {
			s.media.closeMic()
			s.SetMicrophone(true)
		}
	}
	return s.Settings(), nil
}

// mediaStates are the states of the three devices now.
func (s *MeetService) mediaStates() (mic, cam, spk MediaState) {
	p := s.prefs()
	s.prefMu.RLock()
	micErr, camErr, spkErr, spkOn := s.micErr, s.camErr, s.spkErr, s.spkOn
	s.prefMu.RUnlock()
	micOpen, camOpen := false, false
	if s.media != nil {
		micOpen, camOpen = s.media.isOpen()
	}
	state := func(open bool, err *MeetError, id string) MediaState {
		switch {
		case open:
			return MediaState{State: "on", DeviceID: id}
		case err != nil:
			return MediaState{State: "error", DeviceID: id, Error: err}
		}
		return MediaState{State: "off", DeviceID: id}
	}
	mic = state(micOpen, micErr, p.MicrophoneID)
	if micOpen && s.media.silent.Load() {
		mic.Hint = meetErr("microphone-silent", nil, "")
	}
	return mic, state(camOpen, camErr, p.CameraID), state(spkOn, spkErr, p.SpeakerID)
}

// Lobby returns the devices now.
func (s *MeetService) Lobby() Lobby {
	mic, cam, spk := s.mediaStates()
	return Lobby{Microphone: mic, Camera: cam, Speaker: spk}
}

func (s *MeetService) lobbyChanged() {
	s.emit("meet:lobby", s.Lobby())
	if c := s.activeCall(); c != nil {
		c.changed()
	} else {
		s.sendNotch()
		s.pushTray()
	}
}

func (s *MeetService) ready() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.media == nil || !s.running {
		return explain(ErrNotReady)
	}
	return nil
}

// SetMicrophone turns the microphone on or off. On opens it, which asks the system for access the first time; off
// closes it. In a call, the microphone track appears or goes away, so the others see it. The answer is the state of
// the device; an error is also in MediaState.Error.
func (s *MeetService) SetMicrophone(on bool) (MediaState, error) {
	if err := s.ready(); err != nil {
		return MediaState{}, err
	}
	var err error
	if on {
		// The permission first: a refusal is known before a device is opened, and the prompt (macOS) is the person's.
		if _, err = s.requestMicrophone(); err == nil {
			err = s.media.openMic(s.prefs().MicrophoneID)
		}
	} else {
		s.media.closeMic()
	}
	s.prefMu.Lock()
	s.micErr = asMeetError(err)
	s.prefMu.Unlock()
	if c := s.activeCall(); c != nil {
		if pub := c.publisher(); pub != nil {
			pub.setAudio(on && err == nil)
		}
	}
	s.lobbyChanged()
	mic, _, _ := s.mediaStates()
	return mic, err
}

// MicrophoneAccess returns the system's microphone permission: "not-determined" (the person has not been asked;
// macOS shows its prompt at RequestMicrophone), "authorized", "denied", "restricted" (a policy forbids it), or
// "unknown" (the platform cannot say, as on Linux and Windows: opening the microphone tells). It never opens a device
// and never asks.
func (s *MeetService) MicrophoneAccess() (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	status, err := s.hw.MicrophoneAccess()
	if err != nil {
		return micUnknown, nil
	}
	return status, nil
}

// RequestMicrophone asks for microphone access when the person has not been asked, which shows the system prompt on
// macOS, and answers with the status once it is decided. It never opens a device. The prompt waits for the person
// with no time limit of the app's: CancelMicrophoneRequest, leaving the lobby or quitting ends the wait (a
// "cancelled" rejection). A refusal rejects with "microphone-denied" or "microphone-restricted" and the status says
// which. SetMicrophone(true) does the same before it opens the device.
func (s *MeetService) RequestMicrophone() (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	status, err := s.requestMicrophone()
	s.lobbyChanged()
	return status, err
}

// CancelMicrophoneRequest ends a RequestMicrophone or SetMicrophone that waits for the person to answer the system
// prompt. The prompt itself stays on screen until answered. It does nothing when no request waits.
func (s *MeetService) CancelMicrophoneRequest() {
	s.micCancelMu.Lock()
	cancel := s.micCancel
	s.micCancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// requestMicrophone makes the microphone's permission known and, if the person was never asked, asks. One prompt at a
// time. ctx is the service's own, cancelled with it and by CancelMicrophoneRequest; there is no timeout.
func (s *MeetService) requestMicrophone() (string, error) {
	s.micAskMu.Lock()
	defer s.micAskMu.Unlock()
	status, err := s.hw.MicrophoneAccess()
	if err != nil {
		return micUnknown, nil // nothing to ask here; the open tells
	}
	if status == micNotDetermined {
		ctx, cancel := context.WithCancel(s.ctx)
		s.micCancelMu.Lock()
		s.micCancel = cancel
		s.micCancelMu.Unlock()
		err := s.hw.RequestMicrophone(ctx)
		s.micCancelMu.Lock()
		s.micCancel = nil
		s.micCancelMu.Unlock()
		cancel()
		if err != nil {
			if now, e := s.hw.MicrophoneAccess(); e == nil {
				status = now
			}
			return status, micRequestError(err, status)
		}
		if now, e := s.hw.MicrophoneAccess(); e == nil {
			status = now
		}
	}
	switch status {
	case micDenied:
		return status, meetErr("microphone-denied", nil, "")
	case micRestricted:
		return status, meetErr("microphone-restricted", nil, "")
	}
	return status, nil
}

// micRequestError is the error of a prompt that did not end in access: the person's refusal, a policy, or a wait that
// was cancelled.
func micRequestError(err error, status string) *MeetError {
	switch {
	case errors.Is(err, context.Canceled):
		return meetErr("cancelled", err, "")
	case status == micRestricted:
		return meetErr("microphone-restricted", err, "")
	case errors.Is(err, koloaudio.ErrPermission), errors.Is(err, fs.ErrPermission):
		return meetErr("microphone-denied", err, "")
	}
	return deviceError(devMicrophone, err)
}

// SetCamera turns the camera on or off, like SetMicrophone.
func (s *MeetService) SetCamera(on bool) (MediaState, error) {
	if err := s.ready(); err != nil {
		return MediaState{}, err
	}
	var err error
	if on {
		ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute) // the system prompt waits for a person
		err = s.media.openCamera(ctx, s.prefs().CameraID)
		cancel()
	} else {
		s.media.closeCamera()
	}
	s.prefMu.Lock()
	s.camErr = asMeetError(err)
	s.prefMu.Unlock()
	if c := s.activeCall(); c != nil && !(on && err == nil) {
		if pub := c.publisher(); pub != nil {
			pub.setVideo(false)
		}
	}
	s.lobbyChanged()
	_, cam, _ := s.mediaStates()
	return cam, err
}

// ToggleMicrophone and ToggleCamera flip the device; the notch and the tray use them.
func (s *MeetService) ToggleMicrophone() (MediaState, error) {
	mic, _, _ := s.mediaStates()
	return s.SetMicrophone(mic.State != "on")
}

func (s *MeetService) ToggleCamera() (MediaState, error) {
	_, cam, _ := s.mediaStates()
	return s.SetCamera(cam.State != "on")
}

func asMeetError(err error) *MeetError {
	if err == nil {
		return nil
	}
	var me *MeetError
	if errors.As(err, &me) {
		return me
	}
	return meetErr("internal", err, "")
}

// mediaLost runs when a device stops in the middle of use: the person sees why, and the track goes away.
func (s *MeetService) mediaLost(kind string, err error) {
	me := deviceError(kind, err)
	s.prefMu.Lock()
	if kind == devMicrophone {
		s.micErr = me
	} else {
		s.camErr = me
	}
	s.prefMu.Unlock()
	if c := s.activeCall(); c != nil {
		if pub := c.publisher(); pub != nil {
			if kind == devMicrophone {
				pub.setAudio(false)
			} else {
				pub.setVideo(false)
			}
		}
	}
	s.lobbyChanged()
}

// openSpeaker opens the speaker for a call.
func (s *MeetService) openSpeaker() (speakerStream, error) {
	out, err := s.hw.OpenSpeaker(s.prefs().SpeakerID)
	s.prefMu.Lock()
	s.spkErr, s.spkOn = asMeetError(deviceErrorOrNil(devSpeaker, err)), err == nil
	s.prefMu.Unlock()
	s.emit("meet:lobby", s.Lobby())
	if err != nil {
		return nil, err
	}
	return out, nil
}

func deviceErrorOrNil(kind string, err error) error {
	if err == nil {
		return nil
	}
	return deviceError(kind, err)
}

// speakerLost runs when the speaker fails during a call; the call goes on without sound.
func (s *MeetService) speakerLost(err error) {
	s.prefMu.Lock()
	s.spkErr, s.spkOn = deviceError(devSpeaker, err), false
	s.prefMu.Unlock()
	s.lobbyChanged()
}

func (s *MeetService) clearDeviceErrors() {
	s.prefMu.Lock()
	s.spkOn, s.spkErr = false, nil
	s.prefMu.Unlock()
}

// LevelsEvent is the payload of the "meet:levels" event: how loud everyone is, ten times a second while anyone is.
type LevelsEvent struct {
	Levels []Level `json:"levels"`
}

// speakerWatch tells the notch when the speaker of a call changes. A new call starts with nobody speaking, whoever
// spoke at the end of the last one.
type speakerWatch struct {
	call *call
	last string
}

func (w *speakerWatch) update(c *call, levels []Level) {
	if c != w.call {
		w.call, w.last = c, ""
	}
	if who := c.speakerID(levels); who != w.last {
		w.last = who
		c.changedSpeaker(who)
	}
}

// levelLoop sends the levels of the microphone and of the other participants, and tells the notch when the speaker
// changes.
func (s *MeetService) levelLoop() {
	tick := time.NewTicker(levelInterval)
	defer tick.Stop()
	var wasLoud bool
	var watch speakerWatch
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		}
		levels := []Level{s.media.selfLevel()}
		if c := s.activeCall(); c != nil {
			levels = append(levels, c.remoteLevels()...)
		}
		loud := false
		for _, l := range levels {
			loud = loud || l.Level > 0
		}
		if loud || wasLoud {
			s.emit("meet:levels", LevelsEvent{Levels: levels})
		}
		wasLoud = loud
		if c := s.activeCall(); c != nil {
			watch.update(c, levels)
		}
	}
}
